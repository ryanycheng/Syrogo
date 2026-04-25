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

type KeyUsageStats struct {
	Name                string `json:"name"`
	RequestCount        int    `json:"request_count"`
	InputTokens         int    `json:"input_tokens"`
	OutputTokens        int    `json:"output_tokens"`
	TotalTokens         int    `json:"total_tokens"`
	ProviderUsageCount  int    `json:"provider_usage_count"`
	EstimatedUsageCount int    `json:"estimated_usage_count"`
	LastSeenAt          string `json:"last_seen_at"`
}

type Dispatcher struct {
	mu    sync.Mutex
	stats map[string]KeyUsageStats
}

func NewDispatcher() *Dispatcher {
	return &Dispatcher{stats: make(map[string]KeyUsageStats)}
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
			d.recordUsage(plan.ClientName, resp.Usage)
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
			return d.wrapStream(plan.ClientName, events), nil
		}

		lastErr = err
		if !provider.FallbackAllowed(string(step.OnError), provider.NormalizeError(err), i == len(plan.Steps)-1) {
			return nil, err
		}
	}

	return nil, lastErr
}

func (d *Dispatcher) Snapshot() []KeyUsageStats {
	d.mu.Lock()
	defer d.mu.Unlock()

	items := make([]KeyUsageStats, 0, len(d.stats))
	for _, stat := range d.stats {
		items = append(items, stat)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Name < items[j].Name
	})
	return items
}

func (d *Dispatcher) wrapStream(clientName string, events <-chan runtime.StreamEvent) <-chan runtime.StreamEvent {
	out := make(chan runtime.StreamEvent)
	go func() {
		defer close(out)
		usageRecorded := false
		for event := range events {
			if !usageRecorded && event.Type == runtime.StreamEventUsage && event.Usage != nil {
				d.recordUsage(clientName, event.Usage)
				usageRecorded = true
			}
			if !usageRecorded && event.Type == runtime.StreamEventMessageEnd && event.Usage != nil {
				d.recordUsage(clientName, event.Usage)
				usageRecorded = true
			}
			out <- event
		}
	}()
	return out
}

func (d *Dispatcher) recordUsage(clientName string, usage *runtime.Usage) {
	if clientName == "" || usage == nil {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	stat := d.stats[clientName]
	stat.Name = clientName
	stat.RequestCount++
	stat.InputTokens += usage.InputTokens
	stat.OutputTokens += usage.OutputTokens
	stat.TotalTokens += usage.TotalTokens
	stat.LastSeenAt = time.Now().Format(time.RFC3339Nano)
	if usage.Source == runtime.UsageSourceProvider || usage.Source == runtime.UsageSourceProviderAPI {
		stat.ProviderUsageCount++
	}
	if usage.Source == runtime.UsageSourceEstimated {
		stat.EstimatedUsageCount++
	}
	d.stats[clientName] = stat
}
