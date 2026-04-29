package execution

import (
	"context"
	"fmt"
	"time"

	"github.com/ryanycheng/Syrogo/internal/accounting"
	"github.com/ryanycheng/Syrogo/internal/provider"
	"github.com/ryanycheng/Syrogo/internal/runtime"
)

type Dispatcher struct {
	store accounting.Store
}

func NewDispatcher() *Dispatcher {
	return NewDispatcherWithStore(accounting.NewMemoryStore())
}

func NewDispatcherWithStore(store accounting.Store) *Dispatcher {
	if store == nil {
		store = accounting.NewMemoryStore()
	}
	return &Dispatcher{store: store}
}

func (d *Dispatcher) Dispatch(ctx context.Context, req runtime.Request, plan runtime.ExecutionPlan) (runtime.Response, error) {
	if len(plan.Steps) == 0 {
		return runtime.Response{}, fmt.Errorf("execution plan has no steps")
	}

	startedAt := time.Now()
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
			d.record(finalizeUsageRecord(ctx, plan, step, stepReq.Model, resp.Model, resp.Usage, runtime.UsageStatusSuccess, startedAt, time.Now(), i))
			return resp, nil
		}

		lastErr = err
		if !provider.FallbackAllowed(string(step.OnError), provider.NormalizeError(err), i == len(plan.Steps)-1) {
			d.record(finalizeUsageRecord(ctx, plan, step, stepReq.Model, stepReq.Model, nil, runtime.UsageStatusError, startedAt, time.Now(), i))
			return runtime.Response{}, err
		}
	}

	if len(plan.Steps) > 0 {
		last := plan.Steps[len(plan.Steps)-1]
		d.record(finalizeUsageRecord(ctx, plan, last, req.Model, req.Model, nil, runtime.UsageStatusError, startedAt, time.Now(), len(plan.Steps)-1))
	}
	return runtime.Response{}, lastErr
}

func (d *Dispatcher) DispatchStream(ctx context.Context, req runtime.Request, plan runtime.ExecutionPlan) (<-chan runtime.StreamEvent, error) {
	if len(plan.Steps) == 0 {
		return nil, fmt.Errorf("execution plan has no steps")
	}

	startedAt := time.Now()
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
			return d.wrapStream(ctx, plan, step, stepReq.Model, i, startedAt, events), nil
		}

		lastErr = err
		if !provider.FallbackAllowed(string(step.OnError), provider.NormalizeError(err), i == len(plan.Steps)-1) {
			d.record(finalizeUsageRecord(ctx, plan, step, stepReq.Model, stepReq.Model, nil, runtime.UsageStatusError, startedAt, time.Now(), i))
			return nil, err
		}
	}

	if len(plan.Steps) > 0 {
		last := plan.Steps[len(plan.Steps)-1]
		d.record(finalizeUsageRecord(ctx, plan, last, req.Model, req.Model, nil, runtime.UsageStatusError, startedAt, time.Now(), len(plan.Steps)-1))
	}
	return nil, lastErr
}

func (d *Dispatcher) QueryUsage(groupBy string) ([]accounting.StatsItem, error) {
	return d.QueryUsageBy(accounting.Query{GroupBy: groupBy, Window: accounting.WindowTotal})
}

func (d *Dispatcher) QueryUsageBy(query accounting.Query) ([]accounting.StatsItem, error) {
	return d.store.Query(query)
}

func (d *Dispatcher) Close(ctx context.Context) error {
	return d.store.Close(ctx)
}

func (d *Dispatcher) wrapStream(ctx context.Context, plan runtime.ExecutionPlan, step runtime.ExecutionStep, requestedModel string, fallbackCount int, startedAt time.Time, events <-chan runtime.StreamEvent) <-chan runtime.StreamEvent {
	out := make(chan runtime.StreamEvent)
	go func() {
		defer close(out)
		var usage *runtime.Usage
		executedModel := requestedModel
		recorded := false
		for event := range events {
			if event.Model != "" {
				executedModel = event.Model
			}
			if event.Usage != nil {
				copied := *event.Usage
				usage = &copied
			}
			if event.Type == runtime.StreamEventError && event.Err != nil && !recorded {
				d.record(finalizeUsageRecord(ctx, plan, step, requestedModel, executedModel, usage, runtime.UsageStatusError, startedAt, time.Now(), fallbackCount))
				recorded = true
			}
			out <- event
		}
		if !recorded {
			d.record(finalizeUsageRecord(ctx, plan, step, requestedModel, executedModel, usage, runtime.UsageStatusSuccess, startedAt, time.Now(), fallbackCount))
		}
	}()
	return out
}

func (d *Dispatcher) record(record runtime.UsageRecord) {
	d.store.Record(record)
}

func finalizeUsageRecord(ctx context.Context, plan runtime.ExecutionPlan, step runtime.ExecutionStep, requestedModel, executedModel string, usage *runtime.Usage, status runtime.UsageStatus, startedAt, finishedAt time.Time, fallbackCount int) runtime.UsageRecord {
	breakdown := runtime.UsageBreakdown{RequestCount: 1}
	usageSource := runtime.UsageSource("")
	if usage != nil {
		totalTokens := usage.TotalTokens
		if totalTokens == 0 {
			totalTokens = usage.InputTokens + usage.OutputTokens
		}
		breakdown.InputTokens = usage.InputTokens
		breakdown.OutputTokens = usage.OutputTokens
		breakdown.TotalTokens = totalTokens
		usageSource = usage.Source
	}
	if executedModel == "" {
		executedModel = requestedModel
	}
	requestID, _ := ctx.Value(runtime.ContextKeyRequestID).(string)
	return runtime.UsageRecord{
		RequestID:        requestID,
		ClientName:       plan.ClientName,
		InboundName:      plan.InboundName,
		InboundProtocol:  plan.InboundProtocol,
		ActiveTag:        plan.ActiveTag,
		MatchedRule:      plan.MatchedRule,
		Strategy:         plan.Strategy,
		OutboundName:     step.OutboundName,
		OutboundProtocol: step.OutboundProtocol,
		ProviderName:     providerName(step),
		RequestedModel:   requestedModel,
		ExecutedModel:    executedModel,
		UsageSource:      usageSource,
		Status:           status,
		Breakdown:        breakdown,
		StartedAt:        startedAt.UTC().Format(time.RFC3339Nano),
		FinishedAt:       finishedAt.UTC().Format(time.RFC3339Nano),
		LatencyMs:        finishedAt.Sub(startedAt).Milliseconds(),
		FallbackCount:    fallbackCount,
	}
}

func providerName(step runtime.ExecutionStep) string {
	if step.OutboundTarget == nil {
		return step.OutboundName
	}
	return step.OutboundTarget.Name()
}
