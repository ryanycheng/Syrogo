package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ryanycheng/Syrogo/internal/config"
	"github.com/ryanycheng/Syrogo/internal/execution"
	"github.com/ryanycheng/Syrogo/internal/latency"
	"github.com/ryanycheng/Syrogo/internal/logging"
	"github.com/ryanycheng/Syrogo/internal/provider"
	"github.com/ryanycheng/Syrogo/internal/quota"
	"github.com/ryanycheng/Syrogo/internal/router"
	"github.com/ryanycheng/Syrogo/internal/runtime"
	"github.com/ryanycheng/Syrogo/internal/sessions"
)

type Handler struct {
	runtime        atomic.Value
	registry       *InboundRegistry
	logger         *slog.Logger
	recentLogs     *logging.RecentBuffer
	configReloader ConfigReloader

	accounting   config.AccountingConfig
	admin        config.AdminConfig
	configPath   string
	sessionStore *sessions.Store
}

type ConfigMutation func(config.Config) (config.Config, error)

type ConfigReloader interface {
	ApplyConfig(context.Context) (ReloadResult, error)
	MutateConfig(context.Context, string, ConfigMutation) (ReloadResult, error)
	UpdateConfig(context.Context, string, []byte) (ConfigUpdateResult, error)
	History() []HistoryItem
	HistoryDiff(id string) (HistoryDiff, error)
	Rollback(context.Context, string) (ReloadResult, error)
}

type ConfigUpdateResult struct {
	Saved    bool   `json:"saved"`
	Applied  bool   `json:"applied"`
	Revision string `json:"revision"`
	Checksum string `json:"checksum"`
}

type ConfigRevisionConflictError struct {
	CurrentRevision string
}

func (e *ConfigRevisionConflictError) Error() string { return "config revision conflict" }

type ReloadResult struct {
	OK              bool   `json:"ok"`
	Saved           bool   `json:"saved"`
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
	Checksum  string `json:"checksum"`
}

type HistoryDiff struct {
	ID             string `json:"id"`
	CurrentContent string `json:"current_content"`
	HistoryContent string `json:"history_content"`
}

