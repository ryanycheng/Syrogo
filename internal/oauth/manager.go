package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	claudeAuthorizationURL = "https://claude.ai/oauth/authorize"
	claudeTokenURL         = "https://api.anthropic.com/v1/oauth/token"
	claudeClientID         = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	claudeRedirectURI      = "http://localhost:54545/callback"
	codexClientID          = "app_EMoamEEZ73f0CkXaXp7hrann"
	codexDeviceCodeURL     = "https://auth.openai.com/api/accounts/deviceauth/usercode"
	codexDeviceTokenURL    = "https://auth.openai.com/api/accounts/deviceauth/token"
	codexVerificationURL   = "https://auth.openai.com/codex/device"
	codexTokenURL          = "https://auth.openai.com/oauth/token"
	codexRedirectURI       = "https://auth.openai.com/deviceauth/callback"

	maxOAuthResponseBytes = 1 << 20
	codexDeviceFlowTTL    = 15 * time.Minute
	codexPollInterval     = 5
)

var ErrCodexDeviceAuthorizationPending = errors.New("codex device authorization pending")

type Flow struct {
	ID               string    `json:"flow_id"`
	Provider         Provider  `json:"provider"`
	CredentialID     string    `json:"credential_id"`
	AuthorizationURL string    `json:"authorization_url,omitempty"`
	VerificationURL  string    `json:"verification_url,omitempty"`
	UserCode         string    `json:"user_code,omitempty"`
	Interval         int       `json:"interval,omitempty"`
	ExpiresAt        time.Time `json:"expires_at"`

	state        string
	verifier     string
	deviceAuthID string
}

type Manager struct {
	store      *Store
	httpClient *http.Client
	now        func() time.Time
	mu         sync.Mutex
	flows      map[string]Flow
	refreshMu  sync.Mutex
}

func NewManager(store *Store, httpClient *http.Client) *Manager {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Manager{store: store, httpClient: httpClient, now: time.Now, flows: make(map[string]Flow)}
}

