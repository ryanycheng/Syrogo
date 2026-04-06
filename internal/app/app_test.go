package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"syrogo/internal/config"
)

func baseConfig() config.Config {
	return config.Config{
		Listeners: []config.ListenerSpec{{Name: "public", Listen: ":8080", Inbounds: []string{"openai-entry"}}},
		Inbounds: []config.InboundSpec{{
			Name:     "openai-entry",
			Protocol: "openai_chat",
			Path:     "/v1/chat/completions",
			Clients:  []config.ClientSpec{{Token: "client-token", Tag: "office"}},
		}},
		Routing: config.RoutingConfig{Rules: []config.RoutingRule{{
			Name:     "office-route",
			FromTags: []string{"office"},
			ToTags:   []string{"mock-tag"},
			Strategy: "failover",
		}}},
		Outbounds: []config.OutboundSpec{{Name: "mock", Protocol: "mock", Tag: "mock-tag"}},
	}
}

func baseDualProtocolConfig() config.Config {
	cfg := baseConfig()
	cfg.Inbounds = append(cfg.Inbounds, config.InboundSpec{
		Name:     "anthropic-entry",
		Protocol: "anthropic_messages",
		Path:     "/v1/messages",
		Clients:  []config.ClientSpec{{Token: "anthropic-token", Tag: "office"}},
	})
	cfg.Listeners[0].Inbounds = append(cfg.Listeners[0].Inbounds, "anthropic-entry")
	return cfg
}

func authorizedRequest(method, path, token string, body []byte) *http.Request {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

func TestNewSucceedsWithMockProvider(t *testing.T) {
	app, err := New(baseConfig())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app == nil || app.Server == nil {
		t.Fatal("New() returned nil app or server")
	}
}

func TestNewSucceedsWithOpenAICompatibleProvider(t *testing.T) {
	cfg := baseConfig()
	cfg.Outbounds = []config.OutboundSpec{{
		Name:      "openai",
		Protocol:  "openai_chat",
		Endpoint:  "https://example.com/v1",
		AuthToken: "key-1",
		Tag:       "openai-tag",
	}}
	cfg.Routing.Rules[0].ToTags = []string{"openai-tag"}

	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app == nil || app.Server == nil {
		t.Fatal("New() returned nil app or server")
	}
}

func TestNewBindsEachListenerToItsInbounds(t *testing.T) {
	cfg := baseConfig()
	cfg.Listeners = []config.ListenerSpec{
		{Name: "public", Listen: ":8080", Inbounds: []string{"openai-entry"}},
		{Name: "office", Listen: ":8081", Inbounds: []string{"openai-entry"}},
	}

	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	listeners := app.Server.Listeners()
	if got := len(listeners); got != 2 {
		t.Fatalf("len(app.Server.Listeners()) = %d, want 2", got)
	}
}

func TestNewRoutesByClientTag(t *testing.T) {
	app, err := New(baseConfig())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

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

	listeners := app.Server.Listeners()
	w := httptest.NewRecorder()
	listeners[0].Handler.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/chat/completions", "client-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("route status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
}

func TestNewRoutesWithRuleTargetModel(t *testing.T) {
	cfg := baseConfig()
	cfg.Routing.Rules[0].TargetModel = "gpt-4o-mini"
	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	body, err := json.Marshal(map[string]any{
		"model": "claude-sonnet-4-5",
		"messages": []map[string]string{{
			"role":    "user",
			"content": "hello",
		}},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	listeners := app.Server.Listeners()
	w := httptest.NewRecorder()
	listeners[0].Handler.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/chat/completions", "client-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("rule target model status = %d, want 200, body = %s", w.Code, w.Body.String())
	}

	var resp struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if resp.Model != "gpt-4o-mini" {
		t.Fatalf("resp.Model = %q, want gpt-4o-mini", resp.Model)
	}
}

func TestNewStreamsSSEFromListener(t *testing.T) {
	app, err := New(baseConfig())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	body, err := json.Marshal(map[string]any{
		"model":  "gpt-4",
		"stream": true,
		"messages": []map[string]string{{
			"role":    "user",
			"content": "hello",
		}},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	listeners := app.Server.Listeners()
	w := httptest.NewRecorder()
	listeners[0].Handler.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/chat/completions", "client-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("stream status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	if !strings.Contains(w.Body.String(), "event: message_start\n") || !strings.Contains(w.Body.String(), "data: [DONE]\n\n") {
		t.Fatalf("body = %q, want SSE lifecycle + DONE", w.Body.String())
	}
}

func TestNewSupportsAnthropicInboundProtocol(t *testing.T) {
	app, err := New(baseDualProtocolConfig())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	body, err := json.Marshal(map[string]any{
		"model": "claude-sonnet-4-5",
		"messages": []map[string]string{{
			"role":    "user",
			"content": "hello",
		}},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	listeners := app.Server.Listeners()
	w := httptest.NewRecorder()
	listeners[0].Handler.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/messages", "anthropic-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("route status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"type":"message"`) {
		t.Fatalf("body = %q, want anthropic message response", w.Body.String())
	}
}

func TestNewStreamsAnthropicSSEFromListener(t *testing.T) {
	app, err := New(baseDualProtocolConfig())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	body, err := json.Marshal(map[string]any{
		"model":  "claude-sonnet-4-5",
		"stream": true,
		"messages": []map[string]string{{
			"role":    "user",
			"content": "hello",
		}},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	listeners := app.Server.Listeners()
	w := httptest.NewRecorder()
	listeners[0].Handler.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/messages", "anthropic-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("stream status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	if !strings.Contains(w.Body.String(), `"type":"message_start"`) || !strings.Contains(w.Body.String(), "event: done\ndata: {}\n\n") {
		t.Fatalf("body = %q, want anthropic SSE lifecycle + done", w.Body.String())
	}
}

func TestNewFailsWithUnsupportedProviderProtocol(t *testing.T) {
	cfg := baseConfig()
	cfg.Outbounds = []config.OutboundSpec{{Name: "bad", Protocol: "unknown", Tag: "bad-tag"}}
	cfg.Routing.Rules[0].ToTags = []string{"bad-tag"}

	_, err := New(cfg)
	if err == nil || err.Error() != "unsupported provider protocol \"unknown\"" {
		t.Fatalf("New() error = %v, want unsupported provider protocol error", err)
	}
}
