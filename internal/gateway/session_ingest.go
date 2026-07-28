package gateway

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/ryanycheng/Syrogo/internal/config"
	"github.com/ryanycheng/Syrogo/internal/sessions"
)

type sessionRegisterRequest struct {
	SessionID           string            `json:"session_id"`
	ClientName          string            `json:"client_name"`
	InboundName         string            `json:"inbound_name"`
	Tag                 string            `json:"tag"`
	Host                string            `json:"host"`
	PID                 int               `json:"pid"`
	CWD                 string            `json:"cwd"`
	GitBranch           string            `json:"git_branch"`
	Command             []string          `json:"command"`
	Tmux                sessions.TmuxInfo `json:"tmux"`
	StartedAt           time.Time         `json:"started_at"`
	HeartbeatCapability string            `json:"heartbeat_capability"`
}

type sessionHookEventRequest struct {
	SessionID   string         `json:"session_id"`
	InboundName string         `json:"inbound_name"`
	EventName   string         `json:"event_name"`
	Payload     map[string]any `json:"payload"`
	ReceivedAt  time.Time      `json:"received_at"`
}

type sessionHeartbeatRequest struct {
	SessionID   string `json:"session_id"`
	InboundName string `json:"inbound_name"`
}

type sessionStoppedRequest struct {
	SessionID   string `json:"session_id"`
	InboundName string `json:"inbound_name"`
	ExitCode    int    `json:"exit_code"`
}

func (h *Handler) handleSessionRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
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
	if req.InboundName == "" {
		writeError(w, http.StatusBadRequest, "inbound_name is required")
		return
	}
	if req.HeartbeatCapability != "" && req.HeartbeatCapability != sessions.HeartbeatCapabilityV1 {
		writeError(w, http.StatusBadRequest, "unsupported heartbeat_capability")
		return
	}
	inbound, resolved, ok := h.matchSessionClient(r, req.InboundName)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid client binding")
		return
	}
	if req.ClientName != "" && req.ClientName != resolved.Client.Name {
		writeError(w, http.StatusForbidden, "client does not match token")
		return
	}
	session, err := h.sessionStore.Register(sessions.Session{
		ID:                  req.SessionID,
		ClientName:          resolved.Client.Name,
		InboundName:         inbound.Name,
		Tag:                 resolved.Binding.Tag,
		Host:                req.Host,
		PID:                 req.PID,
		CWD:                 req.CWD,
		GitBranch:           req.GitBranch,
		Command:             req.Command,
		Tmux:                req.Tmux,
		Status:              sessions.StatusUnknown,
		StartedAt:           req.StartedAt,
		HeartbeatCapability: req.HeartbeatCapability,
	})
	if err != nil {
		writeError(w, http.StatusConflict, "session ID belongs to a different owner")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "session": session})
}

func (h *Handler) handleSessionHookEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req sessionHookEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if req.SessionID == "" || req.EventName == "" || req.InboundName == "" {
		writeError(w, http.StatusBadRequest, "session_id, inbound_name, and event_name are required")
		return
	}
	inbound, resolved, ok := h.matchSessionClient(r, req.InboundName)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid client binding")
		return
	}
	session, ok := h.sessionStore.ApplyHookEvent(resolved.Client.Name, inbound.Name, sessions.HookEvent{
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

func (h *Handler) handleSessionHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req sessionHeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if req.SessionID == "" || req.InboundName == "" {
		writeError(w, http.StatusBadRequest, "session_id and inbound_name are required")
		return
	}
	inbound, resolved, ok := h.matchSessionClient(r, req.InboundName)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid client binding")
		return
	}
	session, found, err := h.sessionStore.Heartbeat(resolved.Client.Name, inbound.Name, req.SessionID)
	if !found {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "session": session})
}

func (h *Handler) handleSessionStopped(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req sessionStoppedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if req.SessionID == "" || req.InboundName == "" {
		writeError(w, http.StatusBadRequest, "session_id and inbound_name are required")
		return
	}
	inbound, resolved, ok := h.matchSessionClient(r, req.InboundName)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid client binding")
		return
	}
	session, ok := h.sessionStore.MarkStopped(resolved.Client.Name, inbound.Name, req.SessionID, req.ExitCode)
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "session": session})
}

func (h *Handler) matchSessionClient(r *http.Request, inboundName string) (config.InboundSpec, config.ResolvedClientBinding, bool) {
	token := bearerToken(r.Header.Get("Authorization"))
	if token == "" || inboundName == "" {
		return config.InboundSpec{}, config.ResolvedClientBinding{}, false
	}
	state := h.runtimeState()
	cfg := config.Config{Clients: state.Clients, Inbounds: state.Inbounds}
	for _, inbound := range state.Inbounds {
		if inbound.Name != inboundName {
			continue
		}
		for _, binding := range inbound.Clients {
			if resolved, ok := config.ResolveClientBinding(cfg, binding); ok && resolved.Client.Token == token {
				return inbound, resolved, true
			}
		}
		return config.InboundSpec{}, config.ResolvedClientBinding{}, false
	}
	return config.InboundSpec{}, config.ResolvedClientBinding{}, false
}
