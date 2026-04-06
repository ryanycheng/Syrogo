package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"syrogo/internal/config"
)

func TestNewSucceedsWithMockProvider(t *testing.T) {
	app, err := New(config.Config{
		Server:  config.ServerConfig{Listen: ":8080"},
		Routing: config.RoutingConfig{DefaultProvider: "mock"},
		Provider: []config.ProviderSpec{{
			Name: "mock",
			Type: "mock",
		}},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app == nil || app.Server == nil {
		t.Fatal("New() returned nil app or server")
	}
}

func TestNewSucceedsWithOpenAICompatibleProvider(t *testing.T) {
	app, err := New(config.Config{
		Server:  config.ServerConfig{Listen: ":8080"},
		Routing: config.RoutingConfig{DefaultProvider: "openai"},
		Provider: []config.ProviderSpec{{
			Name:    "openai",
			Type:    "openai_compatible",
			BaseURL: "https://example.com/v1",
			APIKeys: []string{"key-1", "key-2"},
		}},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app == nil || app.Server == nil {
		t.Fatal("New() returned nil app or server")
	}
}

func TestNewSucceedsWithOutboundConfig(t *testing.T) {
	app, err := New(config.Config{
		Listeners: []config.ListenerSpec{{Name: "public", Listen: ":8080", Inbound: "openai-entry"}},
		Inbounds:  []config.InboundSpec{{Name: "openai-entry", Type: "openai_chat"}},
		Routing:   config.RoutingConfig{DefaultOutbound: "mock"},
		Outbound: []config.ProviderSpec{{
			Name: "mock",
			Type: "mock",
		}},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app == nil || app.Server == nil {
		t.Fatal("New() returned nil app or server")
	}
}

func TestNewBindsEachListenerToItsInbound(t *testing.T) {
	app, err := New(config.Config{
		Listeners: []config.ListenerSpec{
			{Name: "public", Listen: ":8080", Inbound: "public-entry"},
			{Name: "office", Listen: ":8081", Inbound: "office-entry"},
		},
		Inbounds: []config.InboundSpec{
			{Name: "public-entry", Type: "openai_chat"},
			{Name: "office-entry", Type: "openai_chat"},
		},
		Routing: config.RoutingConfig{
			DefaultOutbound: "public",
			InboundOutbounds: map[string]string{
				"office-entry": "office",
			},
		},
		Outbound: []config.ProviderSpec{
			{Name: "public", Type: "mock"},
			{Name: "office", Type: "mock"},
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	listeners := app.Server.Listeners()
	if got := len(listeners); got != 2 {
		t.Fatalf("len(app.Server.Listeners()) = %d, want 2", got)
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

	w1 := httptest.NewRecorder()
	listeners[0].Handler.ServeHTTP(w1, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body)))
	if w1.Code != http.StatusOK {
		t.Fatalf("public listener status = %d, want 200, body = %s", w1.Code, w1.Body.String())
	}

	w2 := httptest.NewRecorder()
	listeners[1].Handler.ServeHTTP(w2, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body)))
	if w2.Code != http.StatusOK {
		t.Fatalf("office listener status = %d, want 200, body = %s", w2.Code, w2.Body.String())
	}
}

func TestNewFailsWithUnsupportedProviderType(t *testing.T) {
	_, err := New(config.Config{
		Server:  config.ServerConfig{Listen: ":8080"},
		Routing: config.RoutingConfig{DefaultProvider: "bad"},
		Provider: []config.ProviderSpec{{
			Name: "bad",
			Type: "unknown",
		}},
	})
	if err == nil || err.Error() != "unsupported provider type \"unknown\"" {
		t.Fatalf("New() error = %v, want unsupported provider type error", err)
	}
}
