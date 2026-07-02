package gateway

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ryanycheng/Syrogo/internal/accounting"
	"github.com/ryanycheng/Syrogo/internal/config"
	"github.com/ryanycheng/Syrogo/internal/execution"
	"github.com/ryanycheng/Syrogo/internal/latency"
	"github.com/ryanycheng/Syrogo/internal/provider"
	"github.com/ryanycheng/Syrogo/internal/quota"
	"github.com/ryanycheng/Syrogo/internal/router"
	"github.com/ryanycheng/Syrogo/internal/runtime"
)

type Handler struct {
	runtime        atomic.Value
	registry       *InboundRegistry
	logger         *slog.Logger
	configReloader ConfigReloader

	accounting config.AccountingConfig
	admin      config.AdminConfig
	configPath string
}

type ConfigReloader interface {
	ApplyConfig(context.Context) (ReloadResult, error)
	History() []HistoryItem
	Rollback(context.Context, string) (ReloadResult, error)
}

type ReloadResult struct {
	OK              bool   `json:"ok"`
	Applied         bool   `json:"applied"`
	RestartRequired bool   `json:"restart_required"`
	Reason          string `json:"reason,omitempty"`
	HistoryID       string `json:"history_id,omitempty"`
	QuotaStateReset bool   `json:"quota_state_reset"`
}

type HistoryItem struct {
	ID        string `json:"id"`
	CreatedAt string `json:"created_at"`
	Reason    string `json:"reason"`
	Path      string `json:"path"`
	Checksum  string `json:"checksum"`
}

type RuntimeState struct {
	Router             *router.Router
	Dispatcher         *execution.Dispatcher
	Inbounds           []config.InboundSpec
	ClientQuotaTracker *quota.Tracker
	EventRecorder      *quota.EventRecorder
	LatencyStore       *latency.Store
	Accounting         config.AccountingConfig
	Admin              config.AdminConfig
	ConfigPath         string
}

type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
	recorder   *latency.Recorder
}

func (w *loggingResponseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *loggingResponseWriter) Write(p []byte) (int, error) {
	startedAt := time.Now()
	n, err := w.ResponseWriter.Write(p)
	if w.recorder != nil {
		w.recorder.AddSpan("egress_write", startedAt, nil)
	}
	return n, err
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
	return NewWithClientQuotaEventsAndLatency(r, dispatcher, inbounds, clientQuotaTracker, eventRecorder, nil, accountingCfg, logger)
}

func NewWithClientQuotaEventsAndLatency(r *router.Router, dispatcher *execution.Dispatcher, inbounds []config.InboundSpec, clientQuotaTracker *quota.Tracker, eventRecorder *quota.EventRecorder, latencyStore *latency.Store, accountingCfg config.AccountingConfig, logger *slog.Logger) *Handler {
	return NewWithClientQuotaEventsLatencyAndConfig(r, dispatcher, inbounds, clientQuotaTracker, eventRecorder, latencyStore, "", accountingCfg, logger)
}

func NewWithClientQuotaEventsLatencyAndConfig(r *router.Router, dispatcher *execution.Dispatcher, inbounds []config.InboundSpec, clientQuotaTracker *quota.Tracker, eventRecorder *quota.EventRecorder, latencyStore *latency.Store, configPath string, accountingCfg config.AccountingConfig, logger *slog.Logger) *Handler {
	return NewWithClientQuotaEventsLatencyConfigAndAdmin(r, dispatcher, inbounds, clientQuotaTracker, eventRecorder, latencyStore, configPath, accountingCfg, config.AdminConfig{}, logger)
}

func NewWithClientQuotaEventsLatencyConfigAndAdmin(r *router.Router, dispatcher *execution.Dispatcher, inbounds []config.InboundSpec, clientQuotaTracker *quota.Tracker, eventRecorder *quota.EventRecorder, latencyStore *latency.Store, configPath string, accountingCfg config.AccountingConfig, adminCfg config.AdminConfig, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	h := &Handler{
		registry: DefaultInboundRegistry(),
		logger:   logger,
	}
	h.ApplyRuntime(RuntimeState{
		Router:             r,
		Dispatcher:         dispatcher,
		Inbounds:           inbounds,
		ClientQuotaTracker: clientQuotaTracker,
		EventRecorder:      eventRecorder,
		LatencyStore:       latencyStore,
		Accounting:         accountingCfg,
		Admin:              normalizeAdminConfig(adminCfg),
		ConfigPath:         configPath,
	})
	return h
}

