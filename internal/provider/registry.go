package provider

import (
	"fmt"
	"sort"
	"sync"
)

type Factory func(name, endpoint string, apiKeys []string) (Provider, error)

type FactoryRegistry struct {
	mu        sync.RWMutex
	factories map[string]Factory
}

func NewFactoryRegistry() *FactoryRegistry {
	return &FactoryRegistry{factories: make(map[string]Factory)}
}

func (r *FactoryRegistry) Register(protocol string, factory Factory) error {
	if protocol == "" {
		return fmt.Errorf("provider protocol is required")
	}
	if factory == nil {
		return fmt.Errorf("provider factory %q is nil", protocol)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.factories[protocol]; exists {
		return fmt.Errorf("provider factory %q already registered", protocol)
	}
	if r.factories == nil {
		r.factories = make(map[string]Factory)
	}
	r.factories[protocol] = factory
	return nil
}

func (r *FactoryRegistry) MustRegister(protocol string, factory Factory) {
	if err := r.Register(protocol, factory); err != nil {
		panic(err)
	}
}

func (r *FactoryRegistry) New(protocol, name, endpoint, authToken string) (Provider, error) {
	r.mu.RLock()
	factory, ok := r.factories[protocol]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unsupported provider protocol %q", protocol)
	}

	apiKeys := []string(nil)
	if authToken != "" {
		apiKeys = []string{authToken}
	}
	return factory(name, endpoint, apiKeys)
}

func (r *FactoryRegistry) Has(protocol string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.factories[protocol]
	return ok
}

func (r *FactoryRegistry) Protocols() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	protocols := make([]string, 0, len(r.factories))
	for protocol := range r.factories {
		protocols = append(protocols, protocol)
	}
	sort.Strings(protocols)
	return protocols
}

var defaultFactoryRegistry = newDefaultFactoryRegistry()

func DefaultFactoryRegistry() *FactoryRegistry {
	return defaultFactoryRegistry
}

func newDefaultFactoryRegistry() *FactoryRegistry {
	registry := NewFactoryRegistry()
	registry.MustRegister("mock", func(name, _ string, _ []string) (Provider, error) {
		return NewMock(name), nil
	})
	registry.MustRegister("openai_chat", func(name, endpoint string, apiKeys []string) (Provider, error) {
		return NewOpenAICompatible(name, endpoint, apiKeys, nil), nil
	})
	registry.MustRegister("openai_responses", func(name, endpoint string, apiKeys []string) (Provider, error) {
		return NewOpenAIResponsesCompatible(name, endpoint, apiKeys, nil), nil
	})
	registry.MustRegister("anthropic_messages", func(name, endpoint string, apiKeys []string) (Provider, error) {
		return NewAnthropicMessagesCompatible(name, endpoint, apiKeys, nil), nil
	})
	return registry
}
