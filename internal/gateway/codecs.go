package gateway

import (
	"log/slog"
	"net/http"

	"github.com/ryanycheng/Syrogo/internal/latency"
	"github.com/ryanycheng/Syrogo/internal/provider"
	"github.com/ryanycheng/Syrogo/internal/runtime"
)

func dispatchOrWriteError(h *Handler, w http.ResponseWriter, r *http.Request, req runtime.Request, plan runtime.ExecutionPlan, logger *slog.Logger) (runtime.Response, bool) {
	resp, err := h.runtimeState().Dispatcher.Dispatch(r.Context(), req, plan)
	if err != nil {
		response := gatewayError(err)
		errorKind := string(provider.NormalizeError(err))
		latency.FromContext(r.Context()).SetErrorKind(errorKind)
		logger.Error("request dispatch failed",
			slog.String("model", plannedModel(plan)),
			slog.String("error_kind", errorKind),
			slog.Int("status", response.StatusCode),
			slog.Any("error", err),
		)
		writeExecutionError(w, plan.InboundProtocol, err)
		return runtime.Response{}, false
	}
	return resp, true
}
