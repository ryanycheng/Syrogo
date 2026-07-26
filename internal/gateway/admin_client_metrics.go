package gateway

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ryanycheng/Syrogo/internal/accounting"
	"github.com/ryanycheng/Syrogo/internal/config"
	"github.com/ryanycheng/Syrogo/internal/quota"
)

const (
	defaultClientMetricsDays = 30
	defaultClientUsageDays   = 365
	maxClientUsageDays       = 366
)

type clientFrequency struct {
	Requests             int     `json:"requests"`
	ActiveDays           int     `json:"active_days"`
	CalendarDays         int     `json:"calendar_days"`
	RequestsPerDay       float64 `json:"requests_per_day"`
	RequestsPerActiveDay float64 `json:"requests_per_active_day"`
}

type clientMetricsResponse struct {
	Client    clientResourceResponse `json:"client"`
	AllTime   accounting.StatsItem   `json:"all_time"`
	Frequency clientFrequency        `json:"frequency"`
	Quota     *quota.SnapshotItem    `json:"quota,omitempty"`
}

type clientDailyUsage struct {
	accounting.StatsItem
	Status string `json:"status"`
}

type clientUsageResponse struct {
	Client       clientResourceResponse `json:"client"`
	AllTime      accounting.StatsItem   `json:"all_time"`
	RangeSummary accounting.StatsItem   `json:"range_summary"`
	Quota        *quota.SnapshotItem    `json:"quota,omitempty"`
	Coverage     accounting.Coverage    `json:"coverage"`
	StartDate    string                 `json:"start_date"`
	EndDate      string                 `json:"end_date"`
	Daily        []clientDailyUsage     `json:"daily"`
}

