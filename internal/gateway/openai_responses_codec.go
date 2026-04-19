package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"syrogo/internal/config"
	"syrogo/internal/runtime"
	"syrogo/internal/semantic"
)

type openAIResponsesCodec struct{}

type openAIResponsesRequest struct {
	Model              string          `json:"model"`
	Input              json.RawMessage `json:"input"`
	Stream             bool            `json:"stream"`
	PreviousResponseID string          `json:"previous_response_id,omitempty"`
}

type openAIResponsesInputItem struct {
	Type    string          `json:"type"`
	Role    string          `json:"role,omitempty"`
	Content json.RawMessage `json:"content,omitempty"`
	CallID  string          `json:"call_id,omitempty"`
	Name    string          `json:"name,omitempty"`
	Input   json.RawMessage `json:"input,omitempty"`
	Output  json.RawMessage `json:"output,omitempty"`
	Status  string          `json:"status,omitempty"`
}

type openAIResponsesContentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type openAIResponsesSSEFrame struct {
	event   string
	payload any
}

func (openAIResponsesCodec) Handle(h *Handler, w http.ResponseWriter, r *http.Request, inbound config.InboundSpec, client config.ClientSpec, logger *slog.Logger) {
	if r.Method != http.MethodPost {
		logger.Warn("request rejected", slog.String("reason", "method not allowed"))
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		logger.Warn("request body read failed", slog.Any("error", err))
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}

	var req openAIResponsesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		logger.Warn("request decode failed",
			slog.Any("error", err),
			slog.String("body_preview", previewBody(body)),
		)
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if req.Model == "" {
		logger.Warn("request validation failed", slog.String("reason", "model is required"))
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	if len(req.Input) == 0 {
		logger.Warn("request validation failed",
			slog.String("model", req.Model),
			slog.String("reason", "input is required"),
		)
		writeError(w, http.StatusBadRequest, "input is required")
		return
	}

	internalReq, err := buildRuntimeRequestFromResponses(req)
	if err != nil {
		logger.Warn("request normalize failed",
			slog.String("model", req.Model),
			slog.Any("error", err),
			slog.String("body_preview", previewBody(body)),
		)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	plan, err := h.planRequest(internalReq, inbound, client)
	if err != nil {
		logger.Warn("request routing failed",
			slog.String("model", req.Model),
			slog.Any("error", err),
		)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	logger.Info("request routed",
		slog.String("requested_model", req.Model),
		slog.String("planned_model", plannedModel(plan)),
		slog.String("matched_rule", plan.MatchedRule),
		slog.String("resolved_to", strings.Join(plan.ResolvedToTags, ",")),
		slog.Bool("stream", req.Stream),
	)

	if req.Stream {
		h.handleOpenAIResponsesStreaming(w, r, internalReq, plan, logger)
		return
	}

	resp, ok := dispatchOrWriteError(h, w, r, internalReq, plan, logger)
	if !ok {
		return
	}
	writeOpenAIResponsesResponse(w, resp)
}

func buildRuntimeRequestFromResponses(req openAIResponsesRequest) (runtime.Request, error) {
	semanticReq, err := buildSemanticRequestFromResponses(req)
	if err != nil {
		return runtime.Request{}, err
	}
	return lowerSemanticRequest(semanticReq), nil
}

func buildSemanticRequestFromResponses(req openAIResponsesRequest) (semantic.Request, error) {
	turns, err := parseOpenAIResponsesInput(req.Input)
	if err != nil {
		return semantic.Request{}, err
	}
	return semantic.Request{
		Model: req.Model,
		Turns: turns,
		Options: semantic.GenerateOptions{
			Stream:             req.Stream,
			PreviousResponseID: req.PreviousResponseID,
		},
	}, nil
}

func parseOpenAIResponsesInput(raw json.RawMessage) ([]semantic.Turn, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return []semantic.Turn{{Role: semantic.RoleUser, Segments: []semantic.Segment{{Kind: semantic.SegmentText, Text: text}}}}, nil
	}

	var items []openAIResponsesInputItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("unsupported responses input")
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("input is required")
	}

	turns := make([]semantic.Turn, 0, len(items))
	for _, item := range items {
		switch item.Type {
		case "message":
			segments, err := parseOpenAIResponsesMessageContent(item.Content)
			if err != nil {
				return nil, err
			}
			role := semantic.Role(item.Role)
			if role == "" {
				role = semantic.RoleUser
			}
			turns = append(turns, semantic.Turn{Role: role, Segments: segments})
		case "function_call":
			turns = append(turns, semantic.Turn{Role: semantic.RoleAssistant, Segments: []semantic.Segment{{Kind: semantic.SegmentToolCall, ToolCall: &semantic.ToolCall{ID: item.CallID, Name: item.Name, Arguments: normalizedJSONOrRaw(item.Input)}}}})
		case "function_call_output":
			content, err := parseOpenAIResponsesFunctionOutput(item.Output)
			if err != nil {
				return nil, err
			}
			if item.CallID == "" {
				return nil, fmt.Errorf("function_call_output.call_id is required")
			}
			turns = append(turns, semantic.Turn{Role: semantic.RoleTool, Segments: []semantic.Segment{{Kind: semantic.SegmentToolResult, ToolResult: &semantic.ToolResult{
				ToolCallID: item.CallID,
				Content:    content,
				IsError:    item.Status == "error",
			}}}})
		default:
			return nil, fmt.Errorf("unsupported responses input item type %q", item.Type)
		}
	}
	return turns, nil
}

func parseOpenAIResponsesMessageContent(raw json.RawMessage) ([]semantic.Segment, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return []semantic.Segment{{Kind: semantic.SegmentText, Text: text}}, nil
	}

	var parts []openAIResponsesContentPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, fmt.Errorf("unsupported responses message content")
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("message content must include at least one text part")
	}

	result := make([]semantic.Segment, 0, len(parts))
	for _, part := range parts {
		if part.Type != "input_text" && part.Type != "output_text" && part.Type != "text" {
			return nil, fmt.Errorf("unsupported responses content part type %q", part.Type)
		}
		result = append(result, semantic.Segment{Kind: semantic.SegmentText, Text: part.Text})
	}
	return result, nil
}

