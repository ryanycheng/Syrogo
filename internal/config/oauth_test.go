package config

import (
	"strings"
	"testing"
)

func TestOAuthOutboundValidation(t *testing.T) {
	tests := []struct {
		name      string
		outbound  OutboundSpec
		wantError string
	}{
		{
			name:     "claude consumer oauth",
			outbound: OutboundSpec{Name: "claude", Protocol: "anthropic_messages", Tag: "claude", Auth: OutboundAuthConfig{Type: "claude_consumer_oauth", CredentialRef: "claude-main"}},
		},
		{
			name:      "requires matching protocol",
			outbound:  OutboundSpec{Name: "claude", Protocol: "openai_responses", Tag: "claude", Auth: OutboundAuthConfig{Type: "claude_consumer_oauth", CredentialRef: "claude-main"}},
			wantError: "requires anthropic_messages",
		},
		{
			name:      "requires credential reference",
			outbound:  OutboundSpec{Name: "codex", Protocol: "openai_responses", Tag: "codex", Auth: OutboundAuthConfig{Type: "codex_consumer_oauth"}},
			wantError: "credential_ref is required",
		},
		{
			name:      "rejects endpoint override",
			outbound:  OutboundSpec{Name: "codex", Protocol: "openai_responses", Endpoint: "https://example.invalid", Tag: "codex", Auth: OutboundAuthConfig{Type: "codex_consumer_oauth", CredentialRef: "codex-main"}},
			wantError: "endpoint must be empty",
		},
		{
			name:      "rejects static token",
			outbound:  OutboundSpec{Name: "codex", Protocol: "openai_responses", AuthToken: "secret", Tag: "codex", Auth: OutboundAuthConfig{Type: "codex_consumer_oauth", CredentialRef: "codex-main"}},
			wantError: "cannot be used with auth_token",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig()
			cfg.Outbounds = []OutboundSpec{tc.outbound}
			cfg.Routing.Rules[0].ToTags = []string{tc.outbound.Tag}
			err := cfg.Validate()
			if tc.wantError == "" && err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if tc.wantError != "" && (err == nil || !strings.Contains(err.Error(), tc.wantError)) {
				t.Fatalf("Validate() error = %v, want %q", err, tc.wantError)
			}
		})
	}
}
