package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/ryanycheng/Syrogo/internal/eventstream"
	"github.com/ryanycheng/Syrogo/internal/runtime"
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

	toolArgumentSnapshots := map[int]string{}
	for event := range events {
		if err := writeOpenAISSE(w, openAIStreamChunkWithArgumentsDelta(event, toolArgumentSnapshots)); err != nil {
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

	for _, frame := range openAIResponsesStreamFrames(plan, events) {
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

	runtimeEvents, err := h.dispatcher.DispatchStream(r.Context(), req, plan)
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

	requestID, _ := r.Context().Value(runtime.ContextKeyRequestID).(string)
	var traceBuf bytes.Buffer

	for frame := range anthropicStreamFramesFromEventStream(eventstream.EventStreamFromRuntime(runtimeEvents)) {
		if traceAnthropicStreamEnabled() {
			appendAnthropicSSETrace(&traceBuf, frame.event, frame.payload)
		}
		if err := writeAnthropicSSE(w, frame.event, frame.payload); err != nil {
			logger.Error("stream write failed", kvAny("error", err))
			return
		}
		flusher.Flush()
	}

	if traceAnthropicStreamEnabled() {
		traceBuf.WriteString("event: done\ndata: {}\n\n")
		if err := writeAnthropicStreamTrace(requestID, traceBuf.Bytes()); err != nil {
			logger.Error("anthropic stream trace write failed", kvAny("error", err))
		}
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

func appendAnthropicSSETrace(buf *bytes.Buffer, event string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(buf, "event: %s\n", event)
	_, _ = fmt.Fprintf(buf, "data: %s\n\n", data)
}

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
