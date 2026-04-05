package router

import (
	"fmt"

	"syrogo/internal/config"
	"syrogo/internal/provider"
	"syrogo/internal/runtime"
)

type Router struct {
	defaultProvider   string
	fallbackProviders []string
	modelProviders    map[string]string
	providers         map[string]provider.Provider
}

func New(cfg config.RoutingConfig, providers map[string]provider.Provider) (*Router, error) {
	if _, ok := providers[cfg.DefaultProvider]; !ok {
		return nil, fmt.Errorf("default provider %q not found", cfg.DefaultProvider)
	}
	for _, name := range cfg.FallbackProviders {
		if _, ok := providers[name]; !ok {
			return nil, fmt.Errorf("fallback provider %q not found", name)
		}
	}

	return &Router{
		defaultProvider:   cfg.DefaultProvider,
		fallbackProviders: cfg.FallbackProviders,
		modelProviders:    cfg.ModelProviders,
		providers:         providers,
	}, nil
}

func (r *Router) Plan(ctx runtime.RouteContext) (runtime.ExecutionPlan, error) {
	providerName := r.defaultProvider
	if name, ok := r.modelProviders[ctx.Request.Model]; ok {
		providerName = name
	}

	primary, exists := r.providers[providerName]
	if !exists {
		return runtime.ExecutionPlan{}, fmt.Errorf("provider %q not found for model %q", providerName, ctx.Request.Model)
	}

	steps := []runtime.ExecutionStep{
		{
			Type:           runtime.StepTypeOutbound,
			ProviderName:   providerName,
			ProviderTarget: primary,
			Model:          ctx.Request.Model,
			OnError:        runtime.FallbackOnRetryable,
		},
	}
	for _, name := range r.fallbackProviders {
		if name == providerName {
			continue
		}
		steps = append(steps, runtime.ExecutionStep{
			Type:           runtime.StepTypeOutbound,
			ProviderName:   name,
			ProviderTarget: r.providers[name],
			Model:          ctx.Request.Model,
			OnError:        runtime.FallbackOnRetryable,
		})
	}

	return runtime.ExecutionPlan{
		MatchedRoute: providerName,
		Steps:        steps,
	}, nil
}
