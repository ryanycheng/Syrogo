package gateway

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"syrogo/internal/config"
	"syrogo/internal/eventstream"
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
	all := make([]eventstream.Event, 0, len(events))
	for _, event := range events {
		converted := runtimeStreamEventToEventFrames(event)
		all = append(all, converted...)
	}
	frames := make([]anthropicSSEFrame, 0, len(all)+2)
	for frame := range anthropicStreamFramesFromEventStream(sliceEventStream(all)) {
		frames = append(frames, frame)
	}
	return frames
}

func runtimeStreamEventToEventFrames(event runtime.StreamEvent) []eventstream.Event {
	ch := make(chan runtime.StreamEvent, 1)
	ch <- event
	close(ch)
	frames := make([]eventstream.Event, 0, 4)
	for converted := range eventstream.EventStreamFromRuntime(ch) {
		frames = append(frames, converted)
	}
	return frames
}

func sliceEventStream(events []eventstream.Event) <-chan eventstream.Event {
	ch := make(chan eventstream.Event, len(events))
	for _, event := range events {
		ch <- event
	}
	close(ch)
	return ch
}

func anthropicStreamFramesFromEventStream(events <-chan eventstream.Event) <-chan anthropicSSEFrame {
	frames := make(chan anthropicSSEFrame, 16)
	go func() {
		defer close(frames)
		usage := &runtime.Usage{}
		messageID := ""
		model := ""
		role := runtime.MessageRoleAssistant
		finishReason := eventstream.StopReasonEndTurn
		messageStarted := false
		toolArgumentSnapshots := map[int]string{}
		emitMessageStart := func() {
			if messageStarted {
				return
			}
			messageStarted = true
			frames <- anthropicSSEFrame{event: "message_start", payload: map[string]any{
				"type": "message_start",
				"message": map[string]any{
					"id":            messageID,
					"type":          "message",
					"role":          string(role),
					"model":         model,
					"content":       []any{},
					"stop_reason":   nil,
					"stop_sequence": nil,
					"usage":         map[string]any{"input_tokens": usage.InputTokens, "output_tokens": 0},
				},
			}}
		}
		for event := range events {
			if event.MessageID != "" {
				messageID = event.MessageID
			}
			if event.Model != "" {
				model = event.Model
			}
			if event.Role != "" {
				role = event.Role
			}
			if event.Usage != nil {
				usage = event.Usage
			}
			switch event.Type {
			case eventstream.EventTypeMessageStart:
				continue
			case eventstream.EventTypeContentBlockStart:
				emitMessageStart()
				if event.Block == nil {
					continue
				}
				contentBlock := map[string]any{"type": string(event.Block.Type)}
				switch event.Block.Type {
				case eventstream.BlockTypeText:
					contentBlock["text"] = ""
				case eventstream.BlockTypeToolUse:
					contentBlock["input"] = map[string]any{}
					toolArgumentSnapshots[event.BlockIndex] = ""
					if event.Block.ToolCall != nil {
						contentBlock["id"] = event.Block.ToolCall.ID
						contentBlock["name"] = event.Block.ToolCall.Name
					}
				case eventstream.BlockTypeJSON:
					contentBlock["text"] = string(event.Block.Data)
				}
				frames <- anthropicSSEFrame{event: "content_block_start", payload: map[string]any{"type": "content_block_start", "index": event.BlockIndex, "content_block": contentBlock}}
			case eventstream.EventTypeContentBlockDelta:
				emitMessageStart()
				if event.Block != nil && event.Block.Type == eventstream.BlockTypeText {
					frames <- anthropicSSEFrame{event: "content_block_delta", payload: map[string]any{"type": "content_block_delta", "index": event.BlockIndex, "delta": map[string]any{"type": "text_delta", "text": event.TextDelta}}}
					continue
				}
				if event.Block != nil && event.Block.Type == eventstream.BlockTypeToolUse && event.ToolCall != nil && event.ToolCall.Arguments != "" {
					previous := toolArgumentSnapshots[event.BlockIndex]
					partial := event.ToolCall.Arguments
					if strings.HasPrefix(partial, previous) {
						partial = strings.TrimPrefix(partial, previous)
					}
					toolArgumentSnapshots[event.BlockIndex] = event.ToolCall.Arguments
					if partial == "" {
						continue
					}
					frames <- anthropicSSEFrame{event: "content_block_delta", payload: map[string]any{"type": "content_block_delta", "index": event.BlockIndex, "delta": map[string]any{"type": "input_json_delta", "partial_json": partial}}}
				}
			case eventstream.EventTypeContentBlockStop:
				emitMessageStart()
				delete(toolArgumentSnapshots, event.BlockIndex)
				frames <- anthropicSSEFrame{event: "content_block_stop", payload: map[string]any{"type": "content_block_stop", "index": event.BlockIndex}}
			case eventstream.EventTypeUsage:
				emitMessageStart()
			case eventstream.EventTypeMessageDelta:
				emitMessageStart()
				if event.FinishReason != "" {
					finishReason = event.FinishReason
				}
				frames <- anthropicSSEFrame{event: "message_delta", payload: map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": anthropicStopReason(eventstream.StopReasonToRuntime(finishReason)), "stop_sequence": nil}, "usage": map[string]any{"input_tokens": usage.InputTokens, "output_tokens": usage.OutputTokens}}}
			case eventstream.EventTypeMessageStop:
				emitMessageStart()
				frames <- anthropicSSEFrame{event: "message_stop", payload: map[string]any{"type": "message_stop"}}
			case eventstream.EventTypeError:
				emitMessageStart()
				message := "stream error"
				if event.Err != nil {
					message = event.Err.Error()
				}
				frames <- anthropicSSEFrame{event: "error", payload: map[string]any{"type": "error", "error": map[string]any{"message": message}}}
			}
		}
	}()
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
