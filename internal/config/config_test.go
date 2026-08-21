package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func validConfig() Config {
	return Config{
		Clients:   []ClientSpec{{Name: "office-key", Token: "client-token"}},
		Listeners: []ListenerSpec{{Name: "public", Listen: ":8080", Inbounds: []string{"openai-entry"}}},
		Inbounds: []InboundSpec{{
			Name:     "openai-entry",
			Protocol: "openai_chat",
			Path:     "/v1/chat/completions",
			Clients:  []ClientBindingSpec{{Ref: "office-key", Tag: "office"}},
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

func TestParseBytesValidatesYAMLConfig(t *testing.T) {
	cfg, err := ParseBytes([]byte(`
listeners:
  - name: public
    listen: ":8080"
    inbounds: [openai-entry]
inbounds:
  - name: openai-entry
    protocol: openai_chat
    path: /v1/chat/completions
    clients:
      - name: office-key
        token: client-token
        tag: office
outbounds:
  - name: mock
    protocol: mock
    tag: mock-tag
routing:
  rules:
    - name: office-route
      from_tags: [office]
      to_tags: [mock-tag]
      strategy: failover
`))
	if err != nil {
		t.Fatalf("ParseBytes() error = %v", err)
	}
	if cfg.Inbounds[0].Name != "openai-entry" {
		t.Fatalf("ParseBytes() inbound = %#v, want openai-entry", cfg.Inbounds[0])
	}
	if !cfg.Sessions.Snapshot.EnabledEffective() {
		t.Fatal("sessions.snapshot effective enabled = false, want true")
	}
	if cfg.Sessions.Snapshot.Dir != "./data/sessions" {
		t.Fatalf("sessions.snapshot.dir = %q, want ./data/sessions", cfg.Sessions.Snapshot.Dir)
	}
	if cfg.Sessions.Snapshot.FlushInterval != DurationValue("5s") {
		t.Fatalf("sessions.snapshot.flush_interval = %q, want 5s", cfg.Sessions.Snapshot.FlushInterval)
	}
	if cfg.OAuth.Dir != "./data/oauth" {
		t.Fatalf("oauth.dir = %q, want ./data/oauth", cfg.OAuth.Dir)
	}
}

func TestParseBytesPreservesDisabledSessionSnapshot(t *testing.T) {
	cfg := validConfig()
	disabled := false
	cfg.Sessions.Snapshot.Enabled = &disabled
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("yaml.Marshal() error = %v", err)
	}
	parsed, err := ParseBytes(data)
	if err != nil {
		t.Fatalf("ParseBytes() error = %v", err)
	}
	if parsed.Sessions.Snapshot.EnabledEffective() {
		t.Fatal("sessions.snapshot effective enabled = true, want false")
	}
}

func TestConfigValidateRejectsInvalidSessionSnapshot(t *testing.T) {
	tests := []struct {
		name     string
		dir      string
		interval DurationValue
	}{
		{name: "dir", dir: "   ", interval: DurationValue("5s")},
		{name: "invalid interval", dir: t.TempDir(), interval: DurationValue("later")},
		{name: "non-positive interval", dir: t.TempDir(), interval: DurationValue("0s")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig()
			cfg.Sessions.Snapshot = SessionSnapshotConfig{Dir: tc.dir, FlushInterval: tc.interval}
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "sessions.snapshot") {
				t.Fatalf("Validate() error = %v, want session snapshot validation error", err)
			}
		})
	}
}

func TestParseBytesMigratesLegacyClientsAndMarshalsCanonical(t *testing.T) {
	cfg, err := ParseBytes([]byte(`
server:
  listen: ":8080"
inbounds:
  - name: openai-entry
    protocol: openai_chat
    path: /v1/chat/completions
    clients:
      - name: office-key
        token: client-token
        tag: office
        quota:
          enabled: true
          windows:
            - name: hourly
              duration: 1h
              max_requests: 10
outbounds:
  - name: mock
    protocol: mock
    tag: mock-tag
routing:
  rules:
    - from_tags: [unused-source]
      to_tags: [mock-tag]
      strategy: failover
`))
	if err != nil {
		t.Fatalf("ParseBytes() error = %v", err)
	}
	if len(cfg.Clients) != 1 || cfg.Clients[0].Name != "office-key" || !cfg.Clients[0].Quota.Enabled {
		t.Fatalf("Clients = %#v", cfg.Clients)
	}
	if got := cfg.Inbounds[0].Clients; len(got) != 1 || got[0] != (ClientBindingSpec{Ref: "office-key", Tag: "office"}) {
		t.Fatalf("bindings = %#v", got)
	}
	encoded, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("yaml.Marshal() error = %v", err)
	}
	text := string(encoded)
	if !strings.Contains(text, "clients:\n    - name: office-key\n      token: client-token") || !strings.Contains(text, "ref: office-key") {
		t.Fatalf("canonical YAML =\n%s", text)
	}
	if strings.Contains(text, "tag: office\n      quota:") || strings.Contains(text, "name: office-key\n        token:") {
		t.Fatalf("canonical YAML retained legacy nesting =\n%s", text)
	}
}

func TestParseBytesAcceptsCanonicalClients(t *testing.T) {
	cfg, err := ParseBytes([]byte(`
server: {listen: ":8080"}
clients:
  - name: office-key
    token: client-token
inbounds:
  - name: openai-entry
    protocol: openai_chat
    path: /v1/chat/completions
    clients:
      - ref: office-key
        tag: office
outbounds: [{name: mock, protocol: mock, tag: mock-tag}]
routing:
  rules: [{from_tags: [office], to_tags: [mock-tag], strategy: failover}]
`))
	if err != nil {
		t.Fatalf("ParseBytes() error = %v", err)
	}
	if cfg.Clients[0].Name != "office-key" || cfg.Inbounds[0].Clients[0].Ref != "office-key" {
		t.Fatalf("Config = %#v", cfg)
	}
}

func TestParseBytesRejectsMixedClientSchemas(t *testing.T) {
	cases := []string{
		`server: {listen: ":8080"}
clients: [{name: top, token: top-token}]
inbounds: [{name: entry, protocol: openai_chat, path: /v1, clients: [{name: legacy, token: legacy-token, tag: legacy}]}]`,
		`server: {listen: ":8080"}
inbounds: [{name: entry, protocol: openai_chat, path: /v1, clients: [{ref: top, tag: canonical}, {name: legacy, token: legacy-token, tag: legacy}]}]`,
	}
	for _, data := range cases {
		if _, err := ParseBytes([]byte(data)); err == nil || err.Error() != "mixed canonical and legacy client configuration is not supported" {
			t.Fatalf("ParseBytes() error = %v", err)
		}
	}
}

func TestParseBytesRejectsInvalidYAML(t *testing.T) {
	_, err := ParseBytes([]byte("listeners: ["))
	if err == nil {
		t.Fatal("ParseBytes() error = nil, want YAML error")
	}
}

func TestParseBytesRejectsInvalidConfig(t *testing.T) {
	_, err := ParseBytes([]byte(`
server:
  listen: ":8080"
outbounds:
  - name: mock
    protocol: mock
    tag: mock-tag
`))
	if err == nil {
		t.Fatal("ParseBytes() error = nil, want validation error")
	}
}

func TestWriteValidatedFileReplacesConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte(`
listeners:
  - name: public
    listen: ":8080"
    inbounds: [openai-entry]
inbounds:
  - name: openai-entry
    protocol: openai_chat
    path: /v1/chat/completions
    clients:
      - name: office-key
        token: client-token
        tag: office
outbounds:
  - name: mock
    protocol: mock
    tag: mock-tag
routing:
  rules:
    - name: office-route
      from_tags: [office]
      to_tags: [mock-tag]
      strategy: failover
`)

	if err := WriteValidatedFile(path, data); err != nil {
		t.Fatalf("WriteValidatedFile() error = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	if string(got) != string(data) {
		t.Fatalf("written config = %q, want %q", got, data)
	}
}

func TestWriteValidatedFileRejectsInvalidConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	old := []byte("old-config")
	if err := os.WriteFile(path, old, 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	err := WriteValidatedFile(path, []byte("server:\n  listen: ':8080'\n"))
	if err == nil {
		t.Fatal("WriteValidatedFile() error = nil, want validation error")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	if string(got) != string(old) {
		t.Fatalf("config changed to %q, want original", got)
	}
}

func TestConfigValidateSuccess(t *testing.T) {
	cfg := validConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigValidateAdminRequiresToken(t *testing.T) {
	cfg := validConfig()
	cfg.Admin.Enabled = true

	err := cfg.Validate()
	if err == nil || err.Error() != "admin.token is required when admin.enabled=true" {
		t.Fatalf("Validate() error = %v, want admin token error", err)
	}
}

func TestConfigValidateAdminRejectsInboundClientToken(t *testing.T) {
	cfg := validConfig()
	cfg.Admin.Enabled = true
	cfg.Admin.Token = "client-token"

	err := cfg.Validate()
	if err == nil || err.Error() != "admin.token duplicates client token used by office-key" {
		t.Fatalf("Validate() error = %v, want duplicate client token error", err)
	}
}

func TestConfigValidateAdminRejectsAccountingAdminToken(t *testing.T) {
	cfg := validConfig()
	cfg.Admin.Enabled = true
	cfg.Admin.Token = "shared-token"
	cfg.Accounting = AccountingConfig{Enabled: true, ExposeHTTP: true, AdminToken: "shared-token"}

	err := cfg.Validate()
	if err == nil || err.Error() != "admin.token must be different from accounting.admin_token" {
		t.Fatalf("Validate() error = %v, want duplicate accounting token error", err)
	}
}

func TestConfigValidateAdminSuccess(t *testing.T) {
	cfg := validConfig()
	cfg.Admin = AdminConfig{Enabled: true, Token: "admin-ui-token", Logs: AdminLogsConfig{Enabled: true, Path: "tmp/dev.log", MaxBytes: 1024}}
	cfg.Accounting = AccountingConfig{Enabled: true, ExposeHTTP: true, AdminToken: "accounting-token"}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestParseBytesAppliesAdminLogDefaults(t *testing.T) {
	cfg, err := ParseBytes([]byte(`
listeners:
  - name: public
    listen: ":8080"
    inbounds: [openai-entry]
inbounds:
  - name: openai-entry
    protocol: openai_chat
    path: /v1/chat/completions
    clients:
      - name: office-key
        token: client-token
        tag: office
outbounds:
  - name: mock
    protocol: mock
    tag: mock-tag
routing:
  rules:
    - name: office-route
      from_tags: [office]
      to_tags: [mock-tag]
      strategy: failover
admin:
  enabled: true
  token: admin-token
  logs:
    enabled: true
`))
	if err != nil {
		t.Fatalf("ParseBytes() error = %v", err)
	}
	logs := cfg.Admin.Logs
	if logs.Path != "tmp/dev.log" || logs.MaxBytes != 65536 {
		t.Fatalf("logs = %#v, want default path and max bytes", logs)
	}
	rotation := logs.Rotation
	if rotation.MaxSizeMB != 100 || rotation.MaxFiles != 20 || rotation.MaxAgeDays != 14 || rotation.MaxTotalSizeMB != 1024 || !rotation.CompressionEnabled() {
		t.Fatalf("rotation = %#v, want defaults", rotation)
	}
}

func TestParseBytesPreservesExplicitDisabledLogCompression(t *testing.T) {
	data := []byte(`
server:
  listen: ":8080"
outbounds:
  - name: mock
    protocol: mock
    tag: mock-tag
routing:
  rules:
    - from_tags: [office]
      to_tags: [mock-tag]
      strategy: failover
admin:
  enabled: true
  token: admin-token
  logs:
    enabled: true
    rotation:
      compress: false
`)
	cfg, err := ParseBytes(data)
	if err != nil {
		t.Fatalf("ParseBytes() error = %v", err)
	}
	if cfg.Admin.Logs.Rotation.Compress == nil || cfg.Admin.Logs.Rotation.CompressionEnabled() {
		t.Fatalf("compression = %#v, want explicit false", cfg.Admin.Logs.Rotation.Compress)
	}
}

func TestConfigValidateAdminLogsRequiresPositiveValues(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*AdminLogsConfig)
		wantErr string
	}{
		{"max_bytes", func(c *AdminLogsConfig) { c.MaxBytes = -1 }, "admin.logs.max_bytes must be greater than 0"},
		{"max_size_mb", func(c *AdminLogsConfig) { c.Rotation.MaxSizeMB = -1 }, "admin.logs.rotation.max_size_mb must be greater than 0"},
		{"max_files", func(c *AdminLogsConfig) { c.Rotation.MaxFiles = -1 }, "admin.logs.rotation.max_files must be greater than 0"},
		{"max_age_days", func(c *AdminLogsConfig) { c.Rotation.MaxAgeDays = -1 }, "admin.logs.rotation.max_age_days must be greater than 0"},
		{"max_total_size_mb", func(c *AdminLogsConfig) { c.Rotation.MaxTotalSizeMB = -1 }, "admin.logs.rotation.max_total_size_mb must be greater than 0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig()
			cfg.Admin = AdminConfig{Enabled: true, Token: "admin-token", Logs: AdminLogsConfig{Enabled: true}}
			tc.mutate(&cfg.Admin.Logs)
			if err := cfg.Validate(); err == nil || err.Error() != tc.wantErr {
				t.Fatalf("Validate() error = %v, want %q", err, tc.wantErr)
			}
		})
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
			Clients:  []ClientBindingSpec{{Ref: "office-key", Tag: "office"}},
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
			{Name: "openai-entry", Protocol: "openai_chat", Path: "/v1/chat/completions", Clients: []ClientBindingSpec{{Ref: "office-key", Tag: "office"}}},
			{Name: "anthropic-entry", Protocol: "anthropic_messages", Path: "/v1/messages", Clients: []ClientBindingSpec{{Ref: "thinking-key", Tag: "thinking"}}},
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

func TestConfigValidateRejectsUnsupportedInboundProtocol(t *testing.T) {
	cfg := validConfig()
	cfg.Inbounds[0].Protocol = "unsupported"

	err := cfg.Validate()
	if err == nil || err.Error() != "inbounds.openai-entry.protocol \"unsupported\" is unsupported" {
		t.Fatalf("Validate() error = %v, want unsupported inbound protocol error", err)
	}
}

func TestConfigValidateSupportsAnthropicInboundProtocol(t *testing.T) {
	cfg := validConfig()
	cfg.Inbounds = append(cfg.Inbounds, InboundSpec{
		Name:     "anthropic-entry",
		Protocol: "anthropic_messages",
		Path:     "/v1/messages",
		Clients:  []ClientBindingSpec{{Ref: "office-key", Tag: "office"}},
	})
	cfg.Listeners[0].Inbounds = append(cfg.Listeners[0].Inbounds, "anthropic-entry")

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigValidateRequiresClientName(t *testing.T) {
	cfg := validConfig()
	cfg.Clients[0].Name = ""

	err := cfg.Validate()
	if err == nil || err.Error() != "clients[0].name is required" {
		t.Fatalf("Validate() error = %v, want missing client name error", err)
	}
}

func TestConfigValidateRequiresClientToken(t *testing.T) {
	cfg := validConfig()
	cfg.Clients[0].Token = ""

	err := cfg.Validate()
	if err == nil || err.Error() != "clients[0].token is required" {
		t.Fatalf("Validate() error = %v, want missing client token error", err)
	}
}

func TestConfigValidateRequiresBindingTag(t *testing.T) {
	cfg := validConfig()
	cfg.Inbounds[0].Clients[0].Tag = ""

	err := cfg.Validate()
	if err == nil || err.Error() != "inbounds.openai-entry.clients[0].tag is required" {
		t.Fatalf("Validate() error = %v, want missing binding tag error", err)
	}
}

func TestConfigValidateRejectsDuplicateClientName(t *testing.T) {
	cfg := validConfig()
	cfg.Clients = append(cfg.Clients, ClientSpec{Name: "office-key", Token: "other-token"})

	err := cfg.Validate()
	if err == nil || err.Error() != "clients[1].name duplicates \"office-key\"" {
		t.Fatalf("Validate() error = %v, want duplicate client name error", err)
	}
}

func TestConfigValidateRejectsDuplicateClientToken(t *testing.T) {
	cfg := validConfig()
	cfg.Clients = append(cfg.Clients, ClientSpec{Name: "other-key", Token: "client-token"})

	err := cfg.Validate()
	if err == nil || err.Error() != "clients[1].token duplicates token used by office-key" {
		t.Fatalf("Validate() error = %v, want duplicate token error", err)
	}
}

func TestConfigValidateBindingContract(t *testing.T) {
	t.Run("missing ref", func(t *testing.T) {
		cfg := validConfig()
		cfg.Inbounds[0].Clients[0].Ref = ""
		if err := cfg.Validate(); err == nil || err.Error() != "inbounds.openai-entry.clients[0].ref is required" {
			t.Fatalf("Validate() error = %v", err)
		}
	})
	t.Run("unknown ref", func(t *testing.T) {
		cfg := validConfig()
		cfg.Inbounds[0].Clients[0].Ref = "missing"
		if err := cfg.Validate(); err == nil || err.Error() != "inbounds.openai-entry.clients[0].ref \"missing\" not found in clients" {
			t.Fatalf("Validate() error = %v", err)
		}
	})
	t.Run("duplicate ref in inbound", func(t *testing.T) {
		cfg := validConfig()
		cfg.Inbounds[0].Clients = append(cfg.Inbounds[0].Clients, ClientBindingSpec{Ref: "office-key", Tag: "shared"})
		if err := cfg.Validate(); err == nil || err.Error() != "inbounds.openai-entry.clients[1].ref duplicates \"office-key\"" {
			t.Fatalf("Validate() error = %v", err)
		}
	})
	t.Run("shared tag and zero bindings", func(t *testing.T) {
		cfg := validConfig()
		cfg.Clients = append(cfg.Clients, ClientSpec{Name: "mobile", Token: "mobile-token"})
		cfg.Inbounds[0].Clients = append(cfg.Inbounds[0].Clients, ClientBindingSpec{Ref: "mobile", Tag: "office"})
		cfg.Inbounds = append(cfg.Inbounds, InboundSpec{Name: "empty-entry", Protocol: "openai_chat", Path: "/empty"})
		cfg.Listeners[0].Inbounds = append(cfg.Listeners[0].Inbounds, "empty-entry")
		if err := cfg.Validate(); err != nil {
			t.Fatalf("Validate() error = %v", err)
		}
	})
}

func TestConfigValidateSupportsClientQuotaWindows(t *testing.T) {
	cfg := validConfig()
	cfg.Clients[0].Quota = ClientQuotaConfig{
		Enabled: true,
		Windows: []QuotaWindowConfig{
			{Name: "hourly", Duration: DurationValue("1h"), MaxRequests: 100},
			{Name: "daily", Duration: DurationValue("24h"), MaxRequests: 1000},
		},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigValidateRequiresClientQuotaWindows(t *testing.T) {
	cfg := validConfig()
	cfg.Clients[0].Quota = ClientQuotaConfig{Enabled: true}

	err := cfg.Validate()
	if err == nil || err.Error() != "clients[0].quota.windows is required when quota is enabled" {
		t.Fatalf("Validate() error = %v, want missing client quota windows error", err)
	}
}

func TestConfigValidateRejectsDuplicateClientQuotaWindowName(t *testing.T) {
	cfg := validConfig()
	cfg.Clients[0].Quota = ClientQuotaConfig{
		Enabled: true,
		Windows: []QuotaWindowConfig{
			{Name: "daily", Duration: DurationValue("24h"), MaxRequests: 100},
			{Name: "daily", Duration: DurationValue("168h"), MaxRequests: 1000},
		},
	}

	err := cfg.Validate()
	if err == nil || err.Error() != "clients[0].quota.windows[1].name duplicates \"daily\"" {
		t.Fatalf("Validate() error = %v, want duplicate client quota window name error", err)
	}
}

func TestConfigValidateRequiresPositiveClientQuotaDuration(t *testing.T) {
	cfg := validConfig()
	cfg.Clients[0].Quota = ClientQuotaConfig{
		Enabled: true,
		Windows: []QuotaWindowConfig{{Name: "daily", Duration: DurationValue("0s"), MaxRequests: 100}},
	}

	err := cfg.Validate()
	if err == nil || err.Error() != "clients[0].quota.windows[0].duration must be a positive duration" {
		t.Fatalf("Validate() error = %v, want invalid client quota duration error", err)
	}
}

func TestConfigValidateRequiresPositiveClientQuotaLimit(t *testing.T) {
	cfg := validConfig()
	cfg.Clients[0].Quota = ClientQuotaConfig{
		Enabled: true,
		Windows: []QuotaWindowConfig{{Name: "daily", Duration: DurationValue("24h"), MaxRequests: 0}},
	}

	err := cfg.Validate()
	if err == nil || err.Error() != "clients[0].quota.windows[0].max_requests must be greater than 0" {
		t.Fatalf("Validate() error = %v, want invalid client quota limit error", err)
	}
}

func TestConfigValidateClientTypedQuotaWindows(t *testing.T) {
	cfg := validConfig()
	cfg.Clients[0].Quota = ClientQuotaConfig{Enabled: true, Windows: []QuotaWindowConfig{
		{Name: "requests", Type: "requests", Duration: "1h", MaxRequests: 10},
		{Name: "tokens", Type: "tokens", Duration: "1h", MaxTokens: 1000},
		{Name: "cost", Type: "cost", Duration: "1h", MaxCostUSD: "1.234567"},
	}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestParseBytesCanonicalizesLegacyClientQuotaType(t *testing.T) {
	cfg := validConfig()
	cfg.Clients[0].Quota = ClientQuotaConfig{Enabled: true, Windows: []QuotaWindowConfig{{Name: "hourly", Duration: "1h", MaxRequests: 10}}}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Clients[0].Quota.Windows[0].Type != "requests" {
		t.Fatalf("type = %q, want requests", parsed.Clients[0].Quota.Windows[0].Type)
	}
	canonical, err := yaml.Marshal(parsed)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(canonical), "type: requests") {
		t.Fatalf("canonical config = %s", canonical)
	}
}

func TestConfigValidateRejectsInvalidTypedClientQuotaFields(t *testing.T) {
	tests := []QuotaWindowConfig{
		{Name: "w", Type: "requests", Duration: "1h", MaxRequests: 1, MaxTokens: 1},
		{Name: "w", Type: "tokens", Duration: "1h"},
		{Name: "w", Type: "cost", Duration: "1h", MaxCostUSD: "0.0000001"},
		{Name: "w", Type: "cost", Duration: "1h", MaxCostUSD: "0"},
	}
	for _, window := range tests {
		cfg := validConfig()
		cfg.Clients[0].Quota = ClientQuotaConfig{Enabled: true, Windows: []QuotaWindowConfig{window}}
		if err := cfg.Validate(); err == nil {
			t.Fatalf("Validate(%#v) error = nil", window)
		}
	}
}

func TestConfigValidateSupportsGovernanceQuotaSnapshot(t *testing.T) {
	cfg := validConfig()
	cfg.Governance.Quota.Snapshot = GovernanceQuotaSnapshotConfig{Enabled: true, Dir: "./tmp/quota", FlushInterval: DurationValue("2s")}
	cfg.Governance.Quota.Events = GovernanceQuotaEventsConfig{Enabled: true, MaxEntries: 100}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigValidateRequiresGovernanceQuotaSnapshotDir(t *testing.T) {
	cfg := validConfig()
	cfg.Governance.Quota.Snapshot = GovernanceQuotaSnapshotConfig{Enabled: true, FlushInterval: DurationValue("2s")}

	err := cfg.Validate()
	if err == nil || err.Error() != "governance.quota.snapshot.dir is required when governance.quota.snapshot.enabled=true" {
		t.Fatalf("Validate() error = %v, want missing governance quota snapshot dir error", err)
	}
}

func TestConfigValidateRequiresGovernanceQuotaSnapshotFlushInterval(t *testing.T) {
	cfg := validConfig()
	cfg.Governance.Quota.Snapshot = GovernanceQuotaSnapshotConfig{Enabled: true, Dir: "./tmp/quota"}

	err := cfg.Validate()
	if err == nil || err.Error() != "governance.quota.snapshot.flush_interval must be a positive duration" {
		t.Fatalf("Validate() error = %v, want invalid governance quota snapshot flush interval error", err)
	}
}

func TestConfigValidateRequiresPositiveGovernanceQuotaEventLimit(t *testing.T) {
	cfg := validConfig()
	cfg.Governance.Quota.Events = GovernanceQuotaEventsConfig{Enabled: true}

	err := cfg.Validate()
	if err == nil || err.Error() != "governance.quota.events.max_entries must be greater than 0" {
		t.Fatalf("Validate() error = %v, want invalid governance quota events max entries error", err)
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

func TestConfigValidateRejectsUnsupportedOutboundProtocol(t *testing.T) {
	cfg := validConfig()
	cfg.Outbounds[0] = OutboundSpec{Name: "unknown", Protocol: "unsupported", Tag: "unknown-tag"}
	cfg.Routing.Rules[0].ToTags = []string{"unknown-tag"}

	err := cfg.Validate()
	if err == nil || err.Error() != "outbounds.unknown.protocol \"unsupported\" is unsupported" {
		t.Fatalf("Validate() error = %v, want unsupported outbound protocol error", err)
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

func TestConfigValidateSupportsOpenAIResponsesOutbound(t *testing.T) {
	cfg := validConfig()
	cfg.Outbounds[0] = OutboundSpec{Name: "responses", Protocol: "openai_responses", Tag: "responses-tag", Endpoint: "https://example.com/v1", AuthToken: "key-1"}
	cfg.Routing.Rules[0].ToTags = []string{"responses-tag"}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigValidateRejectsCapabilitiesOnNonResponsesOutbound(t *testing.T) {
	cfg := validConfig()
	disabled := false
	cfg.Outbounds[0] = OutboundSpec{
		Name:      "openai",
		Protocol:  "openai_chat",
		Tag:       "openai-tag",
		Endpoint:  "https://example.com/v1",
		AuthToken: "key-1",
		Capabilities: OutboundCapabilities{
			ResponsesBuiltinTools: &disabled,
		},
	}
	cfg.Routing.Rules[0].ToTags = []string{"openai-tag"}

	err := cfg.Validate()
	if err == nil || err.Error() != "outbounds.openai.responses capabilities are only supported for openai_responses" {
		t.Fatalf("Validate() error = %v, want unsupported capabilities error", err)
	}
}

func TestConfigLoadParsesOutboundCapabilities(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte(`listeners:
  - name: "public"
    listen: ":8080"
    inbounds:
      - "openai-entry"
inbounds:
  - name: "openai-entry"
    protocol: "openai_chat"
    path: "/v1/chat/completions"
    clients:
      - name: "office-key"
        token: "client-token"
        tag: "office"
routing:
  rules:
    - name: "office-route"
      from_tags:
        - "office"
      to_tags:
        - "responses-tag"
      strategy: "failover"
outbounds:
  - name: "responses"
    protocol: "openai_responses"
    endpoint: "https://example.com/v1"
    auth_token: "key-1"
    tag: "responses-tag"
    capabilities:
      responses_previous_response_id: false
      responses_builtin_tools: true
      responses_tool_result_status_error: false
      responses_assistant_history_native: true
`)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	caps := cfg.Outbounds[0].Capabilities
	if caps.ResponsesPreviousResponseID == nil || *caps.ResponsesPreviousResponseID {
		t.Fatalf("ResponsesPreviousResponseID = %#v, want false", caps.ResponsesPreviousResponseID)
	}
	if caps.ResponsesBuiltinTools == nil || !*caps.ResponsesBuiltinTools {
		t.Fatalf("ResponsesBuiltinTools = %#v, want true", caps.ResponsesBuiltinTools)
	}
	if caps.ResponsesToolResultStatusError == nil || *caps.ResponsesToolResultStatusError {
		t.Fatalf("ResponsesToolResultStatusError = %#v, want false", caps.ResponsesToolResultStatusError)
	}
	if caps.ResponsesAssistantHistoryNative == nil || !*caps.ResponsesAssistantHistoryNative {
		t.Fatalf("ResponsesAssistantHistoryNative = %#v, want true", caps.ResponsesAssistantHistoryNative)
	}
}

func TestConfigValidateSupportsAnthropicMessagesOutbound(t *testing.T) {
	cfg := validConfig()
	cfg.Outbounds[0] = OutboundSpec{Name: "anthropic", Protocol: "anthropic_messages", Tag: "anthropic-tag", Endpoint: "https://example.com/v1", AuthToken: "key-1"}
	cfg.Routing.Rules[0].ToTags = []string{"anthropic-tag"}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigValidateAnthropicMessagesRequiresEndpointAndAuthToken(t *testing.T) {
	cfg := validConfig()
	cfg.Outbounds[0] = OutboundSpec{Name: "anthropic", Protocol: "anthropic_messages", Tag: "anthropic-tag"}
	cfg.Routing.Rules[0].ToTags = []string{"anthropic-tag"}

	err := cfg.Validate()
	if err == nil || err.Error() != "outbounds.anthropic.endpoint is required" {
		t.Fatalf("Validate() error = %v, want missing endpoint error", err)
	}

	cfg.Outbounds[0].Endpoint = "https://example.com/v1"
	err = cfg.Validate()
	if err == nil || err.Error() != "outbounds.anthropic.auth_token is required" {
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

func TestConfigValidateSupportsRoutingModelMap(t *testing.T) {
	cfg := validConfig()
	cfg.Routing.Rules[0].ModelMap = map[string]string{"gpt-4": "gpt-4o-mini"}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigValidateRejectsRoutingModelMapWithTargetModel(t *testing.T) {
	cfg := validConfig()
	cfg.Routing.Rules[0].TargetModel = "gpt-4o-mini"
	cfg.Routing.Rules[0].ModelMap = map[string]string{"gpt-4": "gpt-4o-mini"}

	err := cfg.Validate()
	if err == nil || err.Error() != "routing.rules[0].model_map cannot be used with target_model" {
		t.Fatalf("Validate() error = %v, want model_map target_model conflict error", err)
	}
}

func TestConfigValidateRejectsRoutingModelMapEmptyTarget(t *testing.T) {
	cfg := validConfig()
	cfg.Routing.Rules[0].ModelMap = map[string]string{"gpt-4": ""}

	err := cfg.Validate()
	if err == nil || err.Error() != "routing.rules[0].model_map.gpt-4 must not be empty" {
		t.Fatalf("Validate() error = %v, want empty model_map target error", err)
	}
}

func TestConfigValidateRoutingRuleModelMatch(t *testing.T) {
	t.Run("valid patterns are accepted", func(t *testing.T) {
		cfg := validConfig()
		cfg.Routing.Rules[0].Match = &RoutingRuleMatch{Models: []string{"claude-*", "literal?[]\\", "*"}}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("Validate() error = %v", err)
		}
	})

	tests := []struct {
		name    string
		models  []string
		wantErr string
	}{
		{name: "missing models", wantErr: "routing.rules[0].match.models must contain at least one pattern"},
		{name: "empty pattern", models: []string{""}, wantErr: "routing.rules[0].match.models[0] must not be empty"},
		{name: "leading whitespace", models: []string{" claude-*"}, wantErr: "routing.rules[0].match.models[0] must not have leading or trailing whitespace"},
		{name: "trailing whitespace", models: []string{"claude-* "}, wantErr: "routing.rules[0].match.models[0] must not have leading or trailing whitespace"},
		{name: "control character", models: []string{"claude-\x00model"}, wantErr: "routing.rules[0].match.models[0] must not contain control characters"},
		{name: "duplicate", models: []string{"claude-*", "claude-*"}, wantErr: "routing.rules[0].match.models[1] duplicates pattern \"claude-*\""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig()
			cfg.Routing.Rules[0].Match = &RoutingRuleMatch{Models: tc.models}
			if err := cfg.Validate(); err == nil || err.Error() != tc.wantErr {
				t.Fatalf("Validate() error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestParseBytesRoutingRuleMatchOmittedAndNullAreFallbacks(t *testing.T) {
	for _, matchYAML := range []string{"", "      match: null\n"} {
		data := []byte(`server: {listen: ":8080"}
outbounds: [{name: mock, protocol: mock, tag: mock-tag}]
routing:
  rules:
    - from_tags: [office]
      to_tags: [mock-tag]
      strategy: failover
` + matchYAML)
		cfg, err := ParseBytes(data)
		if err != nil {
			t.Fatalf("ParseBytes() error = %v", err)
		}
		if cfg.Routing.Rules[0].Match != nil {
			t.Fatalf("Match = %#v, want nil fallback", cfg.Routing.Rules[0].Match)
		}
	}
}

func TestParseBytesRoutingRuleMatchModels(t *testing.T) {
	data := []byte(`server: {listen: ":8080"}
outbounds: [{name: mock, protocol: mock, tag: mock-tag}]
routing:
  rules:
    - from_tags: [office]
      match:
        models: ["claude-*", "openai/gpt-?"]
      to_tags: [mock-tag]
      strategy: failover
`)
	cfg, err := ParseBytes(data)
	if err != nil {
		t.Fatalf("ParseBytes() error = %v", err)
	}
	match := cfg.Routing.Rules[0].Match
	if match == nil || len(match.Models) != 2 || match.Models[1] != "openai/gpt-?" {
		t.Fatalf("Match = %#v, want parsed model patterns", match)
	}
}

func TestConfigValidateSupportsWeightedRoundRobin(t *testing.T) {
	cfg := validConfig()
	cfg.Routing.Rules[0].Strategy = "weighted_round_robin"
	cfg.Routing.Rules[0].ToTags = []string{"mock-tag"}
	cfg.Routing.Rules[0].Weights = map[string]int{"mock-tag": 2}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigValidateWeightedRoundRobinRequiresWeights(t *testing.T) {
	cfg := validConfig()
	cfg.Routing.Rules[0].Strategy = "weighted_round_robin"
	cfg.Routing.Rules[0].Weights = nil

	err := cfg.Validate()
	if err == nil || err.Error() != "routing.rules[0].weights is required when strategy=weighted_round_robin" {
		t.Fatalf("Validate() error = %v, want missing weights error", err)
	}
}

func TestConfigValidateWeightedRoundRobinRequiresWeightForEachToTag(t *testing.T) {
	cfg := validConfig()
	cfg.Outbounds = append(cfg.Outbounds, OutboundSpec{Name: "mock-2", Protocol: "mock", Tag: "mock-tag-2"})
	cfg.Routing.Rules[0].Strategy = "weighted_round_robin"
	cfg.Routing.Rules[0].ToTags = []string{"mock-tag", "mock-tag-2"}
	cfg.Routing.Rules[0].Weights = map[string]int{"mock-tag": 2}

	err := cfg.Validate()
	if err == nil || err.Error() != "routing.rules[0].weights.mock-tag-2 is required" {
		t.Fatalf("Validate() error = %v, want missing per-tag weight error", err)
	}
}

func TestConfigValidateWeightedRoundRobinRejectsUnknownWeightTag(t *testing.T) {
	cfg := validConfig()
	cfg.Routing.Rules[0].Strategy = "weighted_round_robin"
	cfg.Routing.Rules[0].Weights = map[string]int{"mock-tag": 2, "other-tag": 1}

	err := cfg.Validate()
	if err == nil || err.Error() != "routing.rules[0].weights.other-tag does not match any to_tags entry" {
		t.Fatalf("Validate() error = %v, want unknown weight tag error", err)
	}
}

func TestConfigValidateWeightedRoundRobinRejectsNonPositiveWeight(t *testing.T) {
	cfg := validConfig()
	cfg.Routing.Rules[0].Strategy = "weighted_round_robin"
	cfg.Routing.Rules[0].Weights = map[string]int{"mock-tag": 0}

	err := cfg.Validate()
	if err == nil || err.Error() != "routing.rules[0].weights.mock-tag must be greater than 0" {
		t.Fatalf("Validate() error = %v, want invalid weight error", err)
	}
}

func TestConfigValidateRejectsWeightsOnNonWeightedRoundRobin(t *testing.T) {
	cfg := validConfig()
	cfg.Routing.Rules[0].Strategy = "failover"
	cfg.Routing.Rules[0].Weights = map[string]int{"mock-tag": 1}

	err := cfg.Validate()
	if err == nil || err.Error() != "routing.rules[0].weights is only supported when strategy=weighted_round_robin" {
		t.Fatalf("Validate() error = %v, want unexpected weights error", err)
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

func TestConfigValidateSupportsAccountingLocalFile(t *testing.T) {
	cfg := validConfig()
	cfg.Accounting = AccountingConfig{
		Enabled:    true,
		Backend:    "local_file",
		ExposeHTTP: true,
		AdminToken: "admin-token",
		LocalFile: AccountingLocalFileConfig{
			Dir:                   t.TempDir(),
			RotateMaxSizeMB:       16,
			RetentionDays:         7,
			SnapshotRetentionDays: 7,
			WriteBufferRecords:    64,
			FlushInterval:         DurationValue("1s"),
			QueueSize:             1024,
		},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigValidateRequiresAccountingLocalFileDir(t *testing.T) {
	cfg := validConfig()
	cfg.Accounting = AccountingConfig{
		Enabled: true,
		Backend: "local_file",
		LocalFile: AccountingLocalFileConfig{
			RotateMaxSizeMB:    16,
			WriteBufferRecords: 64,
			FlushInterval:      DurationValue("1s"),
			QueueSize:          1024,
		},
	}

	err := cfg.Validate()
	if err == nil || err.Error() != "accounting.local_file.dir is required when accounting.backend=local_file" {
		t.Fatalf("Validate() error = %v, want missing local_file dir error", err)
	}
}

func TestConfigValidateRequiresPositiveAccountingLocalFileFlushInterval(t *testing.T) {
	cfg := validConfig()
	cfg.Accounting = AccountingConfig{
		Enabled: true,
		Backend: "local_file",
		LocalFile: AccountingLocalFileConfig{
			Dir:                t.TempDir(),
			RotateMaxSizeMB:    16,
			WriteBufferRecords: 64,
			FlushInterval:      DurationValue("0s"),
			QueueSize:          1024,
		},
	}

	err := cfg.Validate()
	if err == nil || err.Error() != "accounting.local_file.flush_interval must be a positive duration" {
		t.Fatalf("Validate() error = %v, want invalid flush_interval error", err)
	}
}
func TestConfigValidateSupportsOutboundProxyURL(t *testing.T) {
	for _, proxyURL := range []string{
		"http://127.0.0.1:7890",
		"https://proxy.example:443",
		"socks5://127.0.0.1:1080",
	} {
		t.Run(proxyURL, func(t *testing.T) {
			cfg := validConfig()
			cfg.Outbounds[0].Proxy = OutboundProxyConfig{URL: proxyURL}

			if err := cfg.Validate(); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

func TestConfigValidateRejectsInvalidOutboundProxyURL(t *testing.T) {
	cases := []struct {
		name     string
		proxyURL string
		wantErr  string
	}{
		{name: "missing-host", proxyURL: "http://", wantErr: "outbounds.mock.proxy.url host is required"},
		{name: "unsupported-scheme", proxyURL: "ftp://proxy.example:21", wantErr: "outbounds.mock.proxy.url has unsupported scheme \"ftp\""},
		{name: "invalid-url", proxyURL: "http://[::1", wantErr: "outbounds.mock.proxy.url is invalid: parse \"http://[::1\": missing ']' in host"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig()
			cfg.Outbounds[0].Proxy = OutboundProxyConfig{URL: tc.proxyURL}

			err := cfg.Validate()
			if err == nil || err.Error() != tc.wantErr {
				t.Fatalf("Validate() error = %v, want %s", err, tc.wantErr)
			}
		})
	}
}

func TestConfigValidateSupportsOutboundQuotaWindows(t *testing.T) {
	cfg := validConfig()
	cfg.Outbounds[0].Quota = OutboundQuotaConfig{
		Enabled:       true,
		Cooldown:      DurationValue("10m"),
		ProbeInterval: DurationValue("1m"),
		Windows: []OutboundQuotaWindowConfig{
			{Name: "five-hour", Duration: DurationValue("5h"), MaxRequests: 100},
			{Name: "weekly", Duration: DurationValue("168h"), MaxRequests: 1000},
		},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigValidateRequiresOutboundQuotaWindows(t *testing.T) {
	cfg := validConfig()
	cfg.Outbounds[0].Quota = OutboundQuotaConfig{Enabled: true, Cooldown: DurationValue("10m"), ProbeInterval: DurationValue("1m")}

	err := cfg.Validate()
	if err == nil || err.Error() != "outbounds.mock.quota.windows is required when quota is enabled" {
		t.Fatalf("Validate() error = %v, want missing quota windows error", err)
	}
}

func TestConfigValidateRejectsDuplicateOutboundQuotaWindowName(t *testing.T) {
	cfg := validConfig()
	cfg.Outbounds[0].Quota = OutboundQuotaConfig{
		Enabled:       true,
		Cooldown:      DurationValue("10m"),
		ProbeInterval: DurationValue("1m"),
		Windows: []OutboundQuotaWindowConfig{
			{Name: "daily", Duration: DurationValue("24h"), MaxRequests: 100},
			{Name: "daily", Duration: DurationValue("168h"), MaxRequests: 500},
		},
	}

	err := cfg.Validate()
	if err == nil || err.Error() != "outbounds.mock.quota.windows[1].name duplicates \"daily\"" {
		t.Fatalf("Validate() error = %v, want duplicate quota window name error", err)
	}
}

func TestConfigValidateRequiresPositiveOutboundQuotaDuration(t *testing.T) {
	cfg := validConfig()
	cfg.Outbounds[0].Quota = OutboundQuotaConfig{
		Enabled:       true,
		Cooldown:      DurationValue("10m"),
		ProbeInterval: DurationValue("1m"),
		Windows:       []OutboundQuotaWindowConfig{{Name: "daily", Duration: DurationValue("0s"), MaxRequests: 100}},
	}

	err := cfg.Validate()
	if err == nil || err.Error() != "outbounds.mock.quota.windows[0].duration must be a positive duration when reset=rolling" {
		t.Fatalf("Validate() error = %v, want invalid quota duration error", err)
	}
}

func TestConfigValidateRequiresPositiveOutboundQuotaLimit(t *testing.T) {
	cfg := validConfig()
	cfg.Outbounds[0].Quota = OutboundQuotaConfig{
		Enabled:       true,
		Cooldown:      DurationValue("10m"),
		ProbeInterval: DurationValue("1m"),
		Windows:       []OutboundQuotaWindowConfig{{Name: "daily", Duration: DurationValue("24h"), MaxRequests: 0}},
	}

	err := cfg.Validate()
	if err == nil || err.Error() != "outbounds.mock.quota.windows[0] requires max_requests or max_tokens greater than 0" {
		t.Fatalf("Validate() error = %v, want missing quota limits error", err)
	}
}

func TestConfigValidateRequiresPositiveOutboundQuotaCooldown(t *testing.T) {
	cfg := validConfig()
	cfg.Outbounds[0].Quota = OutboundQuotaConfig{
		Enabled:       true,
		Cooldown:      DurationValue("0s"),
		ProbeInterval: DurationValue("1m"),
		Windows:       []OutboundQuotaWindowConfig{{Name: "daily", Duration: DurationValue("24h"), MaxRequests: 100}},
	}

	err := cfg.Validate()
	if err == nil || err.Error() != "outbounds.mock.quota.cooldown must be a positive duration" {
		t.Fatalf("Validate() error = %v, want invalid quota cooldown error", err)
	}
}

func TestConfigValidateRequiresPositiveOutboundQuotaProbeInterval(t *testing.T) {
	cfg := validConfig()
	cfg.Outbounds[0].Quota = OutboundQuotaConfig{
		Enabled:       true,
		Cooldown:      DurationValue("10m"),
		ProbeInterval: DurationValue("0s"),
		Windows:       []OutboundQuotaWindowConfig{{Name: "daily", Duration: DurationValue("24h"), MaxRequests: 100}},
	}

	err := cfg.Validate()
	if err == nil || err.Error() != "outbounds.mock.quota.probe_interval must be a positive duration" {
		t.Fatalf("Validate() error = %v, want invalid quota probe interval error", err)
	}
}

func TestLoadParsesOutboundQuotaWindows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("listeners:\n  - name: \"public\"\n    listen: \":8080\"\n    inbounds:\n      - \"openai-entry\"\ninbounds:\n  - name: \"openai-entry\"\n    protocol: \"openai_chat\"\n    path: \"/v1/chat/completions\"\n    clients:\n      - name: \"office-key\"\n        token: \"client-token\"\n        tag: \"office\"\nrouting:\n  rules:\n    - name: \"office-route\"\n      from_tags:\n        - \"office\"\n      to_tags:\n        - \"mock-tag\"\n      strategy: \"failover\"\noutbounds:\n  - name: \"mock\"\n    protocol: \"mock\"\n    tag: \"mock-tag\"\n    quota:\n      enabled: true\n      cooldown: \"10m\"\n      probe_interval: \"1m\"\n      windows:\n        - name: \"five-hour\"\n          duration: \"5h\"\n          max_requests: 100\n        - name: \"weekly\"\n          duration: \"168h\"\n          max_requests: 1000\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	quota := cfg.Outbounds[0].Quota
	if !quota.Enabled || quota.Cooldown != "10m" || quota.ProbeInterval != "1m" {
		t.Fatalf("quota = %#v, want enabled cooldown/probe", quota)
	}
	if len(quota.Windows) != 2 || quota.Windows[0].Name != "five-hour" || quota.Windows[1].MaxRequests != 1000 {
		t.Fatalf("quota.Windows = %#v, want parsed windows", quota.Windows)
	}
}

func TestParseBytesSupportsOutboundQuotaResetSchemas(t *testing.T) {
	base := `
server:
  listen: ":8080"
outbounds:
  - name: mock
    protocol: mock
    tag: mock-tag
    quota:
      enabled: true
      cooldown: 10m
      probe_interval: 1m
%s
routing:
  rules:
    - from_tags: [office]
      to_tags: [mock-tag]
      strategy: failover
`
	cases := []struct {
		name  string
		quota string
		check func(*testing.T, OutboundQuotaConfig)
	}{
		{
			name: "legacy rolling requests",
			quota: `      windows:
        - name: hourly
          duration: 1h
          max_requests: 100`,
			check: func(t *testing.T, quota OutboundQuotaConfig) {
				window := quota.Windows[0]
				if window.Reset != "rolling" || window.Duration != "1h" || window.MaxRequests != 100 || window.MaxTokens != 0 {
					t.Fatalf("window = %#v, want equivalent rolling request window", window)
				}
			},
		},
		{
			name: "rolling tokens",
			quota: `      windows:
        - name: token-hour
          reset: rolling
          duration: 1h
          max_tokens: 200000`,
		},
		{
			name: "fixed interval",
			quota: `      windows:
        - name: anchored
          reset: fixed
          duration: 6h
          fixed:
            period: interval
            anchor: "2026-01-02T03:04:05+08:00"
          max_requests: 100
          max_tokens: 200000`,
		},
		{
			name: "fixed daily",
			quota: `      windows:
        - name: daily
          reset: fixed
          fixed:
            period: daily
            time: "09:30:15"
            timezone: Asia/Shanghai
          max_requests: 100`,
		},
		{
			name: "fixed weekly and weekly reset all",
			quota: `      windows:
        - name: weekly
          reset: fixed
          fixed:
            period: weekly
            time: "09:30"
            timezone: Europe/London
            weekday: monday
          max_tokens: 1000000
      reset_all:
        enabled: true
        schedule:
          period: weekly
          time: "00:00"
          timezone: UTC
          weekday: sunday`,
		},
		{
			name: "interval reset all",
			quota: `      windows:
        - name: hourly
          duration: 1h
          max_requests: 100
      reset_all:
        enabled: true
        schedule:
          period: interval
          duration: 24h
          anchor: "2026-01-02T00:00:00Z"`,
		},
		{
			name: "disabled empty reset all",
			quota: `      windows:
        - name: hourly
          duration: 1h
          max_requests: 100
      reset_all:
        enabled: false`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ParseBytes([]byte(fmt.Sprintf(base, tc.quota)))
			if err != nil {
				t.Fatalf("ParseBytes() error = %v", err)
			}
			if tc.check != nil {
				tc.check(t, cfg.Outbounds[0].Quota)
			}
		})
	}
}

func TestParseBytesRejectsInvalidOutboundQuotaResetSchemas(t *testing.T) {
	base := `
server:
  listen: ":8080"
outbounds:
  - name: mock
    protocol: mock
    tag: mock-tag
    quota:
      enabled: true
      cooldown: 10m
      probe_interval: 1m
%s
routing:
  rules:
    - from_tags: [office]
      to_tags: [mock-tag]
      strategy: failover
`
	cases := []struct {
		name  string
		quota string
	}{
		{"negative requests", `      windows: [{name: bad, duration: 1h, max_requests: -1}]`},
		{"negative tokens", `      windows: [{name: bad, duration: 1h, max_tokens: -1}]`},
		{"no limits", `      windows: [{name: bad, duration: 1h}]`},
		{"unsupported reset", `      windows: [{name: bad, reset: calendar, duration: 1h, max_requests: 1}]`},
		{"rolling missing duration", `      windows: [{name: bad, reset: rolling, max_requests: 1}]`},
		{"rolling fixed conflict", `      windows:
        - name: bad
          duration: 1h
          fixed: {period: daily, time: "09:00", timezone: UTC}
          max_requests: 1`},
		{"fixed interval missing duration", `      windows:
        - name: bad
          reset: fixed
          fixed: {period: interval, anchor: "2026-01-02T00:00:00Z"}
          max_requests: 1`},
		{"fixed interval anchor missing offset", `      windows:
        - name: bad
          reset: fixed
          duration: 1h
          fixed: {period: interval, anchor: "2026-01-02T00:00:00"}
          max_requests: 1`},
		{"fixed interval wall clock conflict", `      windows:
        - name: bad
          reset: fixed
          duration: 1h
          fixed: {period: interval, anchor: "2026-01-02T00:00:00Z", time: "09:00"}
          max_requests: 1`},
		{"fixed daily duration conflict", `      windows:
        - name: bad
          reset: fixed
          duration: 24h
          fixed: {period: daily, time: "09:00", timezone: UTC}
          max_requests: 1`},
		{"fixed daily invalid time", `      windows:
        - name: bad
          reset: fixed
          fixed: {period: daily, time: "25:00", timezone: UTC}
          max_requests: 1`},
		{"fixed daily invalid timezone", `      windows:
        - name: bad
          reset: fixed
          fixed: {period: daily, time: "09:00", timezone: Mars/Olympus}
          max_requests: 1`},
		{"fixed weekly missing weekday", `      windows:
        - name: bad
          reset: fixed
          fixed: {period: weekly, time: "09:00", timezone: UTC}
          max_requests: 1`},
		{"reset all missing schedule", `      windows: [{name: hourly, duration: 1h, max_requests: 1}]
      reset_all: {enabled: true}`},
		{"reset all interval missing anchor", `      windows: [{name: hourly, duration: 1h, max_requests: 1}]
      reset_all:
        enabled: true
        schedule: {period: interval, duration: 24h}`},
		{"reset all daily interval conflict", `      windows: [{name: hourly, duration: 1h, max_requests: 1}]
      reset_all:
        enabled: true
        schedule: {period: daily, duration: 24h, time: "00:00", timezone: UTC}`},
		{"reset all weekly invalid weekday", `      windows: [{name: hourly, duration: 1h, max_requests: 1}]
      reset_all:
        enabled: true
        schedule: {period: weekly, time: "00:00", timezone: UTC, weekday: someday}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseBytes([]byte(fmt.Sprintf(base, tc.quota))); err == nil {
				t.Fatal("ParseBytes() error = nil, want quota schema validation error")
			}
		})
	}
}

func TestOutboundQuotaContractJSONUsesSnakeCase(t *testing.T) {
	quota := OutboundQuotaConfig{
		Enabled: true,
		Windows: []OutboundQuotaWindowConfig{{
			Name:      "weekly",
			Reset:     "fixed",
			Fixed:     QuotaFixedScheduleConfig{Period: "weekly", Time: "09:00", Timezone: "UTC", Weekday: "monday"},
			MaxTokens: 1000,
		}},
		ResetAll: QuotaResetAllConfig{Enabled: true, Schedule: QuotaResetScheduleConfig{Period: "daily", Time: "00:00", Timezone: "UTC"}},
	}
	data, err := json.Marshal(quota)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	text := string(data)
	for _, key := range []string{`"max_tokens"`, `"max_requests"`, `"reset_all"`, `"timezone"`} {
		if !strings.Contains(text, key) {
			t.Fatalf("JSON = %s, want key %s", text, key)
		}
	}
}

func TestConfigValidateSupportsUsageEstimationForOpenAIChat(t *testing.T) {
	cfg := validConfig()
	cfg.Outbounds[0] = OutboundSpec{
		Name:      "openai",
		Protocol:  "openai_chat",
		Tag:       "openai-tag",
		Endpoint:  "https://example.com/v1",
		AuthToken: "key-1",
		Capabilities: OutboundCapabilities{
			UsageEstimation:     true,
			UsageEstimationMode: "heuristic",
		},
	}
	cfg.Routing.Rules[0].ToTags = []string{"openai-tag"}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigValidateRejectsUsageEstimationModeWithoutEnable(t *testing.T) {
	cfg := validConfig()
	cfg.Outbounds[0] = OutboundSpec{
		Name:      "openai",
		Protocol:  "openai_chat",
		Tag:       "openai-tag",
		Endpoint:  "https://example.com/v1",
		AuthToken: "key-1",
		Capabilities: OutboundCapabilities{
			UsageEstimationMode: "heuristic",
		},
	}
	cfg.Routing.Rules[0].ToTags = []string{"openai-tag"}

	err := cfg.Validate()
	if err == nil || err.Error() != "outbounds.openai.usage_estimation_mode requires usage_estimation=true" {
		t.Fatalf("Validate() error = %v, want usage estimation enable error", err)
	}
}

func TestConfigValidateRejectsUsageEstimationForUnsupportedProtocol(t *testing.T) {
	cfg := validConfig()
	cfg.Outbounds[0] = OutboundSpec{
		Name:      "responses",
		Protocol:  "openai_responses",
		Tag:       "responses-tag",
		Endpoint:  "https://example.com/v1",
		AuthToken: "key-1",
		Capabilities: OutboundCapabilities{
			UsageEstimation:     true,
			UsageEstimationMode: "heuristic",
		},
	}
	cfg.Routing.Rules[0].ToTags = []string{"responses-tag"}

	err := cfg.Validate()
	if err == nil || err.Error() != "outbounds.responses.usage_estimation is only supported for openai_chat and anthropic_messages" {
		t.Fatalf("Validate() error = %v, want usage estimation protocol error", err)
	}
}

func TestConfigValidateRejectsUnsupportedUsageEstimationMode(t *testing.T) {
	cfg := validConfig()
	cfg.Outbounds[0] = OutboundSpec{
		Name:      "anthropic",
		Protocol:  "anthropic_messages",
		Tag:       "anthropic-tag",
		Endpoint:  "https://example.com/v1",
		AuthToken: "key-1",
		Capabilities: OutboundCapabilities{
			UsageEstimation:     true,
			UsageEstimationMode: "exact",
		},
	}
	cfg.Routing.Rules[0].ToTags = []string{"anthropic-tag"}

	err := cfg.Validate()
	if err == nil || err.Error() != "outbounds.anthropic.usage_estimation_mode \"exact\" is unsupported" {
		t.Fatalf("Validate() error = %v, want unsupported mode error", err)
	}
}

func TestConfigValidateRequiresAccountingAdminTokenWhenExposeHTTPEnabled(t *testing.T) {
	cfg := validConfig()
	cfg.Accounting = AccountingConfig{Enabled: true, Backend: "memory", ExposeHTTP: true}

	err := cfg.Validate()
	if err == nil || err.Error() != "accounting.admin_token is required when accounting.expose_http is enabled" {
		t.Fatalf("Validate() error = %v, want missing accounting admin token error", err)
	}
}

func TestConfigValidateRejectsUnsupportedAccountingBackend(t *testing.T) {
	cfg := validConfig()
	cfg.Accounting = AccountingConfig{Enabled: true, Backend: "redis"}

	err := cfg.Validate()
	if err == nil || err.Error() != "accounting.backend \"redis\" is unsupported" {
		t.Fatalf("Validate() error = %v, want unsupported accounting backend error", err)
	}
}

func TestConfigValidateSupportsMemoryAccounting(t *testing.T) {
	cfg := validConfig()
	cfg.Accounting = AccountingConfig{Enabled: true, Backend: "memory", ExposeHTTP: true, AdminToken: "admin-token"}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigValidateOutboundModelsContract(t *testing.T) {
	t.Run("empty is unrestricted", func(t *testing.T) {
		cfg := validConfig()
		if err := cfg.Validate(); err != nil {
			t.Fatalf("Validate() error = %v", err)
		}
	})

	t.Run("mock supports models and case is significant", func(t *testing.T) {
		cfg := validConfig()
		cfg.Outbounds[0].Models = []OutboundModelSpec{
			{Name: "GPT-4", Aliases: []string{"premium"}},
			{Name: "gpt-4", Aliases: []string{"Premium"}},
		}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("Validate() error = %v", err)
		}
	})

	cases := []struct {
		name   string
		models []OutboundModelSpec
	}{
		{name: "empty canonical", models: []OutboundModelSpec{{Name: ""}}},
		{name: "untrimmed canonical", models: []OutboundModelSpec{{Name: " gpt-4"}}},
		{name: "empty alias", models: []OutboundModelSpec{{Name: "gpt-4", Aliases: []string{""}}}},
		{name: "untrimmed alias", models: []OutboundModelSpec{{Name: "gpt-4", Aliases: []string{"fast "}}}},
		{name: "duplicate canonical", models: []OutboundModelSpec{{Name: "gpt-4"}, {Name: "gpt-4"}}},
		{name: "duplicate alias", models: []OutboundModelSpec{{Name: "gpt-4", Aliases: []string{"fast"}}, {Name: "gpt-4o", Aliases: []string{"fast"}}}},
		{name: "alias conflicts canonical", models: []OutboundModelSpec{{Name: "gpt-4", Aliases: []string{"gpt-4o"}}, {Name: "gpt-4o"}}},
		{name: "canonical conflicts alias", models: []OutboundModelSpec{{Name: "gpt-4"}, {Name: "gpt-4o", Aliases: []string{"gpt-4"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig()
			cfg.Outbounds[0].Models = tc.models
			if err := cfg.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want models validation error")
			}
		})
	}
}

func TestOutboundModelsRoundTripUsesSnakeCase(t *testing.T) {
	data := []byte(`
server:
  listen: ":8080"
outbounds:
  - name: mock
    protocol: mock
    tag: mock-tag
    models:
      - name: gpt-4o
        aliases: [latest, fast]
routing:
  rules:
    - from_tags: [office]
      to_tags: [mock-tag]
      strategy: failover
`)
	cfg, err := ParseBytes(data)
	if err != nil {
		t.Fatalf("ParseBytes() error = %v", err)
	}
	if got := cfg.Outbounds[0].Models; len(got) != 1 || got[0].Name != "gpt-4o" || len(got[0].Aliases) != 2 {
		t.Fatalf("models = %#v, want parsed canonical and aliases", got)
	}
	encoded, err := json.Marshal(cfg.Outbounds[0].Models[0])
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if text := string(encoded); !strings.Contains(text, `"name"`) || !strings.Contains(text, `"aliases"`) || strings.Contains(text, `"Name"`) {
		t.Fatalf("JSON = %s, want snake_case contract", text)
	}
}

func TestConsoleBootstrapConfigIsMinimalAndValid(t *testing.T) {
	path := filepath.Join("..", "..", "configs", "config.console-bootstrap.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	cfg, err := ParseBytes(data)
	if err != nil {
		t.Fatalf("ParseBytes() error = %v", err)
	}

	if len(cfg.Listeners) != 1 || cfg.Listeners[0].Listen != "127.0.0.1:23234" {
		t.Fatalf("listeners = %#v, want one loopback listener", cfg.Listeners)
	}
	if !cfg.Admin.Enabled || cfg.Admin.Token != "__SYROGO_CONSOLE_ADMIN_TOKEN__" {
		t.Fatalf("admin = %#v, want enabled with installer placeholder", cfg.Admin)
	}
	if len(cfg.Clients) != 1 || cfg.Clients[0].Token != "__SYROGO_CONSOLE_CLIENT_TOKEN__" {
		t.Fatalf("clients = %#v, want one installer placeholder", cfg.Clients)
	}
	if len(cfg.Inbounds) != 1 || len(cfg.Routing.Rules) != 1 || len(cfg.Outbounds) != 1 || cfg.Outbounds[0].Protocol != "mock" {
		t.Fatalf("bootstrap path is not minimal: inbounds=%#v routing=%#v outbounds=%#v", cfg.Inbounds, cfg.Routing, cfg.Outbounds)
	}
	if strings.Count(string(data), "__SYROGO_CONSOLE_") != 2 {
		t.Fatalf("bootstrap placeholders = %d, want exactly 2", strings.Count(string(data), "__SYROGO_CONSOLE_"))
	}
}

func TestLoadSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("listeners:\n  - name: \"public\"\n    listen: \":8080\"\n    inbounds:\n      - \"openai-entry\"\ninbounds:\n  - name: \"openai-entry\"\n    protocol: \"openai_chat\"\n    path: \"/v1/chat/completions\"\n    clients:\n      - name: \"office-key\"\n        token: \"client-token\"\n        tag: \"office\"\nrouting:\n  rules:\n    - name: \"office-route\"\n      from_tags:\n        - \"office\"\n      to_tags:\n        - \"mock-tag\"\n      strategy: \"failover\"\noutbounds:\n  - name: \"mock\"\n    protocol: \"mock\"\n    tag: \"mock-tag\"\n")
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
