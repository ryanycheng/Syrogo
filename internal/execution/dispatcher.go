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

func (d *Dispatcher) Dispatch(ctx context.Context, req runtime.Request, plan runtime.ExecutionPlan) (runtime.Response, error) {
	if len(plan.Steps) == 0 {
		return runtime.Response{}, fmt.Errorf("execution plan has no steps")
	}

	var lastErr error
	for i, step := range plan.Steps {
		if step.Type != runtime.StepTypeOutbound {
			return runtime.Response{}, fmt.Errorf("unsupported execution step type %q", step.Type)
		}
		if step.ProviderTarget == nil {
			return runtime.Response{}, fmt.Errorf("provider target is required")
		}

		resp, err := step.ProviderTarget.ChatCompletion(ctx, req)
		if err == nil {
			return resp, nil
		}

		lastErr = err
		if !provider.FallbackAllowed(string(step.OnError), provider.NormalizeError(err), i == len(plan.Steps)-1) {
			return runtime.Response{}, err
		}
	}

	return runtime.Response{}, lastErr
}
