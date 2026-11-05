package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealth(t *testing.T) {
	for _, tc := range []struct {
		path       string
		status     int
		bodyStatus string
	}{
		{"/healthz", http.StatusOK, "alive"},
		{"/readyz", http.StatusServiceUnavailable, "database_not_connected"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			NewHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rec.Code != tc.status {
				t.Fatalf("status=%d, want=%d", rec.Code, tc.status)
			}
			var got statusResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Status != tc.bodyStatus {
				t.Fatalf("status=%q, want=%q", got.Status, tc.bodyStatus)
			}
		})
	}
}
func TestUnsupportedMethod(t *testing.T) {
	rec := httptest.NewRecorder()
	NewHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/healthz", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("got %d", rec.Code)
	}
}
