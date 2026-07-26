package gateway

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	"github.com/ryanycheng/Syrogo/internal/runtime"
	"gopkg.in/yaml.v3"
)

const (
	defaultAdminLogPath     = "tmp/dev.log"
	defaultAdminLogMaxBytes = 64 * 1024
	hardAdminLogMaxBytes    = 512 * 1024
)

//go:embed adminui/*
var adminUIFiles embed.FS

type routeDryRunRequest struct {
	Inbound string `json:"inbound"`
	Client  string `json:"client"`
	Model   string `json:"model"`
	Stream  bool   `json:"stream"`
}

type routeDryRunStep struct {
	Index            int    `json:"index"`
	OutboundName     string `json:"outbound_name"`
	OutboundProtocol string `json:"outbound_protocol"`
	Model            string `json:"model,omitempty"`
	OnError          string `json:"on_error,omitempty"`
}

type routeDryRunResponse struct {
	Inbound         string            `json:"inbound"`
	InboundProtocol string            `json:"inbound_protocol"`
	Client          string            `json:"client"`
	ActiveTag       string            `json:"active_tag"`
	RequestedModel  string            `json:"requested_model"`
	MatchedRule     string            `json:"matched_rule"`
	Strategy        string            `json:"strategy"`
	ResolvedToTags  []string          `json:"resolved_to_tags"`
	Steps           []routeDryRunStep `json:"steps"`
}

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

