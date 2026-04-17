package gateway

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"syrogo/internal/config"
	"syrogo/internal/runtime"
)

type anthropicMessagesCodec struct{}

type anthropicSSEFrame struct {
	event   string
	payload any
}

func (anthropicMessagesCodec) Handle(h *Handler, w http.ResponseWriter, r *http.Request, inbound config.InboundSpec, client config.ClientSpec, logger *slog.Logger) {
	requestID, _ := r.Context().Value(runtime.ContextKeyRequestID).(string)
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

	var req inboundRequest
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
	if len(req.Messages) == 0 {
		logger.Warn("request validation failed",
			slog.String("model", req.Model),
			slog.String("reason", "messages is required"),
		)
		writeError(w, http.StatusBadRequest, "messages is required")
		return
	}

	debugSnapshot := inboundDebugSnapshot{
		RequestID:  requestID,
		Path:       r.URL.Path,
		Inbound:    inbound.Name,
		ClientTag:  client.Tag,
		ReceivedAt: time.Now().Format(time.RFC3339Nano),
		RawBody:    append(json.RawMessage(nil), body...),
		Parsed:     debugInboundRequest(req),
	}

	internalReq, err := buildRuntimeRequest(req)
	if err != nil {
		debugSnapshot.Error = err.Error()
		if snapErr := writeInboundDebugSnapshot(debugSnapshot); snapErr != nil {
			logger.Warn("anthropic debug snapshot write failed", slog.Any("error", snapErr))
		}
		logger.Warn("request normalize failed",
			slog.String("model", req.Model),
			slog.Any("error", err),
			slog.String("body_preview", previewBody(body)),
		)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	debugSnapshot.Runtime = debugRuntimeRequest(internalReq)

	plan, err := h.planRequest(internalReq, inbound, client)
	if err != nil {
		debugSnapshot.Error = err.Error()
		if snapErr := writeInboundDebugSnapshot(debugSnapshot); snapErr != nil {
			logger.Warn("anthropic debug snapshot write failed", slog.Any("error", snapErr))
		}
		logger.Warn("request routing failed",
			slog.String("model", req.Model),
			slog.Any("error", err),
		)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	debugSnapshot.PlannedModel = plannedModel(plan)
	debugSnapshot.ResolvedTo = append([]string(nil), plan.ResolvedToTags...)
	if snapErr := writeInboundDebugSnapshot(debugSnapshot); snapErr != nil {
		logger.Warn("anthropic debug snapshot write failed", slog.Any("error", snapErr))
	}

	logger.Info("request routed",
		slog.String("requested_model", req.Model),
		slog.String("planned_model", plannedModel(plan)),
		slog.String("matched_rule", plan.MatchedRule),
		slog.String("resolved_to", strings.Join(plan.ResolvedToTags, ",")),
		slog.Bool("stream", req.Stream),
	)

	if req.Stream {
		h.handleAnthropicStreaming(w, r, internalReq, plan, logger)
		return
	}

	resp, ok := dispatchOrWriteError(h, w, r, internalReq, plan, logger)
	if !ok {
		return
	}
	writeAnthropicMessageResponse(w, resp)
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