func (h *Handler) handleConfigClientMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAdmin(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	days, err := parseClientMetricsDays(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	state := h.runtimeState()
	cfg, ok := h.clientMetricsConfigView(w, state)
	if !ok {
		return
	}
	if state.Dispatcher == nil {
		writeError(w, http.StatusServiceUnavailable, "dispatcher is not configured")
		return
	}

	now := time.Now().UTC()
	today := utcDate(now)
	end := today.AddDate(0, 0, 1)
	start := end.AddDate(0, 0, -days)
	allTimeItems, err := state.Dispatcher.QueryUsageBy(accounting.Query{GroupBy: "key", Window: accounting.WindowTotal})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	allTimeByClient := statsByValue(allTimeItems)
	quotaByClient := clientQuotaByName(state.ClientQuotaTracker)
	redacted := config.RedactedConfig(cfg)
	items := make([]clientMetricsResponse, 0, len(redacted.Clients))
	for _, client := range redacted.Clients {
		daily, err := state.Dispatcher.QueryUsageBy(accounting.Query{
			GroupBy: "date", StartDate: formatUTCDate(start), EndDate: formatUTCDate(end), ClientName: client.Name,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		item := clientMetricsResponse{
			Client:    clientResource(redacted, client),
			AllTime:   normalizedClientStats(allTimeByClient[client.Name], client.Name),
			Frequency: summarizeClientFrequency(daily, days),
		}
		if snapshot, exists := quotaByClient[client.Name]; exists {
			copied := snapshot
			item.Quota = &copied
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items, "days": days, "start_date": formatUTCDate(start), "end_date": formatUTCDate(end),
	})
}

func (h *Handler) handleConfigClientUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAdmin(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	state := h.runtimeState()
	cfg, ok := h.clientMetricsConfigView(w, state)
	if !ok {
		return
	}
	client, found := findConfiguredClient(config.RedactedConfig(cfg), name)
	if !found {
		writeError(w, http.StatusNotFound, "client not found")
		return
	}
	now := time.Now()
	start, end, err := parseClientUsageRange(r, now)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if state.Dispatcher == nil {
		writeError(w, http.StatusServiceUnavailable, "dispatcher is not configured")
		return
	}
	allTimeItems, err := state.Dispatcher.QueryUsageBy(accounting.Query{GroupBy: "key", Window: accounting.WindowTotal})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	dailyItems, err := state.Dispatcher.QueryUsageBy(accounting.Query{
		GroupBy: "date", StartDate: formatUTCDate(start), EndDate: formatUTCDate(end), ClientName: name,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	coverage := state.Dispatcher.QueryUsageCoverage()
	daily := denseClientDaily(dailyItems, start, end, now, coverage)
	response := clientUsageResponse{
		Client:       clientResource(config.RedactedConfig(cfg), client),
		AllTime:      normalizedClientStats(statsByValue(allTimeItems)[name], name),
		RangeSummary: summarizeDailyUsage(daily, name),
		Coverage:     coverage,
		StartDate:    formatUTCDate(start),
		EndDate:      formatUTCDate(end),
		Daily:        daily,
	}
	if snapshot, exists := clientQuotaByName(state.ClientQuotaTracker)[name]; exists {
		copied := snapshot
		response.Quota = &copied
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) clientMetricsConfigView(w http.ResponseWriter, state RuntimeState) (config.Config, bool) {
	if len(state.Clients) > 0 || len(state.Inbounds) > 0 {
		return config.Config{
			Clients:  append([]config.ClientSpec(nil), state.Clients...),
			Inbounds: append([]config.InboundSpec(nil), state.Inbounds...),
		}, true
	}
	return h.readAdminConfigForResource(w)
}

func parseClientMetricsDays(r *http.Request) (int, error) {
	value := strings.TrimSpace(r.URL.Query().Get("days"))
	if value == "" {
		return defaultClientMetricsDays, nil
	}
	days, err := strconv.Atoi(value)
	if err != nil || days < 1 || days > maxClientUsageDays {
		return 0, fmt.Errorf("days must be an integer between 1 and %d", maxClientUsageDays)
	}
	return days, nil
}

func parseClientUsageRange(r *http.Request, now time.Time) (time.Time, time.Time, error) {
	values := r.URL.Query()
	startValue := strings.TrimSpace(values.Get("start_date"))
	endValue := strings.TrimSpace(values.Get("end_date"))
	today := utcDate(now)
	tomorrow := today.AddDate(0, 0, 1)
	if startValue == "" && endValue == "" {
		return tomorrow.AddDate(0, 0, -defaultClientUsageDays), tomorrow, nil
	}
	if startValue == "" || endValue == "" {
		return time.Time{}, time.Time{}, fmt.Errorf("start_date and end_date must be provided together")
	}
	start, end, err := parseUsageDateRange(startValue, endValue)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if end.After(tomorrow) {
		return time.Time{}, time.Time{}, fmt.Errorf("end_date must not be later than tomorrow UTC")
	}
	if days := int(end.Sub(start) / (24 * time.Hour)); days > maxClientUsageDays {
		return time.Time{}, time.Time{}, fmt.Errorf("date range must not exceed %d days", maxClientUsageDays)
	}
	return start, end, nil
}

func findConfiguredClient(cfg config.Config, name string) (config.ClientSpec, bool) {
	return config.FindClient(cfg, name)
}

func clientResource(cfg config.Config, client config.ClientSpec) clientResourceResponse {
	bindings := config.ClientBindings(cfg, client.Name)
	resources := make([]clientBindingResourceResponse, 0, len(bindings))
	for _, item := range bindings {
		resources = append(resources, clientBindingResourceResponse{
			Inbound:         item.Inbound.Name,
			InboundProtocol: item.Inbound.Protocol,
			InboundPath:     item.Inbound.Path,
			Ref:             item.Binding.Ref,
			Tag:             item.Binding.Tag,
		})
	}
	return clientResourceResponse{Name: client.Name, Token: client.Token, Quota: client.Quota, Bindings: resources}
}

func clientQuotaByName(tracker *quota.Tracker) map[string]quota.SnapshotItem {
	items := map[string]quota.SnapshotItem{}
	if tracker == nil {
		return items
	}
	for _, item := range tracker.ClientSnapshot() {
		items[item.Client] = item
	}
	return items
}

func statsByValue(items []accounting.StatsItem) map[string]accounting.StatsItem {
	result := make(map[string]accounting.StatsItem, len(items))
	for _, item := range items {
		result[item.Value] = item
	}
	return result
}

func normalizedClientStats(item accounting.StatsItem, value string) accounting.StatsItem {
	item.Value = value
	return item
}

func summarizeClientFrequency(items []accounting.StatsItem, calendarDays int) clientFrequency {
	frequency := clientFrequency{CalendarDays: calendarDays}
	for _, item := range items {
		frequency.Requests += item.RequestCount
		if item.RequestCount > 0 {
			frequency.ActiveDays++
		}
	}
	if calendarDays > 0 {
		frequency.RequestsPerDay = float64(frequency.Requests) / float64(calendarDays)
	}
	if frequency.ActiveDays > 0 {
		frequency.RequestsPerActiveDay = float64(frequency.Requests) / float64(frequency.ActiveDays)
	}
	return frequency
}

func denseClientDaily(items []accounting.StatsItem, start, end, now time.Time, coverage accounting.Coverage) []clientDailyUsage {
	byDate := statsByValue(items)
	result := make([]clientDailyUsage, 0, int(end.Sub(start)/(24*time.Hour)))
	today := utcDate(now)
	trackingDate, trackingKnown := coverageTrackingDate(coverage)
	for day := start; day.Before(end); day = day.AddDate(0, 0, 1) {
		date := formatUTCDate(day)
		item := byDate[date]
		item.Value = date
		item.Date = date
		status := "unknown"
		if day.Equal(today) {
			status = "partial"
		} else if day.Before(today) && coverage.Known && trackingKnown && !day.Before(trackingDate) {
			status = "complete"
		}
		result = append(result, clientDailyUsage{StatsItem: item, Status: status})
	}
	return result
}

func coverageTrackingDate(coverage accounting.Coverage) (time.Time, bool) {
	if !coverage.Known || coverage.TrackingStartedAt == "" {
		return time.Time{}, false
	}
	trackedAt, err := time.Parse(time.RFC3339Nano, coverage.TrackingStartedAt)
	if err != nil {
		return time.Time{}, false
	}
	return utcDate(trackedAt), true
}

func summarizeDailyUsage(daily []clientDailyUsage, value string) accounting.StatsItem {
	summary := accounting.StatsItem{Value: value}
	for _, day := range daily {
		summary = mergeClientStats(summary, day.StatsItem, value)
	}
	return summary
}

func mergeClientStats(dst, src accounting.StatsItem, value string) accounting.StatsItem {
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

func utcDate(value time.Time) time.Time {
	value = value.UTC()
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}

func formatUTCDate(value time.Time) string {
	return value.UTC().Format("2006-01-02")
}
