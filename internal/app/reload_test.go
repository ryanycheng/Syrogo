package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryanycheng/Syrogo/internal/config"
	"gopkg.in/yaml.v3"
)

func TestReloadManagerAppliesClientTokenChange(t *testing.T) {
	cfg := baseConfig()
	cfg.Admin = config.AdminConfig{Enabled: true, Token: "admin-ui-token"}
	path := writeReloadTestConfig(t, cfg)
	app, err := NewWithOptions(cfg, Options{ConfigPath: path})
	if err != nil {
		t.Fatalf("NewWithOptions() error = %v", err)
	}

	next := cloneConfig(t, cfg)
	next.Inbounds[0].Clients[0].Token = "next-client-token"
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
	next.Inbounds[0].Clients[0].Token = "next-client-token"
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
