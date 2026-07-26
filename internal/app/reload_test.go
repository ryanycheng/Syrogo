package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ryanycheng/Syrogo/internal/config"
	"github.com/ryanycheng/Syrogo/internal/quota"
	"gopkg.in/yaml.v3"
)

func TestReloadManagerConditionalConfigUpdate(t *testing.T) {
	cfg := baseConfig()
	path := writeReloadTestConfig(t, cfg)
	app, err := NewWithOptions(cfg, Options{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	next := cloneConfig(t, cfg)
	next.Clients[0].Token = "replacement-token"
	nextData, err := yaml.Marshal(next)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := app.reloadManager.UpdateConfig(context.Background(), "sha256:stale", nextData); err == nil {
		t.Fatal("stale update error = nil")
	}
	unchanged, _ := os.ReadFile(path)
	if string(unchanged) != string(current) {
		t.Fatal("stale update changed config")
	}
	result, err := app.reloadManager.UpdateConfig(context.Background(), configRevision(current), nextData)
	if err != nil || !result.Saved || result.Applied || result.Revision != configRevision(nextData) || result.Checksum != checksumHex(nextData) {
		t.Fatalf("conditional update = %#v, err=%v", result, err)
	}
	stored, _ := os.ReadFile(path)
	if string(stored) != string(nextData) {
		t.Fatal("conditional update did not write replacement")
	}
	forced, err := app.reloadManager.UpdateConfig(context.Background(), "*", current)
	if err != nil || !forced.Saved {
		t.Fatalf("force update = %#v, err=%v", forced, err)
	}
}

func TestReloadManagerAppliesClientTokenChange(t *testing.T) {
	cfg := baseConfig()
	cfg.Admin = config.AdminConfig{Enabled: true, Token: "admin-ui-token"}
	path := writeReloadTestConfig(t, cfg)
	app, err := NewWithOptions(cfg, Options{ConfigPath: path})
	if err != nil {
		t.Fatalf("NewWithOptions() error = %v", err)
	}

	next := cloneConfig(t, cfg)
	next.Clients[0].Token = "next-client-token"
	writeConfigFile(t, path, next)
	result, err := app.reloadManager.ApplyConfig(context.Background())
	if err != nil {
		t.Fatalf("ApplyConfig() error = %v", err)
	}
	if !result.OK || !result.Applied || result.RestartRequired || result.HistoryID == "" {
		t.Fatalf("ApplyConfig() = %#v, want applied result with history id", result)
	}

	body := []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`)
	w := httptest.NewRecorder()
	app.Server.Listeners()[0].Handler.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/chat/completions", "client-token", body))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("old token status = %d, want 401", w.Code)
	}
	w = httptest.NewRecorder()
	app.Server.Listeners()[0].Handler.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/chat/completions", "next-client-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("new token status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
}

func TestReloadManagerRequiresRestartForListenerChange(t *testing.T) {
	cfg := baseConfig()
	path := writeReloadTestConfig(t, cfg)
	app, err := NewWithOptions(cfg, Options{ConfigPath: path})
	if err != nil {
		t.Fatalf("NewWithOptions() error = %v", err)
	}

	next := cloneConfig(t, cfg)
	next.Listeners[0].Listen = ":9090"
	writeConfigFile(t, path, next)
	result, err := app.reloadManager.ApplyConfig(context.Background())
	if err != nil {
		t.Fatalf("ApplyConfig() error = %v", err)
	}
	if !result.OK || result.Applied || !result.RestartRequired || !strings.Contains(result.Reason, "listen") {
		t.Fatalf("ApplyConfig() = %#v, want restart required for listener change", result)
	}
}

func TestRestartRequiredReasonForLoggingConfiguration(t *testing.T) {
	falseValue := false
	trueValue := true
	base := baseConfig()
	base.Admin.Logs = config.AdminLogsConfig{
		Enabled:  true,
		Path:     "tmp/dev.log",
		MaxBytes: 65536,
		Rotation: config.AdminLogsRotationConfig{
			MaxSizeMB:      100,
			MaxFiles:       20,
			MaxAgeDays:     14,
			MaxTotalSizeMB: 1024,
		},
	}

	tests := []struct {
		name   string
		mutate func(*config.Config)
		want   string
	}{
		{"path", func(c *config.Config) { c.Admin.Logs.Path = "logs/other.log" }, "logging configuration changed"},
		{"max size", func(c *config.Config) { c.Admin.Logs.Rotation.MaxSizeMB++ }, "logging configuration changed"},
		{"max files", func(c *config.Config) { c.Admin.Logs.Rotation.MaxFiles++ }, "logging configuration changed"},
		{"max age", func(c *config.Config) { c.Admin.Logs.Rotation.MaxAgeDays++ }, "logging configuration changed"},
		{"max total size", func(c *config.Config) { c.Admin.Logs.Rotation.MaxTotalSizeMB++ }, "logging configuration changed"},
		{"effective compression", func(c *config.Config) { c.Admin.Logs.Rotation.Compress = &falseValue }, "logging configuration changed"},
		{"nil and true compression equivalent", func(c *config.Config) { c.Admin.Logs.Rotation.Compress = &trueValue }, ""},
		{"enabled", func(c *config.Config) { c.Admin.Logs.Enabled = false }, ""},
		{"max bytes", func(c *config.Config) { c.Admin.Logs.MaxBytes++ }, ""},
		{"admin token", func(c *config.Config) { c.Admin.Token = "changed" }, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			next := base
			tc.mutate(&next)
			if got := restartRequiredReason(base, next); got != tc.want {
				t.Fatalf("restartRequiredReason() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReloadManagerRollbackRestoresPreviousConfig(t *testing.T) {
	cfg := baseConfig()
	path := writeReloadTestConfig(t, cfg)
	app, err := NewWithOptions(cfg, Options{ConfigPath: path})
	if err != nil {
		t.Fatalf("NewWithOptions() error = %v", err)
	}

	next := cloneConfig(t, cfg)
	next.Clients[0].Token = "next-client-token"
	writeConfigFile(t, path, next)
	result, err := app.reloadManager.ApplyConfig(context.Background())
	if err != nil {
		t.Fatalf("ApplyConfig() error = %v", err)
	}
	if result.HistoryID == "" {
		t.Fatalf("ApplyConfig() history id is empty")
	}

	rollback, err := app.reloadManager.Rollback(context.Background(), result.HistoryID)
	if err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if !rollback.OK || !rollback.Applied {
		t.Fatalf("Rollback() = %#v, want applied", rollback)
	}

	body := []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`)
	w := httptest.NewRecorder()
	app.Server.Listeners()[0].Handler.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/chat/completions", "client-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("rolled back token status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
}

func TestReloadManagerPreservesAndSelectivelyResetsQuotaState(t *testing.T) {
	cfg := baseConfig()
	cfg.Outbounds[0].Quota = config.OutboundQuotaConfig{
		Enabled:       true,
		Cooldown:      config.DurationValue("10m"),
		ProbeInterval: config.DurationValue("1m"),
		Windows: []config.OutboundQuotaWindowConfig{
			{Name: "hourly", Duration: config.DurationValue("1h"), MaxRequests: 10},
			{Name: "daily", Duration: config.DurationValue("24h"), MaxRequests: 100},
		},
	}
	path := writeReloadTestConfig(t, cfg)
	app, err := NewWithOptions(cfg, Options{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close(context.Background()) }()

	tracker := app.outboundQuotaTracker
	tracker.RecordSuccess("mock")
	tracker.RecordQuotaExceeded("mock")
	oldDispatcher := app.dispatcher

	result := applyReloadConfig(t, app, path, cloneConfig(t, cfg))
	if result.QuotaStateReset {
		t.Fatal("identical quota reported reset")
	}
	if app.outboundQuotaTracker != tracker {
		t.Fatal("Apply replaced outbound tracker")
	}
	assertOutboundUsage(t, tracker, 1, 1)
	if got := tracker.Snapshot()[0]; got.State != quota.StateCooldown || got.CooldownUntil == "" {
		t.Fatalf("cooldown was not preserved: %#v", got)
	}

	next := cloneConfig(t, cfg)
	next.Outbounds[0].Quota.Windows[0].MaxRequests = 20
	result = applyReloadConfig(t, app, path, next)
	if result.QuotaStateReset {
		t.Fatal("limit-only change reported reset")
	}
	assertOutboundUsage(t, tracker, 1, 1)

	next.Outbounds[0].Quota.Windows[0].Duration = config.DurationValue("2h")
	result = applyReloadConfig(t, app, path, next)
	if !result.QuotaStateReset {
		t.Fatal("incompatible used window did not report reset")
	}
	assertOutboundUsage(t, tracker, 0, 1)

	tracker.RecordSuccess("mock")
	oldItems := oldDispatcher.QueryQuota()
	if len(oldItems) != 1 || oldItems[0].Windows[0].UsedRequests != 1 || oldItems[0].Windows[1].UsedRequests != 2 {
		t.Fatalf("old dispatcher no longer shares tracker: %#v", oldItems)
	}
}

func TestReloadManagerDoesNotLoadStaleSnapshot(t *testing.T) {
	cfg := baseConfig()
	dir := t.TempDir()
	cfg.Governance.Quota.Snapshot = config.GovernanceQuotaSnapshotConfig{Enabled: true, Dir: dir, FlushInterval: config.DurationValue("1h")}
	cfg.Outbounds[0].Quota = config.OutboundQuotaConfig{Enabled: true, Cooldown: config.DurationValue("10m"), ProbeInterval: config.DurationValue("1m"), Windows: []config.OutboundQuotaWindowConfig{{Name: "hourly", Duration: config.DurationValue("1h"), MaxRequests: 10}}}
	path := writeReloadTestConfig(t, cfg)
	app, err := NewWithOptions(cfg, Options{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close(context.Background()) }()

	if err := app.quotaSnapshotStore.Save(); err != nil {
		t.Fatal(err)
	}
	app.outboundQuotaTracker.RecordSuccess("mock")
	result := applyReloadConfig(t, app, path, cloneConfig(t, cfg))
	if result.QuotaStateReset {
		t.Fatal("identical Apply reported reset")
	}
	assertOutboundUsage(t, app.outboundQuotaTracker, 1)
}

func TestReloadManagerSnapshotLifecycleUsesLiveTracker(t *testing.T) {
	cfg := baseConfig()
	cfg.Outbounds[0].Quota = config.OutboundQuotaConfig{Enabled: true, Cooldown: config.DurationValue("10m"), ProbeInterval: config.DurationValue("1m"), Windows: []config.OutboundQuotaWindowConfig{{Name: "hourly", Duration: config.DurationValue("1h"), MaxRequests: 10}}}
	path := writeReloadTestConfig(t, cfg)
	app, err := NewWithOptions(cfg, Options{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close(context.Background()) }()

	tracker := app.outboundQuotaTracker
	tracker.RecordSuccess("mock")
	if app.quotaSnapshotStore != nil {
		t.Fatal("snapshot store started while disabled")
	}

	next := cloneConfig(t, cfg)
	next.Governance.Quota.Snapshot = config.GovernanceQuotaSnapshotConfig{Enabled: true, Dir: t.TempDir(), FlushInterval: config.DurationValue("1h")}
	if result := applyReloadConfig(t, app, path, next); result.QuotaStateReset {
		t.Fatal("enabling snapshots reported quota reset")
	}
	if app.quotaSnapshotStore == nil || app.outboundQuotaTracker != tracker {
		t.Fatal("snapshot enable did not retain and bind live tracker")
	}
	assertOutboundUsage(t, tracker, 1)

	next.Governance.Quota.Snapshot.Enabled = false
	if result := applyReloadConfig(t, app, path, next); result.QuotaStateReset {
		t.Fatal("disabling snapshots reported quota reset")
	}
	if app.quotaSnapshotStore != nil || app.outboundQuotaTracker != tracker {
		t.Fatal("snapshot disable changed tracker or retained store")
	}
	assertOutboundUsage(t, tracker, 1)
}

func TestReloadManagerQuotaResetFlagIgnoresUnusedChanges(t *testing.T) {
	cfg := baseConfig()
	path := writeReloadTestConfig(t, cfg)
	app, err := NewWithOptions(cfg, Options{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close(context.Background()) }()

	next := cloneConfig(t, cfg)
	next.Outbounds[0].Quota = config.OutboundQuotaConfig{Enabled: true, Cooldown: config.DurationValue("10m"), ProbeInterval: config.DurationValue("1m"), Windows: []config.OutboundQuotaWindowConfig{{Name: "hourly", Duration: config.DurationValue("1h"), MaxRequests: 10}}}
	if result := applyReloadConfig(t, app, path, next); result.QuotaStateReset {
		t.Fatal("enabling an unused quota reported reset")
	}
	app.outboundQuotaTracker.RecordSuccess("mock")
	next.Outbounds[0].Quota.Enabled = false
	if result := applyReloadConfig(t, app, path, next); !result.QuotaStateReset {
		t.Fatal("disabling a used quota did not report reset")
	}
}

func TestAdminClientMutationsApplyTokensAndDeleteImmediately(t *testing.T) {
	cfg := baseConfig()
	cfg.Admin = config.AdminConfig{Enabled: true, Token: "admin-ui-token"}
	path := writeReloadTestConfig(t, cfg)
	app, err := NewWithOptions(cfg, Options{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close(context.Background()) }()

	handler := app.Server.Listeners()[0].Handler
	mutate := func(path, body string) ReloadResult {
		t.Helper()
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, authorizedRequest(http.MethodPost, path, "admin-ui-token", []byte(body)))
		if w.Code != http.StatusOK {
			t.Fatalf("%s status = %d, body=%s", path, w.Code, w.Body.String())
		}
		var result ReloadResult
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if !result.OK || !result.Saved || !result.Applied || result.HistoryID == "" {
			t.Fatalf("%s result = %#v", path, result)
		}
		return result
	}
	request := func(token string) int {
		t.Helper()
		body := []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/chat/completions", token, body))
		return w.Code
	}

	mutate("/admin/config/client/upsert", `{"name":"mobile-key","token":"mobile-token","quota":{}}`)
	mutate("/admin/config/client-binding/upsert", `{"inbound":"openai-entry","ref":"mobile-key","tag":"office"}`)
	if status := request("mobile-token"); status != http.StatusOK {
		t.Fatalf("new token status = %d, want 200", status)
	}
	mutate("/admin/config/client/upsert", `{"name":"mobile-key","token":"<redacted>","quota":{}}`)
	if status := request("mobile-token"); status != http.StatusOK {
		t.Fatalf("preserved token status = %d, want 200", status)
	}
	mutate("/admin/config/client/upsert", `{"name":"mobile-key","token":"rotated-token","quota":{}}`)
	if status := request("mobile-token"); status != http.StatusUnauthorized {
		t.Fatalf("old rotated token status = %d, want 401", status)
	}
	if status := request("rotated-token"); status != http.StatusOK {
		t.Fatalf("rotated token status = %d, want 200", status)
	}
	mutate("/admin/config/client-binding/delete", `{"inbound":"openai-entry","ref":"mobile-key"}`)
	mutate("/admin/config/client/delete", `{"name":"mobile-key"}`)
	if status := request("rotated-token"); status != http.StatusUnauthorized {
		t.Fatalf("deleted token status = %d, want 401", status)
	}
	stored, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Clients) != 1 || stored.Clients[0].Name != "office-key" {
		t.Fatalf("stored clients = %#v", stored.Clients)
	}
}

func TestAdminClientMutationFailurePreservesDiskAndRuntime(t *testing.T) {
	cfg := baseConfig()
	cfg.Admin = config.AdminConfig{Enabled: true, Token: "admin-ui-token"}
	path := writeReloadTestConfig(t, cfg)
	app, err := NewWithOptions(cfg, Options{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close(context.Background()) }()
	originalData, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	app.Server.Listeners()[0].Handler.ServeHTTP(w, authorizedRequest(http.MethodPost, "/admin/config/client/upsert", "admin-ui-token", []byte(`{"name":"mobile-key","token":"client-token","quota":{}}`)))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "duplicates token") {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	gotData, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotData) != string(originalData) || len(app.cfg.Inbounds[0].Clients) != 1 {
		t.Fatal("failed client mutation changed disk or runtime")
	}
	body := []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`)
	request := httptest.NewRecorder()
	app.Server.Listeners()[0].Handler.ServeHTTP(request, authorizedRequest(http.MethodPost, "/v1/chat/completions", "client-token", body))
	if request.Code != http.StatusOK {
		t.Fatalf("original token status = %d, body=%s", request.Code, request.Body.String())
	}
}

func TestReloadManagerMutationSavesAppliesAndRecordsHistory(t *testing.T) {
	cfg := baseConfig()
	cfg.Outbounds = append(cfg.Outbounds, config.OutboundSpec{Name: "backup", Protocol: "mock", Tag: cfg.Outbounds[0].Tag})
	path := writeReloadTestConfig(t, cfg)
	app, err := NewWithOptions(cfg, Options{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close(context.Background()) }()

	result, err := app.reloadManager.MutateConfig(context.Background(), "provider_disable_mock", func(next config.Config) (config.Config, error) {
		return config.SetOutboundEnabled(next, "mock", false)
	})
	if err != nil {
		t.Fatalf("MutateConfig() error = %v", err)
	}
	if !result.OK || !result.Saved || !result.Applied || result.RestartRequired || result.HistoryID == "" || result.Reason != "provider_disable_mock" {
		t.Fatalf("MutateConfig() = %#v", result)
	}
	stored, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.OutboundEnabled(stored.Outbounds[0]) || config.OutboundEnabled(app.cfg.Outbounds[0]) {
		t.Fatal("provider disable was not saved and applied")
	}

	enabled, err := app.reloadManager.MutateConfig(context.Background(), "provider_enable_mock", func(next config.Config) (config.Config, error) {
		return config.SetOutboundEnabled(next, "mock", true)
	})
	if err != nil || !enabled.Saved || !enabled.Applied || !config.OutboundEnabled(app.cfg.Outbounds[0]) {
		t.Fatalf("provider enable = %#v, err=%v", enabled, err)
	}
	deleted, err := app.reloadManager.MutateConfig(context.Background(), "provider_delete_backup", func(next config.Config) (config.Config, error) {
		return config.DeleteOutbound(next, "backup"), nil
	})
	if err != nil || !deleted.Saved || !deleted.Applied || len(app.cfg.Outbounds) != 1 {
		t.Fatalf("provider delete = %#v, err=%v, outbounds=%#v", deleted, err, app.cfg.Outbounds)
	}
	stored, err = config.Load(path)
	if err != nil || len(stored.Outbounds) != 1 || stored.Outbounds[0].Name != "mock" {
		t.Fatalf("stored provider delete = %#v, err=%v", stored.Outbounds, err)
	}

	historyData, item, err := app.reloadManager.history.Read(result.HistoryID)
	if err != nil {
		t.Fatal(err)
	}
	historyCfg, err := config.ParseBytes(historyData)
	if err != nil {
		t.Fatal(err)
	}
	if !config.OutboundEnabled(historyCfg.Outbounds[0]) || item.Reason != "provider_disable_mock" {
		t.Fatalf("history = %#v, cfg = %#v", item, historyCfg.Outbounds[0])
	}
}

func TestReloadManagerDisablesSoleProviderAndRestoresItAtomically(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-reload","object":"chat.completion","created":1,"model":"gpt-4","choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()

	cfg := baseConfig()
	cfg.Outbounds[0] = config.OutboundSpec{Name: "openai", Protocol: "openai_chat", Endpoint: upstream.URL + "/v1", AuthToken: "upstream-key", Tag: "mock-tag"}
	path := writeReloadTestConfig(t, cfg)
	app, err := NewWithOptions(cfg, Options{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close(context.Background()) }()

	body := []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`)
	request := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		app.Server.Listeners()[0].Handler.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/chat/completions", "client-token", body))
		return w
	}
	if w := request(); w.Code != http.StatusOK || calls.Load() != 1 {
		t.Fatalf("initial request status = %d, calls = %d, body=%s", w.Code, calls.Load(), w.Body.String())
	}

	disabled, err := app.reloadManager.MutateConfig(context.Background(), "provider_disable_openai", func(next config.Config) (config.Config, error) {
		return config.SetOutboundEnabled(next, "openai", false)
	})
	if err != nil || !disabled.OK || !disabled.Saved || !disabled.Applied {
		t.Fatalf("disable result = %#v, err=%v", disabled, err)
	}
	if w := request(); w.Code != http.StatusBadGateway || calls.Load() != 1 || !strings.Contains(w.Body.String(), "upstream temporarily unavailable") {
		t.Fatalf("disabled request status = %d, calls = %d, body=%s", w.Code, calls.Load(), w.Body.String())
	}

	enabled, err := app.reloadManager.MutateConfig(context.Background(), "provider_enable_openai", func(next config.Config) (config.Config, error) {
		return config.SetOutboundEnabled(next, "openai", true)
	})
	if err != nil || !enabled.OK || !enabled.Saved || !enabled.Applied {
		t.Fatalf("enable result = %#v, err=%v", enabled, err)
	}
	if w := request(); w.Code != http.StatusOK || calls.Load() != 2 {
		t.Fatalf("restored request status = %d, calls = %d, body=%s", w.Code, calls.Load(), w.Body.String())
	}
}

func TestReloadManagerRejectsRouteWithUnknownOutboundTag(t *testing.T) {
	cfg := baseConfig()
	path := writeReloadTestConfig(t, cfg)
	app, err := NewWithOptions(cfg, Options{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close(context.Background()) }()

	_, err = app.reloadManager.MutateConfig(context.Background(), "route_typo", func(next config.Config) (config.Config, error) {
		next.Routing.Rules[0].ToTags = []string{"missing-tag"}
		return next, nil
	})
	if err == nil || !strings.Contains(err.Error(), `to_tags "missing-tag" not found in outbounds`) {
		t.Fatalf("MutateConfig() error = %v, want unknown outbound tag validation error", err)
	}
	if app.cfg.Routing.Rules[0].ToTags[0] != "mock-tag" {
		t.Fatalf("runtime route changed after rejected mutation: %#v", app.cfg.Routing.Rules[0])
	}
}

func TestReloadManagerMutationFailurePreservesDiskAndRuntime(t *testing.T) {
	cfg := baseConfig()
	path := writeReloadTestConfig(t, cfg)
	app, err := NewWithOptions(cfg, Options{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close(context.Background()) }()
	originalData, _ := os.ReadFile(path)
	originalDispatcher := app.dispatcher

	t.Run("validation", func(t *testing.T) {
		_, err := app.reloadManager.MutateConfig(context.Background(), "invalid_provider", func(next config.Config) (config.Config, error) {
			next.Outbounds[0].Name = ""
			return next, nil
		})
		if err == nil {
			t.Fatal("MutateConfig() error = nil")
		}
	})
	t.Run("build", func(t *testing.T) {
		blockedDir := filepath.Join(t.TempDir(), "not-a-directory")
		if err := os.WriteFile(blockedDir, []byte("file"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := app.reloadManager.MutateConfig(context.Background(), "unbuildable_provider", func(next config.Config) (config.Config, error) {
			next.Governance.Quota.Snapshot = config.GovernanceQuotaSnapshotConfig{Enabled: true, Dir: blockedDir, FlushInterval: "1h"}
			next.Outbounds[0].Quota = config.OutboundQuotaConfig{Enabled: true, Cooldown: "10m", ProbeInterval: "1m", Windows: []config.OutboundQuotaWindowConfig{{Name: "hourly", Duration: "1h", MaxRequests: 10}}}
			return next, nil
		})
		if err == nil {
			t.Fatal("MutateConfig() error = nil")
		}
	})
	gotData, _ := os.ReadFile(path)
	if string(gotData) != string(originalData) {
		t.Fatalf("config changed after failed mutation\ngot:\n%s\nwant:\n%s", gotData, originalData)
	}
	if app.dispatcher != originalDispatcher || app.cfg.Outbounds[0].Name != cfg.Outbounds[0].Name {
		t.Fatal("runtime changed after failed mutation")
	}
}

func TestReloadManagerMutationRejectsPendingRestartRequiredConfig(t *testing.T) {
	cfg := baseConfig()
	path := writeReloadTestConfig(t, cfg)
	app, err := NewWithOptions(cfg, Options{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close(context.Background()) }()

	pending := cloneConfig(t, cfg)
	pending.Listeners[0].Listen = ":9090"
	writeConfigFile(t, path, pending)
	result, err := app.reloadManager.MutateConfig(context.Background(), "provider_disable_mock", func(next config.Config) (config.Config, error) {
		return config.SetOutboundEnabled(next, "mock", false)
	})
	if err == nil || !result.RestartRequired || !strings.Contains(result.Reason, "listen") {
		t.Fatalf("MutateConfig() = %#v, err = %v", result, err)
	}
	stored, loadErr := config.Load(path)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if stored.Listeners[0].Listen != ":9090" || !config.OutboundEnabled(stored.Outbounds[0]) || !config.OutboundEnabled(app.cfg.Outbounds[0]) {
		t.Fatalf("disk/runtime changed unexpectedly: stored=%#v runtime=%#v", stored, app.cfg)
	}
}

func TestReloadManagerMutationMigratesClientQuotaState(t *testing.T) {
	cfg := baseConfig()
	cfg.Clients[0].Quota = config.ClientQuotaConfig{Enabled: true, Windows: []config.QuotaWindowConfig{{Name: "hourly", Duration: "1h", MaxRequests: 10}}}
	path := writeReloadTestConfig(t, cfg)
	app, err := NewWithOptions(cfg, Options{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close(context.Background()) }()
	app.clientQuotaTracker.RecordClientRequest("office-key")

	result, err := app.reloadManager.MutateConfig(context.Background(), "client_quota_update_openai-entry_office-key", func(next config.Config) (config.Config, error) {
		next.Clients[0].Quota.Windows[0].MaxRequests = 20
		return next, nil
	})
	if err != nil || result.QuotaStateReset {
		t.Fatalf("compatible mutation = %#v, err=%v", result, err)
	}
	assertClientUsage(t, app.clientQuotaTracker, 1)

	result, err = app.reloadManager.MutateConfig(context.Background(), "client_quota_window_openai-entry_office-key", func(next config.Config) (config.Config, error) {
		next.Clients[0].Quota.Windows[0].Duration = "2h"
		return next, nil
	})
	if err != nil || !result.QuotaStateReset {
		t.Fatalf("incompatible mutation = %#v, err=%v", result, err)
	}
	assertClientUsage(t, app.clientQuotaTracker, 0)

	app.clientQuotaTracker.RecordClientRequest("office-key")
	result, err = app.reloadManager.MutateConfig(context.Background(), "client_quota_type_office-key", func(next config.Config) (config.Config, error) {
		next.Clients[0].Quota.Windows[0].Type = "tokens"
		next.Clients[0].Quota.Windows[0].MaxRequests = 0
		next.Clients[0].Quota.Windows[0].MaxTokens = 1000
		return next, nil
	})
	if err != nil || !result.QuotaStateReset {
		t.Fatalf("type mutation = %#v, err=%v", result, err)
	}
	items := app.clientQuotaTracker.ClientSnapshot()
	if len(items) != 1 || items[0].Windows[0].Type != "tokens" || items[0].Windows[0].UsedTokens != 0 {
		t.Fatalf("client quota after type mutation = %#v", items)
	}
}

func TestReloadManagerMutationMigratesProviderQuotaState(t *testing.T) {
	cfg := baseConfig()
	cfg.Outbounds[0].Quota = config.OutboundQuotaConfig{Enabled: true, Cooldown: "10m", ProbeInterval: "1m", Windows: []config.OutboundQuotaWindowConfig{{Name: "hourly", Duration: "1h", MaxRequests: 10}}}
	path := writeReloadTestConfig(t, cfg)
	app, err := NewWithOptions(cfg, Options{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close(context.Background()) }()
	app.outboundQuotaTracker.RecordSuccess("mock")

	result, err := app.reloadManager.MutateConfig(context.Background(), "provider_quota_update_mock", func(next config.Config) (config.Config, error) {
		next.Outbounds[0].Quota.Windows[0].MaxRequests = 20
		return next, nil
	})
	if err != nil || result.QuotaStateReset {
		t.Fatalf("compatible mutation = %#v, err=%v", result, err)
	}
	assertOutboundUsage(t, app.outboundQuotaTracker, 1)

	result, err = app.reloadManager.MutateConfig(context.Background(), "provider_quota_window_mock", func(next config.Config) (config.Config, error) {
		next.Outbounds[0].Quota.Windows[0].Duration = "2h"
		return next, nil
	})
	if err != nil || !result.QuotaStateReset {
		t.Fatalf("incompatible mutation = %#v, err=%v", result, err)
	}
	assertOutboundUsage(t, app.outboundQuotaTracker, 0)
}

func applyReloadConfig(t *testing.T, app *App, path string, cfg config.Config) ReloadResult {
	t.Helper()
	writeConfigFile(t, path, cfg)
	result, err := app.reloadManager.applyConfig(context.Background(), "test")
	if err != nil {
		t.Fatalf("ApplyConfig() error = %v", err)
	}
	if !result.OK || !result.Applied {
		t.Fatalf("ApplyConfig() = %#v, want applied", result)
	}
	return result
}

func assertClientUsage(t *testing.T, tracker *quota.Tracker, want int) {
	t.Helper()
	items := tracker.ClientSnapshot()
	if len(items) != 1 || len(items[0].Windows) != 1 || items[0].Windows[0].UsedRequests != want {
		t.Fatalf("client quota snapshot = %#v, want used requests %d", items, want)
	}
}

func assertOutboundUsage(t *testing.T, tracker *quota.Tracker, want ...int) {
	t.Helper()
	items := tracker.Snapshot()
	if len(items) != 1 || len(items[0].Windows) != len(want) {
		t.Fatalf("quota snapshot = %#v", items)
	}
	for i, used := range want {
		if got := items[0].Windows[i].UsedRequests; got != used {
			t.Fatalf("window %d used requests = %d, want %d", i, got, used)
		}
	}
}

func cloneConfig(t *testing.T, cfg config.Config) config.Config {
	t.Helper()
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("yaml.Marshal() error = %v", err)
	}
	cloned, err := config.ParseBytes(data)
	if err != nil {
		t.Fatalf("config.ParseBytes() error = %v", err)
	}
	return cloned
}

func writeReloadTestConfig(t *testing.T, cfg config.Config) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeConfigFile(t, path, cfg)
	return path
}

func writeConfigFile(t *testing.T, path string, cfg config.Config) {
	t.Helper()
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("yaml.Marshal() error = %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
}
