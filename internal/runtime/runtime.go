package runtime

import "syrogo/internal/provider"

type InternalRequest struct {
	Model    string
	Messages []provider.ChatMessage
}

type RouteContext struct {
	Request InternalRequest
}

type StepType string

const (
	StepTypeOutbound StepType = "outbound"
)

type ExecutionStep struct {
	Type           StepType
	ProviderName   string
	ProviderTarget provider.Provider
	Model          string
}

type ExecutionPlan struct {
	MatchedRoute string
	Steps        []ExecutionStep
}
