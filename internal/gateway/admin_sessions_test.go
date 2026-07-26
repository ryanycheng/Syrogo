package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ryanycheng/Syrogo/internal/config"
	"github.com/ryanycheng/Syrogo/internal/provider"
	"github.com/ryanycheng/Syrogo/internal/runtime"
	"github.com/ryanycheng/Syrogo/internal/sessions"
)

func TestSessionRegisterUsesClientToken(t *testing.T) {
	h := newAdminTestHandler(t)
	mux := http.NewServeMux()
	h.Register(mux)

	body := `{"session_id":"s1","client_name":"office-key","inbound_name":"openai-entry","host":"dev","pid":123,"cwd":"/repo","git_branch":"master","command":["claude"],"tmux":{"present":true,"session":"work","window_index":"1","pane_id":"%1"}}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/session/register", "client-token", []byte(body)))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var response struct {
		Session struct {
			ClientName  string `json:"client_name"`
			InboundName string `json:"inbound_name"`
			Tag         string `json:"tag"`
			Status      string `json:"status"`
		} `json:"session"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if response.Session.ClientName != "office-key" || response.Session.InboundName != "openai-entry" || response.Session.Tag != "office" {
		t.Fatalf("session owner = %#v", response.Session)
	}
}

func TestSessionRegisterRequiresInboundAndUsesBindingTag(t *testing.T) {
	h := newAdminTestHandler(t)
	h.ApplyRuntime(RuntimeState{
		Clients: []config.ClientSpec{{Name: "shared", Token: "shared-token"}},
		Inbounds: []config.InboundSpec{
			{Name: "first", Clients: []config.ClientBindingSpec{{Ref: "shared", Tag: "first-tag"}}},
			{Name: "second", Clients: []config.ClientBindingSpec{{Ref: "shared", Tag: "second-tag"}}},
		},
		Admin: config.AdminConfig{Enabled: true, Token: "admin-ui-token"},
	})
	mux := http.NewServeMux()
	h.Register(mux)

	missing := httptest.NewRecorder()
	mux.ServeHTTP(missing, authorizedRequest(http.MethodPost, "/session/register", "shared-token", []byte(`{"session_id":"missing"}`)))
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("missing inbound status = %d, want 400, body=%s", missing.Code, missing.Body.String())
	}

	first := httptest.NewRecorder()
	mux.ServeHTTP(first, authorizedRequest(http.MethodPost, "/session/register", "shared-token", []byte(`{"session_id":"first-session","inbound_name":"first","tag":"forged"}`)))
	second := httptest.NewRecorder()
	mux.ServeHTTP(second, authorizedRequest(http.MethodPost, "/session/register", "shared-token", []byte(`{"session_id":"second-session","inbound_name":"second","tag":"forged"}`)))
	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("register statuses = %d/%d, bodies=%s / %s", first.Code, second.Code, first.Body.String(), second.Body.String())
	}

	items := h.sessionStore.List(sessions.ListFilter{})
	byID := make(map[string]sessions.Session, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}
	if byID["first-session"].InboundName != "first" || byID["first-session"].Tag != "first-tag" {
		t.Fatalf("first session = %#v", byID["first-session"])
	}
	if byID["second-session"].InboundName != "second" || byID["second-session"].Tag != "second-tag" {
		t.Fatalf("second session = %#v", byID["second-session"])
	}
}

