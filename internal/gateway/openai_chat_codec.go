package gateway

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/ryanycheng/Syrogo/internal/config"
	"github.com/ryanycheng/Syrogo/internal/runtime"
)

type openAIChatCodec struct{}

type openAIChatInboundRequest struct {
	Model              string                     `json:"model"`
	System             json.RawMessage            `json:"system"`
	MaxTokens          int                        `json:"max_tokens"`
	Messages           []inboundMessage           `json:"messages"`
	Tools              []openAIChatToolDefinition `json:"tools"`
	ToolChoice         json.RawMessage            `json:"tool_choice"`
	Stream             bool                       `json:"stream"`
	PreviousResponseID string                     `json:"previous_response_id"`
	Metadata           json.RawMessage            `json:"metadata"`
	Thinking           json.RawMessage            `json:"thinking"`
	ContextManagement  json.RawMessage            `json:"context_management"`
	OutputConfig       json.RawMessage            `json:"output_config"`
}

func (openAIChatCodec) Handle(h *Handler, w http.ResponseWriter, r *http.Request, inbound config.InboundSpec, client config.ClientSpec, logger *slog.Logger) {
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

	var req openAIChatInboundRequest
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

	internalReq, err := buildRuntimeRequestFromOpenAIChat(inboundRequest{
		Model:              req.Model,
		System:             req.System,
		MaxTokens:          req.MaxTokens,
		Messages:           req.Messages,
		Tools:              nil,
		ToolChoice:         req.ToolChoice,
		Stream:             req.Stream,
		PreviousResponseID: req.PreviousResponseID,
		Metadata:           req.Metadata,
		Thinking:           req.Thinking,
		ContextManagement:  req.ContextManagement,
		OutputConfig:       req.OutputConfig,
	}, req.Tools)
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
		h.handleOpenAIStreaming(w, r, internalReq, plan, logger)
		return
	}

	resp, ok := dispatchOrWriteError(h, w, r, internalReq, plan, logger)
	if !ok {
		return
	}
	writeOpenAIChatResponse(w, resp)
}

func writeOpenAIChatResponse(w http.ResponseWriter, resp runtime.Response) {
	content := any(firstTextPart(resp.Message))
	if len(resp.Message.ToolCalls) > 0 && content == "" {
		content = nil
	}
	message := map[string]any{
		"role":    string(resp.Message.Role),
		"content": content,
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

	choice := map[string]any{
		"index":         0,
		"message":       message,
		"finish_reason": openAIFinishReason(resp.FinishReason),
	}
	body := map[string]any{
		"id":      resp.ID,
		"object":  resp.Object,
		"model":   resp.Model,
		"choices": []map[string]any{choice},
	}
	if resp.Usage != nil {
		body["usage"] = map[string]any{
			"prompt_tokens":     resp.Usage.InputTokens,
			"completion_tokens": resp.Usage.OutputTokens,
			"total_tokens":      resp.Usage.TotalTokens,
		}
	}
	writeJSON(w, http.StatusOK, body)
}

func openAIFinishReason(reason runtime.FinishReason) string {
	switch reason {
	case runtime.FinishReasonToolUse:
		return "tool_calls"
	case runtime.FinishReasonLength:
		return "length"
	case runtime.FinishReasonStop, runtime.FinishReasonEndTurn, runtime.FinishReasonError:
		return "stop"
	default:
		return string(reason)
	}
}

func openAIStreamChunkWithArgumentsDelta(event runtime.StreamEvent, toolArgumentSnapshots map[int]string) any {
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
			arguments := event.ToolCall.Arguments
			if toolArgumentSnapshots != nil {
				previous := toolArgumentSnapshots[event.ToolCallIndex]
				arguments = strings.TrimPrefix(arguments, previous)
				toolArgumentSnapshots[event.ToolCallIndex] = event.ToolCall.Arguments
			}
			delta["tool_calls"] = []map[string]any{{
				"index": event.ToolCallIndex,
				"id":    event.ToolCall.ID,
				"type":  "function",
				"function": map[string]any{
					"name":      event.ToolCall.Name,
					"arguments": arguments,
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
			"finish_reason": openAIFinishReason(event.FinishReason),
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