func (h *Handler) ApplyRuntime(state RuntimeState) {
	state.Inbounds = append([]config.InboundSpec(nil), state.Inbounds...)
	state.Admin = normalizeAdminConfig(state.Admin)
	h.runtime.Store(state)
}

func (h *Handler) runtimeState() RuntimeState {
	state, _ := h.runtime.Load().(RuntimeState)
	if h.accounting != (config.AccountingConfig{}) {
		state.Accounting = h.accounting
	}
	if h.admin != (config.AdminConfig{}) {
		state.Admin = normalizeAdminConfig(h.admin)
	}
	if h.configPath != "" {
		state.ConfigPath = h.configPath
	}
	return state
}

func (h *Handler) SetConfigReloader(reloader ConfigReloader) {
	h.configReloader = reloader
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", h.handleHealthz)
	mux.HandleFunc("/stats/usage", h.handleUsageStats)
	mux.HandleFunc("/stats/quota", h.handleQuotaStats)
	mux.HandleFunc("/stats/client-quota", h.handleClientQuotaStats)
	mux.HandleFunc("/stats/governance", h.handleGovernanceStats)
	mux.HandleFunc("/stats/latency/summary", h.handleLatencySummaryStats)
	mux.HandleFunc("/stats/latency", h.handleLatencyStats)
	mux.HandleFunc("/admin/usage", h.withAdminAudit("usage", h.handleAdminUsage))
	mux.HandleFunc("/admin/quota", h.withAdminAudit("quota", h.handleAdminQuota))
	mux.HandleFunc("/admin/logs", h.withAdminAudit("logs", h.handleAdminLogs))
	mux.HandleFunc("/admin/overview", h.withAdminAudit("overview", h.handleAdminOverview))
	mux.HandleFunc("/admin/latency/summary", h.withAdminAudit("latency_summary", h.handleAdminLatencySummary))
	mux.HandleFunc("/admin/latency", h.withAdminAudit("latency", h.handleAdminLatency))
	mux.HandleFunc("/admin/config", h.withAdminAudit("config_read", h.handleAdminConfig))
	mux.HandleFunc("/admin/config/validate", h.withAdminAudit("config_validate", h.handleConfigValidate))
	mux.HandleFunc("/admin/config/update", h.withAdminAudit("config_update", h.handleConfigUpdate))
	mux.HandleFunc("/admin/config/apply", h.withAdminAudit("config_apply", h.handleConfigApply))
	mux.HandleFunc("/admin/config/history", h.withAdminAudit("config_history", h.handleConfigHistory))
	mux.HandleFunc("/admin/config/rollback", h.withAdminAudit("config_rollback", h.handleConfigRollback))
	mux.Handle("/admin/", http.HandlerFunc(h.handleAdminUI))
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
	items, err := h.runtimeState().Dispatcher.QueryUsageBy(accounting.Query{
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
	writeJSON(w, http.StatusOK, map[string]any{"items": h.runtimeState().Dispatcher.QueryQuota()})
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
	tracker := h.runtimeState().ClientQuotaTracker
	if tracker != nil {
		items = tracker.ClientSnapshot()
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
	state := h.runtimeState()
	clientQuota := []quota.SnapshotItem(nil)
	if state.ClientQuotaTracker != nil {
		clientQuota = state.ClientQuotaTracker.ClientSnapshot()
	}
	events := []quota.Event(nil)
	if state.EventRecorder != nil {
		events = state.EventRecorder.Snapshot()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"provider_health": state.Dispatcher.QueryProviderHealth(),
		"quota": map[string]any{
			"outbound": state.Dispatcher.QueryQuota(),
			"client":   clientQuota,
		},
		"events": events,
	})
}

func (h *Handler) handleLatencyStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAccounting(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	writeJSON(w, http.StatusOK, h.runtimeState().Dispatcher.QueryLatency())
}

func (h *Handler) handleLatencySummaryStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAccounting(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	writeJSON(w, http.StatusOK, h.runtimeState().Dispatcher.QueryLatencySummary())
}

func (h *Handler) handleConfigValidate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAdminOrAccounting(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read config: "+err.Error())
		return
	}
	if _, err := config.ParseBytes(body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) handleConfigUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAdminOrAccounting(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	state := h.runtimeState()
	if state.ConfigPath == "" {
		writeError(w, http.StatusServiceUnavailable, "config path is not configured")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read config: "+err.Error())
		return
	}
	if err := config.WriteValidatedFile(state.ConfigPath, body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": state.ConfigPath, "applied": false})
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
	if r.URL.Path == "/stats/latency" {
		h.handleLatencyStats(w, r)
		return
	}
	if r.URL.Path == "/admin/config/validate" {
		h.handleConfigValidate(w, r)
		return
	}
	if r.URL.Path == "/admin/config/update" {
		h.handleConfigUpdate(w, r)
		return
	}
	if r.URL.Path == "/admin/config/apply" {
		h.handleConfigApply(w, r)
		return
	}
	if r.URL.Path == "/admin/config/history" {
		h.handleConfigHistory(w, r)
		return
	}
	if r.URL.Path == "/admin/config/rollback" {
		h.handleConfigRollback(w, r)
		return
	}

	startedAt := time.Now()
	lw := &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
	requestID := startedAt.Format("20060102-150405.000000000")
	state := h.runtimeState()
	ctx, latencyRecorder := latency.Start(r.Context(), state.LatencyStore, requestID, r.Method, r.URL.Path, startedAt)
	lw.recorder = latencyRecorder
	r = r.WithContext(withRequestID(ctx, requestID))
	defer func() {
		latencyRecorder.Finish(lw.statusCode, time.Now())
	}()

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

	latencyRecorder.SetRoute(inbound.Name, inbound.Protocol, client.Name, client.Tag)

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

	for _, inbound := range h.runtimeState().Inbounds {
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
	accounting := h.runtimeState().Accounting
	if !accounting.Enabled || !accounting.ExposeHTTP || accounting.AdminToken == "" {
		return false
	}
	return bearerToken(r.Header.Get("Authorization")) == accounting.AdminToken
}

func (h *Handler) beforeClientRequest(clientName string) quota.Decision {
	tracker := h.runtimeState().ClientQuotaTracker
	if tracker == nil {
		return quota.Decision{Allowed: true}
	}
	return tracker.BeforeClientRequest(clientName)
}

func (h *Handler) recordClientRequest(clientName string) {
	tracker := h.runtimeState().ClientQuotaTracker
	if tracker != nil {
		tracker.RecordClientRequest(clientName)
	}
}

func (h *Handler) recordClientLimited(clientName string, inboundName string, decision quota.Decision) {
	recorder := h.runtimeState().EventRecorder
	if recorder != nil {
		recorder.Record(quota.Event{Type: quota.EventClientLimited, Client: clientName, Inbound: inboundName, Reason: decision.Reason, RetryAfter: formatRetryAfter(decision.RetryAfter)})
	}
}

func formatRetryAfter(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func (h *Handler) planRequest(ctx context.Context, req runtime.Request, inbound config.InboundSpec, client config.ClientSpec) (runtime.ExecutionPlan, error) {
	startedAt := time.Now()
	plan, err := h.runtimeState().Router.Plan(runtime.RouteContext{
		Request:         req,
		ClientName:      client.Name,
		InboundName:     inbound.Name,
		InboundProtocol: inbound.Protocol,
		ActiveTag:       client.Tag,
	})
	attrs := map[string]string{"inbound": inbound.Name, "active_tag": client.Tag}
	if len(plan.Steps) > 0 {
		attrs["matched_rule"] = plan.MatchedRule
		attrs["outbound"] = plan.Steps[0].OutboundName
	}
	latency.RecordSpan(ctx, "route_plan", startedAt, attrs)
	return plan, err
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
