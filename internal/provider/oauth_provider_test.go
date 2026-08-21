package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ryanycheng/Syrogo/internal/config"
	"github.com/ryanycheng/Syrogo/internal/runtime"
)

type testCredentialSource struct{ credential Credential }

func (s testCredentialSource) Credential(context.Context) (Credential, error) {
	return s.credential, nil
}
func (testCredentialSource) Invalidate() {}

func TestAnthropicOAuthUsesBearerAuthorization(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer oauth-access-token" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("x-api-key"); got != "" {
			t.Errorf("x-api-key = %q, want empty", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-test","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()

	provider := NewAnthropicMessagesOAuthCompatible("claude", server.URL, testCredentialSource{credential: Credential{Kind: CredentialKindOAuth, Value: "oauth-access-token"}}, runtimeCapabilities(), server.Client())
	response, err := provider.ChatCompletion(context.Background(), runtime.Request{Model: "claude-test", Messages: []runtime.Message{{Role: runtime.MessageRoleUser, Parts: []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: "hi"}}}}})
	if err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}
	if len(response.Message.Parts) != 1 || response.Message.Parts[0].Text != "ok" {
		t.Fatalf("response = %#v", response)
	}
}

func runtimeCapabilities() config.OutboundCapabilities { return config.OutboundCapabilities{} }
