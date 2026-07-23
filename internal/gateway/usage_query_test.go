package gateway

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/ryanycheng/Syrogo/internal/accounting"
	"github.com/ryanycheng/Syrogo/internal/config"
	"github.com/ryanycheng/Syrogo/internal/provider"
)

func TestUsageEndpointsShareDateRangeValidation(t *testing.T) {
	h := newTestHandler(t, map[string]provider.Provider{"mock": provider.NewMock("mock")}, testRoutingConfig(), testInbounds(), testOutbounds())
	h.accounting = config.AccountingConfig{Enabled: true, ExposeHTTP: true, AdminToken: "accounting-token"}
	h.admin = config.AdminConfig{Enabled: true, Token: "admin-token"}
	mux := http.NewServeMux()
	h.Register(mux)

	for _, tc := range []struct {
		path  string
		token string
	}{
		{path: "/stats/usage?start_date=2026-04-01", token: "accounting-token"},
		{path: "/admin/usage?start_date=2026-04-01", token: "admin-token"},
	} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, authorizedRequest(http.MethodGet, tc.path, tc.token, nil))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("path=%s status = %d, want 400, body=%s", tc.path, w.Code, w.Body.String())
		}
	}
}

func TestParseUsageQueryDefaultsToLastSevenUTCDates(t *testing.T) {
	query, err := parseUsageQuery(url.Values{}, time.Date(2026, 4, 29, 23, 30, 0, 0, time.FixedZone("UTC-7", -7*60*60)))
	if err != nil {
		t.Fatalf("parseUsageQuery() error = %v", err)
	}
	if query.GroupBy != "key" || query.Window != accounting.WindowTotal || query.StartDate != "2026-04-24" || query.EndDate != "2026-05-01" {
		t.Fatalf("query = %#v, want today-6d through tomorrow in UTC", query)
	}
}

func TestParseUsageQueryPreservesExplicitLegacySemantics(t *testing.T) {
	query, err := parseUsageQuery(url.Values{"group_by": {"source"}, "window": {"day"}, "bucket": {"2026-04-27"}}, time.Now())
	if err != nil {
		t.Fatalf("parseUsageQuery() error = %v", err)
	}
	if query.GroupBy != "source" || query.Window != accounting.WindowDay || query.Bucket != "2026-04-27" || query.StartDate != "" || query.EndDate != "" {
		t.Fatalf("query = %#v, want unchanged legacy query", query)
	}
}

func TestParseUsageQueryAcceptsExplicitHalfOpenRange(t *testing.T) {
	query, err := parseUsageQuery(url.Values{"group_by": {"date"}, "start_date": {"2026-04-01"}, "end_date": {"2026-05-01"}}, time.Now())
	if err != nil {
		t.Fatalf("parseUsageQuery() error = %v", err)
	}
	if query.GroupBy != "date" || query.StartDate != "2026-04-01" || query.EndDate != "2026-05-01" {
		t.Fatalf("query = %#v, want explicit date range", query)
	}
}

func TestParseUsageQueryRejectsConflictsMissingBoundsAndInvalidDates(t *testing.T) {
	for _, values := range []url.Values{
		{"window": {"total"}, "start_date": {"2026-04-01"}, "end_date": {"2026-05-01"}},
		{"bucket": {"2026-04-01"}, "start_date": {"2026-04-01"}, "end_date": {"2026-05-01"}},
		{"start_date": {"2026-04-01"}},
		{"start_date": {"2026-04-31"}, "end_date": {"2026-05-01"}},
		{"start_date": {"2026-04-01T00:00:00Z"}, "end_date": {"2026-05-01"}},
		{"start_date": {"2026-05-01"}, "end_date": {"2026-05-01"}},
	} {
		if _, err := parseUsageQuery(values, time.Now()); err == nil {
			t.Fatalf("parseUsageQuery(%v) error = nil, want validation error", values)
		}
	}
}
