package accounting

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/ryanycheng/Syrogo/internal/runtime"
)

type MemoryStore struct {
	mu      sync.Mutex
	totals  map[string]map[string]StatsItem
	windows map[Window]map[string]map[string]StatsItem
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		totals: make(map[string]map[string]StatsItem),
		windows: map[Window]map[string]map[string]StatsItem{
			WindowDay:   make(map[string]map[string]StatsItem),
			WindowWeek:  make(map[string]map[string]StatsItem),
			WindowMonth: make(map[string]map[string]StatsItem),
		},
	}
}

func (s *MemoryStore) Record(record runtime.UsageRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applyRecord(record)
}

func (s *MemoryStore) Query(query Query) ([]StatsItem, error) {
	if query.GroupBy == "" {
		query.GroupBy = "key"
	}
	if query.Window == "" {
		query.Window = WindowTotal
	}
	if !supportedGroupBy(query.GroupBy) {
		return nil, fmt.Errorf("unsupported group_by %q", query.GroupBy)
	}
	if !supportedWindow(query.Window) {
		return nil, fmt.Errorf("unsupported window %q", query.Window)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var source map[string]StatsItem
	switch query.Window {
	case WindowTotal:
		source = s.totals[query.GroupBy]
	case WindowDay, WindowWeek, WindowMonth:
		if query.Bucket == "" {
			return nil, fmt.Errorf("bucket is required when window=%q", query.Window)
		}
		source = s.windows[query.Window][bucketKey(query.Window, query.Bucket, query.GroupBy)]
	}
	items := make([]StatsItem, 0, len(source))
	for _, item := range source {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Value < items[j].Value })
	return items, nil
}

func (s *MemoryStore) Close(context.Context) error {
	return nil
}

func (s *MemoryStore) applyRecord(record runtime.UsageRecord) {
	s.applyToGroup(s.ensureTotalGroup(record, "key"), usageGroupValue(record, "key"), record)
	s.applyToGroup(s.ensureTotalGroup(record, "provider"), usageGroupValue(record, "provider"), record)
	s.applyToGroup(s.ensureTotalGroup(record, "model"), usageGroupValue(record, "model"), record)
	s.applyToGroup(s.ensureTotalGroup(record, "inbound"), usageGroupValue(record, "inbound"), record)
	s.applyToGroup(s.ensureTotalGroup(record, "source"), usageGroupValue(record, "source"), record)
	s.applyToGroup(s.ensureTotalGroup(record, "outbound"), usageGroupValue(record, "outbound"), record)

	day, week, month := timeBuckets(record)
	for _, groupBy := range []string{"key", "provider", "model", "inbound", "source", "outbound"} {
		value := usageGroupValue(record, groupBy)
		s.applyToGroup(s.ensureWindowGroup(WindowDay, day, groupBy), value, record)
		s.applyToGroup(s.ensureWindowGroup(WindowWeek, week, groupBy), value, record)
		s.applyToGroup(s.ensureWindowGroup(WindowMonth, month, groupBy), value, record)
	}
}

func (s *MemoryStore) ensureTotalGroup(_ runtime.UsageRecord, groupBy string) map[string]StatsItem {
	group := s.totals[groupBy]
	if group == nil {
		group = make(map[string]StatsItem)
		s.totals[groupBy] = group
	}
	return group
}

func (s *MemoryStore) ensureWindowGroup(window Window, bucket, groupBy string) map[string]StatsItem {
	groups := s.windows[window]
	if groups == nil {
		groups = make(map[string]map[string]StatsItem)
		s.windows[window] = groups
	}
	key := bucketKey(window, bucket, groupBy)
	group := groups[key]
	if group == nil {
		group = make(map[string]StatsItem)
		groups[key] = group
	}
	return group
}

func (s *MemoryStore) applyToGroup(group map[string]StatsItem, value string, record runtime.UsageRecord) {
	item := group[value]
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
	group[value] = item
}

func supportedGroupBy(groupBy string) bool {
	switch groupBy {
	case "key", "provider", "model", "inbound", "source", "outbound":
		return true
	default:
		return false
	}
}

func supportedWindow(window Window) bool {
	switch window {
	case WindowTotal, WindowDay, WindowWeek, WindowMonth:
		return true
	default:
		return false
	}
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

func timeBuckets(record runtime.UsageRecord) (day, week, month string) {
	timestamp := record.FinishedAt
	if timestamp == "" {
		timestamp = record.StartedAt
	}
	parsed, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return "unknown", "unknown", "unknown"
	}
	parsed = parsed.UTC()
	year, isoWeek := parsed.ISOWeek()
	return parsed.Format("2006-01-02"), fmt.Sprintf("%04d-W%02d", year, isoWeek), parsed.Format("2006-01")
}

func bucketKey(window Window, bucket, groupBy string) string {
	return fmt.Sprintf("%s:%s:%s", window, bucket, groupBy)
}
