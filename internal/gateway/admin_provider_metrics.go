package gateway

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ryanycheng/Syrogo/internal/accounting"
	"github.com/ryanycheng/Syrogo/internal/config"
	"github.com/ryanycheng/Syrogo/internal/provider"
	"github.com/ryanycheng/Syrogo/internal/quota"
	"github.com/ryanycheng/Syrogo/internal/runtime"
)

const providerTimelineBucketCount = 36

type providerCheckRequest struct {
	Name     string                   `json:"name"`
	Model    string                   `json:"model"`
	Provider *providerResourceRequest `json:"provider"`
}

type providerCheckResponse struct {
	Name      string `json:"name"`
	OK        bool   `json:"ok"`
	State     string `json:"state"`
	LatencyMs int64  `json:"latency_ms"`
	CheckedAt string `json:"checked_at"`
	Error     string `json:"error,omitempty"`
}

type providerMetricsResponse struct {
	Provider providerResourceResponse     `json:"provider"`
	Usage    accounting.StatsItem         `json:"usage"`
	Health   *provider.HealthSnapshotItem `json:"health,omitempty"`
	Quota    *quota.SnapshotItem          `json:"quota,omitempty"`
	Timeline []providerTimelineBucket     `json:"timeline"`
}

type providerTimelineBucket struct {
	Start        string `json:"start"`
	End          string `json:"end"`
	RequestCount int    `json:"request_count"`
	SuccessCount int    `json:"success_count"`
	ErrorCount   int    `json:"error_count"`
	State        string `json:"state"`
}

func (h *Handler) handleConfigProviderCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAdmin(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	var req providerCheckRequest
	if !decodeJSONResourceRequest(w, r, &req) {
		return
	}
	cfg, ok := h.readAdminConfigForResource(w)
	if !ok {
		return
	}
	outbound, ok := findOutbound(cfg, req.Name)
	if req.Provider != nil {
		outbound = config.OutboundSpec{
			Name:         req.Provider.Name,
			Protocol:     req.Provider.Protocol,
			Endpoint:     req.Provider.Endpoint,
			AuthToken:    req.Provider.AuthToken,
			Auth:         req.Provider.Auth,
			Tag:          req.Provider.Tag,
			Enabled:      req.Provider.Enabled,
			Capabilities: req.Provider.Capabilities,
			Quota:        req.Provider.Quota,
			Proxy:        req.Provider.Proxy,
		}
		if outbound.AuthToken == config.RedactedValue || outbound.AuthToken == "" {
			if current, exists := findOutbound(cfg, outbound.Name); exists {
				outbound.AuthToken = current.AuthToken
			}
		}
		ok = outbound.Name != ""
	}
	if !ok {
		writeError(w, http.StatusNotFound, "provider not found")
		return
	}
	model := strings.TrimSpace(req.Model)
	if outbound.Protocol != "mock" && model == "" {
		writeError(w, http.StatusBadRequest, "model is required for non-mock providers")
		return
	}
	result := checkProviderLive(r.Context(), outbound, model)
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) handleConfigProviderMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAdmin(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	cfg, ok := h.readAdminConfigForResource(w)
	if !ok {
		return
	}
	state := h.runtimeState()
	if state.Dispatcher == nil {
		writeError(w, http.StatusServiceUnavailable, "dispatcher is not configured")
		return
	}

	hours := parseProviderMetricsHours(r)
	now := time.Now().UTC()
	since := now.Add(-time.Duration(hours) * time.Hour)
	usageItems, err := state.Dispatcher.QueryUsageBy(accounting.Query{GroupBy: "provider", Window: accounting.WindowTotal})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	records, err := state.Dispatcher.QueryRecentUsage(accounting.RecentRecordsQuery{Since: since, Limit: 10000})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	redacted := config.RedactedConfig(cfg)
	usageByProvider := map[string]accounting.StatsItem{}
	for _, item := range usageItems {
		usageByProvider[item.Value] = item
	}
	healthByOutbound := map[string]provider.HealthSnapshotItem{}
	for _, item := range state.Dispatcher.QueryProviderHealth() {
		healthByOutbound[item.Outbound] = item
	}
	quotaByOutbound := map[string]quota.SnapshotItem{}
	for _, item := range state.Dispatcher.QueryQuota() {
		quotaByOutbound[item.Outbound] = item
	}

	items := make([]providerMetricsResponse, 0, len(redacted.Outbounds))
	for _, outbound := range redacted.Outbounds {
		providerItem := providerResourceResponse{
			Name:         outbound.Name,
			Protocol:     outbound.Protocol,
			Endpoint:     outbound.Endpoint,
			AuthToken:    outbound.AuthToken,
			Auth:         outbound.Auth,
			Tag:          outbound.Tag,
			Enabled:      config.OutboundEnabled(outbound),
			Capabilities: outbound.Capabilities,
			Quota:        outbound.Quota,
			Proxy:        outbound.Proxy,
		}
		metric := providerMetricsResponse{
			Provider: providerItem,
			Usage:    usageByProvider[outbound.Name],
			Timeline: buildProviderTimeline(outbound.Name, records, now, providerTimelineBucketSize(hours)),
		}
		if health, ok := healthByOutbound[outbound.Name]; ok {
			metric.Health = &health
		}
		if quotaItem, ok := quotaByOutbound[outbound.Name]; ok {
			metric.Quota = &quotaItem
		}
		items = append(items, metric)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "hours": hours, "bucket_minutes": int(providerTimelineBucketSize(hours).Minutes()), "bucket_count": providerTimelineBucketCount})
}

