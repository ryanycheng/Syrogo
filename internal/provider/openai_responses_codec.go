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
	Input     any                       `json:"input,omitempty"`
	Output    string                    `json:"output,omitempty"`
	Status    string                    `json:"status,omitempty"`
}

type openAIResponsesTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Format      json.RawMessage `json:"format,omitempty"`
}

type openAIResponsesRequest struct {
	Model              string                     `json:"model"`
	Instructions       string                     `json:"instructions,omitempty"`
	MaxOutputTokens    int                        `json:"max_output_tokens,omitempty"`
	Input              []openAIResponsesInputItem `json:"input,omitempty"`
	Tools              []any                      `json:"tools,omitempty"`
	ToolChoice         string                     `json:"tool_choice,omitempty"`
	PreviousResponseID string                     `json:"previous_response_id,omitempty"`
	Metadata           json.RawMessage            `json:"metadata,omitempty"`
	Reasoning          *openAIResponsesReasoning  `json:"reasoning,omitempty"`
	ContextManagement  json.RawMessage            `json:"context_management,omitempty"`
}

type openAIResponsesReasoning struct {
	Effort string `json:"effort,omitempty"`
}

type openAIResponsesOutputItem struct {
	Type      string                    `json:"type"`
	Role      string                    `json:"role,omitempty"`
	Content   []openAIResponsesTextPart `json:"content,omitempty"`
	CallID    string                    `json:"call_id,omitempty"`
	Name      string                    `json:"name,omitempty"`
	Arguments string                    `json:"arguments,omitempty"`
	Input     json.RawMessage           `json:"input,omitempty"`
}

type openAIResponsesEnvelope struct {
	ID     string                      `json:"id"`
	Object string                      `json:"object"`
	Model  string                      `json:"model"`
	Status string                      `json:"status,omitempty"`
	Output []openAIResponsesOutputItem `json:"output"`
	Usage  *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage,omitempty"`
}

func encodeOpenAIResponsesRequest(req runtime.Request, compat openAIResponsesCompatibility) any {
	input := make([]openAIResponsesInputItem, 0, len(req.Messages))
	for _, msg := range req.Messages {
		switch {
		case msg.Role == runtime.MessageRoleTool:
			itemType := "function_call_output"
			if msg.ToolCallType == "custom" {
				itemType = "custom_tool_call_output"
			}
			encoded := openAIResponsesInputItem{
				Type:   itemType,
				CallID: msg.ToolCallID,
				Output: joinedToolResultParts(msg),
			}
			if msg.ToolResultIsError && !compat.DropToolErrorStatus {
				encoded.Status = "error"
			}
			input = append(input, encoded)
		case len(msg.ToolCalls) > 0:
			for _, call := range msg.ToolCalls {
				encoded := openAIResponsesInputItem{
					Type:   "function_call",
					CallID: call.ID,
					Name:   call.Name,
				}
				if call.Type == "custom" {
					encoded.Type = "custom_tool_call"
					encoded.Input = call.Input
				} else {
					encoded.Arguments = compactJSONOrEmpty(json.RawMessage(call.Arguments))
				}
				input = append(input, encoded)
			}
		default:
			role := string(msg.Role)
			text := joinedTextParts(msg)
			if compat.RewriteAssistantToUser && msg.Role == runtime.MessageRoleAssistant {
				role = string(runtime.MessageRoleUser)
				text = "Previous assistant message:\n" + text
			}
			input = append(input, openAIResponsesInputItem{
				Type: "message",
				Role: role,
				Content: []openAIResponsesTextPart{{
					Type: "input_text",
					Text: text,
				}},
			})
		}
	}

	payload := openAIResponsesRequest{
		Model: req.Model,
		Input: input,
	}
	if req.PreviousResponseID != "" {
		payload.PreviousResponseID = req.PreviousResponseID
	}
	if len(req.Metadata) > 0 && !compat.DropMetadata {
		payload.Metadata = append(json.RawMessage(nil), req.Metadata...)
	}
	if req.OutputEffort != "" {
		payload.Reasoning = &openAIResponsesReasoning{Effort: req.OutputEffort}
	}
	if len(req.ContextManagement) > 0 && !compat.DropContextManagement {
		payload.ContextManagement = append(json.RawMessage(nil), req.ContextManagement...)
	}
	if req.System != "" {
		payload.Instructions = req.System
	}
	if req.MaxTokens > 0 {
		payload.MaxOutputTokens = req.MaxTokens
	}
	if len(req.Tools) > 0 {
		payload.Tools = make([]any, 0, len(req.Tools))
		for _, tool := range req.Tools {
			if shouldDropOpenAIResponsesTool(tool) {
				continue
			}
			toolType := tool.Type
			if toolType == "" {
				toolType = "function"
			}
			switch toolType {
			case "function":
				payload.Tools = append(payload.Tools, openAIResponsesTool{
					Type:        "function",
					Name:        tool.Name,
					Description: tool.Description,
					Parameters:  normalizedToolSchema(tool.InputSchema),
				})
			case "custom":
				payload.Tools = append(payload.Tools, openAIResponsesTool{
					Type:        "custom",
					Name:        tool.Name,
					Description: tool.Description,
					Format:      append(json.RawMessage(nil), tool.Format...),
				})
			default:
				if len(tool.Raw) > 0 {
					var raw any
					if err := json.Unmarshal(tool.Raw, &raw); err == nil {
						payload.Tools = append(payload.Tools, raw)
						continue
					}
				}
				payload.Tools = append(payload.Tools, map[string]any{"type": toolType})
			}
		}
		if len(payload.Tools) > 0 {
			payload.ToolChoice = "auto"
		}
	}
	return payload
}

