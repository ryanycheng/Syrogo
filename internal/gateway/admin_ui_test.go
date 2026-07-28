package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ryanycheng/Syrogo/internal/accounting"
	"github.com/ryanycheng/Syrogo/internal/config"
	"github.com/ryanycheng/Syrogo/internal/execution"
	"github.com/ryanycheng/Syrogo/internal/latency"
	internallogging "github.com/ryanycheng/Syrogo/internal/logging"
	"github.com/ryanycheng/Syrogo/internal/provider"
	"github.com/ryanycheng/Syrogo/internal/quota"
	"github.com/ryanycheng/Syrogo/internal/runtime"
	"gopkg.in/yaml.v3"
)

func newAdminTestHandler(t *testing.T) *Handler {
	t.Helper()
	h := newTestHandler(t, map[string]provider.Provider{"mock": provider.NewMock("mock")}, testRoutingConfig(), testInbounds(), testOutbounds())
	h.admin = config.AdminConfig{Enabled: true, Token: "admin-ui-token", Logs: config.AdminLogsConfig{Enabled: true, Path: filepath.Join(t.TempDir(), "dev.log"), MaxBytes: 128}}
	return h
}

func TestAdminUIHiddenWhenDisabled(t *testing.T) {
	h := newTestHandler(t, map[string]provider.Provider{"mock": provider.NewMock("mock")}, testRoutingConfig(), testInbounds(), testOutbounds())

	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/", nil))

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestAdminUIReturnsIndexHTMLWhenEnabled(t *testing.T) {
	h := newAdminTestHandler(t)

	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if contentType := w.Header().Get("Content-Type"); !strings.Contains(contentType, "text/html") {
		t.Fatalf("content type = %q, want text/html", contentType)
	}
	body := w.Body.String()
	for _, want := range []string{"Admin UI token", "/admin/app.js", "provider-test-model", "provider-models-json", "Strict JSON array of canonical names and aliases", "An empty array means unrestricted", "each fallback provider then resolves", "atomically update the config and hot-apply", "provider-quota-json", "rolling and fixed interval/daily/weekly", "reset_all", "Usage totals are all-time", "Timeline range", "client-days", "data-client-days=\"7\"", "data-client-days=\"30\"", "data-client-days=\"90\"", "clients-warning", "client-detail", "client-quota-json", "hourly-requests", "type\":\"requests", "max_tokens", "max_cost_usd", "Each window has exactly one type", "unpriced usage counts as $0", "Usage and quota are global", "client-bindings-section", "client-binding-error", "binding-inbound", "binding-tag", "remove every binding first", "usage-range", "usage-start-date", "usage-end-date", "log-bytes", "logs-meta", "overview-summary", "sessions-table", "sessions-view-cards", "sessions-view-table", "session-status-filter", "live-requests-table", "refresh-live-requests", "config-diff", "Apply current file", "config-history", "Debug", "dry-run-model", "First-match wins", "original requested model", "route-match-models", "One pattern per line", "match: null", "only * is a wildcard", "Agent mode, Plan mode, prompts, and tool results are not inspected", "Processing has three stages", "Unknown models do not automatically downgrade"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %s, want %s", body, want)
		}
	}
}

func TestAdminUIReturnsStaticAssetsWhenEnabled(t *testing.T) {
	h := newAdminTestHandler(t)

	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/app.js", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if contentType := w.Header().Get("Content-Type"); !strings.Contains(contentType, "javascript") {
		t.Fatalf("content type = %q, want javascript", contentType)
	}
	body := w.Body.String()
	for _, want := range []string{"/admin/sessions", "startSessionsRefresh", "data-provider-check-protocol", "model: testModel", "#provider-models-json", "item.models || []", "models = JSON.parse", "models, capabilities", "#provider-quota-json", "max_tokens", "used_tokens", "fixed_period", "last_quota_exceeded_at", "usage_estimation_mode", "renderSessionCards", "item.tag", "/admin/config/clients", "/admin/config/clients/metrics?days=", "Promise.allSettled", "Metrics unavailable", "clientPayload", "return { name: value(\"#client-name\"), token: value(\"#client-token\"), quota }", "#client-quota-json", "used_cost_usd", "max_cost_usd", "unpriced_count", "quota-warning", "window.type", "error.body = body", "binding_tag_last_source", "route_names", "showClientBindingError", "Add or update another Client binding", "from_tags", "/admin/config/client-binding/upsert", "/admin/config/client-binding/delete", "Remove all ${bindings.length} binding(s)", "renderClientBindings", "response?.saved", "response?.applied", "Client saved and applied.", "Client deleted and applied.", "/admin/config/client/usage?name=", "renderClientHeatmap", "Math.log1p", "tabindex=\"0\"", "role=\"img\"", "partial", "unknown", "Daily aggregates, not a per-request audit log", "/admin/usage", "/admin/logs", "/admin/overview", "/admin/latency/active", "refreshLiveRequests", "redacted_content", "window.confirm", "renderConfigDiff", "max_bytes", "/admin/config/apply", "/admin/config/history", "/admin/config/rollback", "/admin/debug/traces", "/admin/debug/route-dry-run", "/admin/debug/providers", "/admin/config/history/diff", "routeOrderRevision", "response.order_revision", "item.match?.models", "match: matchModels.length ? { models: matchModels } : null", "Priority", "Request models / fallback", "data-route-move=\"up\"", "data-route-move=\"down\"", "/admin/config/routes/reorder", "from_index: fromIndex", "to_index: toIndex", "expected_revision: routeOrderRevision", "error.status === 409", "Route order changed elsewhere", "Route saved and applied.", "Route deleted and applied."} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %s, want %s", body, want)
		}
	}
	if strings.Contains(body, "Client saved. Click Apply") || strings.Contains(body, "Client deleted. Click Apply") {
		t.Fatalf("client CRUD still instructs a separate Apply: %s", body)
	}
	if strings.Contains(body, "Route saved. Click Apply") || strings.Contains(body, "Route deleted. Click Apply") {
		t.Fatalf("route CRUD still instructs a separate Apply: %s", body)
	}
	payloadStart := strings.Index(body, "function providerPayload()")
	if payloadStart < 0 {
		t.Fatal("providerPayload function not found")
	}
	payloadEnd := strings.Index(body[payloadStart:], "function validateProviderDraft()")
	if payloadEnd < 0 {
		t.Fatal("validateProviderDraft function not found")
	}
	payloadSource := body[payloadStart : payloadStart+payloadEnd]
	if strings.Contains(payloadSource, "metrics") {
		t.Fatalf("providerPayload unexpectedly includes metrics: %s", payloadSource)
	}
	clientPayloadStart := strings.Index(body, "function clientPayload()")
	if clientPayloadStart < 0 {
		t.Fatal("clientPayload function not found")
	}
	clientPayloadEnd := strings.Index(body[clientPayloadStart:], "async function saveClient()")
	if clientPayloadEnd < 0 {
		t.Fatal("saveClient function not found")
	}
	clientPayloadSource := body[clientPayloadStart : clientPayloadStart+clientPayloadEnd]
	for _, forbidden := range []string{"inbound:", "tag:", "bindings:"} {
		if strings.Contains(clientPayloadSource, forbidden) {
			t.Fatalf("clientPayload includes Binding field %q: %s", forbidden, clientPayloadSource)
		}
	}
	deleteStart := strings.Index(body, "async function deleteClient()")
	if deleteStart < 0 {
		t.Fatal("deleteClient function not found")
	}
	deleteEnd := strings.Index(body[deleteStart:], "function renderClientBindingEditor()")
	if deleteEnd < 0 {
		t.Fatal("renderClientBindingEditor function not found")
	}
	if strings.Contains(body[deleteStart:deleteStart+deleteEnd], "{ inbound, name }") {
		t.Fatalf("client delete still sends legacy inbound field: %s", body[deleteStart:deleteStart+deleteEnd])
	}
}