func parseProviderMetricsHours(r *http.Request) int {
	hours, err := strconv.Atoi(r.URL.Query().Get("hours"))
	if err != nil || hours <= 0 {
		return 6
	}
	if hours > 48 {
		return 48
	}
	return hours
}

func providerTimelineBucketSize(hours int) time.Duration {
	raw := time.Duration(hours) * time.Hour / providerTimelineBucketCount
	if raw < time.Minute {
		return time.Minute
	}
	return raw.Round(time.Minute)
}

func buildProviderTimeline(providerName string, records []runtime.UsageRecord, until time.Time, bucketSize time.Duration) []providerTimelineBucket {
	start := until.Add(-bucketSize * providerTimelineBucketCount)
	buckets := make([]providerTimelineBucket, 0, providerTimelineBucketCount)
	for i := 0; i < providerTimelineBucketCount; i++ {
		bucketStart := start.Add(time.Duration(i) * bucketSize)
		buckets = append(buckets, providerTimelineBucket{Start: bucketStart.Format(time.RFC3339Nano), End: bucketStart.Add(bucketSize).Format(time.RFC3339Nano), State: "empty"})
	}
	for _, record := range records {
		if recordProviderName(record) != providerName {
			continue
		}
		recordedAt := usageRecordTime(record)
		if recordedAt.IsZero() || recordedAt.Before(start) || !recordedAt.Before(until.Add(bucketSize)) {
			continue
		}
		index := int(recordedAt.Sub(start) / bucketSize)
		if index < 0 || index >= len(buckets) {
			continue
		}
		count := record.Breakdown.RequestCount
		if count <= 0 {
			count = 1
		}
		buckets[index].RequestCount += count
		if record.Status == runtime.UsageStatusSuccess {
			buckets[index].SuccessCount += count
		}
		if record.Status == runtime.UsageStatusError {
			buckets[index].ErrorCount += count
		}
	}
	for i := range buckets {
		buckets[i].State = providerTimelineState(buckets[i])
	}
	return buckets
}

func findOutbound(cfg config.Config, name string) (config.OutboundSpec, bool) {
	for _, outbound := range cfg.Outbounds {
		if outbound.Name == name {
			return outbound, true
		}
	}
	return config.OutboundSpec{}, false
}

func checkProviderLive(parent context.Context, outbound config.OutboundSpec, model string) providerCheckResponse {
	startedAt := time.Now()
	checkedAt := startedAt.UTC().Format(time.RFC3339Nano)
	model = strings.TrimSpace(model)
	if outbound.Protocol == "mock" && model == "" {
		model = "syrogo-health-check"
	}
	if outbound.Auth.Type != "" {
		return providerCheckResponse{Name: outbound.Name, OK: false, State: "unknown", CheckedAt: checkedAt, Error: "OAuth provider checks are unavailable; connect the credential and send a real request"}
	}
	if strings.TrimSpace(outbound.AuthToken) == config.RedactedValue {
		return providerCheckResponse{Name: outbound.Name, OK: false, State: "unknown", CheckedAt: checkedAt, Error: "auth_token is redacted; load the active runtime provider or enter a token before testing"}
	}
	httpClient, err := provider.NewHTTPClient(outbound.Proxy)
	if err != nil {
		return providerCheckResponse{Name: outbound.Name, OK: false, State: "unavailable", CheckedAt: checkedAt, Error: err.Error()}
	}
	client, err := provider.DefaultFactoryRegistry().New(outbound.Protocol, outbound.Name, outbound.Endpoint, outbound.AuthToken, outbound.Capabilities, httpClient)
	if err != nil {
		return providerCheckResponse{Name: outbound.Name, OK: false, State: "unavailable", CheckedAt: checkedAt, Error: err.Error()}
	}
	ctx, cancel := context.WithTimeout(parent, 12*time.Second)
	defer cancel()
	_, err = client.ChatCompletion(ctx, runtime.Request{
		Model:     model,
		MaxTokens: 1,
		Messages:  []runtime.Message{{Role: runtime.MessageRoleUser, Parts: []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: "ping"}}}},
	})
	latencyMs := time.Since(startedAt).Milliseconds()
	if err != nil {
		return providerCheckResponse{Name: outbound.Name, OK: false, State: "unavailable", LatencyMs: latencyMs, CheckedAt: checkedAt, Error: fmt.Sprint(err)}
	}
	return providerCheckResponse{Name: outbound.Name, OK: true, State: "live", LatencyMs: latencyMs, CheckedAt: checkedAt}
}

func recordProviderName(record runtime.UsageRecord) string {
	if record.ProviderName != "" {
		return record.ProviderName
	}
	return record.OutboundName
}

func usageRecordTime(record runtime.UsageRecord) time.Time {
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

func providerTimelineState(bucket providerTimelineBucket) string {
	if bucket.RequestCount == 0 {
		return "empty"
	}
	if bucket.ErrorCount == 0 {
		return "success"
	}
	if bucket.SuccessCount == 0 {
		return "failed"
	}
	errorRatio := float64(bucket.ErrorCount) / float64(bucket.RequestCount)
	if errorRatio >= 0.7 {
		return "mostly_failed"
	}
	return "partial_failed"
}