func shouldDropOpenAIResponsesTool(tool runtime.ToolDefinition) bool {
	if tool.Type == "custom" {
		return false
	}
	var schema map[string]any
	if len(tool.InputSchema) > 0 && json.Unmarshal(tool.InputSchema, &schema) == nil {
		if kind, _ := schema["type"].(string); kind != "" && kind != "object" {
			return true
		}
	}

	return false
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
			arguments := item.Arguments
			if arguments == "" && len(item.Input) > 0 {
				arguments = compactJSONOrEmpty(item.Input)
			}
			message.ToolCalls = append(message.ToolCalls, runtime.ToolCall{ID: item.CallID, Type: "function", Name: item.Name, Arguments: arguments})
		case "custom_tool_call":
			var input string
			if len(item.Input) > 0 {
				if err := json.Unmarshal(item.Input, &input); err != nil {
					input = string(item.Input)
				}
			}
			message.ToolCalls = append(message.ToolCalls, runtime.ToolCall{ID: item.CallID, Type: "custom", Name: item.Name, Input: input})
		}
	}
	if len(message.Parts) == 0 && len(message.ToolCalls) == 0 {
		return runtime.Response{}, NewFatalError(fmt.Errorf("upstream returned no content and no tool calls"))
	}

	finishReason := runtime.FinishReasonStop
	if len(message.ToolCalls) > 0 {
		finishReason = runtime.FinishReasonToolUse
	}
	switch resp.Status {
	case "incomplete":
		if len(message.ToolCalls) == 0 {
			finishReason = runtime.FinishReasonLength
		}
	case "completed", "", "in_progress":
		if len(message.ToolCalls) == 0 {
			finishReason = runtime.FinishReasonStop
		}
	}

	response := runtime.Response{
		ID:           resp.ID,
		Object:       resp.Object,
		Model:        resp.Model,
		FinishReason: finishReason,
		Message:      message,
	}
	if resp.Usage != nil {
		response.Usage = &runtime.Usage{InputTokens: resp.Usage.InputTokens, OutputTokens: resp.Usage.OutputTokens, TotalTokens: resp.Usage.TotalTokens}
	}
	return response, nil
}
