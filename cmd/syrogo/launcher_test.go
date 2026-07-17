package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseLauncherOptionsInfersBaseURLFromConfig(t *testing.T) {
	configPath := writeLauncherConfig(t, `
listeners:
  - name: "debug"
    listen: ":23235"
    inbounds: ["responses-entry"]
inbounds:
  - name: "responses-entry"
    protocol: "openai_responses"
    path: "/v1/responses"
    clients:
      - name: "responses-key"
        token: "token"
        tag: "responses"
outbounds:
  - name: "mock"
    protocol: "mock"
    tag: "mock"
routing:
  rules:
    - from_tags: ["responses"]
      to_tags: ["mock"]
      strategy: "failover"
`)

	var opts launcherOptions
	err := parseLauncherOptions([]string{"codex", "--config", configPath, "exec", "hello"}, &opts)
	if err != nil {
		t.Fatalf("parseLauncherOptions() error = %v", err)
	}
	if opts.BaseURL != "http://127.0.0.1:23235" {
		t.Fatalf("opts.BaseURL = %q, want inferred debug port", opts.BaseURL)
	}
}

func TestParseLauncherOptionsKeepsExplicitBaseURL(t *testing.T) {
	configPath := writeLauncherConfig(t, `
server:
  listen: ":23235"
inbounds:
  - name: "responses-entry"
    protocol: "openai_responses"
    path: "/v1/responses"
    clients:
      - name: "responses-key"
        token: "token"
        tag: "responses"
outbounds:
  - name: "mock"
    protocol: "mock"
    tag: "mock"
routing:
  rules:
    - from_tags: ["responses"]
      to_tags: ["mock"]
      strategy: "failover"
`)

	var opts launcherOptions
	err := parseLauncherOptions([]string{"codex", "--config", configPath, "--base-url", "http://gateway.local", "exec", "hello"}, &opts)
	if err != nil {
		t.Fatalf("parseLauncherOptions() error = %v", err)
	}
	if opts.BaseURL != "http://gateway.local" {
		t.Fatalf("opts.BaseURL = %q, want explicit base URL", opts.BaseURL)
	}
}

func TestParseLauncherOptionsPassesAgentFlagsWithoutSeparator(t *testing.T) {
	configPath := writeLauncherConfig(t, `
server:
  listen: ":23234"
inbounds:
  - name: "anthropic-entry"
    protocol: "anthropic_messages"
    path: "/v1/messages"
    clients:
      - name: "claude-key"
        token: "token"
        tag: "claude"
outbounds:
  - name: "mock"
    protocol: "mock"
    tag: "mock"
routing:
  rules:
    - from_tags: ["claude"]
      to_tags: ["mock"]
      strategy: "failover"
`)

	var opts launcherOptions
	err := parseLauncherOptions([]string{"claude", "--config", configPath, "--client", "claude-key", "--dangerously-skip-permissions", "--model", "claude-sonnet-4-6"}, &opts)
	if err != nil {
		t.Fatalf("parseLauncherOptions() error = %v", err)
	}
	if opts.Client != "claude-key" {
		t.Fatalf("opts.Client = %q, want claude-key", opts.Client)
	}
	want := []string{"claude", "--dangerously-skip-permissions", "--model", "claude-sonnet-4-6"}
	if !reflect.DeepEqual(opts.Args, want) {
		t.Fatalf("opts.Args = %#v, want %#v", opts.Args, want)
	}
}

func TestParseLauncherOptionsPassesAgentCommandArguments(t *testing.T) {
	configPath := writeLauncherConfig(t, `
server:
  listen: ":23235"
inbounds:
  - name: "responses-entry"
    protocol: "openai_responses"
    path: "/v1/responses"
    clients:
      - name: "responses-key"
        token: "token"
        tag: "responses"
outbounds:
  - name: "mock"
    protocol: "mock"
    tag: "mock"
routing:
  rules:
    - from_tags: ["responses"]
      to_tags: ["mock"]
      strategy: "failover"
`)

	var opts launcherOptions
	err := parseLauncherOptions([]string{"codex", "--config", configPath, "exec", "hello"}, &opts)
	if err != nil {
		t.Fatalf("parseLauncherOptions() error = %v", err)
	}
	want := []string{"codex", "exec", "hello"}
	if !reflect.DeepEqual(opts.Args, want) {
		t.Fatalf("opts.Args = %#v, want %#v", opts.Args, want)
	}
}

