package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"

	"syrogo/internal/runtime"
)

func writeOpenAIChatResponse(w http.ResponseWriter, resp runtime.Response) {
	message := map[string]any{
		"role":    string(resp.Message.Role),
		"content": firstTextPart(resp.Message),
	}
	if len(resp.Message.ToolCalls) > 0 {
		toolCalls := make([]map[string]any, 0, len(resp.Message.ToolCalls))
		for _, call := range resp.Message.ToolCalls {
			toolCalls = append(toolCalls, map[string]any{
				"id":   call.ID,
				"type": "function",
				"function": map[string]any{
					"name":      call.Name,
					"arguments": call.Arguments,
				},
			})
		}
		message["tool_calls"] = toolCalls
	}
	if resp.Message.ToolCallID != "" {
		message["tool_call_id"] = resp.Message.ToolCallID
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":     resp.ID,
		"object": resp.Object,
		"model":  resp.Model,
		"choices": []map[string]any{{
			"index":   0,
			"message": message,
		}},
	})
}

func writeOpenAIResponsesResponse(w http.ResponseWriter, resp runtime.Response) {
	output := buildOpenAIResponsesOutput(resp)
	body := map[string]any{
		"id":     resp.ID,
		"object": nonEmpty(resp.Object, "response"),
		"model":  resp.Model,
		"output": output,
	}
	if resp.Usage != nil {
		body["usage"] = map[string]any{
			"input_tokens":  resp.Usage.InputTokens,
			"output_tokens": resp.Usage.OutputTokens,
			"total_tokens":  resp.Usage.TotalTokens,
		}
	}
	writeJSON(w, http.StatusOK, body)
}

func buildOpenAIResponsesOutput(resp runtime.Response) []map[string]any {
	output := make([]map[string]any, 0, 1+len(resp.Message.ToolCalls))
	messageContent := make([]map[string]any, 0, len(resp.Message.Parts))
	for _, part := range resp.Message.Parts {
		if part.Type != runtime.ContentPartTypeText {
			continue
		}
		messageContent = append(messageContent, map[string]any{
			"type": "output_text",
			"text": part.Text,
		})
	}
	if len(messageContent) > 0 {
		output = append(output, map[string]any{
			"type":    "message",
			"role":    string(resp.Message.Role),
			"content": messageContent,
		})
	}
	for _, call := range resp.Message.ToolCalls {
		var input any
		if err := json.Unmarshal([]byte(call.Arguments), &input); err != nil {
			input = map[string]any{}
		}
		output = append(output, map[string]any{
			"type":    "function_call",
			"call_id": call.ID,
			"name":    call.Name,
			"input":   input,
		})
	}
	if len(output) == 0 {
		output = append(output, map[string]any{
			"type": "message",
			"role": string(resp.Message.Role),
			"content": []map[string]any{{
				"type": "output_text",
				"text": "",
			}},
		})
	}
	return output
}

func writeAnthropicMessageResponse(w http.ResponseWriter, resp runtime.Response) {
	content := make([]map[string]any, 0, len(resp.Message.Parts)+len(resp.Message.ToolCalls))
	for _, part := range resp.Message.Parts {
		switch part.Type {
		case runtime.ContentPartTypeText:
			content = append(content, map[string]any{
				"type": "text",
				"text": part.Text,
			})
		case runtime.ContentPartTypeJSON:
			content = append(content, map[string]any{
				"type": "text",
				"text": string(part.Data),
			})
		}
	}
	for _, call := range resp.Message.ToolCalls {
		var input any
		if err := json.Unmarshal([]byte(call.Arguments), &input); err != nil {
			input = map[string]any{}
		}
		content = append(content, map[string]any{
			"type":  "tool_use",
			"id":    call.ID,
			"name":  call.Name,
			"input": input,
		})
	}
	if len(content) == 0 {
		content = append(content, map[string]any{"type": "text", "text": ""})
	}

	body := map[string]any{
		"id":          resp.ID,
		"type":        "message",
		"role":        string(resp.Message.Role),
		"model":       resp.Model,
		"content":     content,
		"stop_reason": anthropicStopReason(resp.FinishReason),
	}
	if resp.Usage != nil {
		body["usage"] = map[string]any{
			"input_tokens":  resp.Usage.InputTokens,
			"output_tokens": resp.Usage.OutputTokens,
		}
	}
	writeJSON(w, http.StatusOK, body)
}

