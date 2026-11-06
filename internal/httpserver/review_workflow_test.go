package httpserver

import "testing"

func TestAllowedReviewAction(t *testing.T) {
	cases := []struct {
		action, status, permission string
		want                       bool
	}{
		{"submit", "draft", "write", true}, {"submit", "draft", "admin", true},
		{"submit", "draft", "read", false}, {"submit", "in_review", "write", false},
		{"approve", "in_review", "approve", true}, {"approve", "in_review", "admin", true},
		{"approve", "in_review", "write", false}, {"approve", "draft", "admin", false},
		{"return", "in_review", "approve", true}, {"return", "approved", "admin", false},
	}
	for _, tt := range cases {
		if got := allowedReviewAction(tt.action, tt.status, tt.permission); got != tt.want {
			t.Errorf("%s/%s/%s: got %v want %v", tt.action, tt.status, tt.permission, got, tt.want)
		}
	}
}
