package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ryanycheng/Syrogo/internal/provider"
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
			Status      string `json:"status"`
		} `json:"session"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if response.Session.ClientName != "office-key" || response.Session.InboundName != "openai-entry" {
		t.Fatalf("session owner = %#v", response.Session)
	}
}

func TestSessionRegisterRejectsInvalidToken(t *testing.T) {
	h := newAdminTestHandler(t)
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/session/register", "bad-token", []byte(`{"session_id":"s1"}`)))

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestSessionHookEventAndStopped(t *testing.T) {
	h := newAdminTestHandler(t)
	mux := http.NewServeMux()
	h.Register(mux)

	register := httptest.NewRecorder()
	mux.ServeHTTP(register, authorizedRequest(http.MethodPost, "/session/register", "client-token", []byte(`{"session_id":"s1"}`)))
	if register.Code != http.StatusOK {
		t.Fatalf("register status = %d, body=%s", register.Code, register.Body.String())
	}

	stopHook := httptest.NewRecorder()
	mux.ServeHTTP(stopHook, authorizedRequest(http.MethodPost, "/session/hook-event", "client-token", []byte(`{"session_id":"s1","event_name":"Stop"}`)))
	if stopHook.Code != http.StatusOK || !strings.Contains(stopHook.Body.String(), `"status":"idle"`) {
		t.Fatalf("hook status = %d body=%s", stopHook.Code, stopHook.Body.String())
	}

	stopped := httptest.NewRecorder()
	mux.ServeHTTP(stopped, authorizedRequest(http.MethodPost, "/session/stopped", "client-token", []byte(`{"session_id":"s1","exit_code":0}`)))
	if stopped.Code != http.StatusOK || !strings.Contains(stopped.Body.String(), `"status":"stopped"`) {
		t.Fatalf("stopped status = %d body=%s", stopped.Code, stopped.Body.String())
	}
}

func TestAdminSessionsRequiresAdminToken(t *testing.T) {
	h := newAdminTestHandler(t)
	mux := http.NewServeMux()
	h.Register(mux)

	register := httptest.NewRecorder()
	mux.ServeHTTP(register, authorizedRequest(http.MethodPost, "/session/register", "client-token", []byte(`{"session_id":"s1","host":"dev","tmux":{"present":true,"session":"work","window_index":"2","pane_id":"%3"}}`)))
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
	for _, want := range []string{"office-key", "tmux attach -t", "tmux select-window", "tmux select-pane"} {
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
