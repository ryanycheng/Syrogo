package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"syrogo/internal/runtime"
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
