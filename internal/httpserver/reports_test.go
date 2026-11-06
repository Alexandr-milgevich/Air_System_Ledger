package httpserver

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReportPeriod(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name, path, from, to, worker string
		invalid                      bool
	}{
		{name: "defaults", path: "/app/revisions/id/reports", from: "2026-11-01", to: "2026-11-06"},
		{name: "explicit", path: "/app/revisions/id/reports?from=2026-10-01&to=2026-10-31", from: "2026-10-01", to: "2026-10-31"},
		{name: "reversed", path: "/app/revisions/id/reports?from=2026-11-10&to=2026-11-01", invalid: true},
		{name: "invaliddate", path: "/app/revisions/id/reports?from=2026-02-31", invalid: true},
		{name: "invalidworker", path: "/app/revisions/id/reports?worker=hello", invalid: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", tc.path, nil)
			a, b, _, err := reportPeriod(r, now)
			if (err != nil) != tc.invalid {
				t.Fatalf("err=%v want invalid=%v", err, tc.invalid)
			}
			if !tc.invalid && (a != tc.from || b != tc.to) {
				t.Fatalf("period %s..%s", a, b)
			}
		})
	}
}
func TestReportTemplate(t *testing.T) {
	if !strings.Contains(reportsHTML, `method="post" action="/app/revisions/{{.RevisionID}}/reports/payments"`) {
		t.Fatal("payment form missing")
	}
	if reportsTemplate == nil {
		t.Fatal("template missing")
	}
}