func TestAdminUIClientHeatmapStylesUseNativeGridAndFiveLevels(t *testing.T) {
	h := newAdminTestHandler(t)
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/styles.css", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{".contribution-heatmap", "display: grid", "grid-template-rows: repeat(7", ".heatmap-cell.level-1", ".heatmap-cell.level-2", ".heatmap-cell.level-3", ".heatmap-cell.level-4", ".heatmap-cell.level-5", ".heatmap-cell.unknown", ".heatmap-cell.partial", ".heatmap-cell:hover::after", ".heatmap-cell:focus::after"} {
		if !strings.Contains(body, want) {
			t.Fatalf("styles missing %q", want)
		}
	}
}

func TestAdminUIRouteOrderingStyles(t *testing.T) {
	h := newAdminTestHandler(t)
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/styles.css", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"button:disabled", ".route-table", ".route-order-actions", ".route-patterns"} {
		if !strings.Contains(body, want) {
			t.Fatalf("styles missing %q", want)
		}
	}
}

func TestAdminAPIRejectsClientToken(t *testing.T) {
	h := newAdminTestHandler(t)

	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodGet, "/admin/usage", "client-token", nil))

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestAdminUsageAcceptsAdminToken(t *testing.T) {
	h := newAdminTestHandler(t)

	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodGet, "/admin/usage?group_by=source&window=day&bucket=2026-04-27", "admin-ui-token", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"items"`) {
		t.Fatalf("body = %s, want items", w.Body.String())
	}
}

func TestAdminQuotaAcceptsAdminToken(t *testing.T) {
	h := newAdminTestHandler(t)

	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodGet, "/admin/quota", "admin-ui-token", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"outbound"`) || !strings.Contains(w.Body.String(), `"client"`) {
		t.Fatalf("body = %s, want quota sections", w.Body.String())
	}
}

func TestAdminLatencyAcceptsAdminToken(t *testing.T) {
	h := newAdminTestHandler(t)

	mux := http.NewServeMux()
	h.Register(mux)

	for _, path := range []string{"/admin/latency", "/admin/latency/summary", "/admin/latency/active"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, authorizedRequest(http.MethodGet, path, "admin-ui-token", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("path=%s status = %d, want 200, body=%s", path, w.Code, w.Body.String())
		}
	}
}

func TestAdminLatencyRejectsClientToken(t *testing.T) {
	h := newAdminTestHandler(t)

	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodGet, "/admin/latency", "client-token", nil))

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestAdminActiveLatencyReturnsWaitingDuration(t *testing.T) {
	h := newAdminTestHandler(t)
	latencyStore := latency.NewStore(10)
	state := h.runtimeState()
	state.Dispatcher = execution.NewDispatcherWithStoreQuotaHealthEventsAndLatency(accounting.NewMemoryStore(), nil, nil, nil, latencyStore)
	state.LatencyStore = latencyStore
	h.ApplyRuntime(state)
	ctx, recorder := latency.Start(context.Background(), latencyStore, "active-1", "POST", "/v1/messages", time.Now().Add(-2*time.Second))
	recorder.SetRoute("anthropic-entry", "anthropic_messages", "claude-key", "office")
	recorder.SetProvider("anthropic-primary", "anthropic_messages", time.Now().Add(-time.Second))
	recorder.SetStreamState(latency.StreamStateWaitingFirstToken)
	defer recorder.Finish(http.StatusOK, time.Now())
	_ = ctx

	mux := http.NewServeMux()
	h.Register(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodGet, "/admin/latency/active", "admin-ui-token", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{`"request_id":"active-1"`, `"stream_state":"waiting_first_token"`, `"outbound_name":"anthropic-primary"`, `"elapsed_ms":`, `"waiting_first_token_ms":`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %s, want %s", body, want)
		}
	}
	for _, forbidden := range []string{"client-token", "admin-ui-token", "secret", "prompt"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("body leaked %q: %s", forbidden, body)
		}
	}
}

func TestAdminOverviewAcceptsAdminTokenAndRejectsClientToken(t *testing.T) {
	h := newAdminTestHandler(t)

	mux := http.NewServeMux()
	h.Register(mux)

	ok := httptest.NewRecorder()
	mux.ServeHTTP(ok, authorizedRequest(http.MethodGet, "/admin/overview", "admin-ui-token", nil))
	if ok.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", ok.Code, ok.Body.String())
	}
	for _, want := range []string{`"usage"`, `"latency"`, `"quota"`, `"health"`, `"admin"`, `"logs_enabled"`} {
		if !strings.Contains(ok.Body.String(), want) {
			t.Fatalf("body = %s, want %s", ok.Body.String(), want)
		}
	}

	denied := httptest.NewRecorder()
	mux.ServeHTTP(denied, authorizedRequest(http.MethodGet, "/admin/overview", "client-token", nil))
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", denied.Code)
	}
}

