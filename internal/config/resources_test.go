package config

import "testing"

func TestRedactedConfigRedactsSecrets(t *testing.T) {
	cfg := minimalResourceConfig()
	cfg.Admin.Token = "admin-token"
	cfg.Accounting.AdminToken = "accounting-token"
	cfg.Clients[0].Token = "client-token"
	cfg.Outbounds[0].AuthToken = "provider-token"

	redacted := RedactedConfig(cfg)
	if redacted.Admin.Token != RedactedValue {
		t.Fatalf("Admin.Token = %q, want redacted", redacted.Admin.Token)
	}
	if redacted.Accounting.AdminToken != RedactedValue {
		t.Fatalf("Accounting.AdminToken = %q, want redacted", redacted.Accounting.AdminToken)
	}
	if redacted.Clients[0].Token != RedactedValue {
		t.Fatalf("Client.Token = %q, want redacted", redacted.Clients[0].Token)
	}
	if redacted.Outbounds[0].AuthToken != RedactedValue {
		t.Fatalf("Outbound.AuthToken = %q, want redacted", redacted.Outbounds[0].AuthToken)
	}
	if cfg.Clients[0].Token != "client-token" {
		t.Fatalf("RedactedConfig mutated original token to %q", cfg.Clients[0].Token)
	}
}

func TestClientResources(t *testing.T) {
	cfg := minimalResourceConfig()
	if got, ok := FindClient(cfg, "office-key"); !ok || got.Token != "client-token" {
		t.Fatalf("FindClient() = %#v, %v", got, ok)
	}
	cfg = UpsertClient(cfg, ClientSpec{Name: "mobile", Token: "mobile-token"})
	cfg = UpsertClient(cfg, ClientSpec{Name: "mobile", Token: "new-token"})
	if len(cfg.Clients) != 2 || cfg.Clients[1].Token != "new-token" {
		t.Fatalf("Clients = %#v, want replaced mobile", cfg.Clients)
	}
	cfg = DeleteClient(cfg, "office-key")
	if len(cfg.Clients) != 1 || cfg.Clients[0].Name != "mobile" {
		t.Fatalf("Clients = %#v, want only mobile", cfg.Clients)
	}
	if len(cfg.Inbounds[0].Clients) != 1 || cfg.Inbounds[0].Clients[0].Ref != "office-key" {
		t.Fatalf("DeleteClient cascaded bindings: %#v", cfg.Inbounds[0].Clients)
	}
}

func TestBindingResourcesAndResolution(t *testing.T) {
	cfg := minimalResourceConfig()
	binding, ok, err := FindBinding(cfg, "openai-entry", "office-key")
	if err != nil || !ok || binding.Tag != "office" {
		t.Fatalf("FindBinding() = %#v, %v, %v", binding, ok, err)
	}
	cfg, err = UpsertBinding(cfg, "openai-entry", ClientBindingSpec{Ref: "office-key", Tag: "shared"})
	if err != nil {
		t.Fatalf("UpsertBinding() error = %v", err)
	}
	cfg = UpsertClient(cfg, ClientSpec{Name: "mobile", Token: "mobile-token"})
	cfg, err = UpsertBinding(cfg, "openai-entry", ClientBindingSpec{Ref: "mobile", Tag: "shared"})
	if err != nil {
		t.Fatalf("UpsertBinding() append error = %v", err)
	}
	resolved := ResolveInboundClients(cfg, cfg.Inbounds[0])
	if len(resolved) != 2 || resolved[0].Client.Name != "office-key" || resolved[0].Binding.Tag != "shared" {
		t.Fatalf("ResolveInboundClients() = %#v", resolved)
	}
	bindings := ClientBindings(cfg, "office-key")
	if len(bindings) != 1 || bindings[0].Inbound.Name != "openai-entry" || bindings[0].Binding.Tag != "shared" {
		t.Fatalf("ClientBindings() = %#v", bindings)
	}
	cfg, err = DeleteBinding(cfg, "openai-entry", "mobile")
	if err != nil || len(cfg.Inbounds[0].Clients) != 1 {
		t.Fatalf("DeleteBinding() = %#v, %v", cfg.Inbounds[0].Clients, err)
	}
}

func TestBindingResourcesRejectMissingInbound(t *testing.T) {
	cfg := minimalResourceConfig()
	if _, _, err := FindBinding(cfg, "missing", "office-key"); err == nil {
		t.Fatal("FindBinding() error = nil")
	}
	if _, err := UpsertBinding(cfg, "missing", ClientBindingSpec{Ref: "office-key", Tag: "office"}); err == nil {
		t.Fatal("UpsertBinding() error = nil")
	}
	if _, err := DeleteBinding(cfg, "missing", "office-key"); err == nil {
		t.Fatal("DeleteBinding() error = nil")
	}
}

func TestOutboundAndRouteResources(t *testing.T) {
	cfg := minimalResourceConfig()
	cfg = UpsertOutbound(cfg, OutboundSpec{Name: "backup", Protocol: "mock", Tag: "backup"})
	cfg = UpsertOutbound(cfg, OutboundSpec{Name: "backup", Protocol: "mock", Tag: "backup-2"})
	if len(cfg.Outbounds) != 2 || cfg.Outbounds[1].Tag != "backup-2" {
		t.Fatalf("Outbounds = %#v", cfg.Outbounds)
	}
	cfg = DeleteOutbound(cfg, "backup")
	if len(cfg.Outbounds) != 1 {
		t.Fatalf("Outbounds = %#v", cfg.Outbounds)
	}
	cfg = UpsertRoute(cfg, RoutingRule{Name: "mobile-route", FromTags: []string{"mobile"}, ToTags: []string{"mock-primary"}, Strategy: "failover"})
	cfg = UpsertRoute(cfg, RoutingRule{Name: "mobile-route", FromTags: []string{"mobile"}, ToTags: []string{"mock-primary"}, Strategy: "round_robin"})
	if len(cfg.Routing.Rules) != 2 || cfg.Routing.Rules[1].Strategy != "round_robin" {
		t.Fatalf("Rules = %#v", cfg.Routing.Rules)
	}
	cfg = DeleteRoute(cfg, "mobile-route")
	if len(cfg.Routing.Rules) != 1 {
		t.Fatalf("Rules = %#v", cfg.Routing.Rules)
	}
}

func TestPreserveSecret(t *testing.T) {
	if got := PreserveSecret("", "old"); got != "old" {
		t.Fatalf("PreserveSecret(empty) = %q, want old", got)
	}
	if got := PreserveSecret(RedactedValue, "old"); got != "old" {
		t.Fatalf("PreserveSecret(redacted) = %q, want old", got)
	}
	if got := PreserveSecret("new", "old"); got != "new" {
		t.Fatalf("PreserveSecret(new) = %q, want new", got)
	}
}

func minimalResourceConfig() Config {
	return Config{
		Server:  ServerConfig{Listen: ":23234"},
		Clients: []ClientSpec{{Name: "office-key", Token: "client-token"}},
		Inbounds: []InboundSpec{{
			Name:     "openai-entry",
			Protocol: "openai_chat",
			Path:     "/v1/chat/completions",
			Clients:  []ClientBindingSpec{{Ref: "office-key", Tag: "office"}},
		}},
		Routing:   RoutingConfig{Rules: []RoutingRule{{Name: "office-route", FromTags: []string{"office"}, ToTags: []string{"mock-primary"}, Strategy: "failover"}}},
		Outbounds: []OutboundSpec{{Name: "mock", Protocol: "mock", Tag: "mock-primary"}},
	}
}