func TestBuildLaunchPlanForClaude(t *testing.T) {
	configPath := writeLauncherConfig(t, `
server:
  listen: ":23234"
inbounds:
  - name: "anthropic-entry"
    protocol: "anthropic_messages"
    path: "/v1/messages"
    clients:
      - name: "claude-key"
        token: "claude-token"
        tag: "claude"
outbounds:
  - name: "mock"
    protocol: "mock"
    tag: "mock"
routing:
  rules:
    - from_tags: ["claude"]
      to_tags: ["mock"]
      strategy: "failover"
`)

	plan, err := buildLaunchPlan(launcherOptions{ConfigPath: configPath, BaseURL: "http://127.0.0.1:23234", Args: []string{"claude", "--model", "claude-sonnet-4-6"}})
	if err != nil {
		t.Fatalf("buildLaunchPlan() error = %v", err)
	}
	if plan.Command != "claude" || !reflect.DeepEqual(plan.Args, []string{"--model", "claude-sonnet-4-6"}) {
		t.Fatalf("plan command/args = %#v, want claude model args", plan)
	}
	if plan.Env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:23234" || plan.Env["ANTHROPIC_AUTH_TOKEN"] != "claude-token" {
		t.Fatalf("plan env = %#v, want anthropic Syrogo env", plan.Env)
	}
}

func TestBuildLaunchPlanForCodexWithTokenOverride(t *testing.T) {
	plan, err := buildLaunchPlan(launcherOptions{BaseURL: "http://gateway.local", Token: "manual-token", Args: []string{"codex", "exec", "hello"}})
	if err != nil {
		t.Fatalf("buildLaunchPlan() error = %v", err)
	}
	if plan.Command != "codex" || !reflect.DeepEqual(plan.Args, []string{"exec", "hello"}) {
		t.Fatalf("plan command/args = %#v, want codex exec args", plan)
	}
	if plan.Env["OPENAI_BASE_URL"] != "http://gateway.local" || plan.Env["OPENAI_API_KEY"] != "manual-token" {
		t.Fatalf("plan env = %#v, want OpenAI Syrogo env", plan.Env)
	}
}

func TestBuildLaunchPlanRequiresDisambiguatedClient(t *testing.T) {
	configPath := writeLauncherConfig(t, `
server:
  listen: ":23234"
inbounds:
  - name: "anthropic-entry"
    protocol: "anthropic_messages"
    path: "/v1/messages"
    clients:
      - name: "claude-a"
        token: "token-a"
        tag: "a"
      - name: "claude-b"
        token: "token-b"
        tag: "b"
outbounds:
  - name: "mock"
    protocol: "mock"
    tag: "mock"
routing:
  rules:
    - from_tags: ["a"]
      to_tags: ["mock"]
      strategy: "failover"
`)

	_, err := buildLaunchPlan(launcherOptions{ConfigPath: configPath, BaseURL: "http://127.0.0.1:23234", Args: []string{"claude"}})
	if err == nil || !strings.Contains(err.Error(), "multiple clients matched") {
		t.Fatalf("buildLaunchPlan() error = %v, want multiple clients error", err)
	}
	plan, err := buildLaunchPlan(launcherOptions{ConfigPath: configPath, BaseURL: "http://127.0.0.1:23234", Client: "claude-b", Args: []string{"claude"}})
	if err != nil {
		t.Fatalf("buildLaunchPlan() with client error = %v", err)
	}
	if plan.Env["ANTHROPIC_AUTH_TOKEN"] != "token-b" {
		t.Fatalf("plan env = %#v, want selected client token", plan.Env)
	}
}

