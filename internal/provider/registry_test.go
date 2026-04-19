package provider

import (
	"testing"

	"github.com/ryanycheng/Syrogo/internal/config"
)

func TestFactoryRegistryNewBuildsRegisteredProvider(t *testing.T) {
	registry := NewFactoryRegistry()
	if err := registry.Register("mock", func(name, endpoint string, apiKeys []string, capabilities config.OutboundCapabilities) (Provider, error) {
		return NewMock(name), nil
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	got, err := registry.New("mock", "demo", "", "", config.OutboundCapabilities{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if got.Name() != "demo" {
		t.Fatalf("got.Name() = %q, want demo", got.Name())
	}
}

func TestFactoryRegistryRejectsDuplicateRegister(t *testing.T) {
	registry := NewFactoryRegistry()
	factory := func(name, endpoint string, apiKeys []string, capabilities config.OutboundCapabilities) (Provider, error) {
		return NewMock(name), nil
	}
	if err := registry.Register("mock", factory); err != nil {
		t.Fatalf("first Register() error = %v", err)
	}

	err := registry.Register("mock", factory)
	if err == nil || err.Error() != "provider factory \"mock\" already registered" {
		t.Fatalf("second Register() error = %v, want duplicate register error", err)
	}
}

func TestFactoryRegistryRejectsUnknownProtocol(t *testing.T) {
	registry := NewFactoryRegistry()

	_, err := registry.New("missing", "demo", "", "", config.OutboundCapabilities{})
	if err == nil || err.Error() != "unsupported provider protocol \"missing\"" {
		t.Fatalf("New() error = %v, want unsupported provider protocol error", err)
	}
}