func TestSessionEndpointsRejectCrossOwnerUpdates(t *testing.T) {
	h := newAdminTestHandler(t)
	h.ApplyRuntime(RuntimeState{
		Clients: []config.ClientSpec{{Name: "first-client", Token: "first-token"}, {Name: "second-client", Token: "second-token"}},
		Inbounds: []config.InboundSpec{
			{Name: "first", Clients: []config.ClientBindingSpec{{Ref: "first-client", Tag: "first-tag"}}},
			{Name: "second", Clients: []config.ClientBindingSpec{{Ref: "second-client", Tag: "second-tag"}}},
		},
	})
	mux := http.NewServeMux()
	h.Register(mux)

	register := httptest.NewRecorder()
	mux.ServeHTTP(register, authorizedRequest(http.MethodPost, "/session/register", "first-token", []byte(`{"session_id":"s1","inbound_name":"first"}`)))
	if register.Code != http.StatusOK {
		t.Fatalf("register status = %d, body=%s", register.Code, register.Body.String())
	}

	for path, body := range map[string]string{
		"/session/hook-event": `{"session_id":"s1","inbound_name":"second","event_name":"Stop"}`,
		"/session/stopped":    `{"session_id":"s1","inbound_name":"second","exit_code":0}`,
	} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, authorizedRequest(http.MethodPost, path, "second-token", []byte(body)))
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404, body=%s", path, w.Code, w.Body.String())
		}
	}
}

func TestSessionRegisterRejectsCrossOwnerReplacement(t *testing.T) {
	h := newAdminTestHandler(t)
	h.ApplyRuntime(RuntimeState{
		Clients: []config.ClientSpec{{Name: "first-client", Token: "first-token"}, {Name: "second-client", Token: "second-token"}},
		Inbounds: []config.InboundSpec{
			{Name: "first", Clients: []config.ClientBindingSpec{{Ref: "first-client", Tag: "first-tag"}}},
			{Name: "second", Clients: []config.ClientBindingSpec{{Ref: "second-client", Tag: "second-tag"}}},
		},
	})
	mux := http.NewServeMux()
	h.Register(mux)

	first := httptest.NewRecorder()
	mux.ServeHTTP(first, authorizedRequest(http.MethodPost, "/session/register", "first-token", []byte(`{"session_id":"shared-id","inbound_name":"first","host":"original"}`)))
	if first.Code != http.StatusOK {
		t.Fatalf("first register status = %d, body=%s", first.Code, first.Body.String())
	}

	attack := httptest.NewRecorder()
	mux.ServeHTTP(attack, authorizedRequest(http.MethodPost, "/session/register", "second-token", []byte(`{"session_id":"shared-id","inbound_name":"second","host":"attacker"}`)))
	if attack.Code != http.StatusConflict {
		t.Fatalf("cross-owner register status = %d, want 409, body=%s", attack.Code, attack.Body.String())
	}

	session, ok := h.sessionStore.GetOwned("shared-id", "first-client", "first")
	if !ok || session.Host != "original" {
		t.Fatalf("original session was replaced: %#v, ok=%v", session, ok)
	}
}