func TestAdminConfigReadsConfiguredPath(t *testing.T) {
	h := newAdminTestHandler(t)
	h.configPath = filepath.Join(t.TempDir(), "config.yaml")
	content := []byte(validGatewayConfigYAML() + `
admin:
  enabled: true
  token: admin-secret
accounting:
  enabled: true
  expose_http: true
  admin_token: accounting-secret
`)
	if err := os.WriteFile(h.configPath, content, 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodGet, "/admin/config", "admin-ui-token", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var response struct {
		ConfigReady     bool   `json:"config_ready"`
		RedactedContent string `json:"redacted_content"`
		Revision        string `json:"revision"`
		Checksum        string `json:"checksum"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if !response.ConfigReady || !strings.Contains(response.RedactedContent, "openai-entry") || !strings.HasPrefix(response.Revision, "sha256:") || response.Checksum == "" {
		t.Fatalf("response = %#v, want ready redacted config with revision", response)
	}
	if strings.Contains(w.Body.String(), h.configPath) || strings.Contains(w.Body.String(), `"content"`) {
		t.Fatalf("response exposed path or raw content: %s", w.Body.String())
	}
	if strings.Contains(response.RedactedContent, "admin-secret") || strings.Contains(response.RedactedContent, "accounting-secret") || strings.Contains(response.RedactedContent, "client-token") {
		t.Fatalf("redacted_content = %s, want secrets redacted", response.RedactedContent)
	}
	if !strings.Contains(response.RedactedContent, "<redacted>") {
		t.Fatalf("redacted_content = %s, want redaction marker", response.RedactedContent)
	}
	if w.Header().Get("ETag") != response.Revision || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("headers = %#v, want ETag revision and no-store", w.Header())
	}
}

func TestAdminLogsReadsConfiguredPathAndRedactsSecrets(t *testing.T) {
	h := newAdminTestHandler(t)
	logPath := h.admin.Logs.Path
	content := "first\nAuthorization: Bearer secret-token\napi_key=secret-key\n" + strings.Repeat("padding\n", 40) + "last\n"
	if err := os.WriteFile(logPath, []byte(content), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodGet, "/admin/logs?lines=3&bytes=96", "admin-ui-token", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	var response struct {
		Path      string `json:"path"`
		MaxBytes  int    `json:"max_bytes"`
		Lines     int    `json:"lines"`
		Truncated bool   `json:"truncated"`
		Content   string `json:"content"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if response.Path == "" || !strings.Contains(response.Content, "last") {
		t.Fatalf("body = %s, want log content", body)
	}
	if response.MaxBytes != 96 || response.Lines != 3 || !response.Truncated {
		t.Fatalf("response = %#v, want max_bytes 96, lines 3, truncated true", response)
	}
	if strings.Contains(response.Content, "secret-token") || strings.Contains(response.Content, "secret-key") {
		t.Fatalf("body = %s, want redacted secrets", body)
	}
}

func TestAdminLogsUsesCoveredRecentBufferAndStatusFamilies(t *testing.T) {
	h := newAdminTestHandler(t)
	buffer := internallogging.NewRecentBuffer(5*time.Minute, 8*1024*1024)
	h.SetRecentLogs(buffer)
	now := time.Now().UTC()
	lines := []string{
		fmt.Sprintf("time=%s level=INFO msg=ok status=200", now.Add(100*time.Millisecond).Format(time.RFC3339Nano)),
		fmt.Sprintf("time=%s level=WARN msg=not-found status=404", now.Add(200*time.Millisecond).Format(time.RFC3339Nano)),
		fmt.Sprintf("time=%s level=ERROR msg=bad-gateway status=502", now.Add(300*time.Millisecond).Format(time.RFC3339Nano)),
		fmt.Sprintf("time=%s level=ERROR msg=unavailable status=503", now.Add(400*time.Millisecond).Format(time.RFC3339Nano)),
	}
	_, _ = buffer.Write([]byte(strings.Join(lines, "\n") + "\n"))
	mux := http.NewServeMux()
	h.Register(mux)

	for _, test := range []struct {
		status string
		want   []string
		not    []string
	}{
		{status: "5xx", want: []string{"bad-gateway", "unavailable"}, not: []string{"not-found", "msg=ok"}},
		{status: "4xx", want: []string{"not-found"}, not: []string{"bad-gateway", "unavailable", "msg=ok"}},
	} {
		w := httptest.NewRecorder()
		url := fmt.Sprintf("/admin/logs?since=%s&until=%s&limit=10&status=%s", now.Format(time.RFC3339Nano), now.Add(time.Second).Format(time.RFC3339Nano), test.status)
		mux.ServeHTTP(w, authorizedRequest(http.MethodGet, url, "admin-ui-token", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("status filter %s: status = %d, body=%s", test.status, w.Code, w.Body.String())
		}
		var response struct {
			Source string         `json:"source"`
			Items  []adminLogItem `json:"items"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Source != "memory" {
			t.Fatalf("source = %q, want memory", response.Source)
		}
		for _, want := range test.want {
			if !strings.Contains(w.Body.String(), want) {
				t.Fatalf("filter %s body = %s, want %s", test.status, w.Body.String(), want)
			}
		}
		for _, unwanted := range test.not {
			if strings.Contains(w.Body.String(), unwanted) {
				t.Fatalf("filter %s body = %s, unwanted %s", test.status, w.Body.String(), unwanted)
			}
		}
	}
}

func TestAdminLogsFallsBackToFileWhenRecentMatchesExceedLimit(t *testing.T) {
	h := newAdminTestHandler(t)
	now := time.Now().UTC()
	fileContent := fmt.Sprintf("time=%s level=ERROR msg=file-newest status=503\ntime=%s level=ERROR msg=file-older status=502\n", now.Add(200*time.Millisecond).Format(time.RFC3339Nano), now.Add(100*time.Millisecond).Format(time.RFC3339Nano))
	if err := os.WriteFile(h.admin.Logs.Path, []byte(fileContent), 0o600); err != nil {
		t.Fatal(err)
	}
	buffer := internallogging.NewRecentBuffer(5*time.Minute, 8*1024*1024)
	h.SetRecentLogs(buffer)
	_, _ = buffer.Write([]byte(fileContent))
	mux := http.NewServeMux()
	h.Register(mux)
	w := httptest.NewRecorder()
	url := fmt.Sprintf("/admin/logs?since=%s&until=%s&limit=1&status=5xx", now.Format(time.RFC3339Nano), now.Add(time.Second).Format(time.RFC3339Nano))
	mux.ServeHTTP(w, authorizedRequest(http.MethodGet, url, "admin-ui-token", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"source":"file"`) || !strings.Contains(w.Body.String(), `"has_more":true`) {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
}

func TestAdminLogsStructuredResponseUsesRedactedContent(t *testing.T) {
	h := newAdminTestHandler(t)
	content := "time=2026-07-22T09:00:00Z level=ERROR msg=failed status=502 token=secret-token\n"
	if err := os.WriteFile(h.admin.Logs.Path, []byte(content), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodGet, "/admin/logs?since=2026-07-22T08:00:00Z&until=2026-07-22T10:00:00Z&limit=10&level=ERROR", "admin-ui-token", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	var response struct {
		Content string         `json:"content"`
		Items   []adminLogItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(response.Items) != 1 || response.Content != response.Items[0].Content {
		t.Fatalf("response = %#v, want matching content", response)
	}
	if strings.Contains(response.Content, "secret-token") || !strings.Contains(response.Content, "<redacted>") {
		t.Fatalf("content = %q, want redacted secret", response.Content)
	}
}

func TestAdminConfigValidateAcceptsAdminTokenAndAccountingToken(t *testing.T) {
	h := newAdminTestHandler(t)
	h.accounting = config.AccountingConfig{Enabled: true, ExposeHTTP: true, AdminToken: "accounting-token"}

	mux := http.NewServeMux()
	h.Register(mux)

	for _, token := range []string{"admin-ui-token", "accounting-token"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/admin/config/validate", token, []byte(validGatewayConfigYAML())))
		if w.Code != http.StatusOK {
			t.Fatalf("token=%s status = %d, want 200, body=%s", token, w.Code, w.Body.String())
		}
	}
}

func TestAdminUIDoesNotOverrideConfigAPI(t *testing.T) {
	h := newAdminTestHandler(t)
	h.accounting = config.AccountingConfig{Enabled: true, ExposeHTTP: true, AdminToken: "accounting-token"}

	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/admin/config/validate", strings.NewReader(validGatewayConfigYAML())))

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 from config API", w.Code)
	}
}

func TestAdminAuditLogsActionWithoutSecrets(t *testing.T) {
	var logs bytes.Buffer
	h := newAdminTestHandler(t)
	h.logger = slog.New(slog.NewTextHandler(&logs, nil))
	logPath := h.admin.Logs.Path
	if err := os.WriteFile(logPath, []byte("token=secret-token\n"), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodGet, "/admin/logs", "admin-ui-token", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	text := logs.String()
	if text != "" {
		t.Fatalf("logs = %s, want no audit for successful log reads", text)
	}

	methodNotAllowed := httptest.NewRecorder()
	mux.ServeHTTP(methodNotAllowed, authorizedRequest(http.MethodPost, "/admin/logs", "admin-ui-token", nil))
	if methodNotAllowed.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", methodNotAllowed.Code)
	}
	text = logs.String()
	if !strings.Contains(text, "action=logs") || !strings.Contains(text, "status=405") {
		t.Fatalf("logs = %s, want method not allowed audit", text)
	}
}

type fakeConfigReloader struct {
	mutate func(context.Context, string, ConfigMutation) (ReloadResult, error)
	update func(context.Context, string, []byte) (ConfigUpdateResult, error)
}

func (fakeConfigReloader) ApplyConfig(context.Context) (ReloadResult, error) {
	return ReloadResult{OK: true, Applied: true, HistoryID: "history-1", QuotaStateReset: true}, nil
}

func (f fakeConfigReloader) MutateConfig(ctx context.Context, reason string, mutate ConfigMutation) (ReloadResult, error) {
	if f.mutate != nil {
		return f.mutate(ctx, reason, mutate)
	}
	return ReloadResult{OK: true, Saved: true, Applied: true, Reason: reason, HistoryID: "history-mutation"}, nil
}

func (f fakeConfigReloader) UpdateConfig(ctx context.Context, revision string, data []byte) (ConfigUpdateResult, error) {
	if f.update != nil {
		return f.update(ctx, revision, data)
	}
	return ConfigUpdateResult{Saved: true, Applied: false, Revision: "sha256:updated", Checksum: "updated"}, nil
}

func (fakeConfigReloader) History() []HistoryItem {
	return []HistoryItem{{ID: "history-1", CreatedAt: "2026-07-02T00:00:00Z", Reason: "apply", Checksum: "abc"}}
}

func (fakeConfigReloader) HistoryDiff(id string) (HistoryDiff, error) {
	return HistoryDiff{ID: id, CurrentContent: "admin:\n  token: \"<redacted>\"\n", HistoryContent: "admin:\n  token: \"<redacted>\"\n"}, nil
}

func (fakeConfigReloader) Rollback(context.Context, string) (ReloadResult, error) {
	return ReloadResult{OK: true, Applied: true, HistoryID: "history-2", QuotaStateReset: true}, nil
}

func TestAdminConfigUpdatePreconditionsAndPermissions(t *testing.T) {
	h := newAdminTestHandler(t)
	h.accounting = config.AccountingConfig{Enabled: true, ExposeHTTP: true, AdminToken: "accounting-token"}
	var gotRevision string
	h.SetConfigReloader(fakeConfigReloader{update: func(_ context.Context, revision string, _ []byte) (ConfigUpdateResult, error) {
		gotRevision = revision
		if revision == "sha256:stale" {
			return ConfigUpdateResult{}, &ConfigRevisionConflictError{CurrentRevision: "sha256:current"}
		}
		return ConfigUpdateResult{Saved: true, Applied: false, Revision: "sha256:new", Checksum: "new"}, nil
	}})
	mux := http.NewServeMux()
	h.Register(mux)

	accounting := httptest.NewRecorder()
	request := authorizedRequest(http.MethodPost, "/admin/config/update", "accounting-token", []byte(validGatewayConfigYAML()))
	request.Header.Set("If-Match", "*")
	mux.ServeHTTP(accounting, request)
	if accounting.Code != http.StatusUnauthorized {
		t.Fatalf("accounting status = %d, want 401", accounting.Code)
	}

	missing := httptest.NewRecorder()
	mux.ServeHTTP(missing, authorizedRequest(http.MethodPost, "/admin/config/update", "admin-ui-token", []byte(validGatewayConfigYAML())))
	if missing.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing If-Match status = %d, want 428", missing.Code)
	}

	stale := httptest.NewRecorder()
	request = authorizedRequest(http.MethodPost, "/admin/config/update", "admin-ui-token", []byte(validGatewayConfigYAML()))
	request.Header.Set("If-Match", "sha256:stale")
	mux.ServeHTTP(stale, request)
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), `"code":"config_revision_conflict"`) || !strings.Contains(stale.Body.String(), `"current_revision":"sha256:current"`) {
		t.Fatalf("stale status = %d, body=%s", stale.Code, stale.Body.String())
	}

	force := httptest.NewRecorder()
	request = authorizedRequest(http.MethodPost, "/admin/config/update", "admin-ui-token", []byte(validGatewayConfigYAML()))
	request.Header.Set("If-Match", "*")
	mux.ServeHTTP(force, request)
	if force.Code != http.StatusOK || gotRevision != "*" || !strings.Contains(force.Body.String(), `"saved":true`) {
		t.Fatalf("force status = %d, revision=%q, body=%s", force.Code, gotRevision, force.Body.String())
	}
}

func TestAdminConfigApplyUsesReloader(t *testing.T) {
	h := newAdminTestHandler(t)
	h.SetConfigReloader(fakeConfigReloader{})
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/admin/config/apply", "admin-ui-token", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"applied":true`) || !strings.Contains(w.Body.String(), `"history_id":"history-1"`) {
		t.Fatalf("body = %s, want applied reload result", w.Body.String())
	}
}

func TestAdminConfigHistoryUsesReloader(t *testing.T) {
	h := newAdminTestHandler(t)
	h.SetConfigReloader(fakeConfigReloader{})
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodGet, "/admin/config/history", "admin-ui-token", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"id":"history-1"`) {
		t.Fatalf("body = %s, want history item", w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"path"`) {
		t.Fatalf("body exposed path: %s", w.Body.String())
	}
}

func TestAdminConfigRollbackUsesReloader(t *testing.T) {
	h := newAdminTestHandler(t)
	h.SetConfigReloader(fakeConfigReloader{})
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/admin/config/rollback", "admin-ui-token", []byte(`{"id":"history-1"}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"applied":true`) {
		t.Fatalf("body = %s, want applied rollback result", w.Body.String())
	}
}

