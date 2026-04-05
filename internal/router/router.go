package router

import (
	"fmt"

	"syrogo/internal/config"
	"syrogo/internal/provider"
	"syrogo/internal/runtime"
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

func (r *Router) Plan(ctx runtime.RouteContext) (runtime.ExecutionPlan, error) {
	providerName := r.defaultProvider
	if name, ok := r.modelProviders[ctx.Request.Model]; ok {
		providerName = name
	}

	p, exists := r.providers[providerName]
	if !exists {
		return runtime.ExecutionPlan{}, fmt.Errorf("provider %q not found for model %q", providerName, ctx.Request.Model)
	}

	return runtime.ExecutionPlan{
		MatchedRoute: providerName,
		Steps: []runtime.ExecutionStep{
			{
				Type:           runtime.StepTypeOutbound,
				ProviderName:   providerName,
				ProviderTarget: p,
				Model:          ctx.Request.Model,
			},
		},
	}, nil
}
