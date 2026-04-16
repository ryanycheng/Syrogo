package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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

type AnthropicMessagesProvider struct {
	providerName string
	baseURL      string
	apiKeys      []string
	httpClient   *http.Client
}

type openAIProtocolMode string

const (
	openAIProtocolModeChat      openAIProtocolMode = "chat"
	openAIProtocolModeResponses openAIProtocolMode = "responses"
)

var claudeCodeControlToolNames = map[string]struct{}{
	"Agent":           {},
	"AskUserQuestion": {},
	"CronCreate":      {},
	"CronDelete":      {},
	"CronList":        {},
	"EnterPlanMode":   {},
	"EnterWorktree":   {},
	"ExitPlanMode":    {},
	"ExitWorktree":    {},
	"ScheduleWakeup":  {},
	"Skill":           {},
}

func traceModeEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("SYROGO_TRACE")), "1") || strings.EqualFold(strings.TrimSpace(os.Getenv("SYROGO_TRACE")), "full")
}

type providerTraceSnapshot struct {
	RequestID string            `json:"request_id,omitempty"`
	Provider  string            `json:"provider"`
	Protocol  string            `json:"protocol"`
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers,omitempty"`
	Request   json.RawMessage   `json:"request,omitempty"`
	Status    int               `json:"status,omitempty"`
	Response  json.RawMessage   `json:"response,omitempty"`
	Error     string            `json:"error,omitempty"`
	CreatedAt string            `json:"created_at"`
}

func requestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(runtime.ContextKeyRequestID).(string)
	return v
}

func redactHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	masked := make(map[string]string, len(headers))
	for key, value := range headers {
		switch strings.ToLower(key) {
		case "authorization":
			masked[key] = maskCredential(value, "Bearer ")
		case "x-api-key":
			masked[key] = maskCredential(value, "")
		default:
			masked[key] = value
		}
	}
	return masked
}

func maskCredential(value, prefix string) string {
	if value == "" {
		return ""
	}
	if prefix != "" {
		if !strings.HasPrefix(value, prefix) {
			return "***"
		}
		return prefix + "***"
	}
	return "***"
}

func writeProviderTraceSnapshot(snapshot providerTraceSnapshot) error {
	if err := os.MkdirAll(filepath.Join("tmp", "trace"), 0o755); err != nil {
		return err
	}

	payload, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}

	base := snapshot.RequestID
	if base == "" {
		base = time.Now().Format("20060102-150405.000")
	}
	fileName := fmt.Sprintf("%s.outbound-%s-%s.json", base, snapshot.Provider, snapshot.Protocol)
	return os.WriteFile(filepath.Join("tmp", "trace", fileName), payload, 0o644)
}

func appendProviderTraceSnapshot(snapshot providerTraceSnapshot) {
	if !traceModeEnabled() {
		return
	}
	if err := writeProviderTraceSnapshot(snapshot); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "provider trace write failed provider=%s protocol=%s err=%v\n", snapshot.Provider, snapshot.Protocol, err)
	}
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

func NewAnthropicMessagesCompatible(name, baseURL string, apiKeys []string, httpClient *http.Client) *AnthropicMessagesProvider {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &AnthropicMessagesProvider{
		providerName: name,
		baseURL:      strings.TrimRight(baseURL, "/"),
		apiKeys:      append([]string(nil), apiKeys...),
		httpClient:   httpClient,
	}
}

func (p *MockProvider) Name() string {
	return p.providerName
}

func (p *AnthropicMessagesProvider) Name() string {
	return p.providerName
}

func (p *OpenAICompatibleProvider) Name() string {
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

func normalizedToolSchema(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return append(json.RawMessage(nil), raw...)
	}
	encoded, err := json.Marshal(v)
	if err != nil {
		return append(json.RawMessage(nil), raw...)
	}
	return encoded
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

func joinedTextParts(msg runtime.Message) string {
	parts := make([]string, 0, len(msg.Parts))
	for _, part := range msg.Parts {
		if part.Type == runtime.ContentPartTypeText && part.Text != "" {
			parts = append(parts, part.Text)
		}
	}
	return strings.Join(parts, "\n")
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
