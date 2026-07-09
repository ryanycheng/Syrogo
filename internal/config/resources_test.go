package config

import "testing"

func TestRedactedConfigRedactsSecrets(t *testing.T) {
	cfg := minimalResourceConfig()
	cfg.Admin.Token = "admin-token"
	cfg.Accounting.AdminToken = "accounting-token"
	cfg.Inbounds[0].Clients[0].Token = "client-token"
	cfg.Outbounds[0].AuthToken = "provider-token"

	redacted := RedactedConfig(cfg)
	if redacted.Admin.Token != RedactedValue {
		t.Fatalf("Admin.Token = %q, want redacted", redacted.Admin.Token)
	}
	if redacted.Accounting.AdminToken != RedactedValue {
		t.Fatalf("Accounting.AdminToken = %q, want redacted", redacted.Accounting.AdminToken)
	}
	if redacted.Inbounds[0].Clients[0].Token != RedactedValue {
		t.Fatalf("Client.Token = %q, want redacted", redacted.Inbounds[0].Clients[0].Token)
	}
	if redacted.Outbounds[0].AuthToken != RedactedValue {
		t.Fatalf("Outbound.AuthToken = %q, want redacted", redacted.Outbounds[0].AuthToken)
	}
}

func TestUpsertOutboundAddsAndReplaces(t *testing.T) {
	cfg := minimalResourceConfig()
	cfg = UpsertOutbound(cfg, OutboundSpec{Name: "backup", Protocol: "mock", Tag: "backup"})
	if len(cfg.Outbounds) != 2 || cfg.Outbounds[1].Name != "backup" {
		t.Fatalf("Outbounds = %#v, want backup appended", cfg.Outbounds)
	}
	cfg = UpsertOutbound(cfg, OutboundSpec{Name: "backup", Protocol: "mock", Tag: "backup-2"})
	if len(cfg.Outbounds) != 2 || cfg.Outbounds[1].Tag != "backup-2" {
		t.Fatalf("Outbounds = %#v, want backup replaced", cfg.Outbounds)
	}
}

func TestDeleteOutbound(t *testing.T) {
	cfg := minimalResourceConfig()
	cfg = UpsertOutbound(cfg, OutboundSpec{Name: "backup", Protocol: "mock", Tag: "backup"})
	cfg = DeleteOutbound(cfg, "backup")
	if len(cfg.Outbounds) != 1 || cfg.Outbounds[0].Name != "mock" {
		t.Fatalf("Outbounds = %#v, want only mock", cfg.Outbounds)
	}
}

func TestUpsertClientAddsAndReplaces(t *testing.T) {
	cfg := minimalResourceConfig()
	next, err := UpsertClient(cfg, "openai-entry", ClientSpec{Name: "mobile", Token: "mobile-token", Tag: "mobile"})
	if err != nil {
		t.Fatalf("UpsertClient() error = %v", err)
	}
	if len(next.Inbounds[0].Clients) != 2 {
		t.Fatalf("Clients = %#v, want appended mobile", next.Inbounds[0].Clients)
	}
	next, err = UpsertClient(next, "openai-entry", ClientSpec{Name: "mobile", Token: "new-token", Tag: "mobile-2"})
	if err != nil {
		t.Fatalf("UpsertClient() replace error = %v", err)
	}
	if next.Inbounds[0].Clients[1].Token != "new-token" || next.Inbounds[0].Clients[1].Tag != "mobile-2" {
		t.Fatalf("Clients = %#v, want mobile replaced", next.Inbounds[0].Clients)
	}
}

func TestDeleteClient(t *testing.T) {
	cfg := minimalResourceConfig()
	next, err := UpsertClient(cfg, "openai-entry", ClientSpec{Name: "mobile", Token: "mobile-token", Tag: "mobile"})
	if err != nil {
		t.Fatalf("UpsertClient() error = %v", err)
	}
	next, err = DeleteClient(next, "openai-entry", "mobile")
	if err != nil {
		t.Fatalf("DeleteClient() error = %v", err)
	}
	if len(next.Inbounds[0].Clients) != 1 || next.Inbounds[0].Clients[0].Name != "office-key" {
		t.Fatalf("Clients = %#v, want only office-key", next.Inbounds[0].Clients)
	}
}

func TestUpsertRouteAddsAndReplaces(t *testing.T) {
	cfg := minimalResourceConfig()
	cfg = UpsertRoute(cfg, RoutingRule{Name: "mobile-route", FromTags: []string{"mobile"}, ToTags: []string{"mock-primary"}, Strategy: "failover"})
	if len(cfg.Routing.Rules) != 2 || cfg.Routing.Rules[1].Name != "mobile-route" {
		t.Fatalf("Rules = %#v, want mobile-route appended", cfg.Routing.Rules)
	}
	cfg = UpsertRoute(cfg, RoutingRule{Name: "mobile-route", FromTags: []string{"mobile"}, ToTags: []string{"mock-primary"}, Strategy: "round_robin"})
	if len(cfg.Routing.Rules) != 2 || cfg.Routing.Rules[1].Strategy != "round_robin" {
		t.Fatalf("Rules = %#v, want mobile-route replaced", cfg.Routing.Rules)
	}
}

func TestDeleteRoute(t *testing.T) {
	cfg := minimalResourceConfig()
	cfg = UpsertRoute(cfg, RoutingRule{Name: "mobile-route", FromTags: []string{"mobile"}, ToTags: []string{"mock-primary"}, Strategy: "failover"})
	cfg = DeleteRoute(cfg, "mobile-route")
	if len(cfg.Routing.Rules) != 1 || cfg.Routing.Rules[0].Name != "office-route" {
		t.Fatalf("Rules = %#v, want only office-route", cfg.Routing.Rules)
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
		Server: ServerConfig{Listen: ":23234"},
		Inbounds: []InboundSpec{{
			Name:     "openai-entry",
			Protocol: "openai_chat",
			Path:     "/v1/chat/completions",
			Clients:  []ClientSpec{{Name: "office-key", Token: "client-token", Tag: "office"}},
		}},
		Routing:   RoutingConfig{Rules: []RoutingRule{{Name: "office-route", FromTags: []string{"office"}, ToTags: []string{"mock-primary"}, Strategy: "failover"}}},
		Outbounds: []OutboundSpec{{Name: "mock", Protocol: "mock", Tag: "mock-primary"}},
	}
}
