package gateway

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/ryanycheng/Syrogo/internal/accounting"
	"github.com/ryanycheng/Syrogo/internal/config"
	"github.com/ryanycheng/Syrogo/internal/execution"
	"github.com/ryanycheng/Syrogo/internal/provider"
	"github.com/ryanycheng/Syrogo/internal/quota"
	"github.com/ryanycheng/Syrogo/internal/router"
	"github.com/ryanycheng/Syrogo/internal/runtime"
)

type Handler struct {
	router             *router.Router
	dispatcher         *execution.Dispatcher
	inbounds           []config.InboundSpec
	clientQuotaTracker *quota.Tracker
	eventRecorder      *quota.EventRecorder
	registry           *InboundRegistry
	logger             *slog.Logger
	accounting         config.AccountingConfig
}

type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (w *loggingResponseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *loggingResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func withRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, runtime.ContextKeyRequestID, requestID)
}

func New(r *router.Router, dispatcher *execution.Dispatcher, inbounds []config.InboundSpec, accountingCfg config.AccountingConfig, logger *slog.Logger) *Handler {
	return NewWithClientQuota(r, dispatcher, inbounds, nil, accountingCfg, logger)
}

func NewWithClientQuota(r *router.Router, dispatcher *execution.Dispatcher, inbounds []config.InboundSpec, clientQuotaTracker *quota.Tracker, accountingCfg config.AccountingConfig, logger *slog.Logger) *Handler {
	return NewWithClientQuotaAndEvents(r, dispatcher, inbounds, clientQuotaTracker, nil, accountingCfg, logger)
}

func NewWithClientQuotaAndEvents(r *router.Router, dispatcher *execution.Dispatcher, inbounds []config.InboundSpec, clientQuotaTracker *quota.Tracker, eventRecorder *quota.EventRecorder, accountingCfg config.AccountingConfig, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		router:             r,
		dispatcher:         dispatcher,
		inbounds:           append([]config.InboundSpec(nil), inbounds...),
		clientQuotaTracker: clientQuotaTracker,
		eventRecorder:      eventRecorder,
		registry:           DefaultInboundRegistry(),
		logger:             logger,
		accounting:         accountingCfg,
	}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", h.handleHealthz)
	mux.HandleFunc("/stats/usage", h.handleUsageStats)
	mux.HandleFunc("/stats/quota", h.handleQuotaStats)
	mux.HandleFunc("/stats/client-quota", h.handleClientQuotaStats)
	mux.HandleFunc("/stats/governance", h.handleGovernanceStats)
	mux.HandleFunc("/", h.handleRequest)
}

func (h *Handler) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleUsageStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAccounting(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	groupBy := strings.TrimSpace(r.URL.Query().Get("group_by"))
	if groupBy == "" {
		groupBy = "key"
	}
	window := accounting.Window(strings.TrimSpace(r.URL.Query().Get("window")))
	if window == "" {
		window = accounting.WindowTotal
	}
	items, err := h.dispatcher.QueryUsageBy(accounting.Query{
		GroupBy: groupBy,
		Window:  window,
		Bucket:  strings.TrimSpace(r.URL.Query().Get("bucket")),
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) handleQuotaStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAccounting(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": h.dispatcher.QueryQuota()})
}

func (h *Handler) handleClientQuotaStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAccounting(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	items := []quota.SnapshotItem(nil)
	if h.clientQuotaTracker != nil {
		items = h.clientQuotaTracker.ClientSnapshot()
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) handleGovernanceStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAccounting(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	clientQuota := []quota.SnapshotItem(nil)
	if h.clientQuotaTracker != nil {
		clientQuota = h.clientQuotaTracker.ClientSnapshot()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"provider_health": h.dispatcher.QueryProviderHealth(),
		"quota": map[string]any{
			"outbound": h.dispatcher.QueryQuota(),
			"client":   clientQuota,
		},
		"events": h.eventRecorder.Snapshot(),
	})
}

func (h *Handler) handleByCodec(w http.ResponseWriter, r *http.Request, inbound config.InboundSpec, client config.ClientSpec, logger *slog.Logger) bool {
	codec, ok := h.registry.Get(inbound.Protocol)
	if !ok {
		logger.Warn("request rejected", slog.String("reason", "unsupported inbound protocol"))
		writeError(w, http.StatusNotFound, "unsupported inbound protocol")
		return false
	}
	codec.Handle(h, w, r, inbound, client, logger)
	return true
}

func (h *Handler) handleRequest(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		h.handleHealthz(w, r)
		return
	}
	if r.URL.Path == "/stats/usage" {
		h.handleUsageStats(w, r)
		return
	}
	if r.URL.Path == "/stats/quota" {
		h.handleQuotaStats(w, r)
		return
	}
	if r.URL.Path == "/stats/client-quota" {
		h.handleClientQuotaStats(w, r)
		return
	}
	if r.URL.Path == "/stats/governance" {
		h.handleGovernanceStats(w, r)
		return
	}

	startedAt := time.Now()
	lw := &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
	requestID := startedAt.Format("20060102-150405.000000000")
	r = r.WithContext(withRequestID(r.Context(), requestID))

	inbound, client, ok := h.matchInbound(r)
	if !ok {
		h.logger.Warn("request rejected",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.String("remote", r.RemoteAddr),
			slog.String("reason", "invalid token or inbound"),
		)
		writeError(lw, http.StatusUnauthorized, "invalid token or inbound")
		h.logger.Info("request completed",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", lw.statusCode),
			slog.Duration("duration", time.Since(startedAt)),
		)
		return
	}

	requestLogger := h.logger.With(
		slog.String("request_id", requestID),
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.String("inbound", inbound.Name),
		slog.String("protocol", inbound.Protocol),
		slog.String("client_name", client.Name),
		slog.String("active_tag", client.Tag),
		slog.String("remote", r.RemoteAddr),
	)
	requestLogger.Info("request started")
	if decision := h.beforeClientRequest(client.Name); !decision.Allowed {
		h.recordClientLimited(client.Name, inbound.Name, decision)
		requestLogger.Warn("request rejected", slog.String("reason", "client quota exceeded"))
		writeClientQuotaError(lw, client.Name, inbound.Name, decision)
		requestLogger.Info("request completed",
			slog.Int("status", lw.statusCode),
			slog.Duration("duration", time.Since(startedAt)),
		)
		return
	}
	h.recordClientRequest(client.Name)
	h.handleByCodec(lw, r, inbound, client, requestLogger)
	requestLogger.Info("request completed",
		slog.Int("status", lw.statusCode),
		slog.Duration("duration", time.Since(startedAt)),
	)
}

func (h *Handler) matchInbound(r *http.Request) (config.InboundSpec, config.ClientSpec, bool) {
	token := bearerToken(r.Header.Get("Authorization"))
	if token == "" {
		return config.InboundSpec{}, config.ClientSpec{}, false
	}

	for _, inbound := range h.inbounds {
		if inbound.Path != r.URL.Path {
			continue
		}
		for _, client := range inbound.Clients {
			if client.Token == token {
				return inbound, client, true
			}
		}
	}
	return config.InboundSpec{}, config.ClientSpec{}, false
}

func bearerToken(header string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, prefix))
}

