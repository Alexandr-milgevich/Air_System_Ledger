package httpserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestStage132AllocationSQLAndAudit(t *testing.T) {
	db := stage128Pool(t)
	ctx := context.Background()
	for _, n := range []int{10, 11, 12} {
		matches, err := filepath.Glob(fmt.Sprintf("../../migrations/%06d_*.up.sql", n))
		if err != nil || len(matches) != 1 {
			t.Fatalf("migration %d: %v %v", n, matches, err)
		}
		sql, err := os.ReadFile(matches[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("migration %d: %v", n, err)
		}
	}
	actor, project, revision, _, item, _, assignment := fixture114(t, db)
	// fixture planned item is six billable units.
	var total string
	if err := db.QueryRow(ctx, `SELECT billable_quantity::text FROM project_work_items WHERE id=$1`, item).Scan(&total); err != nil {
		t.Fatal(err)
	}
	httpTestExec(t, db, `INSERT INTO work_assignment_allocations(assignment_id,planned_quantity,updated_by) VALUES($1,$2,$3)`, assignment, total, actor)
	if _, err := db.Exec(ctx, `UPDATE work_assignment_allocations SET planned_quantity=planned_quantity+1,updated_by=$2 WHERE assignment_id=$1`, assignment, actor); err == nil {
		t.Fatal("overbooking accepted")
	}
	// Two assignments may split the same item up to the total, never more.
	otherWorker := httpTestID(t, db, `INSERT INTO workers(display_name) VALUES('Stage132 second worker') RETURNING id::text`)
	httpTestExec(t, db, `INSERT INTO project_workers(project_id,worker_id) VALUES($1,$2)`, project, otherWorker)
	other := httpTestID(t, db, `INSERT INTO work_assignments(revision_id,work_item_id,worker_id,planned_quantity) VALUES($1,$2,$3,0) RETURNING id::text`, revision, item, otherWorker)
	if _, err := db.Exec(ctx, `INSERT INTO work_assignment_allocations(assignment_id,planned_quantity,updated_by) VALUES($1,1,$2)`, other, actor); err == nil {
		t.Fatal("second assignment overbooking accepted")
	}
	httpTestExec(t, db, `UPDATE work_assignment_allocations SET planned_quantity=planned_quantity-1,updated_by=$2 WHERE assignment_id=$1`, assignment, actor)
	httpTestExec(t, db, `INSERT INTO work_assignment_allocations(assignment_id,planned_quantity,updated_by) VALUES($1,1,$2)`, other, actor)
	if _, err := db.Exec(ctx, `UPDATE project_work_items SET billable_quantity=5 WHERE id=$1`, item); err == nil {
		t.Fatal("item quantity shrink under allocation accepted")
	}
	var n int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM work_assignment_allocation_events WHERE assignment_id=$1`, assignment).Scan(&n); err != nil || n != 2 {
		t.Fatalf("audit count %d err %v", n, err)
	}
	if _, err := db.Exec(ctx, `DELETE FROM work_assignment_allocation_events WHERE assignment_id=$1`, assignment); err == nil {
		t.Fatal("audit is mutable")
	}
	rows, err := db.Query(ctx, workloadSQL, revision)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for rows.Next() {
		var e workloadEntry
		if err = rows.Scan(&e.ID, &e.WorkerID, &e.Worker, &e.System, &e.Work, &e.Unit, &e.Total, &e.Allocated, &e.Unallocated, &e.Recorded, &e.Approved, &e.Start, &e.End, &e.Priority, &e.Overlaps); err != nil {
			t.Fatal(err)
		}
		count++
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if count != 2 {
		t.Fatalf("got %d assignments", count)
	}
}