type RuntimeState struct {
	Router             *router.Router
	Dispatcher         *execution.Dispatcher
	Inbounds           []config.InboundSpec
	Clients            []config.ClientSpec
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

func withRequestContext(ctx context.Context, requestID string, r *http.Request, inbound config.InboundSpec, resolved config.ResolvedClientBinding, sessionStore *sessions.Store) context.Context {
	ctx = context.WithValue(ctx, runtime.ContextKeyRequestID, requestID)
	explicitSessionID := strings.TrimSpace(r.Header.Get("X-Syrogo-Session-ID"))
	sessionID := ""
	agent := strings.TrimSpace(r.Header.Get("X-Syrogo-Agent"))
	if explicitSessionID != "" && sessionStore != nil {
		if session, ok := sessionStore.GetOwned(explicitSessionID, resolved.Client.Name, inbound.Name); ok {
			sessionID = session.ID
			if agent == "" && len(session.Command) > 0 {
				agent = session.Command[0]
			}
		}
	} else if explicitSessionID == "" && sessionStore != nil {
		if session, ok := sessionStore.LatestActive(resolved.Client.Name, inbound.Name); ok {
			sessionID = session.ID
			if agent == "" && len(session.Command) > 0 {
				agent = session.Command[0]
			}
		}
	}
	if sessionID != "" {
		ctx = context.WithValue(ctx, runtime.ContextKeySessionID, sessionID)
	}
	if agent != "" {
		ctx = context.WithValue(ctx, runtime.ContextKeyAgent, agent)
	}
	return ctx
}

func New(r *router.Router, dispatcher *execution.Dispatcher, clients []config.ClientSpec, inbounds []config.InboundSpec, accountingCfg config.AccountingConfig, logger *slog.Logger) *Handler {
	return NewWithClientQuota(r, dispatcher, clients, inbounds, nil, accountingCfg, logger)
}

func NewWithClientQuota(r *router.Router, dispatcher *execution.Dispatcher, clients []config.ClientSpec, inbounds []config.InboundSpec, clientQuotaTracker *quota.Tracker, accountingCfg config.AccountingConfig, logger *slog.Logger) *Handler {
	return NewWithClientQuotaAndEvents(r, dispatcher, clients, inbounds, clientQuotaTracker, nil, accountingCfg, logger)
}

func NewWithClientQuotaAndEvents(r *router.Router, dispatcher *execution.Dispatcher, clients []config.ClientSpec, inbounds []config.InboundSpec, clientQuotaTracker *quota.Tracker, eventRecorder *quota.EventRecorder, accountingCfg config.AccountingConfig, logger *slog.Logger) *Handler {
	return NewWithClientQuotaEventsAndLatency(r, dispatcher, clients, inbounds, clientQuotaTracker, eventRecorder, nil, accountingCfg, logger)
}

func NewWithClientQuotaEventsAndLatency(r *router.Router, dispatcher *execution.Dispatcher, clients []config.ClientSpec, inbounds []config.InboundSpec, clientQuotaTracker *quota.Tracker, eventRecorder *quota.EventRecorder, latencyStore *latency.Store, accountingCfg config.AccountingConfig, logger *slog.Logger) *Handler {
	return NewWithClientQuotaEventsLatencyAndConfig(r, dispatcher, clients, inbounds, clientQuotaTracker, eventRecorder, latencyStore, "", accountingCfg, logger)
}

func NewWithClientQuotaEventsLatencyAndConfig(r *router.Router, dispatcher *execution.Dispatcher, clients []config.ClientSpec, inbounds []config.InboundSpec, clientQuotaTracker *quota.Tracker, eventRecorder *quota.EventRecorder, latencyStore *latency.Store, configPath string, accountingCfg config.AccountingConfig, logger *slog.Logger) *Handler {
	return NewWithClientQuotaEventsLatencyConfigAndAdmin(r, dispatcher, clients, inbounds, clientQuotaTracker, eventRecorder, latencyStore, configPath, accountingCfg, config.AdminConfig{}, logger)
}

func NewWithClientQuotaEventsLatencyConfigAndAdmin(r *router.Router, dispatcher *execution.Dispatcher, clients []config.ClientSpec, inbounds []config.InboundSpec, clientQuotaTracker *quota.Tracker, eventRecorder *quota.EventRecorder, latencyStore *latency.Store, configPath string, accountingCfg config.AccountingConfig, adminCfg config.AdminConfig, logger *slog.Logger) *Handler {
	return NewWithClientQuotaEventsLatencyConfigAdminAndSessions(r, dispatcher, clients, inbounds, clientQuotaTracker, eventRecorder, latencyStore, configPath, accountingCfg, adminCfg, nil, logger)
}

func NewWithClientQuotaEventsLatencyConfigAdminAndSessions(r *router.Router, dispatcher *execution.Dispatcher, clients []config.ClientSpec, inbounds []config.InboundSpec, clientQuotaTracker *quota.Tracker, eventRecorder *quota.EventRecorder, latencyStore *latency.Store, configPath string, accountingCfg config.AccountingConfig, adminCfg config.AdminConfig, sessionStore *sessions.Store, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	if sessionStore == nil {
		sessionStore = sessions.NewStore()
	}
	h := &Handler{
		registry:     DefaultInboundRegistry(),
		logger:       logger,
		sessionStore: sessionStore,
	}
	h.ApplyRuntime(RuntimeState{
		Router:             r,
		Dispatcher:         dispatcher,
		Clients:            clients,
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
	state.Clients = append([]config.ClientSpec(nil), state.Clients...)
	state.Admin = normalizeAdminConfig(state.Admin)
	h.runtime.Store(state)
}

func (h *Handler) runtimeState() RuntimeState {
	state, _ := h.runtime.Load().(RuntimeState)
	if hasAccountingOverride(h.accounting) {
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

func hasAccountingOverride(cfg config.AccountingConfig) bool {
	return cfg.Enabled || cfg.Backend != "" || cfg.ExposeHTTP || cfg.AdminToken != "" || cfg.LocalFile != (config.AccountingLocalFileConfig{}) || len(cfg.Pricing) > 0
}

func (h *Handler) SetConfigReloader(reloader ConfigReloader) {
	h.configReloader = reloader
}

func (h *Handler) SetRecentLogs(recentLogs *logging.RecentBuffer) {
	h.recentLogs = recentLogs
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", h.handleHealthz)
	mux.HandleFunc("/stats/usage", h.handleUsageStats)
	mux.HandleFunc("/stats/quota", h.handleQuotaStats)
	mux.HandleFunc("/stats/client-quota", h.handleClientQuotaStats)
	mux.HandleFunc("/stats/governance", h.handleGovernanceStats)
	mux.HandleFunc("/stats/latency/summary", h.handleLatencySummaryStats)
	mux.HandleFunc("/stats/latency", h.handleLatencyStats)
	mux.HandleFunc("/session/register", h.handleSessionRegister)
	mux.HandleFunc("/session/heartbeat", h.handleSessionHeartbeat)
	mux.HandleFunc("/session/hook-event", h.handleSessionHookEvent)
	mux.HandleFunc("/session/stopped", h.handleSessionStopped)
	mux.HandleFunc("/admin/usage", h.withAdminAudit("usage", h.handleAdminUsage))
	mux.HandleFunc("/admin/quota", h.withAdminAudit("quota", h.handleAdminQuota))
	mux.HandleFunc("/admin/logs", h.withAdminFailureAudit("logs", h.handleAdminLogs))
	mux.HandleFunc("/admin/overview", h.withAdminAudit("overview", h.handleAdminOverview))
	mux.HandleFunc("/admin/latency/summary", h.withAdminAudit("latency_summary", h.handleAdminLatencySummary))
	mux.HandleFunc("/admin/latency/active", h.withAdminAudit("latency_active", h.handleAdminActiveLatency))
	mux.HandleFunc("/admin/latency", h.withAdminAudit("latency", h.handleAdminLatency))
	mux.HandleFunc("/admin/session", h.withAdminAudit("session", h.handleAdminSession))
	mux.HandleFunc("/admin/sessions", h.withAdminAudit("sessions", h.handleAdminSessions))
	mux.HandleFunc("/admin/config", h.withAdminAudit("config_read", h.handleAdminConfig))
	mux.HandleFunc("/admin/config/options", h.withAdminAudit("config_options", h.handleConfigOptions))
	mux.HandleFunc("/admin/config/providers", h.withAdminAudit("config_providers", h.handleConfigProviders))
	mux.HandleFunc("/admin/config/providers/metrics", h.withAdminAudit("config_provider_metrics", h.handleConfigProviderMetrics))
	mux.HandleFunc("/admin/config/provider/check", h.withAdminAudit("config_provider_check", h.handleConfigProviderCheck))
	mux.HandleFunc("/admin/config/provider/upsert", h.withAdminAudit("config_provider_upsert", h.handleConfigProviderUpsert))
	mux.HandleFunc("/admin/config/provider/enabled", h.withAdminAudit("config_provider_enabled", h.handleConfigProviderEnabled))
	mux.HandleFunc("/admin/config/provider/delete", h.withAdminAudit("config_provider_delete", h.handleConfigProviderDelete))
	mux.HandleFunc("/admin/config/clients", h.withAdminAudit("config_clients", h.handleConfigClients))
	mux.HandleFunc("/admin/config/clients/metrics", h.withAdminAudit("config_client_metrics", h.handleConfigClientMetrics))
	mux.HandleFunc("/admin/config/client/usage", h.withAdminAudit("config_client_usage", h.handleConfigClientUsage))
	mux.HandleFunc("/admin/config/client/upsert", h.withAdminAudit("config_client_upsert", h.handleConfigClientUpsert))
	mux.HandleFunc("/admin/config/client/delete", h.withAdminAudit("config_client_delete", h.handleConfigClientDelete))
	mux.HandleFunc("/admin/config/client-binding/upsert", h.withAdminAudit("config_client_binding_upsert", h.handleConfigClientBindingUpsert))
	mux.HandleFunc("/admin/config/client-binding/delete", h.withAdminAudit("config_client_binding_delete", h.handleConfigClientBindingDelete))
	mux.HandleFunc("/admin/config/routes", h.withAdminAudit("config_routes", h.handleConfigRoutes))
	mux.HandleFunc("/admin/config/routes/reorder", h.withAdminAudit("config_routes_reorder", h.handleConfigRoutesReorder))
	mux.HandleFunc("/admin/config/route/upsert", h.withAdminAudit("config_route_upsert", h.handleConfigRouteUpsert))
	mux.HandleFunc("/admin/config/route/delete", h.withAdminAudit("config_route_delete", h.handleConfigRouteDelete))
	mux.HandleFunc("/admin/config/validate", h.withAdminAudit("config_validate", h.handleConfigValidate))
	mux.HandleFunc("/admin/config/update", h.withAdminAudit("config_update", h.handleConfigUpdate))
	mux.HandleFunc("/admin/config/apply", h.withAdminAudit("config_apply", h.handleConfigApply))
	mux.HandleFunc("/admin/config/history", h.withAdminAudit("config_history", h.handleConfigHistory))
	mux.HandleFunc("/admin/config/history/diff", h.withAdminAudit("config_history_diff", h.handleConfigHistoryDiff))
	mux.HandleFunc("/admin/config/rollback", h.withAdminAudit("config_rollback", h.handleConfigRollback))
	mux.HandleFunc("/admin/debug/traces", h.withAdminAudit("debug_traces", h.handleDebugTraces))
	mux.HandleFunc("/admin/debug/route-dry-run", h.withAdminAudit("debug_route_dry_run", h.handleRouteDryRun))
	mux.HandleFunc("/admin/debug/providers", h.withAdminAudit("debug_providers", h.handleProviderDebug))
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
	query, err := parseUsageQuery(r.URL.Query(), time.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	items, err := h.runtimeState().Dispatcher.QueryUsageBy(query)
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
	if !h.authorizeAdmin(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	if h.configReloader == nil {
		writeError(w, http.StatusServiceUnavailable, "config reload is not configured")
		return
	}
	ifMatch := strings.TrimSpace(r.Header.Get("If-Match"))
	if ifMatch == "" {
		writeJSON(w, http.StatusPreconditionRequired, map[string]any{"ok": false, "code": "if_match_required", "error": "If-Match header is required"})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read config: "+err.Error())
		return
	}
	result, err := h.configReloader.UpdateConfig(r.Context(), ifMatch, body)
	if err != nil {
		var conflict *ConfigRevisionConflictError
		if errors.As(err, &conflict) {
			writeJSON(w, http.StatusConflict, map[string]any{"ok": false, "code": "config_revision_conflict", "error": err.Error(), "current_revision": conflict.CurrentRevision})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	w.Header().Set("ETag", result.Revision)
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) handleByCodec(w http.ResponseWriter, r *http.Request, inbound config.InboundSpec, client config.ResolvedClientBinding, logger *slog.Logger) bool {
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
	if r.URL.Path == "/admin/session" {
		h.handleAdminSession(w, r)
		return
	}
	if r.URL.Path == "/admin/config/options" {
		h.handleConfigOptions(w, r)
		return
	}
	if r.URL.Path == "/admin/config/providers" {
		h.handleConfigProviders(w, r)
		return
	}
	if r.URL.Path == "/admin/config/providers/metrics" {
		h.handleConfigProviderMetrics(w, r)
		return
	}
	if r.URL.Path == "/admin/config/provider/check" {
		h.handleConfigProviderCheck(w, r)
		return
	}
	if r.URL.Path == "/admin/config/provider/upsert" {
		h.handleConfigProviderUpsert(w, r)
		return
	}
	if r.URL.Path == "/admin/config/provider/enabled" {
		h.handleConfigProviderEnabled(w, r)
		return
	}
	if r.URL.Path == "/admin/config/provider/delete" {
		h.handleConfigProviderDelete(w, r)
		return
	}
	if r.URL.Path == "/admin/config/clients" {
		h.handleConfigClients(w, r)
		return
	}
	if r.URL.Path == "/admin/config/clients/metrics" {
		h.handleConfigClientMetrics(w, r)
		return
	}
	if r.URL.Path == "/admin/config/client/usage" {
		h.handleConfigClientUsage(w, r)
		return
	}
	if r.URL.Path == "/admin/config/client/upsert" {
		h.handleConfigClientUpsert(w, r)
		return
	}
	if r.URL.Path == "/admin/config/client/delete" {
		h.handleConfigClientDelete(w, r)
		return
	}
	if r.URL.Path == "/admin/config/client-binding/upsert" {
		h.handleConfigClientBindingUpsert(w, r)
		return
	}
	if r.URL.Path == "/admin/config/client-binding/delete" {
		h.handleConfigClientBindingDelete(w, r)
		return
	}
	if r.URL.Path == "/admin/config/routes" {
		h.handleConfigRoutes(w, r)
		return
	}
	if r.URL.Path == "/admin/config/routes/reorder" {
		h.handleConfigRoutesReorder(w, r)
		return
	}
	if r.URL.Path == "/admin/config/route/upsert" {
		h.handleConfigRouteUpsert(w, r)
		return
	}
	if r.URL.Path == "/admin/config/route/delete" {
		h.handleConfigRouteDelete(w, r)
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
	if r.URL.Path == "/admin/config/history/diff" {
		h.handleConfigHistoryDiff(w, r)
		return
	}
	if r.URL.Path == "/admin/config/rollback" {
		h.handleConfigRollback(w, r)
		return
	}
	if r.URL.Path == "/admin/debug/traces" {
		h.handleDebugTraces(w, r)
		return
	}
	if r.URL.Path == "/admin/debug/route-dry-run" {
		h.handleRouteDryRun(w, r)
		return
	}
	if r.URL.Path == "/admin/debug/providers" {
		h.handleProviderDebug(w, r)
		return
	}

	startedAt := time.Now()
	lw := &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
	requestID := startedAt.Format("20060102-150405.000000000")
	state := h.runtimeState()
	ctx, latencyRecorder := latency.Start(r.Context(), state.LatencyStore, requestID, r.Method, r.URL.Path, startedAt)
	lw.recorder = latencyRecorder
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

	latencyRecorder.SetRoute(inbound.Name, inbound.Protocol, client.Client.Name, client.Binding.Tag)
	r = r.WithContext(withRequestContext(ctx, requestID, r, inbound, client, h.sessionStore))

	requestLogger := h.logger.With(
		slog.String("request_id", requestID),
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.String("inbound", inbound.Name),
		slog.String("protocol", inbound.Protocol),
		slog.String("client_name", client.Client.Name),
		slog.String("active_tag", client.Binding.Tag),
		slog.String("remote", r.RemoteAddr),
	)
	requestLogger.Info("request started")
	if decision := h.beforeClientRequest(client.Client.Name); !decision.Allowed {
		h.recordClientLimited(client.Client.Name, inbound.Name, decision)
		requestLogger.Warn("request rejected", slog.String("reason", "client quota exceeded"))
		writeClientQuotaError(lw, client.Client.Name, inbound.Name, decision)
		requestLogger.Info("request completed",
			slog.Int("status", lw.statusCode),
			slog.Duration("duration", time.Since(startedAt)),
		)
		return
	}
	h.recordClientRequest(client.Client.Name)
	h.handleByCodec(lw, r, inbound, client, requestLogger)
	requestLogger.Info("request completed",
		slog.Int("status", lw.statusCode),
		slog.Duration("duration", time.Since(startedAt)),
	)
}

func (h *Handler) matchInbound(r *http.Request) (config.InboundSpec, config.ResolvedClientBinding, bool) {
	token := bearerToken(r.Header.Get("Authorization"))
	if token == "" {
		return config.InboundSpec{}, config.ResolvedClientBinding{}, false
	}

	state := h.runtimeState()
	cfg := config.Config{Clients: state.Clients}
	for _, inbound := range state.Inbounds {
		if inbound.Path != r.URL.Path {
			continue
		}
		for _, client := range config.ResolveInboundClients(cfg, inbound) {
			if client.Client.Token == token {
				return inbound, client, true
			}
		}
	}
	return config.InboundSpec{}, config.ResolvedClientBinding{}, false
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

func (h *Handler) planRequest(ctx context.Context, req runtime.Request, inbound config.InboundSpec, client config.ResolvedClientBinding) (runtime.ExecutionPlan, error) {
	startedAt := time.Now()
	plan, err := h.runtimeState().Router.Plan(runtime.RouteContext{
		Request:         req,
		ClientName:      client.Client.Name,
		InboundName:     inbound.Name,
		InboundProtocol: inbound.Protocol,
		ActiveTag:       client.Binding.Tag,
	})
	attrs := map[string]string{"inbound": inbound.Name, "active_tag": client.Binding.Tag}
	if len(plan.Steps) > 0 {
		attrs["matched_rule"] = plan.MatchedRule
		attrs["outbound"] = plan.Steps[0].OutboundName
		latency.FromContext(ctx).SetPlan(plan)
	}
	latency.RecordSpan(ctx, "route_plan", startedAt, attrs)
	return plan, err
}

type executionErrorResponse struct {
	StatusCode int
	Message    string
	Type       string
	Code       string
	RequestID  string
	RetryAfter string
}

func gatewayError(err error) executionErrorResponse {
	kind := provider.NormalizeError(err)
	response := executionErrorResponse{
		StatusCode: http.StatusBadGateway,
		Message:    "upstream request failed",
		Type:       "api_error",
		Code:       string(kind),
	}

	switch kind {
	case provider.ErrorKindQuotaExceeded:
		response.StatusCode = http.StatusTooManyRequests
		response.Message = "upstream quota exceeded"
		response.Type = "rate_limit_error"
		response.Code = "rate_limit_exceeded"
	case provider.ErrorKindRetryable, provider.ErrorKindTimeout, provider.ErrorKindUpstreamServerError:
		response.Message = "upstream temporarily unavailable"
		response.Type = "api_error"
	case provider.ErrorKindAuthFailed:
		response.Message = "upstream authentication failed"
		response.Type = "authentication_error"
	case provider.ErrorKindCapabilityUnsupported:
		response.Message = err.Error()
		response.Type = "invalid_request_error"
	case provider.ErrorKindModelUnavailable:
		response.StatusCode = http.StatusNotFound
		response.Message = err.Error()
		response.Type = "invalid_request_error"
		response.Code = "model_not_found"
	case provider.ErrorKindFatal:
		response.Message = "upstream request failed"
		response.Type = "api_error"
	default:
		response.StatusCode = http.StatusInternalServerError
		response.Message = "internal server error"
		response.Type = "api_error"
		response.Code = "internal_error"
	}

	var providerErr *provider.ProviderError
	if provider.AsProviderError(err, &providerErr) && providerErr != nil {
		response.RequestID = safeExecutionMetadata(providerErr.RequestID)
		response.RetryAfter = safeExecutionRetryAfter(providerErr.RetryAfter)
	}
	if response.RequestID != "" {
		response.Message += " (request_id: " + response.RequestID + ")"
	}
	return response
}

func safeExecutionMetadata(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 256 || strings.ContainsAny(value, "\r\n") {
		return ""
	}
	return value
}

func safeExecutionRetryAfter(value string) string {
	value = safeExecutionMetadata(value)
	if value == "" {
		return ""
	}
	if seconds, err := strconv.ParseUint(value, 10, 64); err == nil {
		return strconv.FormatUint(seconds, 10)
	}
	if retryAt, err := http.ParseTime(value); err == nil {
		return retryAt.UTC().Format(http.TimeFormat)
	}
	return ""
}

func writeRoutingError(w http.ResponseWriter, protocol string, err error) {
	if provider.NormalizeError(err) != provider.ErrorKindUnknown {
		writeExecutionError(w, protocol, err)
		return
	}
	writeError(w, http.StatusBadRequest, err.Error())
}

func writeExecutionError(w http.ResponseWriter, protocol string, err error) {
	response := gatewayError(err)
	if response.RequestID != "" {
		switch protocol {
		case "anthropic_messages":
			w.Header().Set("request-id", response.RequestID)
		case "openai_chat", "openai_responses":
			w.Header().Set("x-request-id", response.RequestID)
		}
	}
	if response.RetryAfter != "" {
		w.Header().Set("Retry-After", response.RetryAfter)
	}

	switch protocol {
	case "anthropic_messages":
		writeJSON(w, response.StatusCode, map[string]any{
			"type": "error",
			"error": map[string]string{
				"type":    response.Type,
				"message": response.Message,
			},
		})
	case "openai_chat", "openai_responses":
		writeJSON(w, response.StatusCode, map[string]any{
			"error": map[string]string{
				"message": response.Message,
				"type":    response.Type,
				"code":    response.Code,
			},
		})
	default:
		writeError(w, response.StatusCode, response.Message)
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
	writeJSON(w, http.StatusTooManyRequests, map[string]any{
		"error":            "client quota exceeded",
		"type":             quota.EventClientLimited,
		"code":             "client_quota_exceeded",
		"quota_subject":    clientName,
		"inbound":          inboundName,
		"reason":           decision.Reason,
		"retry_after":      formatRetryAfter(decision.RetryAfter),
		"blocking_windows": decision.BlockingWindows,
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