func TestAdminConfigHistoryDiffUsesReloader(t *testing.T) {
	h := newAdminTestHandler(t)
	h.SetConfigReloader(fakeConfigReloader{})
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodGet, "/admin/config/history/diff?id=history-1", "admin-ui-token", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"id":"history-1"`) || !strings.Contains(w.Body.String(), `redacted`) {
		t.Fatalf("body = %s, want redacted history diff", w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", w.Header().Get("Cache-Control"))
	}
}

func TestAdminDebugTracesReturnsLatencySnapshot(t *testing.T) {
	store := latency.NewStore(10)
	store.Record(latency.Trace{RequestID: "req-1", MatchedRule: "office", PlannedSteps: []latency.PlanStep{{OutboundName: "mock", OutboundProtocol: "mock"}}})
	h := newAdminTestHandler(t)
	state := h.runtimeState()
	dispatcher := execution.NewDispatcherWithStoreQuotaHealthEventsAndLatency(nil, nil, nil, nil, store)
	h.ApplyRuntime(RuntimeState{
		Router:       state.Router,
		Dispatcher:   dispatcher,
		Inbounds:     state.Inbounds,
		LatencyStore: store,
		Admin:        state.Admin,
	})
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodGet, "/admin/debug/traces", "admin-ui-token", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"request_id":"req-1"`) || !strings.Contains(w.Body.String(), `"matched_rule":"office"`) {
		t.Fatalf("body = %s, want trace metadata", w.Body.String())
	}
}

func TestAdminRouteDryRunReturnsPlanWithoutToken(t *testing.T) {
	h := newAdminTestHandler(t)
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/admin/debug/route-dry-run", "admin-ui-token", []byte(`{"inbound":"openai-entry","client":"office-key","model":"gpt-4"}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"matched_rule"`) || !strings.Contains(body, `"outbound_name"`) {
		t.Fatalf("body = %s, want dry-run plan", body)
	}
	if strings.Contains(body, "client-token") {
		t.Fatalf("body = %s, want no client token", body)
	}
}

