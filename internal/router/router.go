package router

import (
	"fmt"

	"syrogo/internal/config"
	"syrogo/internal/provider"
)

type Router struct {
	defaultProvider string
	modelProviders  map[string]string
	providers       map[string]provider.Provider
}

func New(cfg config.RoutingConfig, providers map[string]provider.Provider) (*Router, error) {
	if _, ok := providers[cfg.DefaultProvider]; !ok {
		return nil, fmt.Errorf("default provider %q not found", cfg.DefaultProvider)
	}

	return &Router{
		defaultProvider: cfg.DefaultProvider,
		modelProviders:  cfg.ModelProviders,
		providers:       providers,
	}, nil
}

func (r *Router) Resolve(model string) (provider.Provider, error) {
	if name, ok := r.modelProviders[model]; ok {
		p, exists := r.providers[name]
		if !exists {
			return nil, fmt.Errorf("provider %q not found for model %q", name, model)
		}
		return p, nil
	}

	return r.providers[r.defaultProvider], nil
}
