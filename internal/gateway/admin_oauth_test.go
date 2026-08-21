package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryanycheng/Syrogo/internal/oauth"
)

func TestOAuthCredentialsRequireAdminAndDoNotCache(t *testing.T) {
	h := newAdminTestHandler(t)
	store, err := oauth.NewStore(filepath.Join(t.TempDir(), "oauth"))
	if err != nil {
		t.Fatal(err)
	}
	h.SetOAuthManager(oauth.NewManager(store, nil))
	mux := http.NewServeMux()
	h.Register(mux)

	unauthorized := httptest.NewRecorder()
	mux.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/admin/oauth/credentials", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}
	if unauthorized.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", unauthorized.Header().Get("Cache-Control"))
	}

	authorized := httptest.NewRecorder()
	mux.ServeHTTP(authorized, authorizedRequest(http.MethodGet, "/admin/oauth/credentials", "admin-ui-token", nil))
	if authorized.Code != http.StatusOK {
		t.Fatalf("authorized status = %d: %s", authorized.Code, authorized.Body.String())
	}
	if authorized.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", authorized.Header().Get("Cache-Control"))
	}
	var response struct {
		Items []oauth.Metadata `json:"items"`
	}
	if err := json.Unmarshal(authorized.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 0 {
		t.Fatalf("items = %#v", response.Items)
	}
}

func TestOAuthClaudeStartDoesNotExposePKCESecrets(t *testing.T) {
	h := newAdminTestHandler(t)
	store, err := oauth.NewStore(filepath.Join(t.TempDir(), "oauth"))
	if err != nil {
		t.Fatal(err)
	}
	h.SetOAuthManager(oauth.NewManager(store, nil))
	mux := http.NewServeMux()
	h.Register(mux)

	body := []byte(`{"credential_id":"claude-main"}`)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/admin/oauth/claude/start", "admin-ui-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", w.Header().Get("Cache-Control"))
	}
	if body := w.Body.String(); !containsAll(body, "flow_id", "authorization_url") || containsAny(body, "code_verifier", "state\":") {
		t.Fatalf("response leaked or missed flow fields: %s", body)
	}
}

func containsAll(value string, wants ...string) bool {
	for _, want := range wants {
		if !contains(value, want) {
			return false
		}
	}
	return true
}

func containsAny(value string, wants ...string) bool {
	for _, want := range wants {
		if contains(value, want) {
			return true
		}
	}
	return false
}

func contains(value, substring string) bool { return strings.Contains(value, substring) }
