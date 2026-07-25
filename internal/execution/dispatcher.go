package execution

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ryanycheng/Syrogo/internal/accounting"
	"github.com/ryanycheng/Syrogo/internal/latency"
	"github.com/ryanycheng/Syrogo/internal/provider"
	"github.com/ryanycheng/Syrogo/internal/quota"
	"github.com/ryanycheng/Syrogo/internal/runtime"
)

type Dispatcher struct {
	store           accounting.Store
	quotaTracker    *quota.Tracker
	healthTracker   *provider.HealthTracker
	eventRecorder   *quota.EventRecorder
	latencyStore    *latency.Store
	priceCalculator accounting.PriceCalculator
}

func NewDispatcher() *Dispatcher {
	return NewDispatcherWithStore(accounting.NewMemoryStore())
}

func NewDispatcherWithStore(store accounting.Store) *Dispatcher {
	return NewDispatcherWithStoreAndQuota(store, nil)
}

func NewDispatcherWithStoreAndQuota(store accounting.Store, quotaTracker *quota.Tracker) *Dispatcher {
	return NewDispatcherWithStoreQuotaAndEvents(store, quotaTracker, nil)
}

func NewDispatcherWithStoreQuotaAndEvents(store accounting.Store, quotaTracker *quota.Tracker, eventRecorder *quota.EventRecorder) *Dispatcher {
	return NewDispatcherWithStoreQuotaHealthAndEvents(store, quotaTracker, nil, eventRecorder)
}

func NewDispatcherWithStoreQuotaHealthAndEvents(store accounting.Store, quotaTracker *quota.Tracker, healthTracker *provider.HealthTracker, eventRecorder *quota.EventRecorder) *Dispatcher {
	return NewDispatcherWithStoreQuotaHealthEventsAndLatency(store, quotaTracker, healthTracker, eventRecorder, nil)
}

func NewDispatcherWithStoreQuotaHealthEventsAndLatency(store accounting.Store, quotaTracker *quota.Tracker, healthTracker *provider.HealthTracker, eventRecorder *quota.EventRecorder, latencyStore *latency.Store) *Dispatcher {
	return NewDispatcherWithStoreQuotaHealthEventsLatencyAndPricing(store, quotaTracker, healthTracker, eventRecorder, latencyStore, accounting.PriceCalculator{})
}

