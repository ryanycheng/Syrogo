package accounting

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/ryanycheng/Syrogo/internal/runtime"
)

const defaultRecentRecordsLimit = 10000

var usageGroupBys = []string{"key", "provider", "model", "inbound", "source", "outbound", "error_kind", "date", "agent", "session"}

type MemoryStore struct {
	mu                sync.Mutex
	totals            map[string]map[string]StatsItem
	windows           map[Window]map[string]map[string]StatsItem
	recent            []runtime.UsageRecord
	trackingStartedAt time.Time
	coverageKnown     bool
}

func NewMemoryStore() *MemoryStore {
	return newMemoryStore(time.Now().UTC(), true)
}

func newMemoryStore(trackingStartedAt time.Time, coverageKnown bool) *MemoryStore {
	return &MemoryStore{
		totals: make(map[string]map[string]StatsItem),
		windows: map[Window]map[string]map[string]StatsItem{
			WindowDay:   make(map[string]map[string]StatsItem),
			WindowWeek:  make(map[string]map[string]StatsItem),
			WindowMonth: make(map[string]map[string]StatsItem),
		},
		trackingStartedAt: trackingStartedAt.UTC(),
		coverageKnown:     coverageKnown,
	}
}

func (s *MemoryStore) Record(record runtime.UsageRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applyRecord(record)
	s.recent = append(s.recent, record)
	if len(s.recent) > defaultRecentRecordsLimit {
		copy(s.recent, s.recent[len(s.recent)-defaultRecentRecordsLimit:])
		s.recent = s.recent[:defaultRecentRecordsLimit]
	}
}

func (s *MemoryStore) Query(query Query) ([]StatsItem, error) {
	hasLegacy := query.Window != "" || query.Bucket != ""
	hasDateRange := query.StartDate != "" || query.EndDate != ""
	if query.ClientName != "" {
		if query.GroupBy != "date" {
			return nil, fmt.Errorf("client_name requires group_by=date")
		}
		if !hasDateRange {
			return nil, fmt.Errorf("client_name requires start_date and end_date")
		}
		if hasLegacy {
			return nil, fmt.Errorf("client_name start_date/end_date cannot be combined with window/bucket")
		}
	}
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

	if hasDateRange {
		start, end, err := parseDateRange(query.StartDate, query.EndDate)
		if err != nil {
			return nil, err
		}
		if query.Window != WindowTotal || query.Bucket != "" {
			return nil, fmt.Errorf("start_date/end_date cannot be combined with window/bucket")
		}
		return s.queryDateRange(query.GroupBy, query.ClientName, start, end), nil
	}

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

func (s *MemoryStore) queryDateRange(groupBy, clientName string, start, end time.Time) []StatsItem {
	merged := make(map[string]StatsItem)
	for day := start; day.Before(end); day = day.AddDate(0, 0, 1) {
		bucket := day.Format("2006-01-02")
		if clientName != "" {
			clients := s.windows[WindowDay][bucketKey(WindowDay, bucket, "key")]
			if item, ok := clients[clientName]; ok {
				merged[bucket] = mergeStatsItem(merged[bucket], item, bucket)
			}
			continue
		}
		group := s.windows[WindowDay][bucketKey(WindowDay, bucket, groupBy)]
		for value, item := range group {
			key := value
			if groupBy == "date" {
				key = bucket
			}
			merged[key] = mergeStatsItem(merged[key], item, key)
		}
	}
	items := make([]StatsItem, 0, len(merged))
	for _, item := range merged {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Value < items[j].Value })
	return items
}

func parseDateRange(startDate, endDate string) (time.Time, time.Time, error) {
	if startDate == "" || endDate == "" {
		return time.Time{}, time.Time{}, fmt.Errorf("start_date and end_date must be provided together")
	}
	parse := func(name, value string) (time.Time, error) {
		parsed, err := time.ParseInLocation("2006-01-02", value, time.UTC)
		if err != nil || parsed.Format("2006-01-02") != value {
			return time.Time{}, fmt.Errorf("%s must be a valid YYYY-MM-DD UTC date", name)
		}
		return parsed, nil
	}
	start, err := parse("start_date", startDate)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	end, err := parse("end_date", endDate)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if !start.Before(end) {
		return time.Time{}, time.Time{}, fmt.Errorf("start_date must be before end_date")
	}
	return start, end, nil
}