func openAIStreamChunk(event runtime.StreamEvent) any {
	chunk := map[string]any{
		"id":     event.ResponseID,
		"object": "chat.completion.chunk",
		"model":  event.Model,
	}

	switch event.Type {
	case runtime.StreamEventMessageStart:
		chunk["choices"] = []map[string]any{{
			"index": 0,
			"delta": map[string]any{"role": string(event.MessageRole)},
		}}
	case runtime.StreamEventContentDelta:
		delta := map[string]any{}
		if event.Delta != nil {
			delta["content"] = event.Delta.Text
		}
		if event.ToolCall != nil {
			delta["tool_calls"] = []map[string]any{{
				"index": event.ToolCallIndex,
				"id":    event.ToolCall.ID,
				"type":  "function",
				"function": map[string]any{
					"name":      event.ToolCall.Name,
					"arguments": event.ToolCall.Arguments,
				},
			}}
		}
		chunk["choices"] = []map[string]any{{
			"index": 0,
			"delta": delta,
		}}
	case runtime.StreamEventUsage:
		chunk["usage"] = event.Usage
	case runtime.StreamEventMessageEnd:
		chunk["choices"] = []map[string]any{{
			"index":         0,
			"delta":         map[string]any{},
			"finish_reason": string(event.FinishReason),
		}}
	case runtime.StreamEventError:
		message := "stream error"
		if event.Err != nil {
			message = event.Err.Error()
		}
		chunk = map[string]any{"error": message}
	}

	return chunk
}

type openAIResponsesSSEFrame struct {
	event   string
	payload any
}

func openAIResponsesStreamPrelude(plan runtime.ExecutionPlan) []openAIResponsesSSEFrame {
	model := plannedModel(plan)
	return []openAIResponsesSSEFrame{
		{event: "response.created", payload: map[string]any{"type": "response", "response": map[string]any{"model": model, "status": "created"}}},
		{event: "response.in_progress", payload: map[string]any{"type": "response", "response": map[string]any{"model": model, "status": "in_progress"}}},
	}
}

func openAIResponsesStreamFrames(events <-chan runtime.StreamEvent) []openAIResponsesSSEFrame {
	frames := make([]openAIResponsesSSEFrame, 0, 8)
	textItemStarted := false
	toolItemsDone := make(map[string]bool)
	messageItemID := "msg_0"
	messageOutputIndex := 0
	toolOutputIndex := 1
	responseID := ""
	model := ""

	for event := range events {
		if event.ResponseID != "" {
			responseID = event.ResponseID
		}
		if event.Model != "" {
			model = event.Model
		}
		switch event.Type {
		case runtime.StreamEventMessageStart:
			frames = append(frames, openAIResponsesSSEFrame{
				event: "response.output_item.added",
				payload: map[string]any{
					"output_index": messageOutputIndex,
					"item": map[string]any{
						"id":      messageItemID,
						"type":    "message",
						"role":    string(event.MessageRole),
						"content": []map[string]any{},
					},
				},
			})
		case runtime.StreamEventContentDelta:
			if event.Delta != nil {
				if !textItemStarted {
					frames = append(frames, openAIResponsesSSEFrame{
						event: "response.content_part.added",
						payload: map[string]any{
							"output_index":  messageOutputIndex,
							"item_id":       messageItemID,
							"content_index": 0,
							"part":          map[string]any{"type": "output_text", "text": ""},
						},
					})
					textItemStarted = true
				}
				frames = append(frames, openAIResponsesSSEFrame{
					event: "response.output_text.delta",
					payload: map[string]any{
						"output_index":  messageOutputIndex,
						"item_id":       messageItemID,
						"content_index": 0,
						"delta":         event.Delta.Text,
					},
				})
			}
			if event.ToolCall != nil {
				var input any
				if err := json.Unmarshal([]byte(event.ToolCall.Arguments), &input); err != nil {
					input = map[string]any{}
				}
				frames = append(frames, openAIResponsesSSEFrame{
					event: "response.output_item.added",
					payload: map[string]any{
						"output_index": toolOutputIndex + event.ToolCallIndex,
						"item": map[string]any{
							"id":      nonEmpty(event.ToolCall.ID, fmt.Sprintf("call_%d", event.ToolCallIndex)),
							"type":    "function_call",
							"call_id": event.ToolCall.ID,
							"name":    event.ToolCall.Name,
							"input":   input,
						},
					},
				})
				toolItemsDone[event.ToolCall.ID] = false
			}
		case runtime.StreamEventUsage:
			if event.Usage != nil {
				frames = append(frames, openAIResponsesSSEFrame{event: "response.usage", payload: map[string]any{"usage": map[string]any{"input_tokens": event.Usage.InputTokens, "output_tokens": event.Usage.OutputTokens, "total_tokens": event.Usage.TotalTokens}}})
			}
		case runtime.StreamEventMessageEnd:
			if textItemStarted {
				frames = append(frames, openAIResponsesSSEFrame{
					event: "response.content_part.done",
					payload: map[string]any{
						"output_index":  messageOutputIndex,
						"item_id":       messageItemID,
						"content_index": 0,
					},
				})
			}
			frames = append(frames, openAIResponsesSSEFrame{event: "response.output_item.done", payload: map[string]any{"output_index": messageOutputIndex, "item_id": messageItemID}})
			for toolCallID, done := range toolItemsDone {
				if done {
					continue
				}
				frames = append(frames, openAIResponsesSSEFrame{event: "response.output_item.done", payload: map[string]any{"item_id": toolCallID}})
				toolItemsDone[toolCallID] = true
			}
			frames = append(frames, openAIResponsesSSEFrame{
				event: "response.completed",
				payload: map[string]any{
					"type": "response",
					"response": map[string]any{
						"id":     responseID,
						"model":  model,
						"status": "completed",
					},
				},
			})
		case runtime.StreamEventError:
			message := "stream error"
			if event.Err != nil {
				message = event.Err.Error()
			}
			frames = append(frames, openAIResponsesSSEFrame{event: "error", payload: map[string]any{"message": message}})
		}
	}

	return frames
}

