package router

import (
	"fmt"
	"strings"
	"sync"

	"github.com/ryanycheng/Syrogo/internal/config"
	"github.com/ryanycheng/Syrogo/internal/provider"
	"github.com/ryanycheng/Syrogo/internal/runtime"
)

type compiledRule struct {
	name             string
	fromTags         []string
	modelPatterns    []wildcardPattern
	hasModelMatch    bool
	toTags           []string
	strategy         runtime.RoutingStrategy
	modelMap         map[string]string
	resolvedSet      []string
	weightedResolved []string
}

type Router struct {
	rules          []compiledRule
	providers      map[string]provider.Provider
	outboundByTag  map[string][]string
	modelsByTarget map[string]map[string]string

	mu         sync.Mutex
	roundRobin map[string]int
}

func New(cfg config.RoutingConfig, providers map[string]provider.Provider, outbounds []config.OutboundSpec) (*Router, error) {
	outboundByTag := make(map[string][]string)
	declaredTags := make(map[string]struct{})
	modelsByTarget := make(map[string]map[string]string, len(outbounds))
	for _, outbound := range outbounds {
		declaredTags[outbound.Tag] = struct{}{}
		if !config.OutboundEnabled(outbound) {
			continue
		}
		if _, ok := providers[outbound.Name]; !ok {
			return nil, fmt.Errorf("outbound %q not found", outbound.Name)
		}
		outboundByTag[outbound.Tag] = append(outboundByTag[outbound.Tag], outbound.Name)
		modelsByTarget[outbound.Name] = compileOutboundModels(outbound.Models)
	}

	for _, rule := range cfg.Rules {
		for _, tag := range rule.ToTags {
			if _, ok := declaredTags[tag]; !ok {
				return nil, fmt.Errorf("routing rule %q references unknown outbound tag %q", rule.Name, tag)
			}
		}
	}
	rules := compileRules(cfg.Rules, outboundByTag)

	return &Router{
		rules:          rules,
		providers:      providers,
		outboundByTag:  outboundByTag,
		modelsByTarget: modelsByTarget,
		roundRobin:     make(map[string]int),
	}, nil
}

func (r *Router) Plan(ctx runtime.RouteContext) (runtime.ExecutionPlan, error) {
	return r.plan(ctx, true)
}

func (r *Router) PlanDryRun(ctx runtime.RouteContext) (runtime.ExecutionPlan, error) {
	return r.plan(ctx, false)
}

