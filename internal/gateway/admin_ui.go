package gateway

import (
	"embed"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ryanycheng/Syrogo/internal/accounting"
	"github.com/ryanycheng/Syrogo/internal/config"
	"github.com/ryanycheng/Syrogo/internal/provider"
	"github.com/ryanycheng/Syrogo/internal/quota"
)

const (
	defaultAdminLogPath     = "tmp/dev.log"
	defaultAdminLogMaxBytes = 64 * 1024
	hardAdminLogMaxBytes    = 512 * 1024
)

//go:embed adminui/*
var adminUIFiles embed.FS

func (h *Handler) handleAdminUI(w http.ResponseWriter, r *http.Request) {
	if !h.runtimeState().Admin.Enabled {
		http.NotFound(w, r)
		return
	}
	adminUIHandler().ServeHTTP(w, r)
}

func adminUIHandler() http.Handler {
	uiFiles, err := fs.Sub(adminUIFiles, "adminui")
	if err != nil {
		return http.NotFoundHandler()
	}
	return http.StripPrefix("/admin/", http.FileServer(http.FS(uiFiles)))
}

func (h *Handler) withAdminAudit(action string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		startedAt := time.Now()
		lw := &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next(lw, r)
		h.logger.Info("admin audit",
			"event", "admin_audit",
			"action", action,
			"method", r.Method,
			"path", r.URL.Path,
			"remote", r.RemoteAddr,
			"status", lw.statusCode,
			"duration", time.Since(startedAt),
			"authorized", lw.statusCode != http.StatusUnauthorized,
		)
	}
}

func (h *Handler) authorizeAdmin(r *http.Request) bool {
	admin := h.runtimeState().Admin
	if !admin.Enabled || admin.Token == "" {
		return false
	}
	return bearerToken(r.Header.Get("Authorization")) == admin.Token
}

func (h *Handler) authorizeAdminOrAccounting(r *http.Request) bool {
	return h.authorizeAdmin(r) || h.authorizeAccounting(r)
}

func (h *Handler) handleAdminConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAdmin(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	state := h.runtimeState()
	if state.ConfigPath == "" {
		writeError(w, http.StatusServiceUnavailable, "config path is not configured")
		return
	}
	content, err := os.ReadFile(state.ConfigPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read config: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":             state.ConfigPath,
		"content":          string(content),
		"redacted_content": redactConfigContent(string(content)),
	})
}

func (h *Handler) handleAdminUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAdmin(r) {
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

func (h *Handler) handleAdminOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAdmin(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	writeJSON(w, http.StatusOK, h.adminOverview())
}

func (h *Handler) adminOverview() map[string]any {
	state := h.runtimeState()
	usageItems, _ := state.Dispatcher.QueryUsageBy(accounting.Query{GroupBy: "key", Window: accounting.WindowTotal})
	usage := map[string]int{"request_count": 0, "success_count": 0, "error_count": 0, "fallback_count": 0}
	for _, item := range usageItems {
		usage["request_count"] += item.RequestCount
		usage["success_count"] += item.SuccessCount
		usage["error_count"] += item.ErrorCount
		usage["fallback_count"] += item.FallbackCount
	}

	latencySummary := state.Dispatcher.QueryLatencySummary()
	outboundQuota := state.Dispatcher.QueryQuota()
	clientQuota := []quota.SnapshotItem(nil)
	if state.ClientQuotaTracker != nil {
		clientQuota = state.ClientQuotaTracker.ClientSnapshot()
	}
	quotaSummary := map[string]int{
		"outbound_items":         len(outboundQuota),
		"client_items":           len(clientQuota),
		"limited_items":          countQuotaState(outboundQuota, quota.StateLimited) + countQuotaState(clientQuota, quota.StateLimited),
		"cooldown_items":         countQuotaState(outboundQuota, quota.StateCooldown) + countQuotaState(clientQuota, quota.StateCooldown),
		"configured_quota_items": len(outboundQuota) + len(clientQuota),
	}

	healthItems := state.Dispatcher.QueryProviderHealth()
	health := map[string]int{"provider_count": len(healthItems), "degraded_count": 0, "probing_count": 0}
	for _, item := range healthItems {
		if item.State == provider.HealthDegraded {
			health["degraded_count"]++
		}
		if item.State == provider.HealthProbing {
			health["probing_count"]++
		}
	}
	events := []quota.Event(nil)
	if state.EventRecorder != nil {
		events = state.EventRecorder.Snapshot()
	}

	return map[string]any{
		"usage": usage,
		"latency": map[string]any{
			"count":  latencySummary.Count,
			"p95_ms": latencySummary.Total.P95Ms,
			"p99_ms": latencySummary.Total.P99Ms,
			"max_ms": latencySummary.Total.MaxMs,
		},
		"quota":  quotaSummary,
		"health": health,
		"admin": map[string]any{
			"enabled":            state.Admin.Enabled,
			"config_path_set":    state.ConfigPath != "",
			"logs_enabled":       state.Admin.Logs.Enabled,
			"logs_path":          state.Admin.Logs.Path,
			"logs_max_bytes":     state.Admin.Logs.MaxBytes,
			"accounting_enabled": state.Accounting.Enabled && state.Accounting.ExposeHTTP,
		},
		"recent_events": map[string]any{"count": len(events), "items": events},
	}
}

func countQuotaState(items []quota.SnapshotItem, state string) int {
	count := 0
	for _, item := range items {
		if item.State == state {
			count++
		}
	}
	return count
}

func (h *Handler) handleAdminQuota(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAdmin(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	state := h.runtimeState()
	clientQuota := []quota.SnapshotItem(nil)
	if state.ClientQuotaTracker != nil {
		clientQuota = state.ClientQuotaTracker.ClientSnapshot()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"outbound": state.Dispatcher.QueryQuota(),
		"client":   clientQuota,
	})
}

func (h *Handler) handleAdminLatency(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAdmin(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	writeJSON(w, http.StatusOK, h.runtimeState().Dispatcher.QueryLatency())
}

func (h *Handler) handleAdminLatencySummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAdmin(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	writeJSON(w, http.StatusOK, h.runtimeState().Dispatcher.QueryLatencySummary())
}

func (h *Handler) handleAdminLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAdmin(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	state := h.runtimeState()
	if !state.Admin.Logs.Enabled {
		writeError(w, http.StatusNotFound, "admin logs are not enabled")
		return
	}
	path := state.Admin.Logs.Path
	if path == "" {
		path = defaultAdminLogPath
	}
	maxBytes := boundedAdminLogBytes(state.Admin.Logs.MaxBytes, r.URL.Query().Get("bytes"))
	content, truncated, err := readTail(path, maxBytes)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read logs: "+err.Error())
		return
	}
	lines := parsePositiveInt(r.URL.Query().Get("lines"))
	if lines > 0 {
		content = tailLines(content, lines)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":      path,
		"max_bytes": maxBytes,
		"lines":     lines,
		"truncated": truncated,
		"content":   redactLogContent(content),
	})
}

