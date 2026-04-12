package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	path         string
	mode         openAIProtocolMode

	mu           sync.Mutex
	nextAPIKeyIx int
}

type openAIProtocolMode string

const (
	openAIProtocolModeChat      openAIProtocolMode = "chat"
	openAIProtocolModeResponses openAIProtocolMode = "responses"
)

type openAIChatMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openAIToolCall struct {
	ID       string                 `json:"id,omitempty"`
	Type     string                 `json:"type"`
	Function openAIToolCallFunction `json:"function"`
}

type openAIToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAIChatResponseEnvelope struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Model   string `json:"model"`
	Choices []struct {
		Message openAIChatMessage `json:"message"`
	} `json:"choices"`
}

type openAIResponsesTextPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type openAIResponsesInputItem struct {
	Type      string                    `json:"type,omitempty"`
	Role      string                    `json:"role,omitempty"`
	Content   []openAIResponsesTextPart `json:"content,omitempty"`
	CallID    string                    `json:"call_id,omitempty"`
	Name      string                    `json:"name,omitempty"`
	Arguments string                    `json:"arguments,omitempty"`
	Output    string                    `json:"output,omitempty"`
}

type openAIResponsesOutputItem struct {
	Type      string                    `json:"type"`
	Role      string                    `json:"role,omitempty"`
	Content   []openAIResponsesTextPart `json:"content,omitempty"`
	CallID    string                    `json:"call_id,omitempty"`
	Name      string                    `json:"name,omitempty"`
	Arguments string                    `json:"arguments,omitempty"`
}

type openAIResponsesEnvelope struct {
	ID     string                      `json:"id"`
	Object string                      `json:"object"`
	Model  string                      `json:"model"`
	Output []openAIResponsesOutputItem `json:"output"`
	Usage  *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage,omitempty"`
}

func NewMock(name string) *MockProvider {
	return &MockProvider{providerName: name}
}

func NewOpenAICompatible(name, baseURL string, apiKeys []string, httpClient *http.Client) *OpenAICompatibleProvider {
	return newOpenAIProvider(name, baseURL, apiKeys, httpClient, "/chat/completions", openAIProtocolModeChat)
}

func NewOpenAIResponsesCompatible(name, baseURL string, apiKeys []string, httpClient *http.Client) *OpenAICompatibleProvider {
	return newOpenAIProvider(name, baseURL, apiKeys, httpClient, "/responses", openAIProtocolModeResponses)
}

