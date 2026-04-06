package router

import (
	"testing"

	"syrogo/internal/config"
	"syrogo/internal/provider"
	"syrogo/internal/runtime"
)

func TestNewSucceedsWhenDefaultProviderExists(t *testing.T) {
	providers := map[string]provider.Provider{
		"mock": provider.NewMock("mock"),
	}

	r, err := New(config.RoutingConfig{DefaultProvider: "mock"}, providers)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if r == nil {
		t.Fatal("New() returned nil router")
	}
}

func TestNewSucceedsWhenDefaultOutboundExists(t *testing.T) {
	providers := map[string]provider.Provider{
		"mock": provider.NewMock("mock"),
	}

	r, err := New(config.RoutingConfig{DefaultOutbound: "mock"}, providers)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if r == nil {
		t.Fatal("New() returned nil router")
	}
}

func TestNewFailsWhenDefaultProviderMissing(t *testing.T) {
	_, err := New(config.RoutingConfig{DefaultProvider: "missing"}, map[string]provider.Provider{
		"mock": provider.NewMock("mock"),
	})
	if err == nil || err.Error() != "default outbound \"missing\" not found" {
		t.Fatalf("New() error = %v, want default provider missing error", err)
	}
}

func TestNewFailsWhenFallbackProviderMissing(t *testing.T) {
	_, err := New(config.RoutingConfig{
		DefaultProvider:   "default",
		FallbackProviders: []string{"fallback"},
	}, map[string]provider.Provider{
		"default": provider.NewMock("default"),
	})
	if err == nil || err.Error() != "fallback outbound \"fallback\" not found" {
		t.Fatalf("New() error = %v, want fallback provider missing error", err)
	}
}

func TestPlanReturnsMappedProviderStep(t *testing.T) {
	providers := map[string]provider.Provider{
		"default":  provider.NewMock("default"),
		"special":  provider.NewMock("special"),
		"fallback": provider.NewMock("fallback"),
	}

	r, err := New(config.RoutingConfig{
		DefaultProvider:   "default",
		FallbackProviders: []string{"fallback"},
		ModelProviders: map[string]string{
			"gpt-4": "special",
		},
	}, providers)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	plan, err := r.Plan(runtime.RouteContext{Request: runtime.Request{Model: "gpt-4"}})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.MatchedRoute != "special" {
		t.Fatalf("Plan().MatchedRoute = %q, want special", plan.MatchedRoute)
	}
	if len(plan.Steps) != 2 {
		t.Fatalf("len(Plan().Steps) = %d, want 2", len(plan.Steps))
	}
	if got := plan.Steps[0].ProviderName; got != "special" {
		t.Fatalf("Plan().Steps[0].ProviderName = %q, want special", got)
	}
	if got := plan.Steps[1].ProviderName; got != "fallback" {
		t.Fatalf("Plan().Steps[1].ProviderName = %q, want fallback", got)
	}
}

func TestPlanReturnsMappedOutboundStep(t *testing.T) {
	providers := map[string]provider.Provider{
		"default":  provider.NewMock("default"),
		"special":  provider.NewMock("special"),
		"fallback": provider.NewMock("fallback"),
	}

	r, err := New(config.RoutingConfig{
		DefaultOutbound:   "default",
		FallbackOutbounds: []string{"fallback"},
		ModelOutbounds: map[string]string{
			"gpt-4": "special",
		},
	}, providers)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	plan, err := r.Plan(runtime.RouteContext{Request: runtime.Request{Model: "gpt-4"}})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.MatchedRoute != "special" {
		t.Fatalf("Plan().MatchedRoute = %q, want special", plan.MatchedRoute)
	}
}

func TestPlanUsesInboundTarget(t *testing.T) {
	providers := map[string]provider.Provider{
		"default": provider.NewMock("default"),
		"office":  provider.NewMock("office"),
	}

	r, err := New(config.RoutingConfig{
		DefaultOutbound: "default",
		InboundOutbounds: map[string]string{
			"office-entry": "office",
		},
	}, providers)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	plan, err := r.Plan(runtime.RouteContext{
		Request:     runtime.Request{Model: "gpt-4"},
		InboundName: "office-entry",
	})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.MatchedRoute != "office" {
		t.Fatalf("Plan().MatchedRoute = %q, want office", plan.MatchedRoute)
	}
}

func TestPlanFallsBackToDefaultProvider(t *testing.T) {
	providers := map[string]provider.Provider{
		"default":  provider.NewMock("default"),
		"fallback": provider.NewMock("fallback"),
	}

	r, err := New(config.RoutingConfig{
		DefaultProvider:   "default",
		FallbackProviders: []string{"fallback"},
	}, providers)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	plan, err := r.Plan(runtime.RouteContext{Request: runtime.Request{Model: "unknown-model"}})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if got := plan.MatchedRoute; got != "default" {
		t.Fatalf("Plan().MatchedRoute = %q, want default", got)
	}
	if len(plan.Steps) != 2 {
		t.Fatalf("len(Plan().Steps) = %d, want 2", len(plan.Steps))
	}
}

func TestPlanSkipsDuplicateFallbackProvider(t *testing.T) {
	providers := map[string]provider.Provider{
		"default": provider.NewMock("default"),
	}

	r, err := New(config.RoutingConfig{
		DefaultProvider:   "default",
		FallbackProviders: []string{"default"},
	}, providers)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	plan, err := r.Plan(runtime.RouteContext{Request: runtime.Request{Model: "gpt-4"}})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if len(plan.Steps) != 1 {
		t.Fatalf("len(Plan().Steps) = %d, want 1", len(plan.Steps))
	}
}

func TestPlanFailsWhenMappedProviderMissing(t *testing.T) {
	providers := map[string]provider.Provider{
		"default": provider.NewMock("default"),
	}

	r, err := New(config.RoutingConfig{
		DefaultProvider: "default",
		ModelProviders: map[string]string{
			"gpt-4": "missing",
		},
	}, providers)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = r.Plan(runtime.RouteContext{Request: runtime.Request{Model: "gpt-4"}})
	if err == nil || err.Error() != "outbound \"missing\" not found for model \"gpt-4\"" {
		t.Fatalf("Plan() error = %v, want mapped provider missing error", err)
	}
}