func TestRequestContextRejectsForeignAndUnknownExplicitSessionIDs(t *testing.T) {
	store := sessions.NewStore()
	if _, err := store.Register(sessions.Session{
		ID:          "owned-session",
		ClientName:  "owner-client",
		InboundName: "owner-inbound",
		Command:     []string{"claude"},
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	inbound := config.InboundSpec{Name: "request-inbound"}
	resolved := config.ResolvedClientBinding{Client: config.ClientSpec{Name: "request-client"}}

	for _, sessionID := range []string{"owned-session", "unknown-session"} {
		t.Run(sessionID, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			req.Header.Set("X-Syrogo-Session-ID", sessionID)
			ctx := withRequestContext(context.Background(), "request-id", req, inbound, resolved, store)
			if got := ctx.Value(runtime.ContextKeySessionID); got != nil {
				t.Fatalf("session ID context = %v, want nil", got)
			}
		})
	}
}

func TestRequestContextAcceptsOwnedExplicitSessionID(t *testing.T) {
	store := sessions.NewStore()
	if _, err := store.Register(sessions.Session{
		ID:          "owned-session",
		ClientName:  "request-client",
		InboundName: "request-inbound",
		Command:     []string{"claude"},
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("X-Syrogo-Session-ID", "owned-session")
	ctx := withRequestContext(
		context.Background(),
		"request-id",
		req,
		config.InboundSpec{Name: "request-inbound"},
		config.ResolvedClientBinding{Client: config.ClientSpec{Name: "request-client"}},
		store,
	)
	if got := ctx.Value(runtime.ContextKeySessionID); got != "owned-session" {
		t.Fatalf("session ID context = %v, want owned-session", got)
	}
	if got := ctx.Value(runtime.ContextKeyAgent); got != "claude" {
		t.Fatalf("agent context = %v, want claude", got)
	}
}

func TestSessionRegisterRejectsInvalidToken(t *testing.T) {
	h := newAdminTestHandler(t)
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/session/register", "bad-token", []byte(`{"session_id":"s1","inbound_name":"openai-entry"}`)))

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestSessionHookEventAndStopped(t *testing.T) {
	h := newAdminTestHandler(t)
	mux := http.NewServeMux()
	h.Register(mux)

	register := httptest.NewRecorder()
	mux.ServeHTTP(register, authorizedRequest(http.MethodPost, "/session/register", "client-token", []byte(`{"session_id":"s1","inbound_name":"openai-entry"}`)))
	if register.Code != http.StatusOK {
		t.Fatalf("register status = %d, body=%s", register.Code, register.Body.String())
	}

	stopHook := httptest.NewRecorder()
	mux.ServeHTTP(stopHook, authorizedRequest(http.MethodPost, "/session/hook-event", "client-token", []byte(`{"session_id":"s1","inbound_name":"openai-entry","event_name":"Stop"}`)))
	if stopHook.Code != http.StatusOK || !strings.Contains(stopHook.Body.String(), `"status":"idle"`) {
		t.Fatalf("hook status = %d body=%s", stopHook.Code, stopHook.Body.String())
	}

	stopped := httptest.NewRecorder()
	mux.ServeHTTP(stopped, authorizedRequest(http.MethodPost, "/session/stopped", "client-token", []byte(`{"session_id":"s1","inbound_name":"openai-entry","exit_code":0}`)))
	if stopped.Code != http.StatusOK || !strings.Contains(stopped.Body.String(), `"status":"stopped"`) {
		t.Fatalf("stopped status = %d body=%s", stopped.Code, stopped.Body.String())
	}
}

func TestAdminSessionsRequiresAdminToken(t *testing.T) {
	h := newAdminTestHandler(t)
	mux := http.NewServeMux()
	h.Register(mux)

	register := httptest.NewRecorder()
	mux.ServeHTTP(register, authorizedRequest(http.MethodPost, "/session/register", "client-token", []byte(`{"session_id":"s1","inbound_name":"openai-entry","host":"dev","tmux":{"present":true,"session":"work","window_index":"2","pane_id":"%3"}}`)))
	if register.Code != http.StatusOK {
		t.Fatalf("register status = %d, body=%s", register.Code, register.Body.String())
	}

	denied := httptest.NewRecorder()
	mux.ServeHTTP(denied, authorizedRequest(http.MethodGet, "/admin/sessions", "client-token", nil))
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", denied.Code)
	}

	ok := httptest.NewRecorder()
	mux.ServeHTTP(ok, authorizedRequest(http.MethodGet, "/admin/sessions", "admin-ui-token", nil))
	if ok.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", ok.Code, ok.Body.String())
	}
	body := ok.Body.String()
	for _, forbidden := range []string{"client-token", "admin-ui-token", "hook_payload", "prompt"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("body leaked %q: %s", forbidden, body)
		}
	}
	for _, want := range []string{"office-key", `"tag":"office"`, "tmux attach -t", "tmux select-window", "tmux select-pane"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %s, want %s", body, want)
		}
	}
}

func TestAdminSessionsSharedStoreCanBeInjected(t *testing.T) {
	h := newTestHandler(t, map[string]provider.Provider{"mock": provider.NewMock("mock")}, testRoutingConfig(), testInbounds(), testOutbounds())
	if h.sessionStore == nil {
		t.Fatalf("expected default session store")
	}
}
