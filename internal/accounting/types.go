package accounting

import (
	"time"

	"github.com/ryanycheng/Syrogo/internal/runtime"
)

type Window string

const (
	WindowTotal Window = "total"
	WindowDay   Window = "day"
	WindowWeek  Window = "week"
	WindowMonth Window = "month"
)

type Query struct {
	GroupBy   string
	Window    Window
	Bucket    string
	StartDate string
	EndDate   string
}

type RecentRecordsQuery struct {
	Since time.Time
	Limit int
}

type StatsItem struct {
	Value                  string             `json:"value"`
	Date                   string             `json:"date,omitempty"`
	Agent                  string             `json:"agent,omitempty"`
	SessionID              string             `json:"session_id,omitempty"`
	Model                  string             `json:"model,omitempty"`
	RequestCount           int                `json:"request_count"`
	SuccessCount           int                `json:"success_count"`
	ErrorCount             int                `json:"error_count"`
	FallbackCount          int                `json:"fallback_count"`
	InputTokens            int                `json:"input_tokens"`
	OutputTokens           int                `json:"output_tokens"`
	CachedInputReadTokens  int                `json:"cached_input_read_tokens"`
	CachedInputWriteTokens int                `json:"cached_input_write_tokens"`
	CacheReadTokens        int                `json:"cache_read_tokens"`
	CacheCreateTokens      int                `json:"cache_create_tokens"`
	TotalTokens            int                `json:"total_tokens"`
	CostUSD                float64            `json:"cost_usd"`
	ProviderUsageCount     int                `json:"provider_usage_count"`
	EstimatedUsageCount    int                `json:"estimated_usage_count"`
	ToolUnits              map[string]float64 `json:"tool_units,omitempty"`
	LastSeenAt             string             `json:"last_seen_at"`
}

type snapshotState struct {
	Totals     map[string]map[string]StatsItem            `json:"totals"`
	Windows    map[string]map[string]map[string]StatsItem `json:"windows"`
	Cursor     snapshotCursor                             `json:"cursor"`
	CapturedAt string                                     `json:"captured_at"`
}

type snapshotCursor struct {
	RecordFile string `json:"record_file"`
	RecordLine int64  `json:"record_line"`
}

type recordEnvelope struct {
	Record runtime.UsageRecord `json:"record"`
}
