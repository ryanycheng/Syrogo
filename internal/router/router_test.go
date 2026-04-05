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

func TestNewFailsWhenDefaultProviderMissing(t *testing.T) {
	_, err := New(config.RoutingConfig{DefaultProvider: "missing"}, map[string]provider.Provider{
		"mock": provider.NewMock("mock"),
	})
	if err == nil || err.Error() != "default provider \"missing\" not found" {
		t.Fatalf("New() error = %v, want default provider missing error", err)
	}
}

func TestPlanReturnsMappedProviderStep(t *testing.T) {
	providers := map[string]provider.Provider{
		"default": provider.NewMock("default"),
		"special": provider.NewMock("special"),
	}

	r, err := New(config.RoutingConfig{
		DefaultProvider: "default",
		ModelProviders: map[string]string{
			"gpt-4": "special",
		},
	}, providers)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	plan, err := r.Plan(runtime.RouteContext{Request: runtime.InternalRequest{Model: "gpt-4"}})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.MatchedRoute != "special" {
		t.Fatalf("Plan().MatchedRoute = %q, want special", plan.MatchedRoute)
	}
	if len(plan.Steps) != 1 {
		t.Fatalf("len(Plan().Steps) = %d, want 1", len(plan.Steps))
	}
	if got := plan.Steps[0].ProviderName; got != "special" {
		t.Fatalf("Plan().Steps[0].ProviderName = %q, want special", got)
	}
	if plan.Steps[0].ProviderTarget == nil {
		t.Fatal("Plan().Steps[0].ProviderTarget = nil, want provider")
	}
}

func TestPlanFallsBackToDefaultProvider(t *testing.T) {
	providers := map[string]provider.Provider{
		"default": provider.NewMock("default"),
	}

	r, err := New(config.RoutingConfig{DefaultProvider: "default"}, providers)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	plan, err := r.Plan(runtime.RouteContext{Request: runtime.InternalRequest{Model: "unknown-model"}})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if got := plan.MatchedRoute; got != "default" {
		t.Fatalf("Plan().MatchedRoute = %q, want default", got)
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

	_, err = r.Plan(runtime.RouteContext{Request: runtime.InternalRequest{Model: "gpt-4"}})
	if err == nil || err.Error() != "provider \"missing\" not found for model \"gpt-4\"" {
		t.Fatalf("Plan() error = %v, want mapped provider missing error", err)
	}
}
