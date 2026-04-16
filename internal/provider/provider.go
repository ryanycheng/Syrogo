package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

type openAIChatMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openAIChatRequest struct {
	Model      string                 `json:"model"`
	Messages   []openAIChatMessage    `json:"messages"`
	MaxTokens  int                    `json:"max_tokens,omitempty"`
	Tools      []openAIToolDefinition `json:"tools,omitempty"`
	ToolChoice string                 `json:"tool_choice,omitempty"`
}

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

type openAIToolDefinition struct {
	Type     string                   `json:"type"`
	Function openAIToolSpecDefinition `json:"function"`
}

type openAIToolSpecDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      bool            `json:"strict,omitempty"`
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

type anthropicMessagesRequest struct {
	Model     string                    `json:"model"`
	System    string                    `json:"system,omitempty"`
	MaxTokens int                       `json:"max_tokens"`
	Messages  []anthropicMessage        `json:"messages"`
	Tools     []anthropicToolDefinition `json:"tools,omitempty"`
	Stream    bool                      `json:"stream,omitempty"`
}

type anthropicToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicMessage struct {
	Role    string                  `json:"role"`
	Content []anthropicContentBlock `json:"content"`
}

type anthropicContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   any             `json:"content,omitempty"`
}

type anthropicMessagesEnvelope struct {
	ID         string                  `json:"id"`
	Type       string                  `json:"type"`
	Role       string                  `json:"role"`
	Model      string                  `json:"model"`
	Content    []anthropicContentBlock `json:"content"`
	StopReason string                  `json:"stop_reason"`
	Usage      *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage,omitempty"`
}

func traceModeEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("SYROGO_TRACE")), "1") || strings.EqualFold(strings.TrimSpace(os.Getenv("SYROGO_TRACE")), "full")
}

func traceInboundEnabled() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("SYROGO_TRACE")))
	return value == "1" || value == "full" || value == "inbound"
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

func (p *AnthropicMessagesProvider) ChatCompletion(ctx context.Context, req runtime.Request) (runtime.Response, error) {
	if req.Model == "" {
		return runtime.Response{}, fmt.Errorf("model is required")
	}
	if len(p.apiKeys) == 0 {
		return runtime.Response{}, fmt.Errorf("api key is required")
	}

	payload := encodeAnthropicMessagesRequest(req)
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		return runtime.Response{}, fmt.Errorf("marshal request: %w", err)
	}

	return p.completionWithAPIKey(ctx, encodedPayload, p.apiKeys[0])
}

