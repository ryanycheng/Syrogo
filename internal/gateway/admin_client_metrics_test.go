package gateway

import (
	"context"
	"encoding/json"
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
	"github.com/ryanycheng/Syrogo/internal/quota"
	"github.com/ryanycheng/Syrogo/internal/runtime"
)

type coverageStore struct {
	*accounting.MemoryStore
	coverage accounting.Coverage
}

func (s *coverageStore) Coverage() accounting.Coverage { return s.coverage }
func (s *coverageStore) Close(context.Context) error   { return nil }

func TestAdminClientMetricsReturnsConfiguredClientsUsageFrequencyAndQuota(t *testing.T) {
	h := newAdminTestHandler(t)
	h.configPath = filepath.Join(t.TempDir(), "config.yaml")
	content := strings.Replace(validGatewayConfigYAML(), "  - name: office-key\n    token: client-token", `  - name: office-key
    token: client-token
    quota:
      enabled: true
      windows:
        - name: daily
          duration: 24h
          max_requests: 10
  - name: idle-key
    token: idle-secret`, 1)
	if err := os.WriteFile(h.configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	store := accounting.NewMemoryStore()
	store.Record(clientUsageRecord("office-key", now.AddDate(0, 0, -2), 3, 30))
	store.Record(clientUsageRecord("office-key", now, 2, 20))
	tracker := quota.NewTestClientTracker([]quota.ClientConfig{{
		Name: "office-key", Inbound: "openai-entry", Windows: []quota.WindowConfig{{Name: "daily", Duration: 24 * time.Hour, MaxRequests: 10}},
	}}, func() time.Time { return now })
	tracker.RecordClientRequest("office-key")
	state := h.runtimeState()
	runtimeConfig, err := config.Load(h.configPath)
	if err != nil {
		t.Fatal(err)
	}
	state.Clients = runtimeConfig.Clients
	state.Inbounds = runtimeConfig.Inbounds
	state.Dispatcher = execution.NewDispatcherWithStore(store)
	state.ClientQuotaTracker = tracker
	h.ApplyRuntime(state)
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodGet, "/admin/config/clients/metrics?days=7", "admin-ui-token", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var response struct {
		Days  int                     `json:"days"`
		Items []clientMetricsResponse `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Days != 7 || len(response.Items) != 2 {
		t.Fatalf("response = %#v, want 7 days and two configured clients", response)
	}
	metric := response.Items[0]
	if metric.Client.Name != "office-key" || metric.Client.Token != config.RedactedValue || metric.AllTime.RequestCount != 5 {
		t.Fatalf("office metric = %#v", metric)
	}
	if metric.Frequency.Requests != 5 || metric.Frequency.ActiveDays != 2 || metric.Frequency.CalendarDays != 7 || metric.Frequency.RequestsPerDay != 5.0/7.0 || metric.Frequency.RequestsPerActiveDay != 2.5 {
		t.Fatalf("frequency = %#v", metric.Frequency)
	}
	if metric.Quota == nil || metric.Quota.Client != "office-key" || metric.Quota.Windows[0].UsedRequests != 1 {
		t.Fatalf("quota = %#v", metric.Quota)
	}
	if response.Items[1].Client.Name != "idle-key" || response.Items[1].AllTime.RequestCount != 0 || response.Items[1].Frequency.Requests != 0 {
		t.Fatalf("idle metric = %#v", response.Items[1])
	}
	if strings.Contains(w.Body.String(), "client-token") || strings.Contains(w.Body.String(), "idle-secret") {
		t.Fatalf("response leaked client token: %s", w.Body.String())
	}
}

func TestAdminClientMetricsUsesRuntimeClientsAndBindingsWhenDiskDiverges(t *testing.T) {
	h := newAdminTestHandler(t)
	h.configPath = filepath.Join(t.TempDir(), "config.yaml")
	diskConfig := strings.ReplaceAll(validGatewayConfigYAML(), "office-key", "saved-only-key")
	if err := os.WriteFile(h.configPath, []byte(diskConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	state := h.runtimeState()
	state.Clients = []config.ClientSpec{{Name: "office-key", Token: "runtime-token"}}
	state.Inbounds = []config.InboundSpec{{
		Name: "openai-entry", Protocol: "openai_chat", Path: "/v1/chat/completions",
		Clients: []config.ClientBindingSpec{{Ref: "office-key", Tag: "office"}},
	}}
	state.Dispatcher = execution.NewDispatcherWithStore(accounting.NewMemoryStore())
	h.ApplyRuntime(state)
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodGet, "/admin/config/clients/metrics?days=7", "admin-ui-token", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var response struct {
		Items []clientMetricsResponse `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 1 || response.Items[0].Client.Name != "office-key" {
		t.Fatalf("items = %#v, want runtime client office-key", response.Items)
	}
	bindings := response.Items[0].Client.Bindings
	if len(bindings) != 1 || bindings[0].Inbound != "openai-entry" {
		t.Fatalf("bindings = %#v, want runtime inbound binding", bindings)
	}
	if strings.Contains(w.Body.String(), "saved-only-key") {
		t.Fatalf("response mixed saved-only disk revision: %s", w.Body.String())
	}
}

func TestAdminClientUsageReturnsDenseDailyStatusesAndSummary(t *testing.T) {
	h := newAdminTestHandler(t)
	h.configPath = filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(h.configPath, []byte(validGatewayConfigYAML()), 0o600); err != nil {
		t.Fatal(err)
	}

	today := utcDate(time.Now())
	start := today.AddDate(0, 0, -2)
	end := today.AddDate(0, 0, 1)
	memory := accounting.NewMemoryStore()
	memory.Record(clientUsageRecord("office-key", start, 4, 40))
	memory.Record(clientUsageRecord("office-key", today, 1, 10))
	store := &coverageStore{MemoryStore: memory, coverage: accounting.Coverage{
		Known: true, Backend: "test", AggregatesPersisted: true, TrackingStartedAt: start.Add(-time.Hour).Format(time.RFC3339Nano),
	}}
	state := h.runtimeState()
	state.Dispatcher = execution.NewDispatcherWithStore(store)
	h.ApplyRuntime(state)
	mux := http.NewServeMux()
	h.Register(mux)

	path := "/admin/config/client/usage?name=office-key&start_date=" + formatUTCDate(start) + "&end_date=" + formatUTCDate(end)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodGet, path, "admin-ui-token", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var response clientUsageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Client.Token != config.RedactedValue || response.AllTime.RequestCount != 5 || response.RangeSummary.RequestCount != 5 {
		t.Fatalf("response summary = %#v", response)
	}
	if len(response.Daily) != 3 {
		t.Fatalf("daily length = %d, want 3", len(response.Daily))
	}
	if response.Daily[0].Value != formatUTCDate(start) || response.Daily[0].Status != "complete" || response.Daily[0].RequestCount != 4 {
		t.Fatalf("first day = %#v", response.Daily[0])
	}
	if response.Daily[1].Status != "complete" || response.Daily[1].RequestCount != 0 {
		t.Fatalf("dense empty day = %#v", response.Daily[1])
	}
	if response.Daily[2].Status != "partial" || response.Daily[2].RequestCount != 1 {
		t.Fatalf("current day = %#v", response.Daily[2])
	}
	if !response.Coverage.Known || response.Coverage.Backend != "test" {
		t.Fatalf("coverage = %#v", response.Coverage)
	}
	if strings.Contains(w.Body.String(), "client-token") {
		t.Fatalf("response leaked client token: %s", w.Body.String())
	}
}

func TestAdminClientUsageRejectsUnknownClientAndInvalidRanges(t *testing.T) {
	h := newAdminTestHandler(t)
	h.configPath = filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(h.configPath, []byte(validGatewayConfigYAML()), 0o600); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)

	unknown := httptest.NewRecorder()
	mux.ServeHTTP(unknown, authorizedRequest(http.MethodGet, "/admin/config/client/usage?name=missing", "admin-ui-token", nil))
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown client status = %d, body = %s", unknown.Code, unknown.Body.String())
	}

	tomorrow := utcDate(time.Now()).AddDate(0, 0, 1)
	invalidPaths := []string{
		"/admin/config/clients/metrics?days=367",
		"/admin/config/client/usage?name=office-key&start_date=2026-01-01",
		"/admin/config/client/usage?name=office-key&start_date=" + formatUTCDate(tomorrow) + "&end_date=" + formatUTCDate(tomorrow.AddDate(0, 0, 1)),
	}
	for _, path := range invalidPaths {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, authorizedRequest(http.MethodGet, path, "admin-ui-token", nil))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("path %s status = %d, body = %s", path, w.Code, w.Body.String())
		}
	}
}

