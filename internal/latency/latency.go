package latency

import (
	"context"
	"sort"
	"sync"
	"time"
)

type contextKey string

const traceContextKey contextKey = "latency_trace"

type Trace struct {
	RequestID       string `json:"request_id"`
	Method          string `json:"method"`
	Path            string `json:"path"`
	Inbound         string `json:"inbound,omitempty"`
	InboundProtocol string `json:"inbound_protocol,omitempty"`
	ClientName      string `json:"client_name,omitempty"`
	ActiveTag       string `json:"active_tag,omitempty"`
	Status          int    `json:"status"`
	ErrorKind       string `json:"error_kind,omitempty"`
	StartedAt       string `json:"started_at"`
	FinishedAt      string `json:"finished_at,omitempty"`
	DurationMs      int64  `json:"duration_ms"`
	Spans           []Span `json:"spans"`
}

type Recorder struct {
	trace     Trace
	startedAt time.Time
	store     *Store
	mu        sync.Mutex
}

type Span struct {
	Name       string            `json:"name"`
	StartedAt  string            `json:"started_at"`
	DurationMs int64             `json:"duration_ms"`
	Attrs      map[string]string `json:"attrs,omitempty"`
}

type Store struct {
	mu     sync.Mutex
	limit  int
	traces []Trace
}

type Snapshot struct {
	Items []Trace `json:"items"`
}

type Summary struct {
	Count int                    `json:"count"`
	Total SummaryItem            `json:"total"`
	Spans map[string]SummaryItem `json:"spans"`
}

type SummaryItem struct {
	Count int   `json:"count"`
	AvgMs int64 `json:"avg_ms"`
	P50Ms int64 `json:"p50_ms"`
	P95Ms int64 `json:"p95_ms"`
	P99Ms int64 `json:"p99_ms"`
	MaxMs int64 `json:"max_ms"`
}

func NewStore(limit int) *Store {
	if limit <= 0 {
		limit = 200
	}
	return &Store{limit: limit}
}

func Start(ctx context.Context, store *Store, requestID, method, path string, startedAt time.Time) (context.Context, *Recorder) {
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	recorder := &Recorder{
		trace: Trace{
			RequestID: requestID,
			Method:    method,
			Path:      path,
			StartedAt: startedAt.UTC().Format(time.RFC3339Nano),
		},
		startedAt: startedAt,
		store:     store,
	}
	return context.WithValue(ctx, traceContextKey, recorder), recorder
}

func FromContext(ctx context.Context) *Recorder {
	if ctx == nil {
		return nil
	}
	recorder, _ := ctx.Value(traceContextKey).(*Recorder)
	return recorder
}

func (r *Recorder) SetRoute(inbound, protocol, clientName, activeTag string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.trace.Inbound = inbound
	r.trace.InboundProtocol = protocol
	r.trace.ClientName = clientName
	r.trace.ActiveTag = activeTag
}

func (r *Recorder) SetErrorKind(errorKind string) {
	if r == nil || errorKind == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.trace.ErrorKind = errorKind
}

func (r *Recorder) AddSpan(name string, startedAt time.Time, attrs map[string]string) {
	if r == nil || name == "" || startedAt.IsZero() {
		return
	}
	span := Span{
		Name:       name,
		StartedAt:  startedAt.UTC().Format(time.RFC3339Nano),
		DurationMs: time.Since(startedAt).Milliseconds(),
	}
	if len(attrs) > 0 {
		span.Attrs = make(map[string]string, len(attrs))
		for key, value := range attrs {
			if value != "" {
				span.Attrs[key] = value
			}
		}
		if len(span.Attrs) == 0 {
			span.Attrs = nil
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.trace.Spans = append(r.trace.Spans, span)
}

func (r *Recorder) Finish(status int, finishedAt time.Time) {
	if r == nil {
		return
	}
	if finishedAt.IsZero() {
		finishedAt = time.Now()
	}
	r.mu.Lock()
	r.trace.Status = status
	r.trace.FinishedAt = finishedAt.UTC().Format(time.RFC3339Nano)
	r.trace.DurationMs = finishedAt.Sub(r.startedAt).Milliseconds()
	snapshot := r.trace
	snapshot.Spans = append([]Span(nil), r.trace.Spans...)
	r.mu.Unlock()

	if r.store != nil {
		r.store.Record(snapshot)
	}
}

func RecordSpan(ctx context.Context, name string, startedAt time.Time, attrs map[string]string) {
	FromContext(ctx).AddSpan(name, startedAt, attrs)
}

func (s *Store) Record(trace Trace) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.traces = append(s.traces, trace)
	if overflow := len(s.traces) - s.limit; overflow > 0 {
		copy(s.traces, s.traces[overflow:])
		s.traces = s.traces[:s.limit]
	}
}

func (s *Store) Snapshot() Snapshot {
	if s == nil {
		return Snapshot{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]Trace, len(s.traces))
	for index, trace := range s.traces {
		items[index] = trace
		items[index].Spans = append([]Span(nil), trace.Spans...)
	}
	return Snapshot{Items: items}
}

func (s *Store) Summary() Summary {
	snapshot := s.Snapshot()
	totalDurations := make([]int64, 0, len(snapshot.Items))
	spanDurations := map[string][]int64{}
	for _, trace := range snapshot.Items {
		totalDurations = append(totalDurations, trace.DurationMs)
		for _, span := range trace.Spans {
			spanDurations[span.Name] = append(spanDurations[span.Name], span.DurationMs)
		}
	}
	spans := make(map[string]SummaryItem, len(spanDurations))
	for name, durations := range spanDurations {
		spans[name] = summarizeDurations(durations)
	}
	return Summary{
		Count: len(snapshot.Items),
		Total: summarizeDurations(totalDurations),
		Spans: spans,
	}
}

func summarizeDurations(durations []int64) SummaryItem {
	if len(durations) == 0 {
		return SummaryItem{}
	}
	values := append([]int64(nil), durations...)
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	var sum int64
	for _, value := range values {
		sum += value
	}
	return SummaryItem{
		Count: len(values),
		AvgMs: sum / int64(len(values)),
		P50Ms: percentile(values, 50),
		P95Ms: percentile(values, 95),
		P99Ms: percentile(values, 99),
		MaxMs: values[len(values)-1],
	}
}

func percentile(sorted []int64, p int) int64 {
	if len(sorted) == 0 {
		return 0
	}
	index := (len(sorted)*p + 99) / 100
	if index < 1 {
		index = 1
	}
	if index > len(sorted) {
		index = len(sorted)
	}
	return sorted[index-1]
}
