package execution

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/ryanycheng/Syrogo/internal/provider"
	"github.com/ryanycheng/Syrogo/internal/runtime"
)

type UsageStatsItem struct {
	Value                  string             `json:"value"`
	RequestCount           int                `json:"request_count"`
	SuccessCount           int                `json:"success_count"`
	ErrorCount             int                `json:"error_count"`
	FallbackCount          int                `json:"fallback_count"`
	InputTokens            int                `json:"input_tokens"`
	OutputTokens           int                `json:"output_tokens"`
	CachedInputReadTokens  int                `json:"cached_input_read_tokens"`
	CachedInputWriteTokens int                `json:"cached_input_write_tokens"`
	TotalTokens            int                `json:"total_tokens"`
	ProviderUsageCount     int                `json:"provider_usage_count"`
	EstimatedUsageCount    int                `json:"estimated_usage_count"`
	ToolUnits              map[string]float64 `json:"tool_units,omitempty"`
	LastSeenAt             string             `json:"last_seen_at"`
}

type Dispatcher struct {
	mu      sync.Mutex
	records []runtime.UsageRecord
}

func NewDispatcher() *Dispatcher {
	return &Dispatcher{}
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

func (d *Dispatcher) QueryUsage(groupBy string) ([]UsageStatsItem, error) {
	switch groupBy {
	case "key", "provider", "model", "inbound", "source", "outbound":
	default:
		return nil, fmt.Errorf("unsupported group_by %q", groupBy)
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	grouped := make(map[string]UsageStatsItem)
	for _, record := range d.records {
		value := usageGroupValue(record, groupBy)
		item := grouped[value]
		item.Value = value
		count := record.Breakdown.RequestCount
		if count <= 0 {
			count = 1
		}
		item.RequestCount += count
		if record.Status == runtime.UsageStatusSuccess {
			item.SuccessCount += count
		}
		if record.Status == runtime.UsageStatusError {
			item.ErrorCount += count
		}
		item.FallbackCount += record.FallbackCount
		item.InputTokens += record.Breakdown.InputTokens
		item.OutputTokens += record.Breakdown.OutputTokens
		item.CachedInputReadTokens += record.Breakdown.CachedInputReadTokens
		item.CachedInputWriteTokens += record.Breakdown.CachedInputWriteTokens
		item.TotalTokens += record.Breakdown.TotalTokens
		if record.UsageSource == runtime.UsageSourceProvider || record.UsageSource == runtime.UsageSourceProviderAPI {
			item.ProviderUsageCount += count
		}
		if record.UsageSource == runtime.UsageSourceEstimated {
			item.EstimatedUsageCount += count
		}
		if len(record.Breakdown.ToolUnits) > 0 {
			if item.ToolUnits == nil {
				item.ToolUnits = make(map[string]float64)
			}
			for name, units := range record.Breakdown.ToolUnits {
				item.ToolUnits[name] += units
			}
		}
		seenAt := record.FinishedAt
		if seenAt == "" {
			seenAt = record.StartedAt
		}
		if seenAt > item.LastSeenAt {
			item.LastSeenAt = seenAt
		}
		grouped[value] = item
	}

	items := make([]UsageStatsItem, 0, len(grouped))
	for _, item := range grouped {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Value < items[j].Value
	})
	return items, nil
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
	d.mu.Lock()
	defer d.mu.Unlock()
	d.records = append(d.records, record)
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

func usageGroupValue(record runtime.UsageRecord, groupBy string) string {
	switch groupBy {
	case "key":
		return nonEmpty(record.ClientName, "unknown")
	case "provider":
		return nonEmpty(record.ProviderName, nonEmpty(record.OutboundName, "unknown"))
	case "model":
		return nonEmpty(record.ExecutedModel, nonEmpty(record.RequestedModel, "unknown"))
	case "inbound":
		return nonEmpty(record.InboundName, "unknown")
	case "source":
		return nonEmpty(string(record.UsageSource), "unknown")
	case "outbound":
		return nonEmpty(record.OutboundName, "unknown")
	default:
		return "unknown"
	}
}

func nonEmpty(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