func TestParseLauncherOptionsRejectsUnsupportedAgent(t *testing.T) {
	var opts launcherOptions
	err := parseLauncherOptions([]string{"unknown"}, &opts)
	if err == nil || !strings.Contains(err.Error(), "unsupported agent") {
		t.Fatalf("parseLauncherOptions() error = %v, want unsupported agent error", err)
	}
}

func TestDefaultLauncherConfigPathPrefersInstalledConfig(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd() error = %v", err)
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "configs"), 0o755); err != nil {
		t.Fatalf("os.Mkdir() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "configs", "config.yaml"), []byte("server:\n  listen: ':23234'\n"), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	installedPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(installedPath, []byte("server:\n  listen: ':23235'\n"), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	oldInstalledConfigPath := installedConfigPath
	installedConfigPath = installedPath
	defer func() {
		installedConfigPath = oldInstalledConfigPath
	}()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("os.Chdir() error = %v", err)
	}
	defer func() {
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("restore cwd error = %v", err)
		}
	}()

	if got := defaultLauncherConfigPath(); got != installedPath {
		t.Fatalf("defaultLauncherConfigPath() = %q, want installed config", got)
	}
}

func TestDefaultLauncherConfigPathFallsBackToLocalConfigForDevelopment(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd() error = %v", err)
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "configs"), 0o755); err != nil {
		t.Fatalf("os.Mkdir() error = %v", err)
	}
	localPath := filepath.Join(".", "configs", "config.yaml")
	if err := os.WriteFile(filepath.Join(dir, "configs", "config.yaml"), []byte("server:\n  listen: ':23234'\n"), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	oldInstalledConfigPath := installedConfigPath
	installedConfigPath = filepath.Join(t.TempDir(), "missing.yaml")
	defer func() {
		installedConfigPath = oldInstalledConfigPath
	}()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("os.Chdir() error = %v", err)
	}
	defer func() {
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("restore cwd error = %v", err)
		}
	}()

	if got := defaultLauncherConfigPath(); got != localPath {
		t.Fatalf("defaultLauncherConfigPath() = %q, want local config", got)
	}
}

func TestLaunchAgentInjectsClaudeHooksWithSettingsOverlay(t *testing.T) {
	binDir := t.TempDir()
	capturePath := filepath.Join(t.TempDir(), "capture.txt")
	settingsSeenPath := filepath.Join(t.TempDir(), "settings-path.txt")
	fakeClaude := filepath.Join(binDir, "claude")
	script := "#!/bin/sh\n" +
		"printf 'args=%s\\n' \"$*\" > \"$CAPTURE_PATH\"\n" +
		"printf 'claude_config_dir=%s\\n' \"$CLAUDE_CONFIG_DIR\" >> \"$CAPTURE_PATH\"\n" +
		"printf 'session_id=%s\\n' \"$SYROGO_SESSION_ID\" >> \"$CAPTURE_PATH\"\n" +
		"if [ \"$1\" = \"--settings\" ]; then printf '%s' \"$2\" > \"$SETTINGS_SEEN_PATH\"; fi\n"
	if err := os.WriteFile(fakeClaude, []byte(script), 0o755); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	var registerSeen, stoppedSeen bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/session/register":
			registerSeen = true
		case "/session/stopped":
			stoppedSeen = true
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+oldPath)
	t.Setenv("CLAUDE_CONFIG_DIR", "/persist/claude")
	t.Setenv("CAPTURE_PATH", capturePath)
	t.Setenv("SETTINGS_SEEN_PATH", settingsSeenPath)

	err := launchAgent(launcherOptions{
		BaseURL: server.URL,
		Token:   "client-token",
		Args:    []string{"claude", "--model", "claude-sonnet-4-6"},
		Stdin:   strings.NewReader(""),
		Stdout:  &bytes.Buffer{},
		Stderr:  &bytes.Buffer{},
	})
	if err != nil {
		t.Fatalf("launchAgent() error = %v", err)
	}
	if !registerSeen || !stoppedSeen {
		t.Fatalf("registerSeen=%v stoppedSeen=%v, want both true", registerSeen, stoppedSeen)
	}

	capture, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("os.ReadFile(capture) error = %v", err)
	}
	content := string(capture)
	if !strings.Contains(content, "args=--settings ") || !strings.Contains(content, " --model claude-sonnet-4-6") {
		t.Fatalf("capture = %s, want --settings before user args", content)
	}
	if !strings.Contains(content, "claude_config_dir=/persist/claude") {
		t.Fatalf("capture = %s, want original CLAUDE_CONFIG_DIR preserved", content)
	}

	settingsPathBytes, err := os.ReadFile(settingsSeenPath)
	if err != nil {
		t.Fatalf("os.ReadFile(settings path) error = %v", err)
	}
	settingsPath := string(settingsPathBytes)
	if _, err := os.Stat(settingsPath); !os.IsNotExist(err) {
		t.Fatalf("settings path %q still exists or stat error = %v, want removed", settingsPath, err)
	}
}
func TestMergeEnvOverridesExistingValues(t *testing.T) {
	got := mergeEnv([]string{"OPENAI_API_KEY=old", "PATH=/bin"}, map[string]string{"OPENAI_API_KEY": "new", "OPENAI_BASE_URL": "http://gateway"})
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "OPENAI_API_KEY=new") || !strings.Contains(joined, "OPENAI_BASE_URL=http://gateway") || !strings.Contains(joined, "PATH=/bin") || strings.Contains(joined, "OPENAI_API_KEY=old") {
		t.Fatalf("mergeEnv() = %#v, want overridden env", got)
	}
}

