package provider

import (
	"context"
	"fmt"
)

type CredentialKind string

const (
	CredentialKindAPIKey CredentialKind = "api_key"
	CredentialKindOAuth  CredentialKind = "oauth"
)

type Credential struct {
	Kind  CredentialKind
	Value string
}

type CredentialSource interface {
	Credential(context.Context) (Credential, error)
	Invalidate()
}

type StaticCredentialSource struct {
	value string
}

func NewStaticCredentialSource(value string) *StaticCredentialSource {
	return &StaticCredentialSource{value: value}
}

func (s *StaticCredentialSource) Credential(context.Context) (Credential, error) {
	if s == nil || s.value == "" {
		return Credential{}, fmt.Errorf("api key is required")
	}
	return Credential{Kind: CredentialKindAPIKey, Value: s.value}, nil
}

func (s *StaticCredentialSource) Invalidate() {}
