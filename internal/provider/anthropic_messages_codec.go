package provider

import (
	"encoding/json"
	"fmt"

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
	IsError   bool            `json:"is_error,omitempty"`
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
					IsError:   msg.ToolResultIsError,
					Content:   anthropicToolResultContent(msg),
				}},
			})
		default:
			encoded := anthropicMessage{Role: string(msg.Role)}
			for _, part := range msg.Parts {
				switch part.Type {
				case runtime.ContentPartTypeText:
					if part.Text != "" {
						encoded.Content = append(encoded.Content, anthropicContentBlock{Type: "text", Text: part.Text})
					}
				case runtime.ContentPartTypeJSON:
					encoded.Content = append(encoded.Content, anthropicContentBlock{Type: "text", Text: string(part.Data)})
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

	finishReason := anthropicFinishReason(resp.StopReason)
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

func anthropicToolResultContent(msg runtime.Message) any {
	blocks := make([]map[string]any, 0, len(msg.Parts))
	for _, part := range msg.Parts {
		switch part.Type {
		case runtime.ContentPartTypeText:
			blocks = append(blocks, map[string]any{"type": "text", "text": part.Text})
		case runtime.ContentPartTypeJSON:
			var value any
			if err := json.Unmarshal(part.Data, &value); err != nil {
				blocks = append(blocks, map[string]any{"type": "text", "text": string(part.Data)})
				continue
			}
			blocks = append(blocks, map[string]any{"type": "json", "value": value})
		}
	}
	if len(blocks) == 1 && blocks[0]["type"] == "text" {
		return blocks[0]["text"]
	}
	if len(blocks) == 0 {
		return ""
	}
	return blocks
}

func anthropicFinishReason(reason string) runtime.FinishReason {
	switch reason {
	case "tool_use":
		return runtime.FinishReasonToolUse
	case "max_tokens":
		return runtime.FinishReasonLength
	case "end_turn", "":
		return runtime.FinishReasonEndTurn
	default:
		return runtime.FinishReasonStop
	}
}