func TestPrintLaunchPlanSortsEnvironment(t *testing.T) {
	var buf bytes.Buffer
	err := printLaunchPlan(&buf, launchPlan{
		Command: "codex",
		Args:    []string{"exec", "hello"},
		Env: map[string]string{
			"OPENAI_API_KEY":  "token",
			"OPENAI_BASE_URL": "http://gateway",
		},
	})
	if err != nil {
		t.Fatalf("printLaunchPlan() error = %v", err)
	}
	want := "OPENAI_API_KEY=<redacted>\nOPENAI_BASE_URL=http://gateway\ncommand=codex exec hello\n"
	if buf.String() != want {
		t.Fatalf("printLaunchPlan() = %q, want %q", buf.String(), want)
	}
}

func TestRedactLaunchEnvValue(t *testing.T) {
	cases := map[string]string{
		"ANTHROPIC_AUTH_TOKEN": "<redacted>",
		"OPENAI_API_KEY":       "<redacted>",
		"OPENAI_BASE_URL":      "http://gateway",
	}
	for key, want := range cases {
		if got := redactLaunchEnvValue(key, "http://gateway"); got != want {
			t.Fatalf("redactLaunchEnvValue(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestParseActivateOptionsRejectsAgentArgs(t *testing.T) {
	var opts launcherOptions
	err := parseActivateOptions([]string{"claude", "--", "--model", "claude-sonnet-4-6"}, &opts)
	if err == nil || err.Error() != "activate does not accept agent command arguments" {
		t.Fatalf("parseActivateOptions() error = %v, want extra args error", err)
	}
}

func TestPrintShellExportsUsesRealValues(t *testing.T) {
	var buf bytes.Buffer
	err := printShellExports(&buf, map[string]string{
		"OPENAI_API_KEY":  "token'with-quote",
		"OPENAI_BASE_URL": "http://gateway",
	})
	if err != nil {
		t.Fatalf("printShellExports() error = %v", err)
	}
	want := "export OPENAI_API_KEY='token'\\''with-quote'\nexport OPENAI_BASE_URL='http://gateway'\n"
	if buf.String() != want {
		t.Fatalf("printShellExports() = %q, want %q", buf.String(), want)
	}
}

func writeLauncherConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	return path
}
