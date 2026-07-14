package gateway

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/ryanycheng/Syrogo/internal/config"
	"github.com/ryanycheng/Syrogo/internal/sessions"
)

type sessionRegisterRequest struct {
	SessionID   string            `json:"session_id"`
	ClientName  string            `json:"client_name"`
	InboundName string            `json:"inbound_name"`
	Host        string            `json:"host"`
	PID         int               `json:"pid"`
	CWD         string            `json:"cwd"`
	GitBranch   string            `json:"git_branch"`
	Command     []string          `json:"command"`
	Tmux        sessions.TmuxInfo `json:"tmux"`
	StartedAt   time.Time         `json:"started_at"`
}

type sessionHookEventRequest struct {
	SessionID  string         `json:"session_id"`
	EventName  string         `json:"event_name"`
	Payload    map[string]any `json:"payload"`
	ReceivedAt time.Time      `json:"received_at"`
}

type sessionStoppedRequest struct {
	SessionID string `json:"session_id"`
	ExitCode  int    `json:"exit_code"`
}

func (h *Handler) handleSessionRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	inbound, client, ok := h.matchSessionClient(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid client token")
		return
	}
	var req sessionRegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if req.SessionID == "" {
		writeError(w, http.StatusBadRequest, "session_id is required")
		return
	}
	if req.ClientName != "" && req.ClientName != client.Name {
		writeError(w, http.StatusForbidden, "client does not match token")
		return
	}
	if req.InboundName != "" && req.InboundName != inbound.Name {
		writeError(w, http.StatusForbidden, "inbound does not match token")
		return
	}
	session := h.sessionStore.Register(sessions.Session{
		ID:          req.SessionID,
		ClientName:  client.Name,
		InboundName: inbound.Name,
		Host:        req.Host,
		PID:         req.PID,
		CWD:         req.CWD,
		GitBranch:   req.GitBranch,
		Command:     req.Command,
		Tmux:        req.Tmux,
		Status:      sessions.StatusUnknown,
		StartedAt:   req.StartedAt,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "session": session})
}

func (h *Handler) handleSessionHookEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if _, _, ok := h.matchSessionClient(r); !ok {
		writeError(w, http.StatusUnauthorized, "invalid client token")
		return
	}
	var req sessionHookEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if req.SessionID == "" || req.EventName == "" {
		writeError(w, http.StatusBadRequest, "session_id and event_name are required")
		return
	}
	session, ok := h.sessionStore.ApplyHookEvent(sessions.HookEvent{
		SessionID:  req.SessionID,
		EventName:  req.EventName,
		Payload:    req.Payload,
		ReceivedAt: req.ReceivedAt,
	})
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "session": session})
}

func (h *Handler) handleSessionStopped(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if _, _, ok := h.matchSessionClient(r); !ok {
		writeError(w, http.StatusUnauthorized, "invalid client token")
		return
	}
	var req sessionStoppedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if req.SessionID == "" {
		writeError(w, http.StatusBadRequest, "session_id is required")
		return
	}
	session, ok := h.sessionStore.MarkStopped(req.SessionID, req.ExitCode)
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "session": session})
}

func (h *Handler) matchSessionClient(r *http.Request) (config.InboundSpec, config.ClientSpec, bool) {
	token := bearerToken(r.Header.Get("Authorization"))
	if token == "" {
		return config.InboundSpec{}, config.ClientSpec{}, false
	}
	for _, inbound := range h.runtimeState().Inbounds {
		for _, client := range inbound.Clients {
			if client.Token == token {
				return inbound, client, true
			}
		}
	}
	return config.InboundSpec{}, config.ClientSpec{}, false
}