func (m *Manager) StartClaudeFlow(credentialID string) (Flow, error) {
	if err := validateCredentialID(credentialID); err != nil {
		return Flow{}, err
	}
	flowID, err := randomURLValue(24)
	if err != nil {
		return Flow{}, err
	}
	state, err := randomURLValue(24)
	if err != nil {
		return Flow{}, err
	}
	verifier, err := randomURLValue(48)
	if err != nil {
		return Flow{}, err
	}
	challenge := pkceChallenge(verifier)
	values := url.Values{
		"code":                  {"true"},
		"client_id":             {claudeClientID},
		"response_type":         {"code"},
		"redirect_uri":          {claudeRedirectURI},
		"scope":                 {"user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
	}
	flow := Flow{ID: flowID, Provider: ProviderClaude, CredentialID: credentialID, AuthorizationURL: claudeAuthorizationURL + "?" + values.Encode(), ExpiresAt: m.now().Add(10 * time.Minute), state: state, verifier: verifier}
	m.mu.Lock()
	m.cleanupLocked()
	m.flows[flow.ID] = flow
	m.mu.Unlock()
	return publicFlow(flow), nil
}

func (m *Manager) StartCodexDeviceFlow(credentialID string) (Flow, error) {
	if err := validateCredentialID(credentialID); err != nil {
		return Flow{}, err
	}
	flowID, err := randomURLValue(24)
	if err != nil {
		return Flow{}, err
	}
	body, err := json.Marshal(struct {
		ClientID string `json:"client_id"`
	}{ClientID: codexClientID})
	if err != nil {
		return Flow{}, fmt.Errorf("encode Codex device authorization request: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, codexDeviceCodeURL, strings.NewReader(string(body)))
	if err != nil {
		return Flow{}, fmt.Errorf("create Codex device authorization request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	response, err := m.httpClient.Do(req)
	if err != nil {
		return Flow{}, fmt.Errorf("codex device authorization request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxOAuthResponseBytes))
	if err != nil {
		return Flow{}, fmt.Errorf("read Codex device authorization response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return Flow{}, fmt.Errorf("codex device authorization failed with status %d", response.StatusCode)
	}
	var device struct {
		DeviceAuthID string `json:"device_auth_id"`
		UserCode     string `json:"user_code"`
		Interval     int    `json:"interval"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(payload, &device); err != nil {
		return Flow{}, fmt.Errorf("decode Codex device authorization response: %w", err)
	}
	if strings.TrimSpace(device.DeviceAuthID) == "" || strings.TrimSpace(device.UserCode) == "" {
		return Flow{}, fmt.Errorf("codex device authorization response is invalid")
	}
	if device.Interval <= 0 {
		device.Interval = codexPollInterval
	}
	expiresAt := m.now().Add(codexDeviceFlowTTL)
	if device.ExpiresIn > 0 && time.Duration(device.ExpiresIn)*time.Second < codexDeviceFlowTTL {
		expiresAt = m.now().Add(time.Duration(device.ExpiresIn) * time.Second)
	}
	flow := Flow{ID: flowID, Provider: ProviderCodex, CredentialID: credentialID, VerificationURL: codexVerificationURL, UserCode: device.UserCode, Interval: device.Interval, ExpiresAt: expiresAt, deviceAuthID: device.DeviceAuthID}
	m.mu.Lock()
	m.cleanupLocked()
	m.flows[flow.ID] = flow
	m.mu.Unlock()
	return publicFlow(flow), nil
}

func (m *Manager) PollCodexDeviceFlow(ctx context.Context, flowID string) (Metadata, error) {
	m.mu.Lock()
	m.cleanupLocked()
	flow, ok := m.flows[flowID]
	m.mu.Unlock()
	if !ok || flow.Provider != ProviderCodex {
		return Metadata{}, fmt.Errorf("oauth flow not found or expired")
	}

	body, err := json.Marshal(struct {
		ClientID     string `json:"client_id"`
		DeviceAuthID string `json:"device_auth_id"`
		UserCode     string `json:"user_code"`
	}{ClientID: codexClientID, DeviceAuthID: flow.deviceAuthID, UserCode: flow.UserCode})
	if err != nil {
		return Metadata{}, fmt.Errorf("encode Codex device token request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexDeviceTokenURL, strings.NewReader(string(body)))
	if err != nil {
		return Metadata{}, fmt.Errorf("create Codex device token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	response, err := m.httpClient.Do(req)
	if err != nil {
		return Metadata{}, fmt.Errorf("codex device token request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxOAuthResponseBytes))
	if err != nil {
		return Metadata{}, fmt.Errorf("read Codex device token response: %w", err)
	}
	if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusUnauthorized {
		return Metadata{}, ErrCodexDeviceAuthorizationPending
	}
	if response.StatusCode != http.StatusOK {
		return Metadata{}, fmt.Errorf("codex device token request failed with status %d", response.StatusCode)
	}
	var deviceToken struct {
		AuthorizationCode string `json:"authorization_code"`
		CodeVerifier      string `json:"code_verifier"`
	}
	if err := json.Unmarshal(payload, &deviceToken); err != nil {
		return Metadata{}, fmt.Errorf("decode Codex device token response: %w", err)
	}
	if strings.TrimSpace(deviceToken.AuthorizationCode) == "" || strings.TrimSpace(deviceToken.CodeVerifier) == "" {
		return Metadata{}, fmt.Errorf("codex device token response is invalid")
	}

	m.mu.Lock()
	if current, exists := m.flows[flowID]; !exists || current.deviceAuthID != flow.deviceAuthID {
		m.mu.Unlock()
		return Metadata{}, fmt.Errorf("oauth flow not found or expired")
	}
	delete(m.flows, flowID)
	m.mu.Unlock()

	return m.exchangeCodexAuthorizationCode(ctx, flow, deviceToken.AuthorizationCode, deviceToken.CodeVerifier)
}

func (m *Manager) exchangeCodexAuthorizationCode(ctx context.Context, flow Flow, authorizationCode, codeVerifier string) (Metadata, error) {
	body, err := json.Marshal(struct {
		ClientID     string `json:"client_id"`
		Code         string `json:"code"`
		CodeVerifier string `json:"code_verifier"`
		GrantType    string `json:"grant_type"`
		RedirectURI  string `json:"redirect_uri"`
	}{ClientID: codexClientID, Code: authorizationCode, CodeVerifier: codeVerifier, GrantType: "authorization_code", RedirectURI: codexRedirectURI})
	if err != nil {
		return Metadata{}, fmt.Errorf("encode Codex OAuth exchange: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexTokenURL, strings.NewReader(string(body)))
	if err != nil {
		return Metadata{}, fmt.Errorf("create Codex OAuth exchange: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	response, err := m.httpClient.Do(req)
	if err != nil {
		return Metadata{}, fmt.Errorf("Codex OAuth exchange request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxOAuthResponseBytes))
	if err != nil {
		return Metadata{}, fmt.Errorf("read Codex OAuth exchange response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return Metadata{}, fmt.Errorf("Codex OAuth exchange failed with status %d", response.StatusCode)
	}
	var token struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
	}
	if err := json.Unmarshal(payload, &token); err != nil {
		return Metadata{}, fmt.Errorf("decode Codex OAuth exchange response: %w", err)
	}
	if strings.TrimSpace(token.AccessToken) == "" || token.ExpiresIn <= 0 {
		return Metadata{}, fmt.Errorf("Codex OAuth exchange response is invalid")
	}
	credential := Credential{ID: flow.CredentialID, Provider: ProviderCodex, AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, ExpiresAt: m.now().Add(time.Duration(token.ExpiresIn) * time.Second), Scope: token.Scope}
	if err := m.store.Save(credential); err != nil {
		return Metadata{}, err
	}
	return credential.Metadata(), nil
}

func (m *Manager) CompleteClaudeFlow(ctx context.Context, flowID, callback string) (Metadata, error) {
	m.mu.Lock()
	m.cleanupLocked()
	flow, ok := m.flows[flowID]
	if ok {
		delete(m.flows, flowID)
	}
	m.mu.Unlock()
	if !ok || flow.Provider != ProviderClaude {
		return Metadata{}, fmt.Errorf("oauth flow not found or expired")
	}
	parsed, err := url.Parse(strings.TrimSpace(callback))
	if err != nil {
		return Metadata{}, fmt.Errorf("parse OAuth callback: %w", err)
	}
	code := parsed.Query().Get("code")
	state := parsed.Query().Get("state")
	if code == "" || state == "" || state != flow.state {
		return Metadata{}, fmt.Errorf("OAuth callback is invalid")
	}
	body, err := json.Marshal(map[string]string{
		"code": code, "state": state, "grant_type": "authorization_code", "client_id": claudeClientID, "redirect_uri": claudeRedirectURI, "code_verifier": flow.verifier,
	})
	if err != nil {
		return Metadata{}, fmt.Errorf("encode OAuth exchange: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, claudeTokenURL, strings.NewReader(string(body)))
	if err != nil {
		return Metadata{}, fmt.Errorf("create OAuth exchange: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	response, err := m.httpClient.Do(req)
	if err != nil {
		return Metadata{}, fmt.Errorf("OAuth exchange request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return Metadata{}, fmt.Errorf("read OAuth exchange response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return Metadata{}, fmt.Errorf("OAuth exchange failed with status %d", response.StatusCode)
	}
	var token struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
		Account      struct {
			Email string `json:"email_address"`
		} `json:"account"`
	}
	if err := json.Unmarshal(payload, &token); err != nil {
		return Metadata{}, fmt.Errorf("decode OAuth exchange response: %w", err)
	}
	credential := Credential{ID: flow.CredentialID, Provider: ProviderClaude, AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, ExpiresAt: m.now().Add(time.Duration(token.ExpiresIn) * time.Second), Scope: token.Scope, AccountLabel: token.Account.Email}
	if err := m.store.Save(credential); err != nil {
		return Metadata{}, err
	}
	return credential.Metadata(), nil
}

func (m *Manager) List() ([]Metadata, error) { return m.store.List() }
func (m *Manager) Delete(id string) error    { return m.store.Delete(id) }

func (m *Manager) Credential(id string, provider Provider) (Credential, error) {
	credential, err := m.store.Load(id)
	if err != nil {
		return Credential{}, err
	}
	if credential.Provider != provider {
		return Credential{}, fmt.Errorf("oauth credential %q is not a %s credential", id, provider)
	}
	if credential.ExpiresAt.After(m.now().Add(time.Minute)) {
		return credential, nil
	}
	return m.refreshCredential(context.Background(), credential)
}

func (m *Manager) RefreshCredential(ctx context.Context, id string, provider Provider) (Credential, error) {
	credential, err := m.store.Load(id)
	if err != nil {
		return Credential{}, err
	}
	if credential.Provider != provider {
		return Credential{}, fmt.Errorf("oauth credential %q is not a %s credential", id, provider)
	}
	return m.refreshCredential(ctx, credential)
}

func (m *Manager) refreshCredential(ctx context.Context, credential Credential) (Credential, error) {
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()
	current, err := m.store.Load(credential.ID)
	if err != nil {
		return Credential{}, err
	}
	if current.Provider != credential.Provider {
		return Credential{}, fmt.Errorf("oauth credential %q provider changed", credential.ID)
	}
	if current.ExpiresAt.After(m.now().Add(time.Minute)) && current.AccessToken != credential.AccessToken {
		return current, nil
	}
	if strings.TrimSpace(current.RefreshToken) == "" {
		return Credential{}, fmt.Errorf("oauth credential %q requires reconnect", current.ID)
	}
	endpoint, clientID := oauthRefreshEndpoint(current.Provider)
	body, err := json.Marshal(map[string]string{"grant_type": "refresh_token", "refresh_token": current.RefreshToken, "client_id": clientID})
	if err != nil {
		return Credential{}, fmt.Errorf("encode OAuth refresh: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return Credential{}, fmt.Errorf("create OAuth refresh: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	response, err := m.httpClient.Do(req)
	if err != nil {
		return Credential{}, fmt.Errorf("OAuth refresh request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxOAuthResponseBytes))
	if err != nil {
		return Credential{}, fmt.Errorf("read OAuth refresh response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return Credential{}, fmt.Errorf("OAuth refresh failed with status %d; reconnect is required", response.StatusCode)
	}
	var token struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
	}
	if err := json.Unmarshal(payload, &token); err != nil {
		return Credential{}, fmt.Errorf("decode OAuth refresh response: %w", err)
	}
	if strings.TrimSpace(token.AccessToken) == "" || token.ExpiresIn <= 0 {
		return Credential{}, fmt.Errorf("OAuth refresh response is invalid; reconnect is required")
	}
	current.AccessToken = token.AccessToken
	if strings.TrimSpace(token.RefreshToken) != "" {
		current.RefreshToken = token.RefreshToken
	}
	if strings.TrimSpace(token.Scope) != "" {
		current.Scope = token.Scope
	}
	current.ExpiresAt = m.now().Add(time.Duration(token.ExpiresIn) * time.Second)
	if err := m.store.Save(current); err != nil {
		return Credential{}, err
	}
	return m.store.Load(current.ID)
}

func oauthRefreshEndpoint(provider Provider) (string, string) {
	if provider == ProviderCodex {
		return codexTokenURL, codexClientID
	}
	return claudeTokenURL, claudeClientID
}

func (m *Manager) cleanupLocked() {
	now := m.now()
	for id, flow := range m.flows {
		if !flow.ExpiresAt.After(now) {
			delete(m.flows, id)
		}
	}
}

func publicFlow(flow Flow) Flow {
	flow.state = ""
	flow.verifier = ""
	return flow
}

func randomURLValue(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("generate OAuth random value: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