func newOpenAIProvider(name, baseURL string, apiKeys []string, httpClient *http.Client, path string, mode openAIProtocolMode) *OpenAICompatibleProvider {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &OpenAICompatibleProvider{
		providerName: name,
		baseURL:      strings.TrimRight(baseURL, "/"),
		apiKeys:      append([]string(nil), apiKeys...),
		httpClient:   httpClient,
		path:         path,
		mode:         mode,
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
	toolCallCount := len(resp.Message.ToolCalls)
	eventCount := 2 + len(resp.Message.Parts) + toolCallCount
	if resp.Usage != nil {
		eventCount++
	}
	ch := make(chan runtime.StreamEvent, eventCount)
	go func() {
		defer close(ch)
		ch <- runtime.StreamEvent{Type: runtime.StreamEventMessageStart, ResponseID: resp.ID, Model: resp.Model, MessageRole: resp.Message.Role}
		for _, part := range resp.Message.Parts {
			partCopy := part
			ch <- runtime.StreamEvent{Type: runtime.StreamEventContentDelta, ResponseID: resp.ID, Model: resp.Model, Delta: &partCopy}
		}
		for i, call := range resp.Message.ToolCalls {
			callCopy := call
			ch <- runtime.StreamEvent{Type: runtime.StreamEventContentDelta, ResponseID: resp.ID, Model: resp.Model, ToolCall: &callCopy, ToolCallIndex: i}
		}
		if resp.Usage != nil {
			ch <- runtime.StreamEvent{Type: runtime.StreamEventUsage, ResponseID: resp.ID, Model: resp.Model, Usage: resp.Usage}
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

	var payload any
	switch p.mode {
	case openAIProtocolModeResponses:
		payload = encodeOpenAIResponsesRequest(req)
	default:
		payload = encodeOpenAIChatRequest(req)
	}
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		return runtime.Response{}, fmt.Errorf("marshal request: %w", err)
	}

	start := p.currentAPIKeyIndex()
	lastErr := error(nil)
	for offset := range p.apiKeys {
		keyIx := (start + offset) % len(p.apiKeys)
		resp, err := p.completionWithAPIKey(ctx, encodedPayload, p.apiKeys[keyIx])
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
	toolCallCount := len(resp.Message.ToolCalls)
	eventCount := 2 + len(resp.Message.Parts) + toolCallCount
	if resp.Usage != nil {
		eventCount++
	}
	ch := make(chan runtime.StreamEvent, eventCount)
	go func() {
		defer close(ch)
		ch <- runtime.StreamEvent{Type: runtime.StreamEventMessageStart, ResponseID: resp.ID, Model: resp.Model, MessageRole: resp.Message.Role}
		for _, part := range resp.Message.Parts {
			partCopy := part
			ch <- runtime.StreamEvent{Type: runtime.StreamEventContentDelta, ResponseID: resp.ID, Model: resp.Model, Delta: &partCopy}
		}
		for i, call := range resp.Message.ToolCalls {
			callCopy := call
			ch <- runtime.StreamEvent{Type: runtime.StreamEventContentDelta, ResponseID: resp.ID, Model: resp.Model, ToolCall: &callCopy, ToolCallIndex: i}
		}
		if resp.Usage != nil {
			ch <- runtime.StreamEvent{Type: runtime.StreamEventUsage, ResponseID: resp.ID, Model: resp.Model, Usage: resp.Usage}
		}
		ch <- runtime.StreamEvent{Type: runtime.StreamEventMessageEnd, ResponseID: resp.ID, Model: resp.Model, FinishReason: resp.FinishReason}
	}()
	return ch, nil
}

func (p *OpenAICompatibleProvider) completionWithAPIKey(ctx context.Context, payload []byte, apiKey string) (runtime.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+p.path, bytes.NewReader(payload))
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

	responseBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return runtime.Response{}, NewRetryableError(fmt.Errorf("read response body: %w", err))
	}

	if httpResp.StatusCode == http.StatusTooManyRequests {
		return runtime.Response{}, NewQuotaExceededError(fmt.Errorf("upstream quota exceeded: %s", previewResponseBody(responseBody)))
	}
	if httpResp.StatusCode >= http.StatusInternalServerError {
		return runtime.Response{}, NewRetryableError(fmt.Errorf("upstream server error: %s body=%s", httpResp.Status, previewResponseBody(responseBody)))
	}
	if httpResp.StatusCode >= http.StatusBadRequest {
		return runtime.Response{}, NewFatalError(fmt.Errorf("upstream request failed: %s body=%s", httpResp.Status, previewResponseBody(responseBody)))
	}

	switch p.mode {
	case openAIProtocolModeResponses:
		var resp openAIResponsesEnvelope
		if err := json.Unmarshal(responseBody, &resp); err != nil {
			return runtime.Response{}, NewRetryableError(fmt.Errorf("decode response: %w", err))
		}
		return decodeOpenAIResponsesResponse(resp)
	default:
		var resp openAIChatResponseEnvelope
		if err := json.Unmarshal(responseBody, &resp); err != nil {
			return runtime.Response{}, NewRetryableError(fmt.Errorf("decode response: %w", err))
		}
		return decodeOpenAIChatResponse(resp)
	}
}

func previewResponseBody(body []byte) string {
	const max = 1024
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return `""`
	}
	if len(trimmed) <= max {
		return trimmed
	}
	return trimmed[:max] + "..."
}

func encodeOpenAIChatRequest(req runtime.Request) any {
	messages := make([]openAIChatMessage, 0, len(req.Messages))
	for _, msg := range req.Messages {
		encoded := openAIChatMessage{
			Role:       string(msg.Role),
			Content:    firstTextPart(msg),
			ToolCallID: msg.ToolCallID,
		}
		if len(msg.ToolCalls) > 0 {
			encoded.ToolCalls = make([]openAIToolCall, 0, len(msg.ToolCalls))
			for _, call := range msg.ToolCalls {
				encoded.ToolCalls = append(encoded.ToolCalls, openAIToolCall{
					ID:   call.ID,
					Type: "function",
					Function: openAIToolCallFunction{
						Name:      call.Name,
						Arguments: call.Arguments,
					},
				})
			}
		}
		messages = append(messages, encoded)
	}
	return map[string]any{
		"model":    req.Model,
		"messages": messages,
	}
}

