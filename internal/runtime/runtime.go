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

type FallbackCondition string

const (
	StepTypeOutbound StepType = "outbound"

	FallbackAlways          FallbackCondition = "always"
	FallbackOnRetryable     FallbackCondition = "retryable"
	FallbackOnQuotaExceeded FallbackCondition = "quota_exceeded"
)

type ExecutionStep struct {
	Type           StepType
	ProviderName   string
	ProviderTarget provider.Provider
	Model          string
	OnError        FallbackCondition
}

type ExecutionPlan struct {
	MatchedRoute string
	Steps        []ExecutionStep
}