func mergeStatsItem(dst, src StatsItem, value string) StatsItem {
	dst.Value = value
	dst.RequestCount += src.RequestCount
	dst.SuccessCount += src.SuccessCount
	dst.ErrorCount += src.ErrorCount
	dst.FallbackCount += src.FallbackCount
	dst.InputTokens += src.InputTokens
	dst.OutputTokens += src.OutputTokens
	dst.CachedInputReadTokens += src.CachedInputReadTokens
	dst.CachedInputWriteTokens += src.CachedInputWriteTokens
	dst.CacheReadTokens = dst.CachedInputReadTokens
	dst.CacheCreateTokens = dst.CachedInputWriteTokens
	dst.TotalTokens += src.TotalTokens
	dst.CostUSD += src.CostUSD
	dst.ProviderUsageCount += src.ProviderUsageCount
	dst.EstimatedUsageCount += src.EstimatedUsageCount
	if dst.Date == "" {
		dst.Date = src.Date
	}
	if dst.Agent == "" {
		dst.Agent = src.Agent
	}
	if dst.SessionID == "" {
		dst.SessionID = src.SessionID
	}
	if dst.Model == "" {
		dst.Model = src.Model
	}
	if src.LastSeenAt > dst.LastSeenAt {
		dst.LastSeenAt = src.LastSeenAt
	}
	if len(src.ToolUnits) > 0 {
		if dst.ToolUnits == nil {
			dst.ToolUnits = make(map[string]float64)
		}
		for name, units := range src.ToolUnits {
			dst.ToolUnits[name] += units
		}
	}
	return dst
}

func (s *MemoryStore) Coverage() Coverage {
	s.mu.Lock()
	defer s.mu.Unlock()

	coverage := Coverage{
		Known:               s.coverageKnown,
		Backend:             "memory",
		AggregatesPersisted: false,
		RawRetentionDays:    0,
	}
	if !s.trackingStartedAt.IsZero() {
		coverage.TrackingStartedAt = s.trackingStartedAt.Format(time.RFC3339Nano)
	}
	return coverage
}

func (s *MemoryStore) RecentRecords(query RecentRecordsQuery) ([]runtime.UsageRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	limit := query.Limit
	if limit <= 0 || limit > len(s.recent) {
		limit = len(s.recent)
	}
	items := make([]runtime.UsageRecord, 0, limit)
	for i := len(s.recent) - 1; i >= 0 && len(items) < limit; i-- {
		record := s.recent[i]
		if !query.Since.IsZero() && recordTime(record).Before(query.Since) {
			continue
		}
		items = append(items, record)
	}
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	return items, nil
}

func (s *MemoryStore) Close(context.Context) error {
	return nil
}

func (s *MemoryStore) applyRecord(record runtime.UsageRecord) {
	for _, groupBy := range usageGroupBys {
		value := usageGroupValue(record, groupBy)
		s.applyToGroup(s.ensureTotalGroup(record, groupBy), value, record)
	}

	day, week, month := timeBuckets(record)
	for _, groupBy := range usageGroupBys {
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
	item.CacheReadTokens = item.CachedInputReadTokens
	item.CacheCreateTokens = item.CachedInputWriteTokens
	item.TotalTokens += record.Breakdown.TotalTokens
	item.CostUSD += record.CostUSD
	if item.Date == "" {
		item.Date = usageGroupValue(record, "date")
	}
	if item.Agent == "" && record.Agent != "" {
		item.Agent = record.Agent
	}
	if item.SessionID == "" && record.SessionID != "" {
		item.SessionID = record.SessionID
	}
	if item.Model == "" {
		item.Model = usageGroupValue(record, "model")
	}
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
	for _, supported := range usageGroupBys {
		if groupBy == supported {
			return true
		}
	}
	return false
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
	case "error_kind":
		return nonEmpty(record.ErrorKind, "none")
	case "date":
		day, _, _ := timeBuckets(record)
		return day
	case "agent":
		return nonEmpty(record.Agent, "unknown")
	case "session":
		return nonEmpty(record.SessionID, "unknown")
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

func recordTime(record runtime.UsageRecord) time.Time {
	timestamp := record.FinishedAt
	if timestamp == "" {
		timestamp = record.StartedAt
	}
	parsed, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
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
