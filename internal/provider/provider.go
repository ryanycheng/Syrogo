package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"syrogo/internal/runtime"
)

type Provider = runtime.CompletionProvider

type MockProvider struct {
	providerName string
}

type OpenAICompatibleProvider struct {
	providerName string
	baseURL      string
	apiKeys      []string
	httpClient   *http.Client

	mu           sync.Mutex
	nextAPIKeyIx int
}

func NewMock(name string) *MockProvider {
	return &MockProvider{providerName: name}
}

func NewOpenAICompatible(name, baseURL string, apiKeys []string, httpClient *http.Client) *OpenAICompatibleProvider {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &OpenAICompatibleProvider{
		providerName: name,
		baseURL:      strings.TrimRight(baseURL, "/"),
		apiKeys:      append([]string(nil), apiKeys...),
		httpClient:   httpClient,
	}
}

func (p *MockProvider) Name() string {
	return p.providerName
}

func (p *MockProvider) ChatCompletion(_ context.Context, req runtime.Request) (runtime.Response, error) {
	if req.Model == "" {
		return runtime.Response{}, fmt.Errorf("model is required")
	}

	return runtime.Response{
		ID:           "chatcmpl-mock",
		Object:       "chat.completion",
		Model:        req.Model,
		FinishReason: runtime.FinishReasonStop,
		Message: runtime.Message{
			Role: runtime.MessageRoleAssistant,
			Parts: []runtime.ContentPart{{
				Type: runtime.ContentPartTypeText,
				Text: "syrogo mock response",
			}},
		},
	}, nil
}

func (p *MockProvider) StreamCompletion(ctx context.Context, req runtime.Request) (<-chan runtime.StreamEvent, error) {
	resp, err := p.ChatCompletion(ctx, req)
	if err != nil {
		return nil, err
	}
	ch := make(chan runtime.StreamEvent, 3)
	go func() {
		defer close(ch)
		ch <- runtime.StreamEvent{Type: runtime.StreamEventMessageStart, ResponseID: resp.ID, Model: resp.Model, MessageRole: resp.Message.Role}
		for _, part := range resp.Message.Parts {
			partCopy := part
			ch <- runtime.StreamEvent{Type: runtime.StreamEventContentDelta, ResponseID: resp.ID, Model: resp.Model, Delta: &partCopy}
		}
		ch <- runtime.StreamEvent{Type: runtime.StreamEventMessageEnd, ResponseID: resp.ID, Model: resp.Model, FinishReason: resp.FinishReason}
	}()
	return ch, nil
}

func (p *OpenAICompatibleProvider) Name() string {
	return p.providerName
}

func (p *OpenAICompatibleProvider) ChatCompletion(ctx context.Context, req runtime.Request) (runtime.Response, error) {
	if req.Model == "" {
		return runtime.Response{}, fmt.Errorf("model is required")
	}
	if len(p.apiKeys) == 0 {
		return runtime.Response{}, fmt.Errorf("api key is required")
	}

	payload, err := json.Marshal(toOpenAIChatRequest(req))
	if err != nil {
		return runtime.Response{}, fmt.Errorf("marshal request: %w", err)
	}

	start := p.currentAPIKeyIndex()
	lastErr := error(nil)
	for offset := range p.apiKeys {
		keyIx := (start + offset) % len(p.apiKeys)
		resp, err := p.chatCompletionWithAPIKey(ctx, payload, p.apiKeys[keyIx])
		if err == nil {
			p.markNextAPIKeyAfter(keyIx)
			return resp, nil
		}

		lastErr = err
		if NormalizeError(err) != ErrorKindQuotaExceeded || offset == len(p.apiKeys)-1 {
			return runtime.Response{}, err
		}

		p.setNextAPIKey((keyIx + 1) % len(p.apiKeys))
	}

	return runtime.Response{}, lastErr
}

func (p *OpenAICompatibleProvider) StreamCompletion(ctx context.Context, req runtime.Request) (<-chan runtime.StreamEvent, error) {
	resp, err := p.ChatCompletion(ctx, req)
	if err != nil {
		return nil, err
	}
	ch := make(chan runtime.StreamEvent, 3)
	go func() {
		defer close(ch)
		ch <- runtime.StreamEvent{Type: runtime.StreamEventMessageStart, ResponseID: resp.ID, Model: resp.Model, MessageRole: resp.Message.Role}
		for _, part := range resp.Message.Parts {
			partCopy := part
			ch <- runtime.StreamEvent{Type: runtime.StreamEventContentDelta, ResponseID: resp.ID, Model: resp.Model, Delta: &partCopy}
		}
		if resp.Usage != nil {
			ch <- runtime.StreamEvent{Type: runtime.StreamEventUsage, ResponseID: resp.ID, Model: resp.Model, Usage: resp.Usage}
		}
		ch <- runtime.StreamEvent{Type: runtime.StreamEventMessageEnd, ResponseID: resp.ID, Model: resp.Model, FinishReason: resp.FinishReason}
	}()
	return ch, nil
}

func (p *OpenAICompatibleProvider) chatCompletionWithAPIKey(ctx context.Context, payload []byte, apiKey string) (runtime.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return runtime.Response{}, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	httpResp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return runtime.Response{}, NewRetryableError(fmt.Errorf("send request: %w", err))
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode == http.StatusTooManyRequests {
		return runtime.Response{}, NewQuotaExceededError(fmt.Errorf("upstream quota exceeded"))
	}
	if httpResp.StatusCode >= http.StatusInternalServerError {
		return runtime.Response{}, NewRetryableError(fmt.Errorf("upstream server error: %s", httpResp.Status))
	}
	if httpResp.StatusCode >= http.StatusBadRequest {
		return runtime.Response{}, NewFatalError(fmt.Errorf("upstream request failed: %s", httpResp.Status))
	}

	var resp struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		return runtime.Response{}, NewRetryableError(fmt.Errorf("decode response: %w", err))
	}
	if len(resp.Choices) == 0 {
		return runtime.Response{}, NewFatalError(fmt.Errorf("upstream response missing choices"))
	}

	return runtime.Response{
		ID:           resp.ID,
		Object:       resp.Object,
		Model:        resp.Model,
		FinishReason: runtime.FinishReasonStop,
		Message: runtime.Message{
			Role: runtime.MessageRole(resp.Choices[0].Message.Role),
			Parts: []runtime.ContentPart{{
				Type: runtime.ContentPartTypeText,
				Text: resp.Choices[0].Message.Content,
			}},
		},
	}, nil
}

func toOpenAIChatRequest(req runtime.Request) any {
	type chatMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	messages := make([]chatMessage, 0, len(req.Messages))
	for _, msg := range req.Messages {
		messages = append(messages, chatMessage{
			Role:    string(msg.Role),
			Content: firstTextPart(msg),
		})
	}
	return map[string]any{
		"model":    req.Model,
		"messages": messages,
	}
}

func firstTextPart(msg runtime.Message) string {
	for _, part := range msg.Parts {
		if part.Type == runtime.ContentPartTypeText {
			return part.Text
		}
	}
	return ""
}

func (p *OpenAICompatibleProvider) currentAPIKeyIndex() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.nextAPIKeyIx
}

func (p *OpenAICompatibleProvider) markNextAPIKeyAfter(current int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.nextAPIKeyIx = (current + 1) % len(p.apiKeys)
}

func (p *OpenAICompatibleProvider) setNextAPIKey(next int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.nextAPIKeyIx = next
}
