package provider

import (
	"context"
	"fmt"
	"sync"

	"github.com/ryanycheng/Syrogo/internal/oauth"
)

type OAuthCredentialSource struct {
	manager     *oauth.Manager
	id          string
	provider    oauth.Provider
	mu          sync.Mutex
	invalidated bool
}

func NewOAuthCredentialSource(manager *oauth.Manager, id string, provider oauth.Provider) *OAuthCredentialSource {
	return &OAuthCredentialSource{manager: manager, id: id, provider: provider}
}

func (s *OAuthCredentialSource) Credential(ctx context.Context) (Credential, error) {
	if s == nil || s.manager == nil {
		return Credential{}, fmt.Errorf("oauth credential manager is required")
	}
	s.mu.Lock()
	invalidated := s.invalidated
	s.invalidated = false
	s.mu.Unlock()
	var (
		credential oauth.Credential
		err        error
	)
	if invalidated {
		credential, err = s.manager.RefreshCredential(ctx, s.id, s.provider)
	} else {
		credential, err = s.manager.Credential(s.id, s.provider)
	}
	if err != nil {
		return Credential{}, err
	}
	return Credential{Kind: CredentialKindOAuth, Value: credential.AccessToken}, nil
}

func (s *OAuthCredentialSource) Invalidate() {
	if s != nil {
		s.mu.Lock()
		s.invalidated = true
		s.mu.Unlock()
	}
}
