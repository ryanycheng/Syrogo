package gateway

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/ryanycheng/Syrogo/internal/config"
	"github.com/ryanycheng/Syrogo/internal/oauth"
)

type oauthCredentialStartRequest struct {
	CredentialID string `json:"credential_id"`
}

type oauthClaudeCompleteRequest struct {
	FlowID   string `json:"flow_id"`
	Callback string `json:"callback"`
}

type oauthCodexPollRequest struct {
	FlowID string `json:"flow_id"`
}

type oauthCredentialDeleteRequest struct {
	CredentialID string `json:"credential_id"`
}

func (h *Handler) handleOAuthCredentials(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAdmin(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	manager, ok := h.oauthManagerForRequest(w)
	if !ok {
		return
	}
	items, err := manager.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list OAuth credentials: "+err.Error())
		return
	}
	writeOAuthJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) handleOAuthClaudeStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAdmin(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	var req oauthCredentialStartRequest
	if !decodeJSONResourceRequest(w, r, &req) {
		return
	}
	manager, ok := h.oauthManagerForRequest(w)
	if !ok {
		return
	}
	flow, err := manager.StartClaudeFlow(strings.TrimSpace(req.CredentialID))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeOAuthJSON(w, http.StatusOK, flow)
}

func (h *Handler) handleOAuthClaudeComplete(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAdmin(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	var req oauthClaudeCompleteRequest
	if !decodeJSONResourceRequest(w, r, &req) {
		return
	}
	manager, ok := h.oauthManagerForRequest(w)
	if !ok {
		return
	}
	metadata, err := manager.CompleteClaudeFlow(r.Context(), strings.TrimSpace(req.FlowID), strings.TrimSpace(req.Callback))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeOAuthJSON(w, http.StatusOK, metadata)
}

func (h *Handler) handleOAuthCodexStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAdmin(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	var req oauthCredentialStartRequest
	if !decodeJSONResourceRequest(w, r, &req) {
		return
	}
	manager, ok := h.oauthManagerForRequest(w)
	if !ok {
		return
	}
	flow, err := manager.StartCodexDeviceFlow(strings.TrimSpace(req.CredentialID))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeOAuthJSON(w, http.StatusOK, flow)
}

func (h *Handler) handleOAuthCodexPoll(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAdmin(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	var req oauthCodexPollRequest
	if !decodeJSONResourceRequest(w, r, &req) {
		return
	}
	manager, ok := h.oauthManagerForRequest(w)
	if !ok {
		return
	}
	metadata, err := manager.PollCodexDeviceFlow(r.Context(), strings.TrimSpace(req.FlowID))
	if errors.Is(err, oauth.ErrCodexDeviceAuthorizationPending) {
		writeOAuthJSON(w, http.StatusOK, map[string]string{"status": "pending"})
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeOAuthJSON(w, http.StatusOK, map[string]any{"status": "completed", "credential": metadata})
}

func (h *Handler) handleOAuthCredentialDelete(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAdmin(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	var req oauthCredentialDeleteRequest
	if !decodeJSONResourceRequest(w, r, &req) {
		return
	}
	manager, ok := h.oauthManagerForRequest(w)
	if !ok {
		return
	}
	cfg, ok := h.readAdminConfigForResource(w)
	if !ok {
		return
	}
	if credentialIsReferenced(cfg, strings.TrimSpace(req.CredentialID)) {
		writeError(w, http.StatusConflict, "OAuth credential is still referenced by an outbound")
		return
	}
	if err := manager.Delete(strings.TrimSpace(req.CredentialID)); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeOAuthJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) oauthManagerForRequest(w http.ResponseWriter) (interface {
	List() ([]oauth.Metadata, error)
	StartClaudeFlow(string) (oauth.Flow, error)
	CompleteClaudeFlow(context.Context, string, string) (oauth.Metadata, error)
	StartCodexDeviceFlow(string) (oauth.Flow, error)
	PollCodexDeviceFlow(context.Context, string) (oauth.Metadata, error)
	Delete(string) error
}, bool) {
	if h.oauthManager == nil {
		writeError(w, http.StatusServiceUnavailable, "OAuth is not configured")
		return nil, false
	}
	return h.oauthManager, true
}

func credentialIsReferenced(cfg config.Config, credentialID string) bool {
	if credentialID == "" {
		return false
	}
	for _, outbound := range cfg.Outbounds {
		if outbound.Auth.CredentialRef == credentialID {
			return true
		}
	}
	return false
}

func writeOAuthJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, value)
}