func parseOpenAIResponsesFunctionOutput(raw json.RawMessage) ([]semantic.Segment, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return []semantic.Segment{{Kind: semantic.SegmentText, Text: text}}, nil
	}
	var parts []openAIResponsesContentPart
	if err := json.Unmarshal(raw, &parts); err == nil {
		segments := make([]semantic.Segment, 0, len(parts))
		for _, part := range parts {
			if part.Type == "output_text" || part.Type == "text" || part.Type == "input_text" {
				segments = append(segments, semantic.Segment{Kind: semantic.SegmentText, Text: part.Text})
			}
		}
		if len(segments) == 0 {
			return nil, fmt.Errorf("function_call_output.output must include at least one text part")
		}
		return segments, nil
	}
	return nil, fmt.Errorf("unsupported function_call_output.output")
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

func openAIResponsesStreamFrames(plan runtime.ExecutionPlan, events <-chan runtime.StreamEvent) []openAIResponsesSSEFrame {
	frames := make([]openAIResponsesSSEFrame, 0, 8)
	textItemStarted := false
	messageItemID := "msg_0"
	messageOutputIndex := 0
	toolOutputIndex := 1
	responseID := ""
	model := plannedModel(plan)
	role := string(runtime.MessageRoleAssistant)
	text := ""
	toolCalls := make([]runtime.ToolCall, 0)
	var usage *runtime.Usage
	sequence := 1

	appendFrame := func(event string, payload map[string]any) {
		payload["type"] = event
		payload["sequence_number"] = sequence
		sequence++
		frames = append(frames, openAIResponsesSSEFrame{event: event, payload: payload})
	}

	responseObject := func(status string, output []map[string]any, completed bool) map[string]any {
		body := map[string]any{
			"id":                   responseID,
			"object":               "response",
			"status":               status,
			"model":                model,
			"output":               output,
			"parallel_tool_calls":  true,
			"previous_response_id": nil,
			"tools":                []any{},
			"metadata":             map[string]any{},
		}
		if usage != nil {
			body["usage"] = map[string]any{
				"input_tokens":  usage.InputTokens,
				"output_tokens": usage.OutputTokens,
				"total_tokens":  usage.TotalTokens,
			}
		} else {
			body["usage"] = nil
		}
		if completed {
			body["completed_at"] = 0
		} else {
			body["completed_at"] = nil
		}
		return body
	}

	messageItem := func(status string) map[string]any {
		return map[string]any{
			"id":     messageItemID,
			"status": status,
			"type":   "message",
			"role":   role,
			"content": []map[string]any{{
				"type":        "output_text",
				"text":        text,
				"annotations": []any{},
			}},
		}
	}

	functionCallItem := func(call runtime.ToolCall, status string) map[string]any {
		return map[string]any{
			"id":        nonEmpty(call.ID, fmt.Sprintf("call_%d", len(toolCalls))),
			"status":    status,
			"type":      "function_call",
			"call_id":   call.ID,
			"name":      call.Name,
			"arguments": call.Arguments,
		}
	}

	appendFrame("response.created", map[string]any{
		"response": responseObject("in_progress", []map[string]any{}, false),
	})
	appendFrame("response.in_progress", map[string]any{
		"response": responseObject("in_progress", []map[string]any{}, false),
	})

	for event := range events {
		if event.ResponseID != "" {
			responseID = event.ResponseID
		}
		if event.Model != "" {
			model = event.Model
		}
		if event.MessageRole != "" {
			role = string(event.MessageRole)
		}
		switch event.Type {
		case runtime.StreamEventMessageStart:
			appendFrame("response.output_item.added", map[string]any{
				"output_index": messageOutputIndex,
				"item": map[string]any{
					"id":      messageItemID,
					"status":  "in_progress",
					"type":    "message",
					"role":    role,
					"content": []map[string]any{},
				},
			})
		case runtime.StreamEventContentDelta:
			if event.Delta != nil {
				if !textItemStarted {
					appendFrame("response.content_part.added", map[string]any{
						"output_index":  messageOutputIndex,
						"item_id":       messageItemID,
						"content_index": 0,
						"part": map[string]any{
							"type":        "output_text",
							"text":        "",
							"annotations": []any{},
						},
					})
					textItemStarted = true
				}
				text += event.Delta.Text
				appendFrame("response.output_text.delta", map[string]any{
					"output_index":  messageOutputIndex,
					"item_id":       messageItemID,
					"content_index": 0,
					"delta":         event.Delta.Text,
				})
			}
			if event.ToolCall != nil {
				call := *event.ToolCall
				toolCalls = append(toolCalls, call)
				itemID := nonEmpty(call.ID, fmt.Sprintf("call_%d", event.ToolCallIndex))
				appendFrame("response.output_item.added", map[string]any{
					"output_index": toolOutputIndex + event.ToolCallIndex,
					"item": map[string]any{
						"id":        itemID,
						"status":    "in_progress",
						"type":      "function_call",
						"call_id":   call.ID,
						"name":      call.Name,
						"arguments": "",
					},
				})
				appendFrame("response.function_call_arguments.done", map[string]any{
					"output_index": toolOutputIndex + event.ToolCallIndex,
					"item_id":      itemID,
					"name":         call.Name,
					"arguments":    call.Arguments,
				})
				appendFrame("response.output_item.done", map[string]any{
					"output_index": toolOutputIndex + event.ToolCallIndex,
					"item":         functionCallItem(call, "completed"),
				})
			}
		case runtime.StreamEventUsage:
			if event.Usage != nil {
				usage = event.Usage
				appendFrame("response.usage", map[string]any{
					"usage": map[string]any{
						"input_tokens":  event.Usage.InputTokens,
						"output_tokens": event.Usage.OutputTokens,
						"total_tokens":  event.Usage.TotalTokens,
					},
				})
			}
		case runtime.StreamEventMessageEnd:
			if textItemStarted {
				appendFrame("response.output_text.done", map[string]any{
					"output_index":  messageOutputIndex,
					"item_id":       messageItemID,
					"content_index": 0,
					"text":          text,
				})
				appendFrame("response.content_part.done", map[string]any{
					"output_index":  messageOutputIndex,
					"item_id":       messageItemID,
					"content_index": 0,
					"part": map[string]any{
						"type":        "output_text",
						"text":        text,
						"annotations": []any{},
					},
				})
			}
			output := make([]map[string]any, 0, 1+len(toolCalls))
			if textItemStarted || len(toolCalls) == 0 {
				output = append(output, messageItem("completed"))
			}
			for _, call := range toolCalls {
				output = append(output, functionCallItem(call, "completed"))
			}
			appendFrame("response.output_item.done", map[string]any{
				"output_index": messageOutputIndex,
				"item":         messageItem("completed"),
			})
			appendFrame("response.completed", map[string]any{
				"response": responseObject("completed", output, true),
			})
		case runtime.StreamEventError:
			message := "stream error"
			if event.Err != nil {
				message = event.Err.Error()
			}
			appendFrame("error", map[string]any{"message": message})
		}
	}

	return frames
}