func TestAdminProviderDebugReturnsAggregates(t *testing.T) {
	h := newAdminTestHandler(t)
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodGet, "/admin/debug/providers", "admin-ui-token", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	for _, want := range []string{`"health"`, `"outbound_quota"`, `"events"`, `"latency_summary"`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("body = %s, want %s", w.Body.String(), want)
		}
	}
}

func TestAdminConfigResourceAPIsReadRedactedResources(t *testing.T) {
	h := newAdminTestHandler(t)
	h.configPath = filepath.Join(t.TempDir(), "config.yaml")
	content := validGatewayConfigYAML() + `
admin:
  enabled: true
  token: admin-secret
`
	if err := os.WriteFile(h.configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	mux := http.NewServeMux()
	h.Register(mux)

	for _, path := range []string{"/admin/session", "/admin/config/options", "/admin/config/providers", "/admin/config/clients", "/admin/config/routes"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, authorizedRequest(http.MethodGet, path, "admin-ui-token", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("path=%s status = %d, want 200, body=%s", path, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "client-token") || strings.Contains(w.Body.String(), "admin-secret") {
			t.Fatalf("path=%s body = %s, want secrets redacted", path, w.Body.String())
		}
		if path == "/admin/config/providers" {
			for _, want := range []string{`"models":`, `"capabilities":{"responses_previous_response_id":`, `"quota":{"enabled":`, `"proxy":{"url":`} {
				if !strings.Contains(w.Body.String(), want) {
					t.Fatalf("path=%s body = %s, want %s", path, w.Body.String(), want)
				}
			}
		}
	}
}

type fileMutationReloader struct {
	path string
}

func (f fileMutationReloader) ApplyConfig(context.Context) (ReloadResult, error) {
	return ReloadResult{}, nil
}

func (f fileMutationReloader) MutateConfig(_ context.Context, reason string, mutate ConfigMutation) (ReloadResult, error) {
	cfg, err := config.Load(f.path)
	if err != nil {
		return ReloadResult{}, err
	}
	next, err := mutate(cfg)
	if err != nil {
		return ReloadResult{}, err
	}
	data, err := yaml.Marshal(next)
	if err != nil {
		return ReloadResult{}, err
	}
	if err := config.WriteValidatedFile(f.path, data); err != nil {
		return ReloadResult{}, err
	}
	return ReloadResult{OK: true, Saved: true, Applied: true, Reason: reason, HistoryID: "history-file"}, nil
}

func (fileMutationReloader) UpdateConfig(context.Context, string, []byte) (ConfigUpdateResult, error) {
	return ConfigUpdateResult{}, nil
}
func (fileMutationReloader) History() []HistoryItem                  { return nil }
func (fileMutationReloader) HistoryDiff(string) (HistoryDiff, error) { return HistoryDiff{}, nil }
func (fileMutationReloader) Rollback(context.Context, string) (ReloadResult, error) {
	return ReloadResult{}, nil
}

func TestAdminConfigProviderMutationUsesAtomicReloader(t *testing.T) {
	h := newAdminTestHandler(t)
	var gotReason string
	h.SetConfigReloader(fakeConfigReloader{mutate: func(_ context.Context, reason string, mutate ConfigMutation) (ReloadResult, error) {
		gotReason = reason
		cfg := config.Config{Outbounds: []config.OutboundSpec{{Name: "mock", Protocol: "mock", Tag: "mock-tag"}}}
		next, err := mutate(cfg)
		if err != nil {
			return ReloadResult{}, err
		}
		if config.OutboundEnabled(next.Outbounds[0]) {
			t.Fatal("enabled mutation did not disable provider")
		}
		return ReloadResult{OK: true, Saved: true, Applied: true, Reason: reason, HistoryID: "history-provider", QuotaStateReset: true}, nil
	}})
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/admin/config/provider/enabled", "admin-ui-token", []byte(`{"name":"mock","enabled":false}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	for _, want := range []string{`"saved":true`, `"applied":true`, `"history_id":"history-provider"`, `"quota_state_reset":true`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("body = %s, want %s", w.Body.String(), want)
		}
	}
	if gotReason != "provider_enabled_mock_false" {
		t.Fatalf("reason = %q", gotReason)
	}
}

func TestAdminConfigProviderDeleteRejectsReferences(t *testing.T) {
	h := newAdminTestHandler(t)
	h.SetConfigReloader(fakeConfigReloader{mutate: func(_ context.Context, _ string, mutate ConfigMutation) (ReloadResult, error) {
		cfg := config.Config{
			Outbounds: []config.OutboundSpec{{Name: "mock", Protocol: "mock", Tag: "mock-tag"}},
			Routing:   config.RoutingConfig{Rules: []config.RoutingRule{{Name: "office", ToTags: []string{"mock-tag"}}}},
		}
		_, err := mutate(cfg)
		return ReloadResult{}, err
	}})
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/admin/config/provider/delete", "admin-ui-token", []byte(`{"name":"mock"}`)))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `routing rule`) || !strings.Contains(w.Body.String(), `mock-tag`) {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
}

func TestAdminConfigProviderUpsertPreservesRedactedSecret(t *testing.T) {
	h := newAdminTestHandler(t)
	h.configPath = filepath.Join(t.TempDir(), "config.yaml")
	content := `
listeners:
  - name: public
    listen: ":8080"
    inbounds: [openai-entry]
inbounds:
  - name: openai-entry
    protocol: openai_chat
    path: /v1/chat/completions
    clients:
      - name: office-key
        token: client-token
        tag: office
outbounds:
  - name: openai
    protocol: openai_chat
    endpoint: https://api.example.test/v1/chat/completions
    auth_token: old-secret
    tag: openai-tag
routing:
  rules:
    - name: office-route
      from_tags: [office]
      to_tags: [openai-tag]
      strategy: failover
`
	if err := os.WriteFile(h.configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	h.SetConfigReloader(fileMutationReloader{path: h.configPath})
	mux := http.NewServeMux()
	h.Register(mux)

	body := []byte(`{"name":"openai","protocol":"openai_chat","endpoint":"https://api.example.test/v1","auth_token":"<redacted>","tag":"openai-tag","models":[{"name":"gpt-4o","aliases":["fast","public-model"]}],"capabilities":{"usage_estimation":true,"usage_estimation_mode":"heuristic"},"quota":{"enabled":true,"windows":[{"name":"hourly","duration":"1h","max_requests":100}],"cooldown":"1m","probe_interval":"30s"},"proxy":{"url":"https://proxy.example.test"}}`)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/admin/config/provider/upsert", "admin-ui-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	cfg, err := config.Load(h.configPath)
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	if cfg.Outbounds[0].AuthToken != "old-secret" || cfg.Outbounds[0].Endpoint != "https://api.example.test/v1" {
		t.Fatalf("outbound = %#v, want preserved token and updated endpoint", cfg.Outbounds[0])
	}
	outbound := cfg.Outbounds[0]
	if len(outbound.Models) != 1 || outbound.Models[0].Name != "gpt-4o" || len(outbound.Models[0].Aliases) != 2 || outbound.Models[0].Aliases[0] != "fast" {
		t.Fatalf("models = %#v, want canonical model and aliases", outbound.Models)
	}
	if !outbound.Capabilities.UsageEstimation || outbound.Capabilities.UsageEstimationMode != "heuristic" {
		t.Fatalf("capabilities = %#v, want usage estimation", outbound.Capabilities)
	}
	if !outbound.Quota.Enabled || len(outbound.Quota.Windows) != 1 || outbound.Quota.Windows[0].MaxRequests != 100 || outbound.Quota.Cooldown != "1m" || outbound.Quota.ProbeInterval != "30s" {
		t.Fatalf("quota = %#v, want decoded snake_case quota", outbound.Quota)
	}
	if outbound.Proxy.URL != "https://proxy.example.test" {
		t.Fatalf("proxy = %#v, want decoded snake_case URL", outbound.Proxy)
	}
}

func TestAdminConfigClientUpsertUsesAtomicReloaderAndPreservesTokens(t *testing.T) {
	tests := []struct {
		name      string
		token     string
		wantToken string
	}{
		{name: "empty", token: "", wantToken: "old-client-token"},
		{name: "redacted", token: config.RedactedValue, wantToken: "old-client-token"},
		{name: "rotation", token: "rotated-client-token", wantToken: "rotated-client-token"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newAdminTestHandler(t)
			var gotReason string
			h.SetConfigReloader(fakeConfigReloader{mutate: func(_ context.Context, reason string, mutate ConfigMutation) (ReloadResult, error) {
				gotReason = reason
				next, err := mutate(clientMutationConfig())
				if err != nil {
					return ReloadResult{}, err
				}
				client := next.Clients[0]
				if client.Token != tc.wantToken {
					t.Fatalf("client = %#v", client)
				}
				return ReloadResult{OK: true, Saved: true, Applied: true, Reason: reason, HistoryID: "history-client", QuotaStateReset: true}, nil
			}})
			mux := http.NewServeMux()
			h.Register(mux)

			body, err := json.Marshal(clientResourceRequest{Name: "office-key", Token: tc.token})
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/admin/config/client/upsert", "admin-ui-token", body))
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
			}
			for _, want := range []string{`"saved":true`, `"applied":true`, `"quota_state_reset":true`} {
				if !strings.Contains(w.Body.String(), want) {
					t.Fatalf("body = %s, want %s", w.Body.String(), want)
				}
			}
			if gotReason != "client_upsert_office-key" {
				t.Fatalf("reason = %q", gotReason)
			}
		})
	}
}

