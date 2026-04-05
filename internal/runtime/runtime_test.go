package runtime

import (
	"testing"

	"syrogo/internal/provider"
)

func TestExecutionPlanCarriesOutboundStep(t *testing.T) {
	p := provider.NewMock("mock")
	plan := ExecutionPlan{
		MatchedRoute: "mock",
		Steps: []ExecutionStep{{
			Type:           StepTypeOutbound,
			ProviderName:   "mock",
			ProviderTarget: p,
			Model:          "gpt-4",
		}},
	}

	if plan.MatchedRoute != "mock" {
		t.Fatalf("MatchedRoute = %q, want mock", plan.MatchedRoute)
	}
	if len(plan.Steps) != 1 {
		t.Fatalf("len(Steps) = %d, want 1", len(plan.Steps))
	}
	if plan.Steps[0].Type != StepTypeOutbound {
		t.Fatalf("Steps[0].Type = %q, want outbound", plan.Steps[0].Type)
	}
	if plan.Steps[0].ProviderTarget == nil {
		t.Fatal("Steps[0].ProviderTarget = nil, want provider")
	}
}
