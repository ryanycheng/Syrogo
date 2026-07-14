package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
)

type tmuxInfo struct {
	Present     bool   `json:"present"`
	Session     string `json:"session,omitempty"`
	WindowIndex string `json:"window_index,omitempty"`
	WindowName  string `json:"window_name,omitempty"`
	PaneIndex   string `json:"pane_index,omitempty"`
	PaneID      string `json:"pane_id,omitempty"`
}

func collectTmuxInfo() tmuxInfo {
	info := tmuxInfo{}
	if os.Getenv("TMUX") == "" && os.Getenv("TMUX_PANE") == "" {
		return info
	}
	info.Present = true
	info.PaneID = os.Getenv("TMUX_PANE")
	info.Session = runTrimmedCommand("tmux", "display-message", "-p", "#S")
	info.WindowIndex = runTrimmedCommand("tmux", "display-message", "-p", "#I")
	info.WindowName = runTrimmedCommand("tmux", "display-message", "-p", "#W")
	info.PaneIndex = runTrimmedCommand("tmux", "display-message", "-p", "#P")
	if paneID := runTrimmedCommand("tmux", "display-message", "-p", "#D"); paneID != "" {
		info.PaneID = paneID
	}
	return info
}

func collectGitBranch() string {
	branch := runTrimmedCommand("git", "rev-parse", "--abbrev-ref", "HEAD")
	if branch != "" && branch != "HEAD" {
		return branch
	}
	return runTrimmedCommand("git", "rev-parse", "--short", "HEAD")
}

func runTrimmedCommand(name string, args ...string) string {
	cmd := exec.Command(name, args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(stdout.String())
}
