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
	for _, want := range []string{"Admin UI token", "/admin/app.js", "usage-range", "usage-start-date", "usage-end-date", "log-bytes", "logs-meta", "overview-summary", "sessions-table", "sessions-view-cards", "sessions-view-table", "session-status-filter", "live-requests-table", "refresh-live-requests", "config-diff", "Apply current file", "config-history", "Debug", "dry-run-model"} {
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
	for _, want := range []string{"/admin/sessions", "startSessionsRefresh", "renderSessionCards", "/admin/usage", "/admin/logs", "/admin/overview", "/admin/latency/active", "refreshLiveRequests", "redacted_content", "window.confirm", "renderConfigDiff", "max_bytes", "/admin/config/apply", "/admin/config/history", "/admin/config/rollback", "/admin/debug/traces", "/admin/debug/route-dry-run", "/admin/debug/providers", "/admin/config/history/diff"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %s, want %s", body, want)
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
		Path            string `json:"path"`
		Content         string `json:"content"`
		RedactedContent string `json:"redacted_content"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if response.Path != h.configPath || !strings.Contains(response.Content, "openai-entry") {
		t.Fatalf("response = %#v, want config path and content", response)
	}
	if !strings.Contains(response.Content, "admin-secret") || !strings.Contains(response.Content, "accounting-secret") {
		t.Fatalf("content = %s, want raw secrets retained for compatibility", response.Content)
	}
	if strings.Contains(response.RedactedContent, "admin-secret") || strings.Contains(response.RedactedContent, "accounting-secret") || strings.Contains(response.RedactedContent, "client-token") {
		t.Fatalf("redacted_content = %s, want secrets redacted", response.RedactedContent)
	}
	if !strings.Contains(response.RedactedContent, "<redacted>") {
		t.Fatalf("redacted_content = %s, want redaction marker", response.RedactedContent)
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

type fakeConfigReloader struct{}

func (fakeConfigReloader) ApplyConfig(context.Context) (ReloadResult, error) {
	return ReloadResult{OK: true, Applied: true, HistoryID: "history-1", QuotaStateReset: true}, nil
}

func (fakeConfigReloader) History() []HistoryItem {
	return []HistoryItem{{ID: "history-1", CreatedAt: "2026-07-02T00:00:00Z", Reason: "apply", Path: "/tmp/config.yaml", Checksum: "abc"}}
}

func (fakeConfigReloader) HistoryDiff(id string) (HistoryDiff, error) {
	return HistoryDiff{ID: id, CurrentContent: "admin:\n  token: \"<redacted>\"\n", HistoryContent: "admin:\n  token: \"<redacted>\"\n"}, nil
}

func (fakeConfigReloader) Rollback(context.Context, string) (ReloadResult, error) {
	return ReloadResult{OK: true, Applied: true, HistoryID: "history-2", QuotaStateReset: true}, nil
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
	mux := http.NewServeMux()
	h.Register(mux)

	body := []byte(`{"name":"openai","protocol":"openai_chat","endpoint":"https://api.example.test/v1","auth_token":"<redacted>","tag":"openai-tag"}`)
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
}

func TestAdminConfigClientUpsertAndRouteDeleteWriteConfig(t *testing.T) {
	h := newAdminTestHandler(t)
	h.configPath = filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(h.configPath, []byte(validGatewayConfigYAML()), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	mux := http.NewServeMux()
	h.Register(mux)

	clientBody := []byte(`{"inbound":"openai-entry","name":"mobile-key","token":"mobile-token","tag":"mobile"}`)
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
	if len(cfg.Inbounds[0].Clients) != 2 || cfg.Inbounds[0].Clients[1].Name != "mobile-key" {
		t.Fatalf("clients = %#v, want inserted client", cfg.Inbounds[0].Clients)
	}
	if len(cfg.Routing.Rules) != 1 {
		t.Fatalf("rules = %#v, want failed delete to preserve config", cfg.Routing.Rules)
	}
}