func (h *Handler) handleConfigApply(w http.ResponseWriter, r *http.Request) {
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
	result, err := h.configReloader.ApplyConfig(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) handleConfigHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
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
	writeJSON(w, http.StatusOK, map[string]any{"items": h.configReloader.History()})
}

func (h *Handler) handleConfigRollback(w http.ResponseWriter, r *http.Request) {
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
	var payload struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&payload); err != nil && err != io.EOF {
		writeError(w, http.StatusBadRequest, "read rollback request: "+err.Error())
		return
	}
	result, err := h.configReloader.Rollback(r.Context(), strings.TrimSpace(payload.ID))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func boundedAdminLogBytes(configured int, requested string) int {
	limit := configured
	if limit <= 0 {
		limit = defaultAdminLogMaxBytes
	}
	if requestedLimit := parsePositiveInt(requested); requestedLimit > 0 && requestedLimit < limit {
		limit = requestedLimit
	}
	if limit > hardAdminLogMaxBytes {
		return hardAdminLogMaxBytes
	}
	return limit
}

func parsePositiveInt(value string) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed
}

func readTail(path string, maxBytes int) (string, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer func() {
		_ = file.Close()
	}()
	info, err := file.Stat()
	if err != nil {
		return "", false, err
	}
	size := info.Size()
	if size <= 0 {
		return "", false, nil
	}
	readSize := int64(maxBytes)
	truncated := size > readSize
	if !truncated {
		readSize = size
	}
	start := size - readSize
	buffer := make([]byte, readSize)
	if _, err := file.ReadAt(buffer, start); err != nil {
		return "", false, err
	}
	return string(buffer), truncated, nil
}

func tailLines(content string, lines int) string {
	parts := strings.Split(content, "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	if len(parts) <= lines {
		return strings.Join(parts, "\n")
	}
	return strings.Join(parts[len(parts)-lines:], "\n")
}

var sensitiveLogPattern = regexp.MustCompile(`(?i)(authorization\s*:\s*bearer\s+)\S+|((?:token|api[_-]?key|auth[_-]?token|secret)(?:=|:|\s+))\S+`)

func redactLogContent(content string) string {
	return sensitiveLogPattern.ReplaceAllStringFunc(content, func(match string) string {
		parts := sensitiveLogPattern.FindStringSubmatch(match)
		if len(parts) >= 2 && parts[1] != "" {
			return parts[1] + "<redacted>"
		}
		if len(parts) >= 3 && parts[2] != "" {
			return parts[2] + "<redacted>"
		}
		return "<redacted>"
	})
}

var sensitiveConfigLinePattern = regexp.MustCompile(`(?i)^(\s*(?:token|auth_token|admin_token|api[_-]?key|secret)\s*:\s*).+$`)

func redactConfigContent(content string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = sensitiveConfigLinePattern.ReplaceAllString(line, "${1}\"<redacted>\"")
	}
	return strings.Join(lines, "\n")
}

func normalizeAdminConfig(cfg config.AdminConfig) config.AdminConfig {
	if cfg.Logs.Path == "" {
		cfg.Logs.Path = defaultAdminLogPath
	}
	if cfg.Logs.MaxBytes <= 0 {
		cfg.Logs.MaxBytes = defaultAdminLogMaxBytes
	}
	return cfg
}
