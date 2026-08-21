package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type oauthRoundTripper func(*http.Request) (*http.Response, error)

func (f oauthRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestStartCodexDeviceFlow(t *testing.T) {
	var requests int
	manager := NewManager(mustTestStore(t), &http.Client{Transport: oauthRoundTripper(func(req *http.Request) (*http.Response, error) {
		requests++
		if req.Method != http.MethodPost || req.URL.String() != codexDeviceCodeURL {
			t.Fatalf("request = %s %s", req.Method, req.URL)
		}
		var body map[string]string
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["client_id"] != codexClientID {
			t.Fatalf("client_id = %q", body["client_id"])
		}
		return jsonResponse(http.StatusOK, `{"device_auth_id":"device-secret","user_code":"ABCD-EFGH","interval":5,"expires_in":600}`), nil
	})})

	flow, err := manager.StartCodexDeviceFlow("codex-main")
	if err != nil {
		t.Fatalf("StartCodexDeviceFlow() error = %v", err)
	}
	if requests != 1 || flow.ID == "" || flow.Provider != ProviderCodex || flow.CredentialID != "codex-main" || flow.VerificationURL != codexVerificationURL || flow.UserCode != "ABCD-EFGH" || flow.Interval != 5 {
		t.Fatalf("flow = %#v, requests = %d", flow, requests)
	}
	encoded, err := json.Marshal(flow)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "device-secret") {
		t.Fatal("public flow exposed device authorization ID")
	}
}

func TestPollCodexDeviceFlowPending(t *testing.T) {
	manager := NewManager(mustTestStore(t), &http.Client{Transport: oauthRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() == codexDeviceCodeURL {
			return jsonResponse(http.StatusOK, `{"device_auth_id":"device-secret","user_code":"ABCD-EFGH","expires_in":600}`), nil
		}
		if req.URL.String() != codexDeviceTokenURL {
			t.Fatalf("unexpected URL %s", req.URL)
		}
		var body map[string]string
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["device_auth_id"] != "device-secret" || body["user_code"] != "ABCD-EFGH" || body["client_id"] != codexClientID {
			t.Fatalf("poll body = %#v", body)
		}
		return jsonResponse(http.StatusForbidden, `{"error":"authorization_pending"}`), nil
	})})
	flow, err := manager.StartCodexDeviceFlow("codex-main")
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.PollCodexDeviceFlow(context.Background(), flow.ID)
	if !errors.Is(err, ErrCodexDeviceAuthorizationPending) {
		t.Fatalf("PollCodexDeviceFlow() error = %v, want pending", err)
	}
}

func TestPollCodexDeviceFlowExchangesAndConsumesFlow(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	manager := NewManager(mustTestStore(t), &http.Client{Transport: oauthRoundTripper(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		calls = append(calls, req.URL.String())
		mu.Unlock()
		switch req.URL.String() {
		case codexDeviceCodeURL:
			return jsonResponse(http.StatusOK, `{"device_auth_id":"device-secret","user_code":"ABCD-EFGH","expires_in":600}`), nil
		case codexDeviceTokenURL:
			return jsonResponse(http.StatusOK, `{"authorization_code":"authorization-secret","code_verifier":"verifier-secret"}`), nil
		case codexTokenURL:
			var body map[string]string
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["client_id"] != codexClientID || body["code"] != "authorization-secret" || body["code_verifier"] != "verifier-secret" || body["grant_type"] != "authorization_code" || body["redirect_uri"] != codexRedirectURI {
				t.Fatalf("exchange body = %#v", body)
			}
			return jsonResponse(http.StatusOK, `{"access_token":"access-secret","refresh_token":"refresh-secret","expires_in":3600,"scope":"openid"}`), nil
		default:
			t.Fatalf("unexpected URL %s", req.URL)
			return nil, nil
		}
	})})
	flow, err := manager.StartCodexDeviceFlow("codex-main")
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := manager.PollCodexDeviceFlow(context.Background(), flow.ID)
	if err != nil {
		t.Fatalf("PollCodexDeviceFlow() error = %v", err)
	}
	if metadata.ID != "codex-main" || metadata.Provider != ProviderCodex || metadata.Scope != "openid" {
		t.Fatalf("metadata = %#v", metadata)
	}
	credential, err := manager.Credential("codex-main", ProviderCodex)
	if err != nil || credential.AccessToken != "access-secret" || credential.RefreshToken != "refresh-secret" {
		t.Fatalf("Credential() = %#v, %v", credential, err)
	}
	if _, err := manager.PollCodexDeviceFlow(context.Background(), flow.ID); err == nil || errors.Is(err, ErrCodexDeviceAuthorizationPending) {
		t.Fatalf("second PollCodexDeviceFlow() error = %v, want consumed flow error", err)
	}
	if len(calls) != 3 {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestPollCodexDeviceFlowExpires(t *testing.T) {
	var calls int
	manager := NewManager(mustTestStore(t), &http.Client{Transport: oauthRoundTripper(func(req *http.Request) (*http.Response, error) {
		calls++
		return jsonResponse(http.StatusOK, `{"device_auth_id":"device-secret","user_code":"ABCD-EFGH","expires_in":1}`), nil
	})})
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }
	flow, err := manager.StartCodexDeviceFlow("codex-main")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	_, err = manager.PollCodexDeviceFlow(context.Background(), flow.ID)
	if err == nil || !strings.Contains(err.Error(), "not found or expired") {
		t.Fatalf("PollCodexDeviceFlow() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("HTTP calls = %d, want only start request", calls)
	}
}

func mustTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return store
}
