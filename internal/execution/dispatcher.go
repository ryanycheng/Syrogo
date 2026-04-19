package execution

import (
	"context"
	"fmt"

	"github.com/ryanycheng/Syrogo/internal/provider"
	"github.com/ryanycheng/Syrogo/internal/runtime"
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
		if step.OutboundTarget == nil {
			return runtime.Response{}, fmt.Errorf("outbound target is required")
		}

		stepReq := req
		if step.Model != "" {
			stepReq.Model = step.Model
		}

		resp, err := step.OutboundTarget.ChatCompletion(ctx, stepReq)
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

func (d *Dispatcher) DispatchStream(ctx context.Context, req runtime.Request, plan runtime.ExecutionPlan) (<-chan runtime.StreamEvent, error) {
	if len(plan.Steps) == 0 {
		return nil, fmt.Errorf("execution plan has no steps")
	}

	var lastErr error
	for i, step := range plan.Steps {
		if step.Type != runtime.StepTypeOutbound {
			return nil, fmt.Errorf("unsupported execution step type %q", step.Type)
		}
		if step.OutboundTarget == nil {
			return nil, fmt.Errorf("outbound target is required")
		}

		stepReq := req
		stepReq.Stream = true
		if step.Model != "" {
			stepReq.Model = step.Model
		}

		events, err := step.OutboundTarget.StreamCompletion(ctx, stepReq)
		if err == nil {
			return events, nil
		}

		lastErr = err
		if !provider.FallbackAllowed(string(step.OnError), provider.NormalizeError(err), i == len(plan.Steps)-1) {
			return nil, err
		}
	}

	return nil, lastErr
}