func TestAdminConfigClientUpsertRejectsMissingTokenForNewClient(t *testing.T) {
	for _, token := range []string{"", config.RedactedValue} {
		t.Run(token, func(t *testing.T) {
			h := newAdminTestHandler(t)
			h.SetConfigReloader(fakeConfigReloader{mutate: func(_ context.Context, _ string, mutate ConfigMutation) (ReloadResult, error) {
				_, err := mutate(clientMutationConfig())
				return ReloadResult{}, err
			}})
			mux := http.NewServeMux()
			h.Register(mux)
			body, err := json.Marshal(clientResourceRequest{Name: "mobile-key", Token: token})
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/admin/config/client/upsert", "admin-ui-token", body))
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "token is required") || !strings.Contains(w.Body.String(), "mobile-key") {
				t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestAdminConfigClientTagRemovalProtectsReferencedLastSource(t *testing.T) {
	tests := []struct {
		name   string
		shared bool
		path   string
		body   string
	}{
		{name: "delete last source", path: "/admin/config/client-binding/delete", body: `{"inbound":"openai-entry","ref":"office-key"}`},
		{name: "change last source", path: "/admin/config/client-binding/upsert", body: `{"inbound":"openai-entry","ref":"office-key","tag":"mobile"}`},
		{name: "delete shared source", shared: true, path: "/admin/config/client-binding/delete", body: `{"inbound":"openai-entry","ref":"office-key"}`},
		{name: "change shared source", shared: true, path: "/admin/config/client-binding/upsert", body: `{"inbound":"openai-entry","ref":"office-key","tag":"mobile"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newAdminTestHandler(t)
			h.SetConfigReloader(fakeConfigReloader{mutate: func(_ context.Context, reason string, mutate ConfigMutation) (ReloadResult, error) {
				cfg := clientMutationConfig()
				cfg.Routing.Rules = append(cfg.Routing.Rules, config.RoutingRule{Name: "office-route-2", FromTags: []string{"office"}, ToTags: []string{"mock-tag"}, Strategy: "failover"})
				if tc.shared {
					cfg.Inbounds[0].Clients = append(cfg.Inbounds[0].Clients, config.ClientBindingSpec{Ref: "shared-key", Tag: "office"})
				}
				_, err := mutate(cfg)
				if err != nil {
					return ReloadResult{}, err
				}
				return ReloadResult{OK: true, Saved: true, Applied: true, Reason: reason, HistoryID: "history-client"}, nil
			}})
			mux := http.NewServeMux()
			h.Register(mux)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, authorizedRequest(http.MethodPost, tc.path, "admin-ui-token", []byte(tc.body)))
			if tc.shared {
				if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"applied":true`) {
					t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
				}
				return
			}
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
			}
			for _, want := range []string{"office-key", "office", "office-route", "office-route-2", `"error_code":"binding_tag_last_source"`, `"route_names"`} {
				if !strings.Contains(w.Body.String(), want) {
					t.Fatalf("body = %s, want %q", w.Body.String(), want)
				}
			}
			operation := "delete"
			if strings.Contains(tc.name, "change") {
				operation = "update"
			}
			if !strings.Contains(w.Body.String(), `"operation":"`+operation+`"`) {
				t.Fatalf("body = %s, want operation %q", w.Body.String(), operation)
			}

		})
	}
}

