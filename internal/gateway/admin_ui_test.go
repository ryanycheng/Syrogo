package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryanycheng/Syrogo/internal/config"
	"github.com/ryanycheng/Syrogo/internal/provider"
)

func newAdminTestHandler(t *testing.T) *Handler {
	t.Helper()
	h := newTestHandler(t, map[string]provider.Provider{"mock": provider.NewMock("mock")}, testRoutingConfig(), testInbounds(), testOutbounds())
	h.admin = config.AdminConfig{Enabled: true, Token: "admin-ui-token", Logs: config.AdminLogsConfig{Enabled: true, Path: filepath.Join(t.TempDir(), "dev.log"), MaxBytes: 128}}
	return h
}

func TestAdminUIHiddenWhenDisabled(t *testing.T) {
	h := newTestHandler(t, map[string]provider.Provider{"mock": provider.NewMock("mock")}, testRoutingConfig(), testInbounds(), testOutbounds())

	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/", nil))

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestAdminUIReturnsIndexHTMLWhenEnabled(t *testing.T) {
	h := newAdminTestHandler(t)

	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if contentType := w.Header().Get("Content-Type"); !strings.Contains(contentType, "text/html") {
		t.Fatalf("content type = %q, want text/html", contentType)
	}
	if !strings.Contains(w.Body.String(), "Admin UI token") || !strings.Contains(w.Body.String(), "/admin/app.js") {
		t.Fatalf("body = %s, want admin UI HTML", w.Body.String())
	}
}

func TestAdminUIReturnsStaticAssetsWhenEnabled(t *testing.T) {
	h := newAdminTestHandler(t)

	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/app.js", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if contentType := w.Header().Get("Content-Type"); !strings.Contains(contentType, "javascript") {
		t.Fatalf("content type = %q, want javascript", contentType)
	}
	if !strings.Contains(w.Body.String(), "/admin/usage") || !strings.Contains(w.Body.String(), "/admin/logs") {
		t.Fatalf("body = %s, want app script", w.Body.String())
	}
}

func TestAdminAPIRejectsClientToken(t *testing.T) {
	h := newAdminTestHandler(t)

	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodGet, "/admin/usage", "client-token", nil))

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestAdminUsageAcceptsAdminToken(t *testing.T) {
	h := newAdminTestHandler(t)

	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodGet, "/admin/usage?group_by=key", "admin-ui-token", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"items"`) {
		t.Fatalf("body = %s, want items", w.Body.String())
	}
}

func TestAdminQuotaAcceptsAdminToken(t *testing.T) {
	h := newAdminTestHandler(t)

	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodGet, "/admin/quota", "admin-ui-token", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"outbound"`) || !strings.Contains(w.Body.String(), `"client"`) {
		t.Fatalf("body = %s, want quota sections", w.Body.String())
	}
}

func TestAdminLatencyAcceptsAdminToken(t *testing.T) {
	h := newAdminTestHandler(t)

	mux := http.NewServeMux()
	h.Register(mux)

	for _, path := range []string{"/admin/latency", "/admin/latency/summary"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, authorizedRequest(http.MethodGet, path, "admin-ui-token", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("path=%s status = %d, want 200, body=%s", path, w.Code, w.Body.String())
		}
	}
}

func TestAdminLatencyRejectsClientToken(t *testing.T) {
	h := newAdminTestHandler(t)

	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodGet, "/admin/latency", "client-token", nil))

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestAdminConfigReadsConfiguredPath(t *testing.T) {
	h := newAdminTestHandler(t)
	h.configPath = filepath.Join(t.TempDir(), "config.yaml")
	content := []byte(validGatewayConfigYAML())
	if err := os.WriteFile(h.configPath, content, 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodGet, "/admin/config", "admin-ui-token", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), h.configPath) || !strings.Contains(w.Body.String(), "openai-entry") {
		t.Fatalf("body = %s, want config path and content", w.Body.String())
	}
}

func TestAdminLogsReadsConfiguredPathAndRedactsSecrets(t *testing.T) {
	h := newAdminTestHandler(t)
	logPath := h.admin.Logs.Path
	if err := os.WriteFile(logPath, []byte("first\nAuthorization: Bearer secret-token\napi_key=secret-key\nlast\n"), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodGet, "/admin/logs?lines=3", "admin-ui-token", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	var response struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if response.Path == "" || !strings.Contains(response.Content, "last") {
		t.Fatalf("body = %s, want log content", body)
	}
	if strings.Contains(response.Content, "secret-token") || strings.Contains(response.Content, "secret-key") {
		t.Fatalf("body = %s, want redacted secrets", body)
	}
	if !strings.Contains(response.Content, "<redacted>") {
		t.Fatalf("body = %s, want redaction marker", body)
	}
}

func TestAdminConfigValidateAcceptsAdminTokenAndAccountingToken(t *testing.T) {
	h := newAdminTestHandler(t)
	h.accounting = config.AccountingConfig{Enabled: true, ExposeHTTP: true, AdminToken: "accounting-token"}

	mux := http.NewServeMux()
	h.Register(mux)

	for _, token := range []string{"admin-ui-token", "accounting-token"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/admin/config/validate", token, []byte(validGatewayConfigYAML())))
		if w.Code != http.StatusOK {
			t.Fatalf("token=%s status = %d, want 200, body=%s", token, w.Code, w.Body.String())
		}
	}
}

func TestAdminUIDoesNotOverrideConfigAPI(t *testing.T) {
	h := newAdminTestHandler(t)
	h.accounting = config.AccountingConfig{Enabled: true, ExposeHTTP: true, AdminToken: "accounting-token"}

	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/admin/config/validate", strings.NewReader(validGatewayConfigYAML())))

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 from config API", w.Code)
	}
}
