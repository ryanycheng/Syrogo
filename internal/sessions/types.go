package sessions

import "time"

type Status string

const (
	StatusUnknown           Status = "unknown"
	StatusRunning           Status = "running"
	StatusToolRunning       Status = "tool_running"
	StatusWaitingPermission Status = "waiting_permission"
	StatusIdle              Status = "idle"
	StatusCompacting        Status = "compacting"
	StatusStopped           Status = "stopped"
)

type Session struct {
	ID          string     `json:"id"`
	ClientName  string     `json:"client_name"`
	InboundName string     `json:"inbound_name"`
	Tag         string     `json:"tag"`
	Host        string     `json:"host"`
	PID         int        `json:"pid"`
	CWD         string     `json:"cwd"`
	GitBranch   string     `json:"git_branch,omitempty"`
	Command     []string   `json:"command,omitempty"`
	Tmux        TmuxInfo   `json:"tmux"`
	Status      Status     `json:"status"`
	Mode        string     `json:"mode,omitempty"`
	LastEvent   string     `json:"last_event,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	LastSeenAt  time.Time  `json:"last_seen_at"`
	StoppedAt   *time.Time `json:"stopped_at,omitempty"`
	ExitCode    *int       `json:"exit_code,omitempty"`
}

type TmuxInfo struct {
	Present     bool   `json:"present"`
	Session     string `json:"session,omitempty"`
	WindowIndex string `json:"window_index,omitempty"`
	WindowName  string `json:"window_name,omitempty"`
	PaneIndex   string `json:"pane_index,omitempty"`
	PaneID      string `json:"pane_id,omitempty"`
}

type HookEvent struct {
	SessionID  string         `json:"session_id"`
	EventName  string         `json:"event_name"`
	Payload    map[string]any `json:"payload,omitempty"`
	ReceivedAt time.Time      `json:"received_at"`
}

type ListFilter struct {
	Client string
	Status Status
	Host   string
	CWD    string
}