func NewDispatcherWithStoreQuotaHealthEventsLatencyAndPricing(store accounting.Store, quotaTracker *quota.Tracker, healthTracker *provider.HealthTracker, eventRecorder *quota.EventRecorder, latencyStore *latency.Store, priceCalculator accounting.PriceCalculator) *Dispatcher {
	if store == nil {
		store = accounting.NewMemoryStore()
	}
	return &Dispatcher{store: store, quotaTracker: quotaTracker, healthTracker: healthTracker, eventRecorder: eventRecorder, latencyStore: latencyStore, priceCalculator: priceCalculator}
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
		if step.ModelUnavailable {
			lastErr = selectPreferredError(lastErr, modelUnavailableError(plan, req))
			continue
		}
		if step.OutboundTarget == nil {
			return runtime.Response{}, fmt.Errorf("outbound target is required")
		}

		stepReq := req
		if step.Model != "" {
			stepReq.Model = step.Model
		}

		decision := d.beforeAttempt(step.OutboundName)
		if !decision.Allowed {
			d.recordOutboundLimited(step.OutboundName, decision)
			lastErr = selectPreferredError(lastErr, provider.NewQuotaExceededError(fmt.Errorf("outbound %q quota %s", step.OutboundName, decision.Reason)))
			continue
		}
		healthDecision := d.beforeHealthAttempt(step.OutboundName)
		if !healthDecision.Allowed {
			d.recordProviderHealthLimited(step.OutboundName, healthDecision)
			lastErr = selectPreferredError(lastErr, provider.NewRetryableError(fmt.Errorf("outbound %q health %s", step.OutboundName, healthDecision.Reason)))
			continue
		}

		attemptStartedAt := time.Now()
		resp, err := step.OutboundTarget.ChatCompletion(ctx, stepReq)
		latency.RecordSpan(ctx, "provider_dispatch", attemptStartedAt, map[string]string{
			"outbound": step.OutboundName,
			"protocol": step.OutboundProtocol,
		})
		if err == nil {
			latency.FromContext(ctx).SetFallbackCount(i)
			d.recordSuccess(step.OutboundName, normalizedUsageTokens(resp.Usage), decision.Probe, healthDecision.Probe)
			d.record(d.finalizeUsageRecord(ctx, plan, step, stepReq.Model, resp.Model, resp.Usage, runtime.UsageStatusSuccess, "", startedAt, time.Now(), i))
			return resp, nil
		}

		lastErr = selectPreferredError(lastErr, err)
		errorKind := provider.NormalizeError(err)
		d.recordProviderError(step.OutboundName, errorKind)
		if !provider.FallbackAllowed(string(step.OnError), errorKind, i == len(plan.Steps)-1) {
			preferredKind := provider.NormalizeError(lastErr)
			d.record(d.finalizeUsageRecord(ctx, plan, step, stepReq.Model, stepReq.Model, nil, runtime.UsageStatusError, string(preferredKind), startedAt, time.Now(), i))
			return runtime.Response{}, lastErr
		}
	}

	if len(plan.Steps) > 0 {
		last := plan.Steps[len(plan.Steps)-1]
		d.record(d.finalizeUsageRecord(ctx, plan, last, req.Model, req.Model, nil, runtime.UsageStatusError, string(provider.NormalizeError(lastErr)), startedAt, time.Now(), len(plan.Steps)-1))
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
		if step.ModelUnavailable {
			lastErr = selectPreferredError(lastErr, modelUnavailableError(plan, req))
			continue
		}
		if step.OutboundTarget == nil {
			return nil, fmt.Errorf("outbound target is required")
		}

		stepReq := req
		stepReq.Stream = true
		if step.Model != "" {
			stepReq.Model = step.Model
		}

		decision := d.beforeAttempt(step.OutboundName)
		if !decision.Allowed {
			d.recordOutboundLimited(step.OutboundName, decision)
			lastErr = selectPreferredError(lastErr, provider.NewQuotaExceededError(fmt.Errorf("outbound %q quota %s", step.OutboundName, decision.Reason)))
			continue
		}
		healthDecision := d.beforeHealthAttempt(step.OutboundName)
		if !healthDecision.Allowed {
			d.recordProviderHealthLimited(step.OutboundName, healthDecision)
			lastErr = selectPreferredError(lastErr, provider.NewRetryableError(fmt.Errorf("outbound %q health %s", step.OutboundName, healthDecision.Reason)))
			continue
		}

		attemptStartedAt := time.Now()
		latency.FromContext(ctx).SetStreamState(latency.StreamStateDispatching)
		events, err := step.OutboundTarget.StreamCompletion(ctx, stepReq)
		latency.RecordSpan(ctx, "provider_dispatch", attemptStartedAt, map[string]string{
			"outbound": step.OutboundName,
			"protocol": step.OutboundProtocol,
			"stream":   "true",
		})
		if err == nil {
			recorder := latency.FromContext(ctx)
			recorder.SetFallbackCount(i)
			recorder.SetProvider(step.OutboundName, step.OutboundProtocol, time.Now())
			recorder.SetStreamState(latency.StreamStateWaitingFirstToken)
			return d.wrapStream(ctx, plan, step, stepReq.Model, i, startedAt, events, decision.Probe, healthDecision.Probe), nil
		}

		lastErr = selectPreferredError(lastErr, err)
		errorKind := provider.NormalizeError(err)
		d.recordProviderError(step.OutboundName, errorKind)
		if !provider.FallbackAllowed(string(step.OnError), errorKind, i == len(plan.Steps)-1) {
			preferredKind := provider.NormalizeError(lastErr)
			d.record(d.finalizeUsageRecord(ctx, plan, step, stepReq.Model, stepReq.Model, nil, runtime.UsageStatusError, string(preferredKind), startedAt, time.Now(), i))
			return nil, lastErr
		}
	}

	if len(plan.Steps) > 0 {
		last := plan.Steps[len(plan.Steps)-1]
		d.record(d.finalizeUsageRecord(ctx, plan, last, req.Model, req.Model, nil, runtime.UsageStatusError, string(provider.NormalizeError(lastErr)), startedAt, time.Now(), len(plan.Steps)-1))
	}
	return nil, lastErr
}

func modelUnavailableError(plan runtime.ExecutionPlan, req runtime.Request) error {
	model := plan.RequestedModel
	if model == "" {
		model = req.Model
	}
	if model == "" {
		return provider.NewModelUnavailableError(errors.New("requested model is unavailable"))
	}
	return provider.NewModelUnavailableError(fmt.Errorf("model %q is unavailable", model))
}

func selectPreferredError(current, candidate error) error {
	if current == nil {
		return candidate
	}
	if candidate == nil {
		return current
	}
	if preferredErrorPriority(candidate) > preferredErrorPriority(current) {
		return candidate
	}
	return current
}

