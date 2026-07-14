package gateway

import (
	"net/http"
	"time"

	"github.com/ryanycheng/Syrogo/internal/latency"
)

type activeLatencyItem struct {
	latency.Trace
	ElapsedMs           int64 `json:"elapsed_ms"`
	WaitingFirstTokenMs int64 `json:"waiting_first_token_ms,omitempty"`
	StreamIdleMs        int64 `json:"stream_idle_ms,omitempty"`
}

func (h *Handler) handleAdminActiveLatency(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAdmin(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	now := time.Now()
	snapshot := h.runtimeState().Dispatcher.QueryActiveLatency()
	items := make([]activeLatencyItem, 0, len(snapshot.Items))
	for _, trace := range snapshot.Items {
		item := activeLatencyItem{Trace: trace}
		item.ElapsedMs = elapsedSince(trace.StartedAt, now)
		if trace.StreamState == latency.StreamStateWaitingFirstToken {
			item.WaitingFirstTokenMs = elapsedSince(trace.ProviderSelectedAt, now)
		}
		if trace.StreamState == latency.StreamStateStreaming {
			item.StreamIdleMs = elapsedSince(trace.LastStreamEventAt, now)
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func elapsedSince(value string, now time.Time) int64 {
	if value == "" {
		return 0
	}
	startedAt, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || now.Before(startedAt) {
		return 0
	}
	return now.Sub(startedAt).Milliseconds()
}
