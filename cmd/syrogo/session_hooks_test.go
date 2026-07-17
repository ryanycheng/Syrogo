package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareClaudeSettingsWithHooksWritesOverlayOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("os.MkdirAll() error = %v", err)
	}
	existing := `{"theme":"light","hooks":{"Stop":[{"hooks":[{"type":"command","command":"existing"}]}]}}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(existing), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	settingsPath, err := prepareClaudeSettingsWithHooks("/usr/local/bin/syrogo")
	if err != nil {
		t.Fatalf("prepareClaudeSettingsWithHooks() error = %v", err)
	}
	defer func() { _ = os.Remove(settingsPath) }()

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if _, ok := settings["theme"]; ok {
		t.Fatalf("settings = %#v, want hooks overlay without copied user settings", settings)
	}
	content := string(data)
	if strings.Contains(content, "existing") || !strings.Contains(content, "session hook-event") || !strings.Contains(content, "Stop") || !strings.Contains(content, "SessionStart") {
		t.Fatalf("settings content = %s", content)
	}
}

func TestRunSessionHookEventMissingEnvReturnsZero(t *testing.T) {
	t.Setenv("SYROGO_SESSION_ID", "")
	var stderr bytes.Buffer
	code := runSessionHookEvent(sessionHookCLIOptions{Event: "Stop", Stdin: strings.NewReader(`{}`), Stderr: &stderr})
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if !strings.Contains(stderr.String(), "missing session environment") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestCollectTmuxInfoWithoutTmux(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	info := collectTmuxInfo()
	if info.Present {
		t.Fatalf("expected no tmux info, got %#v", info)
	}
}
