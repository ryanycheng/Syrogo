package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

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

func TestMergeEnvOverridesExistingValues(t *testing.T) {
	got := mergeEnv([]string{"OPENAI_API_KEY=old", "PATH=/bin"}, map[string]string{"OPENAI_API_KEY": "new", "OPENAI_BASE_URL": "http://gateway"})
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "OPENAI_API_KEY=new") || !strings.Contains(joined, "OPENAI_BASE_URL=http://gateway") || !strings.Contains(joined, "PATH=/bin") || strings.Contains(joined, "OPENAI_API_KEY=old") {
		t.Fatalf("mergeEnv() = %#v, want overridden env", got)
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
