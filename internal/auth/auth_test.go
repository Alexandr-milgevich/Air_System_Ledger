package auth

import "testing"

func TestPassword(t *testing.T) {
	h, e := HashPassword("correct horse battery staple")
	if e != nil {
		t.Fatal(e)
	}
	if !VerifyPassword(h, "correct horse battery staple") {
		t.Fatal("valid password rejected")
	}
	if VerifyPassword(h, "wrong password") || VerifyPassword("invalid", "wrong password") {
		t.Fatal("bad password accepted")
	}
}
func TestPasswordLength(t *testing.T) {
	if _, e := HashPassword("short"); e == nil {
		t.Fatal("weak password accepted")
	}
}
func TestRoles(t *testing.T) {
	u := Identity{Roles: []string{"engineer"}}
	if !HasRole(u, "admin", "engineer") || HasRole(u, "admin") {
		t.Fatal("roles")
	}
}
