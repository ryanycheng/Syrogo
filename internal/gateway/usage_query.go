package gateway

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/ryanycheng/Syrogo/internal/accounting"
)

func parseUsageQuery(values url.Values, now time.Time) (accounting.Query, error) {
	query := accounting.Query{
		GroupBy: strings.TrimSpace(values.Get("group_by")),
		Window:  accounting.Window(strings.TrimSpace(values.Get("window"))),
		Bucket:  strings.TrimSpace(values.Get("bucket")),
	}
	if query.GroupBy == "" {
		query.GroupBy = "key"
	}

	startDate := strings.TrimSpace(values.Get("start_date"))
	endDate := strings.TrimSpace(values.Get("end_date"))
	hasLegacy := values.Has("window") || values.Has("bucket")
	hasRange := values.Has("start_date") || values.Has("end_date")
	if hasLegacy && hasRange {
		return accounting.Query{}, fmt.Errorf("start_date/end_date cannot be combined with window/bucket")
	}
	if hasRange {
		if startDate == "" || endDate == "" {
			return accounting.Query{}, fmt.Errorf("start_date and end_date must be provided together")
		}
		if _, _, err := parseUsageDateRange(startDate, endDate); err != nil {
			return accounting.Query{}, err
		}
		query.Window = accounting.WindowTotal
		query.StartDate = startDate
		query.EndDate = endDate
		return query, nil
	}
	if hasLegacy {
		if query.Window == "" {
			query.Window = accounting.WindowTotal
		}
		return query, nil
	}

	tomorrow := now.UTC().Truncate(24*time.Hour).AddDate(0, 0, 1)
	query.Window = accounting.WindowTotal
	query.StartDate = tomorrow.AddDate(0, 0, -7).Format("2006-01-02")
	query.EndDate = tomorrow.Format("2006-01-02")
	return query, nil
}

func parseUsageDateRange(startDate, endDate string) (time.Time, time.Time, error) {
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