func TestAdminConfigClientDeleteRejectsMissingClient(t *testing.T) {
	h := newAdminTestHandler(t)
	h.SetConfigReloader(fakeConfigReloader{mutate: func(_ context.Context, _ string, mutate ConfigMutation) (ReloadResult, error) {
		_, err := mutate(clientMutationConfig())
		return ReloadResult{}, err
	}})
	mux := http.NewServeMux()
	h.Register(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/admin/config/client/delete", "admin-ui-token", []byte(`{"name":"missing-key"}`)))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `client \"missing-key\" not found`) {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
}

func clientMutationConfig() config.Config {
	return config.Config{
		Clients: []config.ClientSpec{{Name: "office-key", Token: "old-client-token"}},
		Inbounds: []config.InboundSpec{{
			Name:     "openai-entry",
			Protocol: "openai_chat",
			Path:     "/v1/chat/completions",
			Clients:  []config.ClientBindingSpec{{Ref: "office-key", Tag: "office"}},
		}},
		Routing:   config.RoutingConfig{Rules: []config.RoutingRule{{Name: "office-route", FromTags: []string{"office"}, ToTags: []string{"mock-tag"}, Strategy: "failover"}}},
		Outbounds: []config.OutboundSpec{{Name: "mock", Protocol: "mock", Tag: "mock-tag"}},
	}
}

func TestAdminConfigClientsReturnsTopLevelClientsWithAllBindings(t *testing.T) {
	h := newAdminTestHandler(t)
	h.configPath = filepath.Join(t.TempDir(), "config.yaml")
	content := strings.Replace(validGatewayConfigYAML(), "outbounds:", `  - name: anthropic-entry
    protocol: anthropic_messages
    path: /v1/messages
    clients:
      - ref: office-key
        tag: shared
outbounds:`, 1)
	if err := os.WriteFile(h.configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodGet, "/admin/config/clients", "admin-ui-token", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	var response struct {
		Items []clientResourceResponse `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 1 || response.Items[0].Name != "office-key" || response.Items[0].Token != config.RedactedValue {
		t.Fatalf("items = %#v", response.Items)
	}
	bindings := response.Items[0].Bindings
	if len(bindings) != 2 || bindings[0].Inbound != "openai-entry" || bindings[0].InboundProtocol != "openai_chat" || bindings[0].InboundPath != "/v1/chat/completions" || bindings[0].Ref != "office-key" || bindings[0].Tag != "office" || bindings[1].Inbound != "anthropic-entry" || bindings[1].Tag != "shared" {
		t.Fatalf("bindings = %#v", bindings)
	}
	if strings.Contains(w.Body.String(), "client-token") {
		t.Fatalf("response leaked client token: %s", w.Body.String())
	}
}

func TestAdminConfigClientDeleteRejectsBoundClient(t *testing.T) {
	h := newAdminTestHandler(t)
	h.SetConfigReloader(fakeConfigReloader{mutate: func(_ context.Context, _ string, mutate ConfigMutation) (ReloadResult, error) {
		_, err := mutate(clientMutationConfig())
		return ReloadResult{}, err
	}})
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/admin/config/client/delete", "admin-ui-token", []byte(`{"name":"office-key"}`)))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "binding(s) still reference it") {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
}

func TestAdminConfigRoutesMatchAndRevisionRoundTrip(t *testing.T) {
	h := newAdminTestHandler(t)
	h.configPath = filepath.Join(t.TempDir(), "config.yaml")
	content := strings.Replace(validGatewayConfigYAML(), "      strategy: failover", "      strategy: failover\n      match:\n        models: [\"claude-*\"]", 1)
	if err := os.WriteFile(h.configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	h.SetConfigReloader(fileMutationReloader{path: h.configPath})
	mux := http.NewServeMux()
	h.Register(mux)

	getRoutes := func() struct {
		Items         []routeResourceResponse `json:"items"`
		OrderRevision string                  `json:"order_revision"`
	} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, authorizedRequest(http.MethodGet, "/admin/config/routes", "admin-ui-token", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("GET routes status = %d, body=%s", w.Code, w.Body.String())
		}
		var response struct {
			Items         []routeResourceResponse `json:"items"`
			OrderRevision string                  `json:"order_revision"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response
	}

	response := getRoutes()
	if len(response.Items) != 1 || response.Items[0].Match == nil || len(response.Items[0].Match.Models) != 1 || response.Items[0].Match.Models[0] != "claude-*" || response.OrderRevision == "" {
		t.Fatalf("routes response = %#v", response)
	}
	body := []byte(`{"name":"office-route","from_tags":["office"],"to_tags":["mock-tag"],"strategy":"failover","match":null}`)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/admin/config/route/upsert", "admin-ui-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("upsert status = %d, body=%s", w.Code, w.Body.String())
	}
	fallback := getRoutes()
	if fallback.Items[0].Match != nil || !strings.Contains(mustJSON(t, fallback.Items[0]), `"match":null`) {
		t.Fatalf("fallback route = %#v, want match null", fallback.Items[0])
	}
	if fallback.OrderRevision == response.OrderRevision {
		t.Fatal("order revision did not cover complete route content")
	}
}

func TestAdminConfigRoutesReorderCAS(t *testing.T) {
	rules := []config.RoutingRule{{Name: "first"}, {Name: "second"}, {Name: "third"}}
	revision := config.RouteOrderRevision(rules)
	var mutations int
	h := newAdminTestHandler(t)
	h.SetConfigReloader(fakeConfigReloader{mutate: func(_ context.Context, reason string, mutate ConfigMutation) (ReloadResult, error) {
		mutations++
		next, err := mutate(config.Config{Routing: config.RoutingConfig{Rules: rules}})
		if err != nil {
			return ReloadResult{}, err
		}
		if reason != "routes_reorder" || next.Routing.Rules[0].Name != "second" || next.Routing.Rules[2].Name != "first" {
			t.Fatalf("reason=%q rules=%#v", reason, next.Routing.Rules)
		}
		return ReloadResult{OK: true, Saved: true, Applied: true}, nil
	}})
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	body := []byte(fmt.Sprintf(`{"from_index":0,"to_index":2,"expected_revision":%q}`, revision))
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/admin/config/routes/reorder", "admin-ui-token", body))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"applied":true`) {
		t.Fatalf("success status = %d, body=%s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/admin/config/routes/reorder", "admin-ui-token", []byte(`{"from_index":0,"to_index":2,"expected_revision":"stale"}`)))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"error_code":"route_order_conflict"`) || !strings.Contains(w.Body.String(), `"current_revision"`) {
		t.Fatalf("conflict status = %d, body=%s", w.Code, w.Body.String())
	}

	for _, body := range []string{`{"from_index":1,"to_index":1,"expected_revision":"x"}`, `{"from_index":-1,"to_index":0,"expected_revision":"x"}`, fmt.Sprintf(`{"from_index":0,"to_index":3,"expected_revision":%q}`, revision)} {
		w = httptest.NewRecorder()
		mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/admin/config/routes/reorder", "admin-ui-token", []byte(body)))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("invalid reorder status = %d, body=%s", w.Code, w.Body.String())
		}
	}
	if mutations != 3 {
		t.Fatalf("mutation closure calls = %d, want success, conflict, and bounds validation", mutations)
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestAdminConfigRouteMutationsUseAtomicReloader(t *testing.T) {
	tests := []struct {
		path       string
		body       string
		wantReason string
	}{
		{path: "/admin/config/route/upsert", body: `{"name":"new-route","from_tags":["office"],"to_tags":["mock-tag"],"strategy":"failover"}`, wantReason: "route_upsert_new-route"},
		{path: "/admin/config/route/delete", body: `{"name":"office-route"}`, wantReason: "route_delete_office-route"},
	}
	for _, tc := range tests {
		t.Run(tc.wantReason, func(t *testing.T) {
			h := newAdminTestHandler(t)
			var gotReason string
			h.SetConfigReloader(fakeConfigReloader{mutate: func(_ context.Context, reason string, mutate ConfigMutation) (ReloadResult, error) {
				gotReason = reason
				if _, err := mutate(clientMutationConfig()); err != nil {
					return ReloadResult{}, err
				}
				return ReloadResult{OK: true, Saved: true, Applied: true, Reason: reason}, nil
			}})
			mux := http.NewServeMux()
			h.Register(mux)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, authorizedRequest(http.MethodPost, tc.path, "admin-ui-token", []byte(tc.body)))
			if w.Code != http.StatusOK || gotReason != tc.wantReason || !strings.Contains(w.Body.String(), `"applied":true`) {
				t.Fatalf("status = %d, reason = %q, body=%s", w.Code, gotReason, w.Body.String())
			}
		})
	}
}

func TestAdminConfigProviderQuotaRoundTripUsesSnakeCase(t *testing.T) {
	h := newAdminTestHandler(t)
	h.configPath = filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(h.configPath, []byte(validGatewayConfigYAML()), 0o600); err != nil {
		t.Fatal(err)
	}
	h.SetConfigReloader(fileMutationReloader{path: h.configPath})
	mux := http.NewServeMux()
	h.Register(mux)

	quotaJSON := `{"enabled":true,"windows":[{"name":"rolling","reset":"rolling","duration":"5h","max_requests":100,"max_tokens":20000},{"name":"fixed-5h","reset":"fixed","duration":"5h","fixed":{"period":"interval","anchor":"2026-01-01T00:00:00Z"},"max_requests":200,"max_tokens":40000},{"name":"daily","reset":"fixed","fixed":{"period":"daily","time":"04:00","timezone":"America/New_York"},"max_requests":300,"max_tokens":60000},{"name":"weekly","reset":"fixed","fixed":{"period":"weekly","time":"09:30","timezone":"Asia/Shanghai","weekday":"monday"},"max_requests":400,"max_tokens":80000}],"cooldown":"10m","probe_interval":"1m","reset_all":{"enabled":true,"schedule":{"period":"weekly","time":"00:00","timezone":"UTC","weekday":"sunday"}}}`
	body := []byte(`{"name":"mock","protocol":"mock","auth_token":"<redacted>","tag":"mock-tag","models":[{"name":"canonical-model","aliases":["public-alias"]}],"quota":` + quotaJSON + `}`)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/admin/config/provider/upsert", "admin-ui-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("upsert status = %d, body = %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodGet, "/admin/config/providers", "admin-ui-token", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", w.Code, w.Body.String())
	}
	var response struct {
		Items []providerResourceResponse `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(response.Items))
	}
	got := response.Items[0].Quota
	gotModels := response.Items[0].Models
	if len(gotModels) != 1 || gotModels[0].Name != "canonical-model" || len(gotModels[0].Aliases) != 1 || gotModels[0].Aliases[0] != "public-alias" {
		t.Fatalf("models round trip = %#v", gotModels)
	}
	if len(got.Windows) != 4 || got.Windows[0].MaxTokens != 20000 || got.Windows[1].Fixed.Period != "interval" || got.Windows[2].Fixed.Period != "daily" || got.Windows[3].Fixed.Weekday != "monday" || !got.ResetAll.Enabled || got.ResetAll.Schedule.Period != "weekly" {
		t.Fatalf("quota round trip = %#v", got)
	}
	encoded := w.Body.String()
	for _, want := range []string{`"max_tokens"`, `"max_requests"`, `"probe_interval"`, `"reset_all"`, `"timezone"`} {
		if !strings.Contains(encoded, want) {
			t.Fatalf("body = %s, want snake_case key %s", encoded, want)
		}
	}
	for _, forbidden := range []string{`"maxTokens"`, `"maxRequests"`, `"probeInterval"`, `"resetAll"`} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("body = %s, contains non-contract key %s", encoded, forbidden)
		}
	}

	providerDraft, err := json.Marshal(response.Items[0])
	if err != nil {
		t.Fatal(err)
	}
	checkBody := []byte(`{"name":"mock","model":"","provider":` + string(providerDraft) + `}`)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/admin/config/provider/check", "admin-ui-token", checkBody))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("check status = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestAdminProviderMetricsReturnsAllTimeUsageAndQuotaMetadata(t *testing.T) {
	h := newAdminTestHandler(t)
	h.configPath = filepath.Join(t.TempDir(), "config.yaml")
	content := strings.Replace(validGatewayConfigYAML(), "    tag: mock-tag", `    tag: mock-tag
    quota:
      enabled: true
      cooldown: 10m
      probe_interval: 1m
      windows:
        - name: rolling
          reset: rolling
          duration: 5h
          max_requests: 10
          max_tokens: 1000
        - name: daily
          reset: fixed
          fixed: {period: daily, time: "00:00", timezone: UTC}
          max_requests: 20
          max_tokens: 2000`, 1)
	if err := os.WriteFile(h.configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	store := accounting.NewMemoryStore()
	old := time.Now().UTC().Add(-24 * time.Hour)
	store.Record(runtime.UsageRecord{ProviderName: "mock", Status: runtime.UsageStatusSuccess, Breakdown: runtime.UsageBreakdown{RequestCount: 7, TotalTokens: 70}, StartedAt: old.Format(time.RFC3339Nano), FinishedAt: old.Format(time.RFC3339Nano)})
	tracker := quota.NewTrackerFromOutbounds([]config.OutboundSpec{{Name: "mock", Quota: config.OutboundQuotaConfig{Enabled: true, Cooldown: "10m", ProbeInterval: "1m", Windows: []config.OutboundQuotaWindowConfig{{Name: "rolling", Reset: "rolling", Duration: "5h", MaxRequests: 10, MaxTokens: 1000}, {Name: "daily", Reset: "fixed", Fixed: config.QuotaFixedScheduleConfig{Period: "daily", Time: "00:00", Timezone: "UTC"}, MaxRequests: 20, MaxTokens: 2000}}}}})
	tracker.RecordSuccess("mock", 123)
	state := h.runtimeState()
	state.Dispatcher = execution.NewDispatcherWithStoreAndQuota(store, tracker)
	h.ApplyRuntime(state)
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodGet, "/admin/config/providers/metrics?hours=3", "admin-ui-token", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var response struct {
		Hours int                       `json:"hours"`
		Items []providerMetricsResponse `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Hours != 3 || len(response.Items) != 1 {
		t.Fatalf("response = %#v", response)
	}
	item := response.Items[0]
	if item.Usage.RequestCount != 7 {
		t.Fatalf("all-time request_count = %d, want 7", item.Usage.RequestCount)
	}
	for _, bucket := range item.Timeline {
		if bucket.RequestCount != 0 {
			t.Fatalf("timeline includes usage outside hours: %#v", bucket)
		}
	}
	if item.Quota == nil || len(item.Quota.Windows) != 2 {
		t.Fatalf("quota = %#v", item.Quota)
	}
	window := item.Quota.Windows[0]
	if window.MaxRequests != 10 || window.UsedRequests != 1 || window.MaxTokens != 1000 || window.UsedTokens != 123 || window.Reset != "rolling" || window.ResetAt == "" {
		t.Fatalf("rolling quota window = %#v", window)
	}
	fixed := item.Quota.Windows[1]
	if fixed.Reset != "fixed" || fixed.FixedPeriod != "daily" || fixed.ResetAt == "" {
		t.Fatalf("fixed quota window = %#v", fixed)
	}
}

func TestAdminConfigClientUpsertAndRouteDeleteWriteConfig(t *testing.T) {
	h := newAdminTestHandler(t)
	h.configPath = filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(h.configPath, []byte(validGatewayConfigYAML()), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	h.SetConfigReloader(fileMutationReloader{path: h.configPath})
	mux := http.NewServeMux()
	h.Register(mux)

	clientBody := []byte(`{"name":"mobile-key","token":"mobile-token"}`)
	clientResp := httptest.NewRecorder()
	mux.ServeHTTP(clientResp, authorizedRequest(http.MethodPost, "/admin/config/client/upsert", "admin-ui-token", clientBody))
	if clientResp.Code != http.StatusOK {
		t.Fatalf("client upsert status = %d, want 200, body=%s", clientResp.Code, clientResp.Body.String())
	}

	routeBody := []byte(`{"name":"office-route"}`)
	routeResp := httptest.NewRecorder()
	mux.ServeHTTP(routeResp, authorizedRequest(http.MethodPost, "/admin/config/route/delete", "admin-ui-token", routeBody))
	if routeResp.Code != http.StatusBadRequest {
		t.Fatalf("route delete status = %d, want 400 because config must keep at least one route", routeResp.Code)
	}
	if !strings.Contains(routeResp.Body.String(), "at least one routing rule is required") {
		t.Fatalf("body = %s, want validation error", routeResp.Body.String())
	}

	cfg, err := config.Load(h.configPath)
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	if len(cfg.Clients) != 2 || cfg.Clients[1].Name != "mobile-key" {
		t.Fatalf("clients = %#v, want inserted client", cfg.Clients)
	}
	if len(cfg.Routing.Rules) != 1 {
		t.Fatalf("rules = %#v, want failed delete to preserve config", cfg.Routing.Rules)
	}
}
