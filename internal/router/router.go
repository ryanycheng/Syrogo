package router

import (
	"fmt"
	"sync"

	"syrogo/internal/config"
	"syrogo/internal/provider"
	"syrogo/internal/runtime"
)

type compiledRule struct {
	name        string
	fromTags    []string
	toTags      []string
	strategy    runtime.RoutingStrategy
	targetModel string
	resolvedSet []string
}

type Router struct {
	rules         []compiledRule
	providers     map[string]provider.Provider
	outboundByTag map[string][]string

	mu         sync.Mutex
	roundRobin map[string]int
}

func New(cfg config.RoutingConfig, providers map[string]provider.Provider, outbounds []config.OutboundSpec) (*Router, error) {
	outboundByTag := make(map[string][]string)
	for _, outbound := range outbounds {
		if _, ok := providers[outbound.Name]; !ok {
			return nil, fmt.Errorf("outbound %q not found", outbound.Name)
		}
		outboundByTag[outbound.Tag] = append(outboundByTag[outbound.Tag], outbound.Name)
	}

	rules := compileRules(cfg.Rules, outboundByTag)
	for _, rule := range rules {
		if len(rule.resolvedSet) == 0 {
			return nil, fmt.Errorf("routing rule %q resolved no outbounds", rule.name)
		}
	}

	return &Router{
		rules:         rules,
		providers:     providers,
		outboundByTag: outboundByTag,
		roundRobin:    make(map[string]int),
	}, nil
}

func (r *Router) Plan(ctx runtime.RouteContext) (runtime.ExecutionPlan, error) {
	for _, rule := range r.rules {
		if !matchTag(rule.fromTags, ctx.ActiveTag) {
			continue
		}

		ordered := append([]string(nil), rule.resolvedSet...)
		if rule.strategy == runtime.RoutingStrategyRoundRobin {
			ordered = rotate(ordered, r.nextRoundRobinIndex(rule.name, len(ordered)))
		}

		steps := make([]runtime.ExecutionStep, 0, len(ordered))
		model := ctx.Request.Model
		if rule.targetModel != "" {
			model = rule.targetModel
		}
		for _, outboundName := range ordered {
			target := r.providers[outboundName]
			steps = append(steps, runtime.ExecutionStep{
				Type:           runtime.StepTypeOutbound,
				OutboundName:   outboundName,
				OutboundTarget: target,
				Model:          model,
				OnError:        runtime.FallbackOnRetryable,
			})
		}

		return runtime.ExecutionPlan{
			MatchedRule:    rule.name,
			Strategy:       rule.strategy,
			ResolvedToTags: append([]string(nil), rule.toTags...),
			Steps:          steps,
		}, nil
	}

	return runtime.ExecutionPlan{}, fmt.Errorf("no routing rule matched active tag %q", ctx.ActiveTag)
}

func matchTag(tags []string, activeTag string) bool {
	for _, tag := range tags {
		if tag == activeTag {
			return true
		}
	}
	return false
}

func compileRules(rules []config.RoutingRule, outboundByTag map[string][]string) []compiledRule {
	compiled := make([]compiledRule, 0, len(rules))
	for i, rule := range rules {
		name := rule.Name
		if name == "" {
			name = fmt.Sprintf("rule-%d", i)
		}

		resolved := make([]string, 0)
		for _, tag := range rule.ToTags {
			resolved = append(resolved, outboundByTag[tag]...)
		}

		compiled = append(compiled, compiledRule{
			name:        name,
			fromTags:    append([]string(nil), rule.FromTags...),
			toTags:      append([]string(nil), rule.ToTags...),
			strategy:    runtime.RoutingStrategy(rule.Strategy),
			targetModel: rule.TargetModel,
			resolvedSet: resolved,
		})
	}
	return compiled
}

func rotate(values []string, index int) []string {
	if len(values) == 0 {
		return values
	}
	index = index % len(values)
	if index == 0 {
		return values
	}
	rotated := make([]string, 0, len(values))
	rotated = append(rotated, values[index:]...)
	rotated = append(rotated, values[:index]...)
	return rotated
}

func (r *Router) nextRoundRobinIndex(key string, size int) int {
	if size == 0 {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	index := r.roundRobin[key] % size
	r.roundRobin[key] = (index + 1) % size
	return index
}
