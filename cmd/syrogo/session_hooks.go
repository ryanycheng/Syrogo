package main

import (
	"encoding/json"
	"fmt"
	"os"
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

func prepareClaudeSettingsWithHooks(commandPath string) (string, error) {
	hooks := map[string]any{}
	for _, event := range claudeHookEvents {
		hooks[event] = appendClaudeHook(hooks[event], commandPath, event)
	}
	settings := map[string]any{"hooks": hooks}

	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return "", err
	}
	file, err := os.CreateTemp("", "syrogo-claude-settings-*.json")
	if err != nil {
		return "", err
	}
	settingsPath := file.Name()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(settingsPath)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(settingsPath)
		return "", err
	}
	return settingsPath, nil
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
