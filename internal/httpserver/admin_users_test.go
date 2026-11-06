package httpserver

import (
	"bytes"
	"strings"
	"testing"
)

func TestStage129LoginValidation(t *testing.T) {
	for _, test := range []struct {
		login string
		valid bool
	}{
		{"user_01", true}, {"admin.name", true}, {"a-b", true}, {"ab", false}, {"has space", false}, {"кириллица", false}, {"abc/def", false}, {strings.Repeat("x", 121), false},
	} {
		if got := validLogin(test.login); got != test.valid {
			t.Errorf("login %q accepted=%v expected=%v", test.login, got, test.valid)
		}
	}
}
func TestStage129AdminUsersTemplateEscapesInput(t *testing.T) {
	payload := `<script>alert('x')</script>`
	var buf bytes.Buffer
	err := adminUsersTemplate.Execute(&buf, adminUsersPage{CSRF: "csrf", Users: []adminUserRow{{ID: "00000000-0000-0000-0000-000000000001", Login: "test", Name: payload, Role: "installer", Disabled: "Активен"}}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), payload) {
		t.Fatal("unsanitized HTML in admin user list")
	}
	if !strings.Contains(buf.String(), "&lt;script&gt;") {
		t.Fatal("expected HTML escaping")
	}
}
func TestStage129RolesAllowlist(t *testing.T) {
	for _, r := range []string{"admin", "director", "engineer", "manager", "installer"} {
		if !adminRoles[r] {
			t.Fatalf("role %s missing", r)
		}
	}
	if adminRoles["superuser"] || adminRoles[""] {
		t.Fatal("unexpected role accepted")
	}
}