func (h *Handler) withAdminFailureAudit(action string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		startedAt := time.Now()
		lw := &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next(lw, r)
		if lw.statusCode < http.StatusBadRequest {
			return
		}
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
	w.Header().Set("Cache-Control", "no-store")
	state := h.runtimeState()
	if state.ConfigPath == "" {
		writeJSON(w, http.StatusOK, map[string]any{"config_ready": false, "redacted_content": "", "revision": "", "checksum": ""})
		return
	}
	content, err := os.ReadFile(state.ConfigPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read config: "+err.Error())
		return
	}
	redacted, err := redactedConfigYAML(content)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "redact config: "+err.Error())
		return
	}
	checksum := configChecksum(content)
	revision := "sha256:" + checksum
	w.Header().Set("ETag", revision)
	writeJSON(w, http.StatusOK, map[string]any{
		"config_ready":     true,
		"redacted_content": redacted,
		"revision":         revision,
		"checksum":         checksum,
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

func (h *Handler) handleDebugTraces(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) handleRouteDryRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAdmin(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	var payload routeDryRunRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&payload); err != nil && err != io.EOF {
		writeError(w, http.StatusBadRequest, "read dry-run request: "+err.Error())
		return
	}
	inboundName := strings.TrimSpace(payload.Inbound)
	clientName := strings.TrimSpace(payload.Client)
	model := strings.TrimSpace(payload.Model)
	if inboundName == "" || clientName == "" || model == "" {
		writeError(w, http.StatusBadRequest, "inbound, client, and model are required")
		return
	}

	state := h.runtimeState()
	inbound, client, binding, ok := findDryRunClient(state.Clients, state.Inbounds, inboundName, clientName)
	if !ok {
		writeError(w, http.StatusNotFound, "inbound or client not found")
		return
	}
	plan, err := state.Router.PlanDryRun(runtime.RouteContext{
		Request:         runtime.Request{Model: model, Stream: payload.Stream},
		ClientName:      client.Name,
		InboundName:     inbound.Name,
		InboundProtocol: inbound.Protocol,
		ActiveTag:       binding.Tag,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, dryRunResponse(plan))
}

func (h *Handler) handleProviderDebug(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAdmin(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	state := h.runtimeState()
	events := []quota.Event(nil)
	if state.EventRecorder != nil {
		events = state.EventRecorder.Snapshot()
	}
	clientQuota := []quota.SnapshotItem(nil)
	if state.ClientQuotaTracker != nil {
		clientQuota = state.ClientQuotaTracker.ClientSnapshot()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"health":          state.Dispatcher.QueryProviderHealth(),
		"outbound_quota":  state.Dispatcher.QueryQuota(),
		"client_quota":    clientQuota,
		"events":          events,
		"latency_summary": state.Dispatcher.QueryLatencySummary(),
	})
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
	if !hasAdminLogPageQuery(r) {
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
		return
	}
	query, err := parseAdminLogQuery(r.URL.Query(), maxBytes, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	page, memoryHit := adminLogPage{}, false
	if query.Cursor == "" && h.recentLogs != nil {
		if snapshot, covered := h.recentLogs.Snapshot(query.Since, query.Until); covered {
			page, memoryHit = adminLogPageFromRecent(snapshot, query)
		}
	}
	if !memoryHit {
		page, err = readAdminLogPage(state.Admin.Logs, query)
	}
	if err != nil {
		if errors.Is(err, errAdminLogCursorStale) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		if query.Cursor != "" || strings.Contains(err.Error(), "cursor") {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "read logs: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":               path,
		"source":             page.Source,
		"max_bytes":          maxBytes,
		"lines":              query.Limit,
		"truncated":          page.ScanTruncated,
		"content":            redactLogContent(page.Content),
		"since":              page.Since,
		"until":              page.Until,
		"limit":              page.Limit,
		"line_count":         page.LineCount,
		"matched_count":      page.LineCount,
		"scanned_line_count": page.ScannedLineCount,
		"scanned_file_count": page.ScannedFileCount,
		"includes_archives":  page.IncludesArchives,
		"items":              page.Items,
		"bytes_read":         page.BytesRead,
		"has_more":           page.HasMore,
		"next_cursor":        page.NextCursor,
		"scan_truncated":     page.ScanTruncated,
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

func (h *Handler) handleConfigHistoryDiff(w http.ResponseWriter, r *http.Request) {
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
	w.Header().Set("Cache-Control", "no-store")
	diff, err := h.configReloader.HistoryDiff(strings.TrimSpace(r.URL.Query().Get("id")))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, diff)
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

func dryRunResponse(plan runtime.ExecutionPlan) routeDryRunResponse {
	steps := make([]routeDryRunStep, 0, len(plan.Steps))
	for i, step := range plan.Steps {
		steps = append(steps, routeDryRunStep{
			Index:            i,
			OutboundName:     step.OutboundName,
			OutboundProtocol: step.OutboundProtocol,
			Model:            step.Model,
			OnError:          string(step.OnError),
		})
	}
	return routeDryRunResponse{
		Inbound:         plan.InboundName,
		InboundProtocol: plan.InboundProtocol,
		Client:          plan.ClientName,
		ActiveTag:       plan.ActiveTag,
		RequestedModel:  plan.RequestedModel,
		MatchedRule:     plan.MatchedRule,
		Strategy:        string(plan.Strategy),
		ResolvedToTags:  append([]string(nil), plan.ResolvedToTags...),
		Steps:           steps,
	}
}

func findDryRunClient(clients []config.ClientSpec, inbounds []config.InboundSpec, inboundName, clientName string) (config.InboundSpec, config.ClientSpec, config.ClientBindingSpec, bool) {
	cfg := config.Config{Clients: clients, Inbounds: inbounds}
	for _, inbound := range inbounds {
		if inbound.Name != inboundName {
			continue
		}
		for _, binding := range inbound.Clients {
			if binding.Ref == clientName {
				client, ok := config.FindClient(cfg, binding.Ref)
				return inbound, client, binding, ok
			}
		}
		return config.InboundSpec{}, config.ClientSpec{}, config.ClientBindingSpec{}, false
	}
	return config.InboundSpec{}, config.ClientSpec{}, config.ClientBindingSpec{}, false
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

func redactedConfigYAML(data []byte) (string, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return "", err
	}
	redactYAMLNode(&document)
	redacted, err := yaml.Marshal(&document)
	if err != nil {
		return "", err
	}
	return string(redacted), nil
}

func redactYAMLNode(node *yaml.Node) {
	if node == nil {
		return
	}
	if node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			if isSensitiveConfigKey(key.Value) {
				value.Kind = yaml.ScalarNode
				value.Tag = "!!str"
				value.Value = config.RedactedValue
				value.Content = nil
				continue
			}
			redactYAMLNode(value)
		}
		return
	}
	for _, child := range node.Content {
		redactYAMLNode(child)
	}
}

func isSensitiveConfigKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	return normalized == "token" || normalized == "auth_token" || normalized == "admin_token" || normalized == "api_key" || normalized == "secret" || strings.HasSuffix(normalized, "_secret") || strings.HasSuffix(normalized, "_token")
}

func configChecksum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
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