func preferredErrorPriority(err error) int {
	kind := provider.NormalizeError(err)
	var providerErr *provider.ProviderError
	hasHTTPMetadata := provider.AsProviderError(err, &providerErr) && providerErr != nil &&
		(providerErr.StatusCode != 0 || providerErr.RequestID != "" || providerErr.RetryAfter != "")

	switch kind {
	case provider.ErrorKindQuotaExceeded, provider.ErrorKindAuthFailed:
		if hasHTTPMetadata {
			return 5
		}
		return 4
	case provider.ErrorKindFatal, provider.ErrorKindCapabilityUnsupported:
		return 3
	case provider.ErrorKindTimeout, provider.ErrorKindUpstreamServerError, provider.ErrorKindRetryable:
		return 2
	default:
		return 1
	}
}

func (d *Dispatcher) QueryUsage(groupBy string) ([]accounting.StatsItem, error) {
	return d.QueryUsageBy(accounting.Query{GroupBy: groupBy, Window: accounting.WindowTotal})
}

func (d *Dispatcher) QueryUsageBy(query accounting.Query) ([]accounting.StatsItem, error) {
	return d.store.Query(query)
}

func (d *Dispatcher) QueryRecentUsage(query accounting.RecentRecordsQuery) ([]runtime.UsageRecord, error) {
	return d.store.RecentRecords(query)
}

func (d *Dispatcher) QueryQuota() []quota.SnapshotItem {
	if d.quotaTracker == nil {
		return nil
	}
	return d.quotaTracker.Snapshot()
}

func (d *Dispatcher) QueryQuotaEvents() []quota.Event {
	if d.eventRecorder == nil {
		return nil
	}
	return d.eventRecorder.Snapshot()
}

func (d *Dispatcher) QueryProviderHealth() []provider.HealthSnapshotItem {
	if d.healthTracker == nil {
		return nil
	}
	return d.healthTracker.Snapshot()
}

func (d *Dispatcher) QueryLatency() latency.Snapshot {
	if d.latencyStore == nil {
		return latency.Snapshot{}
	}
	return d.latencyStore.Snapshot()
}

func (d *Dispatcher) QueryActiveLatency() latency.Snapshot {
	if d.latencyStore == nil {
		return latency.Snapshot{}
	}
	return d.latencyStore.ActiveSnapshot()
}

func (d *Dispatcher) QueryLatencySummary() latency.Summary {
	if d.latencyStore == nil {
		return latency.Summary{}
	}
	return d.latencyStore.Summary()
}

func (d *Dispatcher) Close(ctx context.Context) error {
	return d.store.Close(ctx)
}

func (d *Dispatcher) wrapStream(ctx context.Context, plan runtime.ExecutionPlan, step runtime.ExecutionStep, requestedModel string, fallbackCount int, startedAt time.Time, events <-chan runtime.StreamEvent, quotaProbe bool, healthProbe bool) <-chan runtime.StreamEvent {
	out := make(chan runtime.StreamEvent)
	go func() {
		defer close(out)
		var usage *runtime.Usage
		executedModel := requestedModel
		recorded := false
		recorder := latency.FromContext(ctx)
		for {
			select {
			case <-ctx.Done():
				recorder.SetStreamState(latency.StreamStateError)
				return
			case event, ok := <-events:
				if !ok {
					if ctx.Err() != nil {
						recorder.SetStreamState(latency.StreamStateError)
						return
					}
					if !recorded {
						recorder.SetStreamState(latency.StreamStateCompleted)
						d.recordSuccess(step.OutboundName, normalizedUsageTokens(usage), quotaProbe, healthProbe)
						d.record(d.finalizeUsageRecord(ctx, plan, step, requestedModel, executedModel, usage, runtime.UsageStatusSuccess, "", startedAt, time.Now(), fallbackCount))
					}
					return
				}
				recorder.MarkStreamEvent(time.Now())
				if isFirstContentDelta(event) {
					recorder.MarkFirstToken(time.Now())
				}
				if event.Model != "" {
					executedModel = event.Model
				}
				if event.Usage != nil {
					copied := *event.Usage
					usage = &copied
				}
				if event.Type == runtime.StreamEventError && event.Err != nil && !recorded {
					recorder.SetStreamState(latency.StreamStateError)
					errorKind := provider.NormalizeError(event.Err)
					d.recordProviderError(step.OutboundName, errorKind)
					d.record(d.finalizeUsageRecord(ctx, plan, step, requestedModel, executedModel, usage, runtime.UsageStatusError, string(errorKind), startedAt, time.Now(), fallbackCount))
					recorded = true
				}
				select {
				case out <- event:
				case <-ctx.Done():
					recorder.SetStreamState(latency.StreamStateError)
					return
				}
			}
		}
	}()
	return out
}

