package gateway

import (
	"log/slog"
	"net/http"

	"github.com/ryanycheng/Syrogo/internal/runtime"
)

func dispatchOrWriteError(h *Handler, w http.ResponseWriter, r *http.Request, req runtime.Request, plan runtime.ExecutionPlan, logger *slog.Logger) (runtime.Response, bool) {
	resp, err := h.dispatcher.Dispatch(r.Context(), req, plan)
	if err != nil {
		status, message := gatewayError(err)
		logger.Error("request dispatch failed",
			slog.String("model", plannedModel(plan)),
			slog.Int("status", status),
			slog.Any("error", err),
		)
		writeError(w, status, message)
		return runtime.Response{}, false
	}
	return resp, true
}
