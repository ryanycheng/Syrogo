package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"syrogo/internal/config"
	"syrogo/internal/execution"
	"syrogo/internal/provider"
	"syrogo/internal/router"
	"syrogo/internal/runtime"
)

type failingProvider struct {
	name string
	err  error
}

func (p *failingProvider) Name() string {
	return p.name
}

func (p *failingProvider) ChatCompletion(_ context.Context, _ runtime.Request) (runtime.Response, error) {
	return runtime.Response{}, p.err
}

func (p *failingProvider) StreamCompletion(_ context.Context, _ runtime.Request) (<-chan runtime.StreamEvent, error) {
	return nil, p.err
}

func newTestHandler(t *testing.T, providers map[string]provider.Provider, routing config.RoutingConfig) *Handler {
	t.Helper()

	r, err := router.New(routing, providers)
	if err != nil {
		t.Fatalf("router.New() error = %v", err)
	}

	return New(r, execution.NewDispatcher(), config.InboundSpec{Name: "test-inbound", Type: "openai_chat"})
}

func TestHealthzReturnsOK(t *testing.T) {
	h := newTestHandler(t, map[string]provider.Provider{
		"mock": provider.NewMock("mock"),
	}, config.RoutingConfig{DefaultProvider: "mock"})

	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if w.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Fatalf("body = %q, want status ok json", w.Body.String())
	}
}

func TestChatCompletionsRejectsInvalidJSON(t *testing.T) {
	h := newTestHandler(t, map[string]provider.Provider{
		"mock": provider.NewMock("mock"),
	}, config.RoutingConfig{DefaultProvider: "mock"})

	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString("{"))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestChatCompletionsUsesDispatcherBackedPlan(t *testing.T) {
	h := newTestHandler(t, map[string]provider.Provider{
		"mock": provider.NewMock("mock"),
	}, config.RoutingConfig{DefaultProvider: "mock"})

	mux := http.NewServeMux()
	h.Register(mux)

	body, err := json.Marshal(map[string]any{
		"model": "gpt-4",
		"messages": []map[string]string{{
			"role":    "user",
			"content": "hello",
		}},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}

	var resp struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Model   string `json:"model"`
		Choices []struct {
			Index   int `json:"index"`
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if resp.Object != "chat.completion" {
		t.Fatalf("object = %q, want chat.completion", resp.Object)
	}
	if resp.Model != "gpt-4" {
		t.Fatalf("model = %q, want gpt-4", resp.Model)
	}
	if len(resp.Choices) != 1 || resp.Choices[0].Message.Role != "assistant" || resp.Choices[0].Message.Content == "" {
		t.Fatalf("choices = %#v, want single assistant response", resp.Choices)
	}
}

func TestChatCompletionsFallsBackToBackupProvider(t *testing.T) {
	h := newTestHandler(t, map[string]provider.Provider{
		"primary":  &failingProvider{name: "primary", err: provider.NewRetryableError(errors.New("temporary upstream failure"))},
		"fallback": provider.NewMock("fallback"),
	}, config.RoutingConfig{
		DefaultProvider:   "primary",
		FallbackProviders: []string{"fallback"},
	})

	mux := http.NewServeMux()
	h.Register(mux)

	body, err := json.Marshal(map[string]any{
		"model": "gpt-4",
		"messages": []map[string]string{{
			"role":    "user",
			"content": "hello",
		}},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}

	var resp struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if resp.Model != "gpt-4" {
		t.Fatalf("model = %q, want gpt-4", resp.Model)
	}
}

func TestChatCompletionsUsesInboundTarget(t *testing.T) {
	h := newTestHandler(t, map[string]provider.Provider{
		"default": provider.NewMock("default"),
		"office":  provider.NewMock("office"),
	}, config.RoutingConfig{
		DefaultOutbound: "default",
		InboundOutbounds: map[string]string{
			"test-inbound": "office",
		},
	})

	mux := http.NewServeMux()
	h.Register(mux)

	body, err := json.Marshal(map[string]any{
		"model": "gpt-4",
		"messages": []map[string]string{{
			"role":    "user",
			"content": "hello",
		}},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}

	var resp struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if resp.Model != "gpt-4" {
		t.Fatalf("model = %q, want gpt-4", resp.Model)
	}
}

func TestChatCompletionsUsesOpenAICompatibleProvider(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %q, want /chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q, want Bearer test-key", got)
		}

		var req struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		if req.Model != "gpt-4o-mini" {
			t.Fatalf("req.Model = %q, want gpt-4o-mini", req.Model)
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "chatcmpl-upstream",
			"object": "chat.completion",
			"model":  req.Model,
			"choices": []map[string]any{{
				"message": map[string]string{
					"role":    "assistant",
					"content": "hello from upstream gateway",
				},
			}},
		})
	}))
	defer upstream.Close()

	h := newTestHandler(t, map[string]provider.Provider{
		"openai": provider.NewOpenAICompatible("openai", upstream.URL, []string{"test-key"}, upstream.Client()),
	}, config.RoutingConfig{DefaultProvider: "openai"})

	mux := http.NewServeMux()
	h.Register(mux)

	body, err := json.Marshal(map[string]any{
		"model": "gpt-4o-mini",
		"messages": []map[string]string{{
			"role":    "user",
			"content": "hello",
		}},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}

	var resp struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if resp.ID != "chatcmpl-upstream" {
		t.Fatalf("id = %q, want chatcmpl-upstream", resp.ID)
	}
	if resp.Model != "gpt-4o-mini" {
		t.Fatalf("model = %q, want gpt-4o-mini", resp.Model)
	}
	if len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "hello from upstream gateway" {
		t.Fatalf("choices = %#v, want upstream assistant message", resp.Choices)
	}
}
