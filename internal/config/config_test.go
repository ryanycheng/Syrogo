package config

import (
	"os"
	"path/filepath"
	"testing"
)

func validConfig() Config {
	return Config{
		Listeners: []ListenerSpec{{Name: "public", Listen: ":8080", Inbounds: []string{"openai-entry"}}},
		Inbounds: []InboundSpec{{
			Name:     "openai-entry",
			Protocol: "openai_chat",
			Path:     "/v1/chat/completions",
			Clients:  []ClientSpec{{Token: "client-token", Tag: "office"}},
		}},
		Routing: RoutingConfig{Rules: []RoutingRule{{
			Name:     "office-route",
			FromTags: []string{"office"},
			ToTags:   []string{"mock-tag"},
			Strategy: "failover",
		}}},
		Outbounds: []OutboundSpec{{Name: "mock", Protocol: "mock", Tag: "mock-tag"}},
	}
}

func TestConfigValidateSuccess(t *testing.T) {
	cfg := validConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigListenAddressesUsesListeners(t *testing.T) {
	cfg := Config{
		Listeners: []ListenerSpec{
			{Name: "public", Listen: ":8080", Inbounds: []string{"openai-entry"}},
			{Name: "private", Listen: ":8081", Inbounds: []string{"openai-entry"}},
		},
	}

	got := cfg.ListenAddresses()
	if len(got) != 2 || got[0] != ":8080" || got[1] != ":8081" {
		t.Fatalf("ListenAddresses() = %#v, want [:8080 :8081]", got)
	}
}

func TestConfigInboundByNameReturnsMatchedInbound(t *testing.T) {
	cfg := Config{
		Inbounds: []InboundSpec{{
			Name:     "office-entry",
			Protocol: "openai_chat",
			Path:     "/v1/chat/completions",
			Clients:  []ClientSpec{{Token: "token", Tag: "office"}},
		}},
	}

	got := cfg.InboundByName("office-entry")
	if got.Name != "office-entry" {
		t.Fatalf("InboundByName() name = %q, want office-entry", got.Name)
	}
	if got.Clients[0].Tag != "office" {
		t.Fatalf("InboundByName() clients = %#v, want office tag", got.Clients)
	}
}

func TestConfigListenerInboundsReturnsAllMatchedInbounds(t *testing.T) {
	cfg := Config{
		Inbounds: []InboundSpec{
			{Name: "openai-entry", Protocol: "openai_chat", Path: "/v1/chat/completions", Clients: []ClientSpec{{Token: "a", Tag: "office"}}},
			{Name: "anthropic-entry", Protocol: "anthropic_messages", Path: "/v1/messages", Clients: []ClientSpec{{Token: "b", Tag: "thinking"}}},
		},
	}

	got := cfg.ListenerInbounds(ListenerSpec{Name: "public", Inbounds: []string{"openai-entry", "anthropic-entry"}})
	if len(got) != 2 {
		t.Fatalf("len(ListenerInbounds()) = %d, want 2", len(got))
	}
	if got[1].Protocol != "anthropic_messages" {
		t.Fatalf("ListenerInbounds()[1].Protocol = %q, want anthropic_messages", got[1].Protocol)
	}
}

func TestConfigValidateListenerInboundNotFound(t *testing.T) {
	cfg := validConfig()
	cfg.Listeners[0].Inbounds = []string{"missing"}

	err := cfg.Validate()
	if err == nil || err.Error() != "listeners.public.inbound \"missing\" not found in inbounds" {
		t.Fatalf("Validate() error = %v, want missing inbound error", err)
	}
}

func TestConfigValidateRequiresInboundProtocol(t *testing.T) {
	cfg := validConfig()
	cfg.Inbounds[0].Protocol = ""

	err := cfg.Validate()
	if err == nil || err.Error() != "inbounds.openai-entry.protocol is required" {
		t.Fatalf("Validate() error = %v, want missing protocol error", err)
	}
}

func TestConfigValidateSupportsAnthropicInboundProtocol(t *testing.T) {
	cfg := validConfig()
	cfg.Inbounds = append(cfg.Inbounds, InboundSpec{
		Name:     "anthropic-entry",
		Protocol: "anthropic_messages",
		Path:     "/v1/messages",
		Clients:  []ClientSpec{{Token: "anthropic-token", Tag: "office"}},
	})
	cfg.Listeners[0].Inbounds = append(cfg.Listeners[0].Inbounds, "anthropic-entry")

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigValidateRequiresClientToken(t *testing.T) {
	cfg := validConfig()
	cfg.Inbounds[0].Clients[0].Token = ""

	err := cfg.Validate()
	if err == nil || err.Error() != "inbounds.openai-entry.clients[0].token is required" {
		t.Fatalf("Validate() error = %v, want missing client token error", err)
	}
}

func TestConfigValidateRequiresClientTag(t *testing.T) {
	cfg := validConfig()
	cfg.Inbounds[0].Clients[0].Tag = ""

	err := cfg.Validate()
	if err == nil || err.Error() != "inbounds.openai-entry.clients[0].tag is required" {
		t.Fatalf("Validate() error = %v, want missing client tag error", err)
	}
}

func TestConfigValidateRejectsDuplicateClientToken(t *testing.T) {
	cfg := validConfig()
	cfg.Inbounds = append(cfg.Inbounds, InboundSpec{
		Name:     "other-entry",
		Protocol: "openai_chat",
		Path:     "/other",
		Clients:  []ClientSpec{{Token: "client-token", Tag: "other"}},
	})
	cfg.Listeners[0].Inbounds = append(cfg.Listeners[0].Inbounds, "other-entry")

	err := cfg.Validate()
	if err == nil || err.Error() != "inbounds.other-entry.clients[0].token duplicates token used by openai-entry" {
		t.Fatalf("Validate() error = %v, want duplicate token error", err)
	}
}

func TestConfigValidateRequiresOutboundTag(t *testing.T) {
	cfg := validConfig()
	cfg.Outbounds[0].Tag = ""

	err := cfg.Validate()
	if err == nil || err.Error() != "outbounds.mock.tag is required" {
		t.Fatalf("Validate() error = %v, want missing outbound tag error", err)
	}
}

func TestConfigValidateOpenAIChatRequiresEndpointAndAuthToken(t *testing.T) {
	cfg := validConfig()
	cfg.Outbounds[0] = OutboundSpec{Name: "openai", Protocol: "openai_chat", Tag: "openai-tag"}
	cfg.Routing.Rules[0].ToTags = []string{"openai-tag"}

	err := cfg.Validate()
	if err == nil || err.Error() != "outbounds.openai.endpoint is required" {
		t.Fatalf("Validate() error = %v, want missing endpoint error", err)
	}

	cfg.Outbounds[0].Endpoint = "https://example.com/v1"
	err = cfg.Validate()
	if err == nil || err.Error() != "outbounds.openai.auth_token is required" {
		t.Fatalf("Validate() error = %v, want missing auth_token error", err)
	}
}

func TestConfigValidateRequiresRuleStrategy(t *testing.T) {
	cfg := validConfig()
	cfg.Routing.Rules[0].Strategy = ""

	err := cfg.Validate()
	if err == nil || err.Error() != "routing.rules[0].strategy is required" {
		t.Fatalf("Validate() error = %v, want missing strategy error", err)
	}
}

func TestConfigValidateRejectsUnsupportedStrategy(t *testing.T) {
	cfg := validConfig()
	cfg.Routing.Rules[0].Strategy = "random"

	err := cfg.Validate()
	if err == nil || err.Error() != "routing.rules[0].strategy \"random\" is unsupported" {
		t.Fatalf("Validate() error = %v, want unsupported strategy error", err)
	}
}

func TestConfigValidateRejectsUnknownToTag(t *testing.T) {
	cfg := validConfig()
	cfg.Routing.Rules[0].ToTags = []string{"missing-tag"}

	err := cfg.Validate()
	if err == nil || err.Error() != "routing.rules[0].to_tags \"missing-tag\" not found in outbounds" {
		t.Fatalf("Validate() error = %v, want missing to_tag error", err)
	}
}

func TestConfigValidateMissingListen(t *testing.T) {
	cfg := validConfig()
	cfg.Listeners = nil
	cfg.Server.Listen = ""

	err := cfg.Validate()
	if err == nil || err.Error() != "server.listen or listeners is required" {
		t.Fatalf("Validate() error = %v, want server.listen or listeners is required", err)
	}
}

func TestConfigValidateRequiresOutbound(t *testing.T) {
	cfg := validConfig()
	cfg.Outbounds = nil

	err := cfg.Validate()
	if err == nil || err.Error() != "at least one outbound is required" {
		t.Fatalf("Validate() error = %v, want at least one outbound is required", err)
	}
}

func TestConfigValidateRequiresRoutingRule(t *testing.T) {
	cfg := validConfig()
	cfg.Routing.Rules = nil

	err := cfg.Validate()
	if err == nil || err.Error() != "at least one routing rule is required" {
		t.Fatalf("Validate() error = %v, want at least one routing rule is required", err)
	}
}

func TestLoadSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("listeners:\n  - name: \"public\"\n    listen: \":8080\"\n    inbounds:\n      - \"openai-entry\"\ninbounds:\n  - name: \"openai-entry\"\n    protocol: \"openai_chat\"\n    path: \"/v1/chat/completions\"\n    clients:\n      - token: \"client-token\"\n        tag: \"office\"\nrouting:\n  rules:\n    - name: \"office-route\"\n      from_tags:\n        - \"office\"\n      to_tags:\n        - \"mock-tag\"\n      strategy: \"failover\"\noutbounds:\n  - name: \"mock\"\n    protocol: \"mock\"\n    tag: \"mock-tag\"\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Listeners[0].Name != "public" {
		t.Fatalf("Listeners[0].Name = %q, want public", cfg.Listeners[0].Name)
	}
	if cfg.Routing.Rules[0].Strategy != "failover" {
		t.Fatalf("Routing.Rules[0].Strategy = %q, want failover", cfg.Routing.Rules[0].Strategy)
	}
}