func (h *Handler) authorizeAccounting(r *http.Request) bool {
	if !h.accounting.Enabled || !h.accounting.ExposeHTTP || h.accounting.AdminToken == "" {
		return false
	}
	return bearerToken(r.Header.Get("Authorization")) == h.accounting.AdminToken
}

func (h *Handler) beforeClientRequest(clientName string) quota.Decision {
	if h.clientQuotaTracker == nil {
		return quota.Decision{Allowed: true}
	}
	return h.clientQuotaTracker.BeforeClientRequest(clientName)
}

func (h *Handler) recordClientRequest(clientName string) {
	if h.clientQuotaTracker != nil {
		h.clientQuotaTracker.RecordClientRequest(clientName)
	}
}

func (h *Handler) recordClientLimited(clientName string, inboundName string, decision quota.Decision) {
	h.eventRecorder.Record(quota.Event{Type: quota.EventClientLimited, Client: clientName, Inbound: inboundName, Reason: decision.Reason, RetryAfter: formatRetryAfter(decision.RetryAfter)})
}

func formatRetryAfter(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func (h *Handler) planRequest(req runtime.Request, inbound config.InboundSpec, client config.ClientSpec) (runtime.ExecutionPlan, error) {
	return h.router.Plan(runtime.RouteContext{
		Request:         req,
		ClientName:      client.Name,
		InboundName:     inbound.Name,
		InboundProtocol: inbound.Protocol,
		ActiveTag:       client.Tag,
	})
}

func gatewayError(err error) (int, string) {
	switch provider.NormalizeError(err) {
	case provider.ErrorKindQuotaExceeded:
		return http.StatusBadGateway, "upstream quota exceeded"
	case provider.ErrorKindRetryable, provider.ErrorKindTimeout, provider.ErrorKindUpstreamServerError:
		return http.StatusBadGateway, "upstream temporarily unavailable"
	case provider.ErrorKindAuthFailed, provider.ErrorKindCapabilityUnsupported, provider.ErrorKindFatal:
		return http.StatusBadGateway, err.Error()
	default:
		return http.StatusInternalServerError, err.Error()
	}
}

func firstTextPart(msg runtime.Message) string {
	for _, part := range msg.Parts {
		if part.Type == runtime.ContentPartTypeText {
			return part.Text
		}
	}
	return ""
}

func nonEmpty(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func writeClientQuotaError(w http.ResponseWriter, clientName string, inboundName string, decision quota.Decision) {
	writeJSON(w, http.StatusTooManyRequests, map[string]string{
		"error":         "client quota exceeded",
		"type":          quota.EventClientLimited,
		"quota_subject": clientName,
		"inbound":       inboundName,
		"reason":        decision.Reason,
		"retry_after":   formatRetryAfter(decision.RetryAfter),
	})
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func previewBody(body []byte) string {
	const max = 512
	trimmed := strings.TrimSpace(string(body))
	if len(trimmed) <= max {
		return trimmed
	}
	return trimmed[:max] + "..."
}

func plannedModel(plan runtime.ExecutionPlan) string {
	if len(plan.Steps) == 0 {
		return ""
	}
	return plan.Steps[0].Model
}
