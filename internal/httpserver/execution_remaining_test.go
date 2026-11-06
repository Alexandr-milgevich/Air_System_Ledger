package httpserver

import (
	"strings"
	"testing"
)

func TestRemainingDecimal(t *testing.T) {
	for _, tc := range []struct{ plan, recorded, want string }{
		{"6.00000000", "4.00000000", "2.00000000"},
		{"2.50000000", "1.00000000", "1.50000000"},
		{"50.00000000", "32.00000000", "18.00000000"},
		{"0.30000000", "0.10000000", "0.20000000"},
	} {
		if got := subtractDecimal(tc.plan, tc.recorded); got != tc.want {
			t.Errorf("%s - %s = %s, want %s", tc.plan, tc.recorded, got, tc.want)
		}
	}
}

func TestExcessVolumeMessage(t *testing.T) {
	got := excessVolumeMessage("32.00000000", "2.00000000")
	for _, word := range []string{"32.00000000", "2.00000000", "неутверждённые"} {
		if !strings.Contains(got, word) {
			t.Errorf("message does not contain %q: %s", word, got)
		}
	}
}