type anthropicSSEFrame struct {
	event   string
	payload any
}

func anthropicStreamFrames(events []runtime.StreamEvent) []anthropicSSEFrame {
	frames := make([]anthropicSSEFrame, 0, 8)
	responseID := ""
	model := ""
	messageRole := runtime.MessageRoleAssistant
	finishReason := runtime.FinishReasonStop
	usage := &runtime.Usage{}
	textBlockIndex := -1
	nextBlockIndex := 0
	hasToolUse := false

	for _, event := range events {
		if event.ResponseID != "" {
			responseID = event.ResponseID
		}
		if event.Model != "" {
			model = event.Model
		}
		if event.MessageRole != "" {
			messageRole = event.MessageRole
		}
		if event.Usage != nil {
			usage = event.Usage
		}
		if event.Type == runtime.StreamEventMessageEnd && event.FinishReason != "" {
			finishReason = event.FinishReason
		}
		if event.ToolCall != nil {
			hasToolUse = true
		}
	}
	if hasToolUse && finishReason == runtime.FinishReasonStop {
		finishReason = runtime.FinishReasonToolUse
	}

	frames = append(frames, anthropicSSEFrame{
		event: "message_start",
		payload: map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":            responseID,
				"type":          "message",
				"role":          string(messageRole),
				"model":         model,
				"content":       []any{},
				"stop_reason":   nil,
				"stop_sequence": nil,
				"usage": map[string]any{
					"input_tokens":  usage.InputTokens,
					"output_tokens": 0,
				},
			},
		},
	})

	for _, event := range events {
		switch event.Type {
		case runtime.StreamEventContentDelta:
			if event.Delta != nil {
				if textBlockIndex == -1 {
					textBlockIndex = nextBlockIndex
					nextBlockIndex++
					frames = append(frames, anthropicSSEFrame{event: "content_block_start", payload: map[string]any{"type": "content_block_start", "index": textBlockIndex, "content_block": map[string]any{"type": "text", "text": ""}}})
				}
				frames = append(frames, anthropicSSEFrame{event: "content_block_delta", payload: map[string]any{"type": "content_block_delta", "index": textBlockIndex, "delta": map[string]any{"type": "text_delta", "text": event.Delta.Text}}})
			}
			if event.ToolCall != nil {
				var input any
				if err := json.Unmarshal([]byte(event.ToolCall.Arguments), &input); err != nil {
					input = map[string]any{}
				}
				toolIndex := nextBlockIndex
				nextBlockIndex++
				frames = append(frames,
					anthropicSSEFrame{event: "content_block_start", payload: map[string]any{"type": "content_block_start", "index": toolIndex, "content_block": map[string]any{"type": "tool_use", "id": event.ToolCall.ID, "name": event.ToolCall.Name, "input": input}}},
					anthropicSSEFrame{event: "content_block_stop", payload: map[string]any{"type": "content_block_stop", "index": toolIndex}},
				)
			}
		case runtime.StreamEventUsage:
			if event.Usage != nil {
				usage = event.Usage
			}
		case runtime.StreamEventError:
			message := "stream error"
			if event.Err != nil {
				message = event.Err.Error()
			}
			return append(frames, anthropicSSEFrame{event: "error", payload: map[string]any{"type": "error", "error": map[string]any{"message": message}}})
		}
	}

	if textBlockIndex != -1 {
		frames = append(frames, anthropicSSEFrame{event: "content_block_stop", payload: map[string]any{"type": "content_block_stop", "index": textBlockIndex}})
	}

	messageDelta := map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   anthropicStopReason(finishReason),
			"stop_sequence": nil,
		},
		"usage": map[string]any{
			"input_tokens":  usage.InputTokens,
			"output_tokens": usage.OutputTokens,
		},
	}
	frames = append(frames,
		anthropicSSEFrame{event: "message_delta", payload: messageDelta},
		anthropicSSEFrame{event: "message_stop", payload: map[string]any{"type": "message_stop"}},
	)

	return frames
}

func anthropicStopReason(reason runtime.FinishReason) string {
	switch reason {
	case runtime.FinishReasonToolUse:
		return "tool_use"
	case runtime.FinishReasonLength:
		return "max_tokens"
	case runtime.FinishReasonEndTurn, runtime.FinishReasonStop, "":
		return "end_turn"
	default:
		return "end_turn"
	}
}

func decodeJSONPart(part runtime.ContentPart) any {
	if len(part.Data) == 0 {
		return map[string]any{}
	}
	var value any
	if err := json.Unmarshal(part.Data, &value); err != nil {
		return string(part.Data)
	}
	return value
}
