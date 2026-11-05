package httpserver

import (
	"net/http/httptest"
	"testing"
)

func TestSameOrigin(t *testing.T) {
	cases := []struct {
		origin, site string
		want         bool
	}{
		{"http://localhost:8080", "same-origin", true},
		{"http://evil.example", "cross-site", false},
		{"", "cross-site", false},
		{"", "same-origin", true},
	}
	for _, c := range cases {
		r := httptest.NewRequest("POST", "http://localhost:8080/login", nil)
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		r.Header.Set("Sec-Fetch-Site", c.site)
		if got := sameOrigin(r); got != c.want {
			t.Errorf("origin=%s site=%s got %v", c.origin, c.site, got)
		}
	}
}
