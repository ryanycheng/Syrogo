package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigValidateSuccess(t *testing.T) {
	cfg := Config{
		Server: ServerConfig{Listen: ":8080"},
		Routing: RoutingConfig{
			DefaultProvider:   "mock",
			FallbackProviders: []string{"backup"},
		},
		Provider: []ProviderSpec{{Name: "mock", Type: "mock"}, {Name: "backup", Type: "mock"}},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigValidateOutboundSuccess(t *testing.T) {
	cfg := Config{
		Listeners: []ListenerSpec{{Name: "public", Listen: ":8080", Inbound: "openai-entry"}},
		Inbounds:  []InboundSpec{{Name: "openai-entry", Type: "openai_chat"}},
		Routing:   RoutingConfig{DefaultOutbound: "openai"},
		Outbound: []ProviderSpec{{
			Name:    "openai",
			Type:    "openai_compatible",
			BaseURL: "https://example.com/v1",
			APIKeys: []string{"key-1", "key-2"},
		}},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigListenAddressesUsesListeners(t *testing.T) {
	cfg := Config{
		Listeners: []ListenerSpec{
			{Name: "public", Listen: ":8080", Inbound: "openai-entry"},
			{Name: "private", Listen: ":8081", Inbound: "openai-entry"},
		},
	}

	got := cfg.ListenAddresses()
	if len(got) != 2 || got[0] != ":8080" || got[1] != ":8081" {
		t.Fatalf("ListenAddresses() = %#v, want [:8080 :8081]", got)
	}
}

func TestConfigInboundByNameReturnsMatchedInbound(t *testing.T) {
	cfg := Config{
		Inbounds: []InboundSpec{
			{Name: "openai-entry", Type: "openai_chat"},
			{Name: "office-entry", Type: "openai_chat", Labels: map[string]string{"source": "office"}},
		},
	}

	got := cfg.InboundByName("office-entry")
	if got.Name != "office-entry" {
		t.Fatalf("InboundByName() name = %q, want office-entry", got.Name)
	}
	if got.Labels["source"] != "office" {
		t.Fatalf("InboundByName() labels = %#v, want source office", got.Labels)
	}
}

func TestConfigValidateOpenAICompatibleSuccess(t *testing.T) {
	cfg := Config{
		Server:  ServerConfig{Listen: ":8080"},
		Routing: RoutingConfig{DefaultProvider: "openai"},
		Provider: []ProviderSpec{{
			Name:    "openai",
			Type:    "openai_compatible",
			BaseURL: "https://example.com/v1",
			APIKeys: []string{"key-1", "key-2"},
		}},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigValidateOpenAICompatibleSingleAPIKeySuccess(t *testing.T) {
	cfg := Config{
		Server:  ServerConfig{Listen: ":8080"},
		Routing: RoutingConfig{DefaultProvider: "openai"},
		Provider: []ProviderSpec{{
			Name:    "openai",
			Type:    "openai_compatible",
			BaseURL: "https://example.com/v1",
			APIKey:  "test-key",
		}},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigValidateMissingListen(t *testing.T) {
	cfg := Config{
		Routing:  RoutingConfig{DefaultProvider: "mock"},
		Provider: []ProviderSpec{{Name: "mock", Type: "mock"}},
	}

	err := cfg.Validate()
	if err == nil || err.Error() != "server.listen or listeners is required" {
		t.Fatalf("Validate() error = %v, want server.listen or listeners is required", err)
	}
}

func TestConfigValidateMissingDefaultProvider(t *testing.T) {
	cfg := Config{
		Server:   ServerConfig{Listen: ":8080"},
		Provider: []ProviderSpec{{Name: "mock", Type: "mock"}},
	}

	err := cfg.Validate()
	if err == nil || err.Error() != "routing.default_provider or routing.default_outbound is required" {
		t.Fatalf("Validate() error = %v, want routing.default_provider or routing.default_outbound is required", err)
	}
}

func TestConfigValidateEmptyProviders(t *testing.T) {
	cfg := Config{
		Server:  ServerConfig{Listen: ":8080"},
		Routing: RoutingConfig{DefaultProvider: "mock"},
	}

	err := cfg.Validate()
	if err == nil || err.Error() != "at least one outbound is required" {
		t.Fatalf("Validate() error = %v, want at least one outbound is required", err)
	}
}

func TestConfigValidateDefaultProviderNotFound(t *testing.T) {
	cfg := Config{
		Server:   ServerConfig{Listen: ":8080"},
		Routing:  RoutingConfig{DefaultProvider: "missing"},
		Provider: []ProviderSpec{{Name: "mock", Type: "mock"}},
	}

	err := cfg.Validate()
	if err == nil || err.Error() != "routing default target \"missing\" not found in outbounds" {
		t.Fatalf("Validate() error = %v, want missing default provider error", err)
	}
}

func TestConfigValidateFallbackProviderNotFound(t *testing.T) {
	cfg := Config{
		Server: ServerConfig{Listen: ":8080"},
		Routing: RoutingConfig{
			DefaultProvider:   "mock",
			FallbackProviders: []string{"backup"},
		},
		Provider: []ProviderSpec{{Name: "mock", Type: "mock"}},
	}

	err := cfg.Validate()
	if err == nil || err.Error() != "routing fallback target \"backup\" not found in outbounds" {
		t.Fatalf("Validate() error = %v, want unknown fallback provider error", err)
	}
}

func TestConfigValidateOpenAICompatibleMissingBaseURL(t *testing.T) {
	cfg := Config{
		Server:  ServerConfig{Listen: ":8080"},
		Routing: RoutingConfig{DefaultProvider: "openai"},
		Provider: []ProviderSpec{{
			Name:   "openai",
			Type:   "openai_compatible",
			APIKey: "test-key",
		}},
	}

	err := cfg.Validate()
	if err == nil || err.Error() != "outbounds.openai.base_url is required" {
		t.Fatalf("Validate() error = %v, want missing base_url error", err)
	}
}

func TestConfigValidateOpenAICompatibleMissingAPIKey(t *testing.T) {
	cfg := Config{
		Server:  ServerConfig{Listen: ":8080"},
		Routing: RoutingConfig{DefaultProvider: "openai"},
		Provider: []ProviderSpec{{
			Name:    "openai",
			Type:    "openai_compatible",
			BaseURL: "https://example.com/v1",
		}},
	}

	err := cfg.Validate()
	if err == nil || err.Error() != "outbounds.openai.api_key or api_keys is required" {
		t.Fatalf("Validate() error = %v, want missing api_key error", err)
	}
}

func TestConfigValidateListenerInboundNotFound(t *testing.T) {
	cfg := Config{
		Listeners: []ListenerSpec{{Name: "public", Listen: ":8080", Inbound: "missing"}},
		Inbounds:  []InboundSpec{{Name: "openai-entry", Type: "openai_chat"}},
		Routing:   RoutingConfig{DefaultOutbound: "mock"},
		Outbound:  []ProviderSpec{{Name: "mock", Type: "mock"}},
	}

	err := cfg.Validate()
	if err == nil || err.Error() != "listeners.public.inbound \"missing\" not found in inbounds" {
		t.Fatalf("Validate() error = %v, want missing inbound error", err)
	}
}

func TestLoadSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("server:\n  listen: \":8080\"\nrouting:\n  default_provider: \"mock\"\n  fallback_providers:\n    - \"backup\"\n  model_providers:\n    default: \"mock\"\nproviders:\n  - name: \"mock\"\n    type: \"mock\"\n  - name: \"backup\"\n    type: \"mock\"\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Listen != ":8080" {
		t.Fatalf("Server.Listen = %q, want :8080", cfg.Server.Listen)
	}
	if cfg.Routing.DefaultProvider != "mock" {
		t.Fatalf("Routing.DefaultProvider = %q, want mock", cfg.Routing.DefaultProvider)
	}
	if len(cfg.Routing.FallbackProviders) != 1 || cfg.Routing.FallbackProviders[0] != "backup" {
		t.Fatalf("Routing.FallbackProviders = %#v, want single backup fallback", cfg.Routing.FallbackProviders)
	}
	if got := cfg.Routing.ModelProviders["default"]; got != "mock" {
		t.Fatalf("Routing.ModelProviders[default] = %q, want mock", got)
	}
	if len(cfg.Provider) != 2 {
		t.Fatalf("len(Provider) = %d, want 2", len(cfg.Provider))
	}
}

func TestLoadOutboundConfigSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("listeners:\n  - name: \"public-http\"\n    listen: \":8080\"\n    inbound: \"openai-entry\"\n  - name: \"private-http\"\n    listen: \":8081\"\n    inbound: \"openai-entry\"\ninbounds:\n  - name: \"openai-entry\"\n    type: \"openai_chat\"\n    labels:\n      source: \"claude-code\"\nrouting:\n  default_outbound: \"openai\"\noutbounds:\n  - name: \"openai\"\n    type: \"openai_compatible\"\n    base_url: \"https://example.com/v1\"\n    api_keys:\n      - \"key-1\"\n      - \"key-2\"\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ListenAddress() != ":8080" {
		t.Fatalf("ListenAddress() = %q, want :8080", cfg.ListenAddress())
	}
	addresses := cfg.ListenAddresses()
	if len(addresses) != 2 || addresses[1] != ":8081" {
		t.Fatalf("ListenAddresses() = %#v, want [:8080 :8081]", addresses)
	}
	if cfg.Routing.DefaultTarget() != "openai" {
		t.Fatalf("Routing.DefaultTarget() = %q, want openai", cfg.Routing.DefaultTarget())
	}
	if len(cfg.OutboundSpecs()) != 1 || cfg.OutboundSpecs()[0].Name != "openai" {
		t.Fatalf("OutboundSpecs() = %#v, want single openai outbound", cfg.OutboundSpecs())
	}
	if cfg.InboundByName("openai-entry").Type != "openai_chat" {
		t.Fatalf("InboundByName(openai-entry).Type = %q, want openai_chat", cfg.InboundByName("openai-entry").Type)
	}
}

func TestLoadOpenAICompatibleSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("server:\n  listen: \":8080\"\nrouting:\n  default_provider: \"openai\"\nproviders:\n  - name: \"openai\"\n    type: \"openai_compatible\"\n    base_url: \"https://example.com/v1\"\n    api_keys:\n      - \"key-1\"\n      - \"key-2\"\n    models:\n      - \"gpt-4o-mini\"\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Provider[0].BaseURL != "https://example.com/v1" {
		t.Fatalf("Provider[0].BaseURL = %q, want https://example.com/v1", cfg.Provider[0].BaseURL)
	}
	if len(cfg.Provider[0].APIKeys) != 2 || cfg.Provider[0].APIKeys[0] != "key-1" || cfg.Provider[0].APIKeys[1] != "key-2" {
		t.Fatalf("Provider[0].APIKeys = %#v, want two keys", cfg.Provider[0].APIKeys)
	}
}

func TestLoadFileNotFound(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err == nil {
		t.Fatal("Load() error = nil, want error")
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("server: ["), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want error")
	}
}

func TestLoadInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("server:\n  listen: \"\"\nrouting:\n  default_provider: \"mock\"\nproviders:\n  - name: \"mock\"\n    type: \"mock\"\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := Load(path)
	if err == nil || err.Error() != "server.listen or listeners is required" {
		t.Fatalf("Load() error = %v, want server.listen or listeners is required", err)
	}
}
