package router

import (
	"testing"

	"syrogo/internal/config"
	"syrogo/internal/provider"
	"syrogo/internal/runtime"
)

func testOutbounds() []config.OutboundSpec {
	return []config.OutboundSpec{
		{Name: "mock-1", Protocol: "mock", Tag: "mock-a"},
		{Name: "mock-2", Protocol: "mock", Tag: "mock-b"},
		{Name: "mock-3", Protocol: "mock", Tag: "mock-c"},
	}
}

func testProviders() map[string]provider.Provider {
	return map[string]provider.Provider{
		"mock-1": provider.NewMock("mock-1"),
		"mock-2": provider.NewMock("mock-2"),
		"mock-3": provider.NewMock("mock-3"),
	}
}

func TestNewFailsWhenOutboundMissing(t *testing.T) {
	_, err := New(config.RoutingConfig{Rules: []config.RoutingRule{{Name: "office", FromTags: []string{"office"}, ToTags: []string{"mock-a"}, Strategy: "failover"}}}, map[string]provider.Provider{}, testOutbounds())
	if err == nil || err.Error() != "outbound \"mock-1\" not found" {
		t.Fatalf("New() error = %v, want missing outbound error", err)
	}
}

func TestPlanUsesFirstMatchingRule(t *testing.T) {
	r, err := New(config.RoutingConfig{Rules: []config.RoutingRule{
		{Name: "rule-1", FromTags: []string{"office"}, ToTags: []string{"mock-a"}, Strategy: "failover"},
		{Name: "rule-2", FromTags: []string{"office"}, ToTags: []string{"mock-b"}, Strategy: "failover"},
	}}, testProviders(), testOutbounds())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	plan, err := r.Plan(runtime.RouteContext{Request: runtime.Request{Model: "gpt-4"}, ActiveTag: "office"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.MatchedRule != "rule-1" {
		t.Fatalf("Plan().MatchedRule = %q, want rule-1", plan.MatchedRule)
	}
	if len(plan.Steps) != 1 || plan.Steps[0].OutboundName != "mock-1" {
		t.Fatalf("Plan().Steps = %#v, want mock-1", plan.Steps)
	}
}

func TestPlanFailoverExpandsOrderedSteps(t *testing.T) {
	r, err := New(config.RoutingConfig{Rules: []config.RoutingRule{{
		Name:     "office",
		FromTags: []string{"office"},
		ToTags:   []string{"mock-a", "mock-b", "mock-c"},
		Strategy: "failover",
	}}}, testProviders(), testOutbounds())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	plan, err := r.Plan(runtime.RouteContext{Request: runtime.Request{Model: "gpt-4"}, ActiveTag: "office"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if len(plan.Steps) != 3 {
		t.Fatalf("len(Plan().Steps) = %d, want 3", len(plan.Steps))
	}
	if plan.Steps[0].OutboundName != "mock-1" || plan.Steps[1].OutboundName != "mock-2" || plan.Steps[2].OutboundName != "mock-3" {
		t.Fatalf("Plan().Steps = %#v, want ordered failover steps", plan.Steps)
	}
}

func TestPlanRoundRobinRotatesStartingOutbound(t *testing.T) {
	r, err := New(config.RoutingConfig{Rules: []config.RoutingRule{{
		Name:     "office",
		FromTags: []string{"office"},
		ToTags:   []string{"mock-a", "mock-b"},
		Strategy: "round_robin",
	}}}, testProviders(), testOutbounds())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	plan1, err := r.Plan(runtime.RouteContext{Request: runtime.Request{Model: "gpt-4"}, ActiveTag: "office"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	plan2, err := r.Plan(runtime.RouteContext{Request: runtime.Request{Model: "gpt-4"}, ActiveTag: "office"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan1.Steps[0].OutboundName != "mock-1" || plan2.Steps[0].OutboundName != "mock-2" {
		t.Fatalf("round robin starts = %q then %q, want mock-1 then mock-2", plan1.Steps[0].OutboundName, plan2.Steps[0].OutboundName)
	}
}

func TestPlanRuleCanOverrideTargetModel(t *testing.T) {
	r, err := New(config.RoutingConfig{Rules: []config.RoutingRule{{
		Name:        "thinking",
		FromTags:    []string{"thinking"},
		ToTags:      []string{"mock-a"},
		Strategy:    "failover",
		TargetModel: "gpt-4o-mini",
	}}}, testProviders(), testOutbounds())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	plan, err := r.Plan(runtime.RouteContext{Request: runtime.Request{Model: "claude-sonnet-4-5"}, ActiveTag: "thinking"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.Steps[0].Model != "gpt-4o-mini" {
		t.Fatalf("Plan().Steps[0].Model = %q, want gpt-4o-mini", plan.Steps[0].Model)
	}
}

func TestPlanFailsWhenNoRuleMatches(t *testing.T) {
	r, err := New(config.RoutingConfig{Rules: []config.RoutingRule{{Name: "office", FromTags: []string{"office"}, ToTags: []string{"mock-a"}, Strategy: "failover"}}}, testProviders(), testOutbounds())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = r.Plan(runtime.RouteContext{Request: runtime.Request{Model: "gpt-4"}, ActiveTag: "unknown"})
	if err == nil || err.Error() != "no routing rule matched active tag \"unknown\"" {
		t.Fatalf("Plan() error = %v, want no matched rule error", err)
	}
}
