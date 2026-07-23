package gateway

import (
	"net/http"
	"strings"

	"github.com/ryanycheng/Syrogo/internal/sessions"
)

type adminSessionItem struct {
	sessions.Session
	Commands sessionCommands `json:"commands"`
}

type sessionCommands struct {
	Attach       string `json:"attach,omitempty"`
	SelectWindow string `json:"select_window,omitempty"`
	SelectPane   string `json:"select_pane,omitempty"`
}

func (h *Handler) handleAdminSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAdmin(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	h.sessionStore.PruneStopped(sessions.DefaultStoppedRetention)
	items := h.sessionStore.List(sessions.ListFilter{
		Client: strings.TrimSpace(r.URL.Query().Get("client")),
		Status: sessions.Status(strings.TrimSpace(r.URL.Query().Get("status"))),
		Host:   strings.TrimSpace(r.URL.Query().Get("host")),
		CWD:    strings.TrimSpace(r.URL.Query().Get("cwd")),
	})
	response := make([]adminSessionItem, 0, len(items))
	for _, item := range items {
		response = append(response, adminSessionItem{Session: item, Commands: tmuxCommands(item.Tmux)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": response})
}

func tmuxCommands(info sessions.TmuxInfo) sessionCommands {
	if !info.Present || info.Session == "" {
		return sessionCommands{}
	}
	commands := sessionCommands{Attach: "tmux attach -t " + shellArg(info.Session)}
	if info.WindowIndex != "" {
		commands.SelectWindow = "tmux select-window -t " + shellArg(info.Session+":"+info.WindowIndex)
	}
	if info.PaneID != "" {
		commands.SelectPane = "tmux select-pane -t " + shellArg(info.PaneID)
	}
	return commands
}

func shellArg(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