func TestDenseClientDailyMarksPreCoverageAndUnknownCoverageDaysUnknown(t *testing.T) {
	today := time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)
	start := today.AddDate(0, 0, -3)
	known := denseClientDaily(nil, start, today, today.Add(12*time.Hour), accounting.Coverage{
		Known: true, TrackingStartedAt: today.AddDate(0, 0, -2).Add(2 * time.Hour).Format(time.RFC3339Nano),
	})
	if known[0].Status != "unknown" || known[1].Status != "complete" || known[2].Status != "complete" {
		t.Fatalf("known coverage statuses = %#v", known)
	}
	unknown := denseClientDaily(nil, start, today, today.Add(12*time.Hour), accounting.Coverage{Known: false})
	for _, item := range unknown {
		if item.Status != "unknown" {
			t.Fatalf("unknown coverage daily = %#v", unknown)
		}
	}
}

func clientUsageRecord(client string, at time.Time, requests, tokens int) runtime.UsageRecord {
	return runtime.UsageRecord{
		ClientName: client, Status: runtime.UsageStatusSuccess,
		Breakdown: runtime.UsageBreakdown{RequestCount: requests, TotalTokens: tokens},
		StartedAt: at.UTC().Format(time.RFC3339Nano), FinishedAt: at.UTC().Format(time.RFC3339Nano),
	}
}
