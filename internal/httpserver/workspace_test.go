package httpserver

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkspaceTemplate(t *testing.T) {
	views := []workspaceData{
		{Title: "Объекты", View: "projects", Admin: true, CSRF: "token", Customers: []namedRow{{ID: "one", Name: "Заказчик"}}},
		{Title: "Объект", View: "project", ProjectID: "one", CanWrite: true, Admin: true, CSRF: "token"},
		{Title: "Ревизия", View: "revision", RevisionID: "one", CanWrite: true, CSRF: "token"},
		{Title: "Работники", View: "workers", Admin: true, CSRF: "token"},
	}
	for _, data := range views {
		t.Run(data.View, func(t *testing.T) {
			w := httptest.NewRecorder()
			workspaceRender(w, 200, data)
			if w.Code != 200 || !strings.Contains(w.Body.String(), data.Title) || !strings.Contains(w.Body.String(), "csrf") {
				t.Fatalf("unexpected response: %d %s", w.Code, w.Body.String())
			}
		})
	}
}
func TestUUIDValidation(t *testing.T) {
	if !uuidPattern.MatchString("11111111-2222-3333-4444-555555555555") {
		t.Fatal("valid uuid rejected")
	}
	for _, bad := range []string{"", "abc", "1' OR TRUE", "11111111-2222-3333-4444-55555555555Z"} {
		if uuidPattern.MatchString(bad) {
			t.Fatalf("invalid UUID accepted: %q", bad)
		}
	}
}
func TestCanEdit(t *testing.T) {
	for _, v := range []string{"read", "approve", "", "superuser"} {
		if canEdit(v) {
			t.Fatalf("unexpected editing permission: %s", v)
		}
	}
	for _, v := range []string{"write", "admin"} {
		if !canEdit(v) {
			t.Fatalf("missing editing permission: %s", v)
		}
	}
}
