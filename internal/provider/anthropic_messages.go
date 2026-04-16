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
	return streamResponse(resp), nil
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
