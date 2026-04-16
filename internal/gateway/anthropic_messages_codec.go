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