func encodeOpenAIResponsesRequest(req runtime.Request) any {
	input := make([]openAIResponsesInputItem, 0, len(req.Messages))
	for _, msg := range req.Messages {
		switch {
		case msg.Role == runtime.MessageRoleTool:
			input = append(input, openAIResponsesInputItem{
				Type:   "function_call_output",
				CallID: msg.ToolCallID,
				Output: firstTextPart(msg),
			})
		case len(msg.ToolCalls) > 0:
			for _, call := range msg.ToolCalls {
				input = append(input, openAIResponsesInputItem{
					Type:      "function_call",
					CallID:    call.ID,
					Name:      call.Name,
					Arguments: compactJSONOrEmpty(json.RawMessage(call.Arguments)),
				})
			}
		default:
			input = append(input, openAIResponsesInputItem{
				Type: "message",
				Role: string(msg.Role),
				Content: []openAIResponsesTextPart{{
					Type: "input_text",
					Text: firstTextPart(msg),
				}},
			})
		}
	}
	return map[string]any{
		"model": req.Model,
		"input": input,
	}
}

func decodeOpenAIChatResponse(resp openAIChatResponseEnvelope) (runtime.Response, error) {
	if len(resp.Choices) == 0 {
		return runtime.Response{}, NewFatalError(fmt.Errorf("upstream response missing choices"))
	}

	message := runtime.Message{Role: runtime.MessageRole(resp.Choices[0].Message.Role)}
	if resp.Choices[0].Message.Content != "" {
		message.Parts = []runtime.ContentPart{{
			Type: runtime.ContentPartTypeText,
			Text: resp.Choices[0].Message.Content,
		}}
	}
	if len(resp.Choices[0].Message.ToolCalls) > 0 {
		message.ToolCalls = make([]runtime.ToolCall, 0, len(resp.Choices[0].Message.ToolCalls))
		for _, call := range resp.Choices[0].Message.ToolCalls {
			message.ToolCalls = append(message.ToolCalls, runtime.ToolCall{
				ID:        call.ID,
				Name:      call.Function.Name,
				Arguments: call.Function.Arguments,
			})
		}
	}
	if len(message.Parts) == 0 && len(message.ToolCalls) == 0 {
		return runtime.Response{}, NewFatalError(fmt.Errorf("upstream returned no content and no tool calls"))
	}

	return runtime.Response{
		ID:           resp.ID,
		Object:       resp.Object,
		Model:        resp.Model,
		FinishReason: runtime.FinishReasonStop,
		Message:      message,
	}, nil
}

func decodeOpenAIResponsesResponse(resp openAIResponsesEnvelope) (runtime.Response, error) {
	if len(resp.Output) == 0 {
		return runtime.Response{}, NewFatalError(fmt.Errorf("upstream response missing output"))
	}

	message := runtime.Message{Role: runtime.MessageRoleAssistant}
	for _, item := range resp.Output {
		switch item.Type {
		case "message":
			if item.Role != "" {
				message.Role = runtime.MessageRole(item.Role)
			}
			for _, part := range item.Content {
				if part.Text != "" {
					message.Parts = append(message.Parts, runtime.ContentPart{Type: runtime.ContentPartTypeText, Text: part.Text})
				}
			}
		case "function_call":
			message.ToolCalls = append(message.ToolCalls, runtime.ToolCall{ID: item.CallID, Name: item.Name, Arguments: item.Arguments})
		}
	}
	if len(message.Parts) == 0 && len(message.ToolCalls) == 0 {
		return runtime.Response{}, NewFatalError(fmt.Errorf("upstream returned no content and no tool calls"))
	}

	response := runtime.Response{
		ID:           resp.ID,
		Object:       resp.Object,
		Model:        resp.Model,
		FinishReason: runtime.FinishReasonStop,
		Message:      message,
	}
	if resp.Usage != nil {
		response.Usage = &runtime.Usage{InputTokens: resp.Usage.InputTokens, OutputTokens: resp.Usage.OutputTokens, TotalTokens: resp.Usage.TotalTokens}
	}
	return response, nil
}

func compactJSONOrEmpty(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	encoded, err := json.Marshal(v)
	if err != nil {
		return string(raw)
	}
	return string(encoded)
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
