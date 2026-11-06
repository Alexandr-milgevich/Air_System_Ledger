package httpserver

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestStage131MonthValidation(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		raw, want string
		valid     bool
	}{
		{"", "2026-11", true}, {"2026-02", "2026-02", true}, {"2026-13", "", false}, {"2026-2", "", false}, {"../../etc", "", false}, {"1900-01", "", false},
	} {
		m, err := scheduleMonth(c.raw, now)
		if (err == nil) != c.valid {
			t.Errorf("%q err=%v", c.raw, err)
		}
		if c.valid && m.Format("2006-01") != c.want {
			t.Errorf("%q got %s", c.raw, m.Format("2006-01"))
		}
	}
}
func TestStage131DerivedStatuses(t *testing.T) {
	today := time.Date(2026, 11, 7, 13, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		planned, recorded, approved, end, want string
		late                                   bool
	}{
		{"10", "10", "10", "2026-11-01", "Выполнено", false},
		{"10", "3", "0", "2026-11-06", "Просрочено", true},
		{"10", "3", "1", "2026-11-08", "В работе", false},
		{"10", "0", "0", "2026-11-08", "Запланировано", false},
		{"10", "0", "0", "", "Без сроков", false},
		{"10", "0", "0", "2026-11-07", "Запланировано", false},
	} {
		got, late := scheduleState(c.planned, c.recorded, c.approved, c.end, today)
		if got != c.want || late != c.late {
			t.Errorf("%+v got %s %t", c, got, late)
		}
	}
}
func TestStage131ScheduleTemplateEscapesUntrustedData(t *testing.T) {
	d := schedulePage{RevisionID: "11111111-1111-1111-1111-111111111111", ProjectName: "<script>bad()</script>", CanWrite: true, Month: "2026-11", Prev: "2026-10", Next: "2026-12", Entries: []scheduleEntry{{ID: "22222222-2222-2222-2222-222222222222", System: "П-31", Worker: "<img src=x onerror=alert(1)>", Work: "<script>alert(1)</script>", Notes: `" onfocus="bad()`}}}
	var b bytes.Buffer
	if err := executionScheduleTemplate.Execute(&b, d); err != nil {
		t.Fatal(err)
	}
	s := b.String()
	if strings.Contains(s, "<script>") || strings.Contains(s, "<img src=x") || strings.Contains(s, `value="" onfocus=`) {
		t.Fatal("raw HTML injection")
	}
	for _, fragment := range []string{"&lt;script&gt;", "&lt;img", `/execution/22222222-2222-2222-2222-222222222222/schedule`} {
		if !strings.Contains(s, fragment) {
			t.Errorf("missing %q", fragment)
		}
	}
}
