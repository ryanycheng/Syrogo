package execution

import (
	"context"
	"fmt"

	"syrogo/internal/provider"
	"syrogo/internal/runtime"
)

type Dispatcher struct{}

func NewDispatcher() *Dispatcher {
	return &Dispatcher{}
}

func (d *Dispatcher) Dispatch(ctx context.Context, req runtime.InternalRequest, plan runtime.ExecutionPlan) (provider.ChatResponse, error) {
	if len(plan.Steps) == 0 {
		return provider.ChatResponse{}, fmt.Errorf("execution plan has no steps")
	}

	var lastErr error
	for i, step := range plan.Steps {
		if step.Type != runtime.StepTypeOutbound {
			return provider.ChatResponse{}, fmt.Errorf("unsupported execution step type %q", step.Type)
		}
		if step.ProviderTarget == nil {
			return provider.ChatResponse{}, fmt.Errorf("provider target is required")
		}

		resp, err := step.ProviderTarget.ChatCompletion(ctx, provider.ChatRequest{
			Model:    step.Model,
			Messages: req.Messages,
		})
		if err == nil {
			return resp, nil
		}

		lastErr = err
		if !shouldFallback(normalizeFallbackCondition(string(step.OnError)), classifyError(err), i == len(plan.Steps)-1) {
			return provider.ChatResponse{}, err
		}
	}

	return provider.ChatResponse{}, lastErr
}
