package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"

	"syrogo/internal/runtime"
)

func (h *Handler) handleOpenAIStreaming(w http.ResponseWriter, r *http.Request, req runtime.Request, plan runtime.ExecutionPlan, logger loggerLike) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		logger.Error("streaming not supported by response writer")
		writeError(w, http.StatusInternalServerError, "streaming is not supported")
		return
	}

	events, err := h.dispatcher.DispatchStream(r.Context(), req, plan)
	if err != nil {
		status, message := gatewayError(err)
		logger.Error("stream dispatch failed",
			kvString("model", plannedModel(plan)),
			kvInt("status", status),
			kvAny("error", err),
		)
		writeError(w, status, message)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	for event := range events {
		if err := writeOpenAISSE(w, openAIStreamChunk(event)); err != nil {
			logger.Error("stream write failed", kvAny("error", err))
			return
		}
		flusher.Flush()
	}

	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func (h *Handler) handleOpenAIResponsesStreaming(w http.ResponseWriter, r *http.Request, req runtime.Request, plan runtime.ExecutionPlan, logger loggerLike) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		logger.Error("streaming not supported by response writer")
		writeError(w, http.StatusInternalServerError, "streaming is not supported")
		return
	}

	events, err := h.dispatcher.DispatchStream(r.Context(), req, plan)
	if err != nil {
		status, message := gatewayError(err)
		logger.Error("stream dispatch failed",
			kvString("model", plannedModel(plan)),
			kvInt("status", status),
			kvAny("error", err),
		)
		writeError(w, status, message)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	for _, frame := range openAIResponsesStreamPrelude(plan) {
		if err := writeOpenAIResponsesSSE(w, frame.event, frame.payload); err != nil {
			logger.Error("stream write failed", kvAny("error", err))
			return
		}
		flusher.Flush()
	}

	for _, frame := range openAIResponsesStreamFrames(events) {
		if err := writeOpenAIResponsesSSE(w, frame.event, frame.payload); err != nil {
			logger.Error("stream write failed", kvAny("error", err))
			return
		}
		flusher.Flush()
	}
}

func (h *Handler) handleAnthropicStreaming(w http.ResponseWriter, r *http.Request, req runtime.Request, plan runtime.ExecutionPlan, logger loggerLike) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		logger.Error("streaming not supported by response writer")
		writeError(w, http.StatusInternalServerError, "streaming is not supported")
		return
	}

	events, err := h.dispatcher.DispatchStream(r.Context(), req, plan)
	if err != nil {
		status, message := gatewayError(err)
		logger.Error("stream dispatch failed",
			kvString("model", plannedModel(plan)),
			kvInt("status", status),
			kvAny("error", err),
		)
		writeError(w, status, message)
		return
	}

	allEvents := make([]runtime.StreamEvent, 0, 8)
	for event := range events {
		allEvents = append(allEvents, event)
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	for _, frame := range anthropicStreamFrames(allEvents) {
		if err := writeAnthropicSSE(w, frame.event, frame.payload); err != nil {
			logger.Error("stream write failed", kvAny("error", err))
			return
		}
		flusher.Flush()
	}

	_, _ = fmt.Fprint(w, "event: done\ndata: {}\n\n")
	flusher.Flush()
}

type loggerLike interface {
	Error(msg string, args ...any)
}

func kvString(key, value string) any  { return fmt.Sprintf("%s=%s", key, value) }
func kvInt(key string, value int) any { return fmt.Sprintf("%s=%d", key, value) }
func kvAny(key string, value any) any { return fmt.Sprintf("%s=%v", key, value) }

func writeAnthropicSSE(w http.ResponseWriter, event string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return err
	}
	return nil
}

func writeOpenAISSE(w http.ResponseWriter, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return err
	}
	return nil
}

func writeOpenAIResponsesSSE(w http.ResponseWriter, event string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return err
	}
	return nil
}
