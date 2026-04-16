package provider

import (
	"encoding/json"
	"fmt"

	"syrogo/internal/runtime"
)

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
