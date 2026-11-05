package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeDatabase struct{ err error }

func (f fakeDatabase) Ping(context.Context) error { return f.err }

func TestHealthIndependentOfDatabase(t *testing.T) {
	for _, db := range []DatabasePinger{nil, fakeDatabase{errors.New("database offline")}} {
		rec := httptest.NewRecorder()
		NewHandler(db).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("health status=%d", rec.Code)
		}
	}
}

func TestReadiness(t *testing.T) {
	for _, tc := range []struct {
		name   string
		db     DatabasePinger
		code   int
		status string
	}{
		{"not configured", nil, 503, "database_unavailable"},
		{"database offline", fakeDatabase{errors.New("secret connection error")}, 503, "database_unavailable"},
		{"database ready", fakeDatabase{}, 200, "ready"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			NewHandler(tc.db).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			if rec.Code != tc.code {
				t.Fatalf("status=%d, want=%d", rec.Code, tc.code)
			}
			var got statusResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Status != tc.status {
				t.Fatalf("status=%q, want=%q", got.Status, tc.status)
			}
			if rec.Body.String() == "secret connection error" {
				t.Fatal("leaked database error")
			}
		})
	}
}
func TestUnsupportedMethod(t *testing.T) {
	rec := httptest.NewRecorder()
	NewHandler(nil).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/healthz", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("got %d", rec.Code)
	}
}
