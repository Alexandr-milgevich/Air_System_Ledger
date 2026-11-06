package httpserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Uses an isolated schema in LEDGER_TEST_DATABASE_URL; never touches production data.
func TestStage131ScheduleSQLAndAudit(t *testing.T) {
	db := stage128Pool(t)
	ctx := context.Background()
	for _, number := range []int{10, 11} {
		matches, err := filepath.Glob(fmt.Sprintf("../../migrations/%06d_*.up.sql", number))
		if err != nil || len(matches) != 1 {
			t.Fatalf("migration %d: %v %v", number, matches, err)
		}
		data, err := os.ReadFile(matches[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(ctx, string(data)); err != nil {
			t.Fatalf("migration %d failed: %v", number, err)
		}
	}
	actor, _, revision, _, _, _, assignment := fixture114(t, db)
	// An unplanned assignment is still visible in the calendar's list.
	rows, err := db.Query(ctx, executionScheduleSQL, revision)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for rows.Next() {
		var e scheduleEntry
		if err = rows.Scan(&e.ID, &e.System, &e.Work, &e.Worker, &e.Unit, &e.Planned, &e.Recorded, &e.Approved, &e.Start, &e.End, &e.Priority, &e.Notes); err != nil {
			t.Fatal(err)
		}
		count++
		if e.Start != "" || e.End != "" {
			t.Fatal("unscheduled entry has dates")
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if count != 1 {
		t.Fatalf("want 1 assignment, got %d", count)
	}
	// Both initial scheduling and changes are audited, with old/new values.
	httpTestExec(t, db, `INSERT INTO work_assignment_schedules(assignment_id,planned_start,planned_end,priority,notes,updated_by) VALUES($1,'2026-11-10','2026-11-15','normal','первый план',$2)`, assignment, actor)
	httpTestExec(t, db, `UPDATE work_assignment_schedules SET planned_end='2026-11-18',priority='high',updated_by=$2,updated_at=now() WHERE assignment_id=$1`, assignment, actor)
	var events int
	if err = db.QueryRow(ctx, `SELECT COUNT(*) FROM work_assignment_schedule_events WHERE assignment_id=$1`, assignment).Scan(&events); err != nil || events != 2 {
		t.Fatalf("audit count %d: %v", events, err)
	}
	var oldEnd, newEnd string
	if err = db.QueryRow(ctx, `SELECT old_end::text,new_end::text FROM work_assignment_schedule_events WHERE assignment_id=$1 ORDER BY id DESC LIMIT 1`, assignment).Scan(&oldEnd, &newEnd); err != nil || oldEnd != "2026-11-15" || newEnd != "2026-11-18" {
		t.Fatalf("audit old/new=%q/%q err=%v", oldEnd, newEnd, err)
	}
	if _, err = db.Exec(ctx, `UPDATE work_assignment_schedule_events SET new_notes='tampered' WHERE assignment_id=$1`, assignment); err == nil {
		t.Fatal("audit must be immutable")
	}
	if _, err = db.Exec(ctx, `UPDATE work_assignment_schedules SET planned_end='2026-11-01' WHERE assignment_id=$1`, assignment); err == nil {
		t.Fatal("invalid end date accepted")
	}
}
