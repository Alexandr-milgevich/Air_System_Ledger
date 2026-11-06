package httpserver

import (
	"bytes"
	"strings"
	"testing"
)

func TestStage132AllocationValidation(t *testing.T) {
	good := []string{"0", "1", "15.25", "0.00000001", "999999999999.99999999"}
	for _, s := range good {
		if _, err := parseAllocation(s); err != nil {
			t.Errorf("valid %q: %v", s, err)
		}
	}
	bad := []string{"", "-1", "+1", "1e2", "1.123456789", "1234567890123", "NaN", "1.2.3", "1,5", " 1", "1.", ".5"}
	for _, s := range bad {
		if _, err := parseAllocation(s); err == nil {
			t.Errorf("accepted %q", s)
		}
	}
}
func TestStage132WorkloadOverlapAndCells(t *testing.T) {
	if !workloadOverlap("2026-11-01", "2026-11-05", "2026-11-05", "2026-11-09") {
		t.Fatal("shared end day is overlapping")
	}
	if workloadOverlap("2026-11-01", "2026-11-04", "2026-11-05", "2026-11-09") {
		t.Fatal("disjoint intervals overlap")
	}
	days := []workloadDay{{Date: "2026-11-04"}, {Date: "2026-11-05"}, {Date: "2026-11-06"}}
	entries := []workloadEntry{{ID: "a", WorkerID: "a-worker", Worker: "А", Start: "2026-11-04", End: "2026-11-06"}, {ID: "b", WorkerID: "a-worker", Worker: "А", Start: "2026-11-05", End: "2026-11-05"}, {ID: "c", WorkerID: "b-worker", Worker: "Б", Start: "2026-11-04", End: "2026-11-06"}}
	got := workloadCells(entries[0].Start, entries[0].End, days, entries, "a-worker", "a")
	if !got[0].Active || got[0].Conflict || !got[1].Conflict || got[2].Conflict {
		t.Fatalf("incorrect day collisions: %+v", got)
	}
}
func TestStage132WorkloadTemplateEscapes(t *testing.T) {
	d := workloadPage{RevisionID: "11111111-1111-1111-1111-111111111111", ProjectName: "<script>alert(1)</script>", CanWrite: true, Month: "2026-11", Days: []workloadDay{{Date: "2026-11-07", Label: "07"}}, Entries: []workloadEntry{{ID: "22222222-2222-2222-2222-222222222222", Worker: "<img src=x onerror=alert(1)>", System: "П-31", Work: "Работа", Allocated: "1.00", Cells: []workloadCell{{Active: true}}}}}
	var b bytes.Buffer
	if err := executionWorkloadTemplate.Execute(&b, d); err != nil {
		t.Fatal(err)
	}
	s := b.String()
	if strings.Contains(s, "<script>") || strings.Contains(s, "<img src=x") {
		t.Fatal("unsafe output")
	}
	if !strings.Contains(s, "&lt;script&gt;") || !strings.Contains(s, "&lt;img") || !strings.Contains(s, "/allocation") {
		t.Fatal("missing escaped output or form")
	}
}