func (r *Router) plan(ctx runtime.RouteContext, mutate bool) (runtime.ExecutionPlan, error) {
	for _, rule := range r.rules {
		if !matchTag(rule.fromTags, ctx.ActiveTag) {
			continue
		}
		if !rule.matchesModel(ctx.Request.Model) {
			continue
		}

		if len(rule.resolvedSet) == 0 {
			return runtime.ExecutionPlan{}, provider.NewRetryableError(fmt.Errorf("routing rule %q has no enabled outbounds", rule.name))
		}

		ordered := append([]string(nil), rule.resolvedSet...)
		switch rule.strategy {
		case runtime.RoutingStrategyRoundRobin:
			if mutate {
				ordered = rotate(ordered, r.nextRoundRobinIndex(rule.name, len(ordered)))
			}
		case runtime.RoutingStrategyWeightedRoundRobin:
			weighted := append([]string(nil), rule.weightedResolved...)
			if mutate {
				weighted = rotate(weighted, r.nextRoundRobinIndex(rule.name, len(weighted)))
			}
			ordered = dedupePreserveOrder(weighted)
		}

		steps := make([]runtime.ExecutionStep, 0, len(ordered))
		routedModel := rule.resolveModel(ctx.Request.Model)
		for _, outboundName := range ordered {
			target := r.providers[outboundName]
			model, unavailable := resolveOutboundModel(r.modelsByTarget[outboundName], routedModel)
			steps = append(steps, runtime.ExecutionStep{
				Type:             runtime.StepTypeOutbound,
				OutboundName:     outboundName,
				OutboundProtocol: providerProtocol(target),
				OutboundTarget:   target,
				Model:            model,
				ModelUnavailable: unavailable,
				OnError:          runtime.FallbackOnRetryable,
			})
		}

		return runtime.ExecutionPlan{
			ClientName:      ctx.ClientName,
			InboundName:     ctx.InboundName,
			InboundProtocol: ctx.InboundProtocol,
			ActiveTag:       ctx.ActiveTag,
			RequestedModel:  ctx.Request.Model,
			MatchedRule:     rule.name,
			Strategy:        rule.strategy,
			ResolvedToTags:  append([]string(nil), rule.toTags...),
			Steps:           steps,
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

type wildcardPattern struct {
	literals     []string
	leadingStar  bool
	trailingStar bool
}

func compileModelPatterns(match *config.RoutingRuleMatch) []wildcardPattern {
	if match == nil {
		return nil
	}
	patterns := make([]wildcardPattern, 0, len(match.Models))
	for _, pattern := range match.Models {
		parts := strings.Split(pattern, "*")
		literals := make([]string, 0, len(parts))
		for _, part := range parts {
			if part != "" {
				literals = append(literals, part)
			}
		}
		patterns = append(patterns, wildcardPattern{
			literals:     literals,
			leadingStar:  strings.HasPrefix(pattern, "*"),
			trailingStar: strings.HasSuffix(pattern, "*"),
		})
	}
	return patterns
}

func (r compiledRule) matchesModel(model string) bool {
	if !r.hasModelMatch {
		return true
	}
	for _, pattern := range r.modelPatterns {
		if pattern.matches(model) {
			return true
		}
	}
	return false
}

func (p wildcardPattern) matches(value string) bool {
	if len(p.literals) == 0 {
		return true
	}

	position := 0
	for i, literal := range p.literals {
		last := i == len(p.literals)-1
		if i == 0 && !p.leadingStar {
			if !strings.HasPrefix(value, literal) {
				return false
			}
			position = len(literal)
			continue
		}
		if last && !p.trailingStar {
			start := len(value) - len(literal)
			return start >= position && strings.HasSuffix(value, literal)
		}
		index := strings.Index(value[position:], literal)
		if index < 0 {
			return false
		}
		position += index + len(literal)
	}
	return p.trailingStar || position == len(value)
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

		weightedResolved := resolved
		if rule.Strategy == string(runtime.RoutingStrategyWeightedRoundRobin) {
			weightedResolved = expandWeightedResolved(rule, outboundByTag)
		}

		compiled = append(compiled, compiledRule{
			name:             name,
			fromTags:         append([]string(nil), rule.FromTags...),
			modelPatterns:    compileModelPatterns(rule.Match),
			hasModelMatch:    rule.Match != nil,
			toTags:           append([]string(nil), rule.ToTags...),
			strategy:         runtime.RoutingStrategy(rule.Strategy),
			modelMap:         compileModelMap(rule),
			resolvedSet:      resolved,
			weightedResolved: weightedResolved,
		})
	}
	return compiled
}

func compileModelMap(rule config.RoutingRule) map[string]string {
	if rule.TargetModel != "" {
		return map[string]string{"*": rule.TargetModel}
	}
	if len(rule.ModelMap) == 0 {
		return nil
	}
	modelMap := make(map[string]string, len(rule.ModelMap))
	for from, to := range rule.ModelMap {
		modelMap[from] = to
	}
	return modelMap
}

func (r compiledRule) resolveModel(requestedModel string) string {
	if mapped := r.modelMap[requestedModel]; mapped != "" {
		return mapped
	}
	if mapped := r.modelMap["*"]; mapped != "" {
		return mapped
	}
	return requestedModel
}

func compileOutboundModels(models []config.OutboundModelSpec) map[string]string {
	if len(models) == 0 {
		return nil
	}
	resolved := make(map[string]string)
	for _, model := range models {
		resolved[model.Name] = model.Name
		for _, alias := range model.Aliases {
			resolved[alias] = model.Name
		}
	}
	return resolved
}

func resolveOutboundModel(models map[string]string, routedModel string) (string, bool) {
	if models == nil {
		return routedModel, false
	}
	model, ok := models[routedModel]
	if !ok {
		return routedModel, true
	}
	return model, false
}

func expandWeightedResolved(rule config.RoutingRule, outboundByTag map[string][]string) []string {
	weighted := make([]string, 0)
	for _, tag := range rule.ToTags {
		outbounds := outboundByTag[tag]
		for i := 0; i < rule.Weights[tag]; i++ {
			weighted = append(weighted, outbounds...)
		}
	}
	return weighted
}

func dedupePreserveOrder(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	ordered := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		ordered = append(ordered, value)
	}
	return ordered
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

func providerProtocol(target provider.Provider) string {
	switch target.(type) {
	case *provider.MockProvider:
		return "mock"
	case *provider.OpenAICompatibleProvider:
		return "openai"
	case *provider.AnthropicMessagesProvider:
		return "anthropic_messages"
	default:
		return "unknown"
	}
}