func (p *AnthropicMessagesProvider) StreamCompletion(ctx context.Context, req runtime.Request) (<-chan runtime.StreamEvent, error) {
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

func (p *AnthropicMessagesProvider) completionWithAPIKey(ctx context.Context, payload []byte, apiKey string) (runtime.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/messages", bytes.NewReader(payload))
	if err != nil {
		return runtime.Response{}, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	trace := providerTraceSnapshot{
		RequestID: requestIDFromContext(ctx),
		Provider:  p.providerName,
		Protocol:  "anthropic_messages",
		Method:    http.MethodPost,
		URL:       httpReq.URL.String(),
		Headers: redactHeaders(map[string]string{
			"Content-Type":      httpReq.Header.Get("Content-Type"),
			"x-api-key":         httpReq.Header.Get("x-api-key"),
			"anthropic-version": httpReq.Header.Get("anthropic-version"),
		}),
		Request:   append(json.RawMessage(nil), payload...),
		CreatedAt: time.Now().Format(time.RFC3339Nano),
	}

	httpResp, err := p.httpClient.Do(httpReq)
	if err != nil {
		trace.Error = err.Error()
		appendProviderTraceSnapshot(trace)
		return runtime.Response{}, NewRetryableError(fmt.Errorf("send request: %w", err))
	}
	defer httpResp.Body.Close()

	responseBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		trace.Status = httpResp.StatusCode
		trace.Error = err.Error()
		appendProviderTraceSnapshot(trace)
		return runtime.Response{}, NewRetryableError(fmt.Errorf("read response body: %w", err))
	}
	trace.Status = httpResp.StatusCode
	trace.Response = append(json.RawMessage(nil), responseBody...)
	appendProviderTraceSnapshot(trace)

	if httpResp.StatusCode == http.StatusTooManyRequests {
		return runtime.Response{}, NewQuotaExceededError(fmt.Errorf("upstream quota exceeded: %s", previewResponseBody(responseBody)))
	}
	if httpResp.StatusCode >= http.StatusInternalServerError {
		return runtime.Response{}, NewRetryableError(fmt.Errorf("upstream server error: %s body=%s", httpResp.Status, previewResponseBody(responseBody)))
	}
	if httpResp.StatusCode >= http.StatusBadRequest {
		return runtime.Response{}, NewFatalError(fmt.Errorf("upstream request failed: %s body=%s", httpResp.Status, previewResponseBody(responseBody)))
	}

	var resp anthropicMessagesEnvelope
	if err := json.Unmarshal(responseBody, &resp); err != nil {
		return runtime.Response{}, NewRetryableError(fmt.Errorf("decode response: %w", err))
	}
	return decodeAnthropicMessagesResponse(resp)
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

	protocol := "openai_chat"
	if p.mode == openAIProtocolModeResponses {
		protocol = "openai_responses"
	}
	trace := providerTraceSnapshot{
		RequestID: requestIDFromContext(ctx),
		Provider:  p.providerName,
		Protocol:  protocol,
		Method:    http.MethodPost,
		URL:       httpReq.URL.String(),
		Headers: redactHeaders(map[string]string{
			"Content-Type":  httpReq.Header.Get("Content-Type"),
			"Authorization": httpReq.Header.Get("Authorization"),
		}),
		Request:   append(json.RawMessage(nil), payload...),
		CreatedAt: time.Now().Format(time.RFC3339Nano),
	}

	httpResp, err := p.httpClient.Do(httpReq)
	if err != nil {
		trace.Error = err.Error()
		appendProviderTraceSnapshot(trace)
		return runtime.Response{}, NewRetryableError(fmt.Errorf("send request: %w", err))
	}
	defer httpResp.Body.Close()

	responseBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		trace.Status = httpResp.StatusCode
		trace.Error = err.Error()
		appendProviderTraceSnapshot(trace)
		return runtime.Response{}, NewRetryableError(fmt.Errorf("read response body: %w", err))
	}
	trace.Status = httpResp.StatusCode
	trace.Response = append(json.RawMessage(nil), responseBody...)
	appendProviderTraceSnapshot(trace)

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
	messages := make([]openAIChatMessage, 0, len(req.Messages)+1)
	if req.System != "" {
		messages = append(messages, openAIChatMessage{
			Role:    string(runtime.MessageRoleSystem),
			Content: req.System,
		})
	}
	for _, msg := range req.Messages {
		encoded := openAIChatMessage{
			Role:       string(msg.Role),
			Content:    joinedTextParts(msg),
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

	payload := openAIChatRequest{
		Model:    req.Model,
		Messages: messages,
	}
	if req.MaxTokens > 0 {
		payload.MaxTokens = req.MaxTokens
	}
	if len(req.Tools) > 0 {
		payload.Tools = make([]openAIToolDefinition, 0, len(req.Tools))
		for _, tool := range req.Tools {
			if shouldDropOpenAIChatTool(tool) {
				continue
			}
			payload.Tools = append(payload.Tools, openAIToolDefinition{
				Type: "function",
				Function: openAIToolSpecDefinition{
					Name:        tool.Name,
					Description: tool.Description,
					Parameters:  normalizedToolSchema(tool.InputSchema),
					Strict:      true,
				},
			})
		}
		if len(payload.Tools) > 0 {
			payload.ToolChoice = "auto"
		}
	}
	return payload
}

func shouldDropOpenAIChatTool(tool runtime.ToolDefinition) bool {
	if _, ok := claudeCodeControlToolNames[tool.Name]; ok {
		return true
	}
	if isClaudeCodeBuiltinToolName(tool.Name) {
		return true
	}

	var schema map[string]any
	if len(tool.InputSchema) > 0 && json.Unmarshal(tool.InputSchema, &schema) == nil {
		if kind, _ := schema["type"].(string); kind != "" && kind != "object" {
			return true
		}
	}

	return false
}

func isClaudeCodeBuiltinToolName(name string) bool {
	switch name {
	case "Agent", "AskUserQuestion", "Bash", "CronCreate", "CronDelete", "CronList", "Edit", "EnterPlanMode", "EnterWorktree", "ExitPlanMode", "ExitWorktree", "Glob", "Grep", "LSP", "NotebookEdit", "Read", "ScheduleWakeup", "Skill", "TaskCreate", "TaskGet", "TaskList", "TaskOutput", "TaskStop", "TaskUpdate", "WebFetch", "WebSearch", "Write", "multi_tool_use", "parallel":
		return true
	default:
		return false
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

func encodeAnthropicMessagesRequest(req runtime.Request) any {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 1024
	}

	messages := make([]anthropicMessage, 0, len(req.Messages))
	for _, msg := range req.Messages {
		switch msg.Role {
		case runtime.MessageRoleSystem:
			if req.System == "" {
				req.System = firstTextPart(msg)
			}
		case runtime.MessageRoleTool:
			messages = append(messages, anthropicMessage{
				Role: "user",
				Content: []anthropicContentBlock{{
					Type:      "tool_result",
					ToolUseID: msg.ToolCallID,
					Content:   firstTextPart(msg),
				}},
			})
		default:
			encoded := anthropicMessage{Role: string(msg.Role)}
			for _, part := range msg.Parts {
				if part.Type == runtime.ContentPartTypeText && part.Text != "" {
					encoded.Content = append(encoded.Content, anthropicContentBlock{Type: "text", Text: part.Text})
				}
			}
			for _, call := range msg.ToolCalls {
				encoded.Content = append(encoded.Content, anthropicContentBlock{
					Type:  "tool_use",
					ID:    call.ID,
					Name:  call.Name,
					Input: json.RawMessage(compactJSONOrEmpty(json.RawMessage(call.Arguments))),
				})
			}
			if len(encoded.Content) > 0 {
				messages = append(messages, encoded)
			}
		}
	}

	payload := anthropicMessagesRequest{
		Model:     req.Model,
		System:    req.System,
		MaxTokens: maxTokens,
		Messages:  messages,
		Stream:    req.Stream,
	}
	if len(req.Tools) > 0 {
		payload.Tools = make([]anthropicToolDefinition, 0, len(req.Tools))
		for _, tool := range req.Tools {
			payload.Tools = append(payload.Tools, anthropicToolDefinition{
				Name:        tool.Name,
				Description: tool.Description,
				InputSchema: normalizedToolSchema(tool.InputSchema),
			})
		}
	}
	return payload
}

func decodeAnthropicMessagesResponse(resp anthropicMessagesEnvelope) (runtime.Response, error) {
	if len(resp.Content) == 0 {
		return runtime.Response{}, NewFatalError(fmt.Errorf("upstream response missing content"))
	}

	message := runtime.Message{Role: runtime.MessageRoleAssistant}
	if resp.Role != "" {
		message.Role = runtime.MessageRole(resp.Role)
	}
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			if block.Text != "" {
				message.Parts = append(message.Parts, runtime.ContentPart{Type: runtime.ContentPartTypeText, Text: block.Text})
			}
		case "tool_use":
			message.ToolCalls = append(message.ToolCalls, runtime.ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: compactJSONOrEmpty(block.Input),
			})
		}
	}
	if len(message.Parts) == 0 && len(message.ToolCalls) == 0 {
		return runtime.Response{}, NewFatalError(fmt.Errorf("upstream returned no content and no tool calls"))
	}

	finishReason := runtime.FinishReasonStop
	switch resp.StopReason {
	case "max_tokens":
		finishReason = runtime.FinishReasonLength
	case "end_turn", "tool_use", "":
		finishReason = runtime.FinishReasonStop
	default:
		finishReason = runtime.FinishReasonStop
	}

	response := runtime.Response{
		ID:           resp.ID,
		Object:       resp.Type,
		Model:        resp.Model,
		FinishReason: finishReason,
		Message:      message,
	}
	if resp.Usage != nil {
		response.Usage = &runtime.Usage{
			InputTokens:  resp.Usage.InputTokens,
			OutputTokens: resp.Usage.OutputTokens,
			TotalTokens:  resp.Usage.InputTokens + resp.Usage.OutputTokens,
		}
	}
	return response, nil
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