func isFirstContentDelta(event runtime.StreamEvent) bool {
	if event.Type != runtime.StreamEventContentDelta {
		return false
	}
	if event.Delta != nil && (event.Delta.Text != "" || len(event.Delta.Data) > 0) {
		return true
	}
	return event.ToolCall != nil && (event.ToolCall.ID != "" || event.ToolCall.Name != "" || event.ToolCall.Arguments != "" || event.ToolCall.Input != "")
}

func (d *Dispatcher) beforeAttempt(outbound string) quota.Decision {
	if d.quotaTracker == nil {
		return quota.Decision{Allowed: true}
	}
	return d.quotaTracker.BeforeAttempt(outbound)
}

func (d *Dispatcher) beforeHealthAttempt(outbound string) provider.HealthDecision {
	if d.healthTracker == nil {
		return provider.HealthDecision{Allowed: true}
	}
	return d.healthTracker.BeforeAttempt(outbound)
}

func (d *Dispatcher) recordSuccess(outbound string, tokens int, quotaProbe bool, healthProbe bool) {
	if d.quotaTracker != nil {
		d.quotaTracker.RecordSuccess(outbound, tokens)
	}
	if d.healthTracker != nil {
		d.healthTracker.RecordSuccess(outbound)
	}
	if quotaProbe {
		d.eventRecorder.Record(quota.Event{Type: quota.EventOutboundProbeSucceeded, Outbound: outbound})
	}
	if healthProbe {
		d.eventRecorder.Record(quota.Event{Type: quota.EventProviderProbeSucceeded, Outbound: outbound})
	}
}

func (d *Dispatcher) recordProviderError(outbound string, kind provider.ErrorKind) {
	if d.healthTracker != nil {
		d.healthTracker.RecordFailure(outbound, kind)
	}
	if d.quotaTracker != nil && kind == provider.ErrorKindQuotaExceeded {
		d.quotaTracker.RecordQuotaExceeded(outbound)
		d.eventRecorder.Record(quota.Event{Type: quota.EventOutboundQuotaExceeded, Outbound: outbound, Reason: string(kind)})
	}
}

func (d *Dispatcher) recordOutboundLimited(outbound string, decision quota.Decision) {
	d.eventRecorder.Record(quota.Event{Type: quota.EventOutboundLimited, Outbound: outbound, Reason: decision.Reason, RetryAfter: formatRetryAfter(decision.RetryAfter)})
}

func (d *Dispatcher) recordProviderHealthLimited(outbound string, decision provider.HealthDecision) {
	d.eventRecorder.Record(quota.Event{Type: quota.EventProviderHealthLimited, Outbound: outbound, Reason: decision.Reason, RetryAfter: formatRetryAfter(decision.RetryAfter)})
}

func formatRetryAfter(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func (d *Dispatcher) record(record runtime.UsageRecord) {
	d.store.Record(record)
}

func normalizedUsageTokens(usage *runtime.Usage) int {
	if usage == nil {
		return 0
	}
	if usage.TotalTokens != 0 {
		return usage.TotalTokens
	}
	return usage.InputTokens + usage.OutputTokens
}

func (d *Dispatcher) finalizeUsageRecord(ctx context.Context, plan runtime.ExecutionPlan, step runtime.ExecutionStep, requestedModel, executedModel string, usage *runtime.Usage, status runtime.UsageStatus, errorKind string, startedAt, finishedAt time.Time, fallbackCount int) runtime.UsageRecord {
	breakdown := runtime.UsageBreakdown{RequestCount: 1}
	usageSource := runtime.UsageSource("")
	if usage != nil {
		breakdown.InputTokens = usage.InputTokens
		breakdown.OutputTokens = usage.OutputTokens
		breakdown.CachedInputReadTokens = usage.CachedInputReadTokens
		breakdown.CachedInputWriteTokens = usage.CachedInputWriteTokens
		breakdown.TotalTokens = normalizedUsageTokens(usage)
		usageSource = usage.Source
	}
	if executedModel == "" {
		executedModel = requestedModel
	}
	requestID, _ := ctx.Value(runtime.ContextKeyRequestID).(string)
	sessionID, _ := ctx.Value(runtime.ContextKeySessionID).(string)
	agent, _ := ctx.Value(runtime.ContextKeyAgent).(string)
	providerName := providerName(step)
	costUSD := d.priceCalculator.CostUSD(providerName, executedModel, breakdown)
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
		ProviderName:     providerName,
		RequestedModel:   requestedModel,
		ExecutedModel:    executedModel,
		UsageSource:      usageSource,
		Status:           status,
		ErrorKind:        errorKind,
		SessionID:        sessionID,
		Agent:            agent,
		CostUSD:          costUSD,
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
