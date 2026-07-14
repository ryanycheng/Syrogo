package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

var claudeHookEvents = []string{
	"SessionStart",
	"UserPromptSubmit",
	"PreToolUse",
	"PostToolUse",
	"Notification",
	"Stop",
	"SubagentStop",
	"PreCompact",
	"SessionEnd",
}

type claudeHookCommand struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

type claudeHookMatcher struct {
	Matcher string              `json:"matcher,omitempty"`
	Hooks   []claudeHookCommand `json:"hooks"`
}

func prepareClaudeConfigWithHooks(commandPath string) (string, error) {
	settings := readClaudeSettings()
	hooks := map[string]any{}
	if existing, ok := settings["hooks"].(map[string]any); ok {
		hooks = existing
	}
	for _, event := range claudeHookEvents {
		hooks[event] = appendClaudeHook(hooks[event], commandPath, event)
	}
	settings["hooks"] = hooks

	dir, err := os.MkdirTemp("", "syrogo-claude-config-*")
	if err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), data, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

func readClaudeSettings() map[string]any {
	settingsPath := ""
	if configDir := os.Getenv("CLAUDE_CONFIG_DIR"); configDir != "" {
		settingsPath = filepath.Join(configDir, "settings.json")
	} else if home, err := os.UserHomeDir(); err == nil && home != "" {
		settingsPath = filepath.Join(home, ".claude", "settings.json")
	}
	settings := map[string]any{}
	if settingsPath == "" {
		return settings
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return settings
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return map[string]any{}
	}
	return settings
}

func appendClaudeHook(existing any, commandPath string, event string) []claudeHookMatcher {
	matchers := decodeHookMatchers(existing)
	command := claudeHookCommand{Type: "command", Command: fmt.Sprintf("%s session hook-event --event %s", shellQuote(commandPath), shellQuote(event))}
	matchers = append(matchers, claudeHookMatcher{Hooks: []claudeHookCommand{command}})
	return matchers
}

func decodeHookMatchers(value any) []claudeHookMatcher {
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var matchers []claudeHookMatcher
	if err := json.Unmarshal(data, &matchers); err == nil {
		return matchers
	}
	return nil
}
