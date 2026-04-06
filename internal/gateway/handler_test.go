package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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

func testRoutingConfig() config.RoutingConfig {
	return config.RoutingConfig{Rules: []config.RoutingRule{{
		Name:     "office",
		FromTags: []string{"office"},
		ToTags:   []string{"mock-tag"},
		Strategy: "failover",
	}}}
}

func testInbounds() []config.InboundSpec {
	return []config.InboundSpec{{
		Name:     "openai-entry",
		Protocol: "openai_chat",
		Path:     "/v1/chat/completions",
		Clients:  []config.ClientSpec{{Token: "client-token", Tag: "office"}},
	}}
}

func testDualProtocolInbounds() []config.InboundSpec {
	return []config.InboundSpec{
		{Name: "openai-entry", Protocol: "openai_chat", Path: "/v1/chat/completions", Clients: []config.ClientSpec{{Token: "client-token", Tag: "office"}}},
		{Name: "anthropic-entry", Protocol: "anthropic_messages", Path: "/v1/messages", Clients: []config.ClientSpec{{Token: "anthropic-token", Tag: "office"}}},
	}
}

func testOutbounds() []config.OutboundSpec {
	return []config.OutboundSpec{{Name: "mock", Protocol: "mock", Tag: "mock-tag"}}
}

func newTestHandler(t *testing.T, providers map[string]provider.Provider, routing config.RoutingConfig, inbounds []config.InboundSpec, outbounds []config.OutboundSpec) *Handler {
	t.Helper()

	r, err := router.New(routing, providers, outbounds)
	if err != nil {
		t.Fatalf("router.New() error = %v", err)
	}

	return New(r, execution.NewDispatcher(), inbounds)
}

func authorizedRequest(method, path, token string, body []byte) *http.Request {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

func TestHealthzReturnsOK(t *testing.T) {
	h := newTestHandler(t, map[string]provider.Provider{"mock": provider.NewMock("mock")}, testRoutingConfig(), testInbounds(), testOutbounds())

	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestChatCompletionsRejectsMissingToken(t *testing.T) {
	h := newTestHandler(t, map[string]provider.Provider{"mock": provider.NewMock("mock")}, testRoutingConfig(), testInbounds(), testOutbounds())
	mux := http.NewServeMux()
	h.Register(mux)

	body := []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hello"}]}`)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body)))

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestChatCompletionsRejectsInvalidJSON(t *testing.T) {
	h := newTestHandler(t, map[string]provider.Provider{"mock": provider.NewMock("mock")}, testRoutingConfig(), testInbounds(), testOutbounds())
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/chat/completions", "client-token", []byte("{")))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestChatCompletionsUsesDispatcherBackedPlan(t *testing.T) {
	h := newTestHandler(t, map[string]provider.Provider{"mock": provider.NewMock("mock")}, testRoutingConfig(), testInbounds(), testOutbounds())
	mux := http.NewServeMux()
	h.Register(mux)

	body, err := json.Marshal(map[string]any{"model": "gpt-4", "messages": []map[string]string{{"role": "user", "content": "hello"}}})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/chat/completions", "client-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
}

func TestChatCompletionsStreamsSSE(t *testing.T) {
	h := newTestHandler(t, map[string]provider.Provider{"mock": provider.NewMock("mock")}, testRoutingConfig(), testInbounds(), testOutbounds())
	mux := http.NewServeMux()
	h.Register(mux)

	body, err := json.Marshal(map[string]any{"model": "gpt-4", "stream": true, "messages": []map[string]string{{"role": "user", "content": "hello"}}})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/chat/completions", "client-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	if !strings.Contains(w.Body.String(), "data: [DONE]\n\n") {
		t.Fatalf("body = %q, want done sentinel", w.Body.String())
	}
}

func TestAnthropicMessagesUsesDispatcherBackedPlan(t *testing.T) {
	h := newTestHandler(t, map[string]provider.Provider{"mock": provider.NewMock("mock")}, testRoutingConfig(), testDualProtocolInbounds(), testOutbounds())
	mux := http.NewServeMux()
	h.Register(mux)

	body, err := json.Marshal(map[string]any{"model": "claude-sonnet-4-5", "messages": []map[string]string{{"role": "user", "content": "hello"}}})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/messages", "anthropic-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	var resp struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if resp.Type != "message" || resp.Role != "assistant" || len(resp.Content) != 1 || resp.Content[0].Text == "" {
		t.Fatalf("resp = %#v, want anthropic message response", resp)
	}
}

func TestAnthropicMessagesStreamsSSE(t *testing.T) {
	h := newTestHandler(t, map[string]provider.Provider{"mock": provider.NewMock("mock")}, testRoutingConfig(), testDualProtocolInbounds(), testOutbounds())
	mux := http.NewServeMux()
	h.Register(mux)

	body, err := json.Marshal(map[string]any{"model": "claude-sonnet-4-5", "stream": true, "messages": []map[string]string{{"role": "user", "content": "hello"}}})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/messages", "anthropic-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"type":"message_start"`) {
		t.Fatalf("body = %q, want anthropic message_start payload", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "event: done\ndata: {}\n\n") {
		t.Fatalf("body = %q, want anthropic done event", w.Body.String())
	}
}

func TestChatCompletionsFallsBackToBackupProvider(t *testing.T) {
	routing := config.RoutingConfig{Rules: []config.RoutingRule{{Name: "office", FromTags: []string{"office"}, ToTags: []string{"primary-tag", "fallback-tag"}, Strategy: "failover"}}}
	outbounds := []config.OutboundSpec{{Name: "primary", Protocol: "mock", Tag: "primary-tag"}, {Name: "fallback", Protocol: "mock", Tag: "fallback-tag"}}
	h := newTestHandler(t, map[string]provider.Provider{
		"primary":  &failingProvider{name: "primary", err: provider.NewRetryableError(errors.New("temporary upstream failure"))},
		"fallback": provider.NewMock("fallback"),
	}, routing, testInbounds(), outbounds)
	mux := http.NewServeMux()
	h.Register(mux)

	body, err := json.Marshal(map[string]any{"model": "gpt-4", "messages": []map[string]string{{"role": "user", "content": "hello"}}})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/chat/completions", "client-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
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
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "chatcmpl-upstream", "object": "chat.completion", "model": "gpt-4o-mini", "choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": "hello from upstream gateway"}}}})
	}))
	defer upstream.Close()

	h := newTestHandler(t, map[string]provider.Provider{"openai": provider.NewOpenAICompatible("openai", upstream.URL, []string{"test-key"}, upstream.Client())}, config.RoutingConfig{Rules: []config.RoutingRule{{Name: "office", FromTags: []string{"office"}, ToTags: []string{"openai-tag"}, Strategy: "failover"}}}, testInbounds(), []config.OutboundSpec{{Name: "openai", Protocol: "openai_chat", Endpoint: upstream.URL, AuthToken: "test-key", Tag: "openai-tag"}})
	mux := http.NewServeMux()
	h.Register(mux)

	body, err := json.Marshal(map[string]any{"model": "gpt-4o-mini", "messages": []map[string]string{{"role": "user", "content": "hello"}}})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/chat/completions", "client-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
}
