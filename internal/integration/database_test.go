package integration

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// These tests intentionally require a dedicated PostgreSQL test database.
// They never touch DATABASE_URL (the application's normal database).
func openTestTx(t *testing.T) (context.Context, pgx.Tx) {
	t.Helper()
	url := os.Getenv("LEDGER_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set LEDGER_TEST_DATABASE_URL to an isolated migrated test database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	var name string
	if err := conn.QueryRow(ctx, "select current_database()").Scan(&name); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(name), "test") {
		t.Fatalf("refusing to write to non-test database %q", name)
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	return ctx, tx
}

func TestRequiredSchema(t *testing.T) {
	ctx, tx := openTestTx(t)
	for _, name := range []string{"users", "customers", "projects", "project_revisions", "project_systems", "project_work_items", "workers", "project_workers", "work_assignments", "work_execution_events", "work_execution_approvals", "worker_rate_snapshots", "project_worker_payments", "user_sessions"} {
		var exists bool
		if err := tx.QueryRow(ctx, `select to_regclass('public.' || $1) is not null`, name).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Errorf("missing table %s", name)
		}
	}
}

func insertID(t *testing.T, ctx context.Context, tx pgx.Tx, sql string, args ...any) string {
	t.Helper()
	var id string
	if err := tx.QueryRow(ctx, sql, args...).Scan(&id); err != nil {
		t.Fatalf("fixture insert: %v", err)
	}
	return id
}
func execOK(t *testing.T, ctx context.Context, tx pgx.Tx, sql string, args ...any) {
	t.Helper()
	if _, err := tx.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("fixture SQL failed: %v", err)
	}
}

// Failed SQL statements abort a PostgreSQL transaction. A savepoint lets us
// verify a rejected operation and continue testing the same isolated fixture.
func requireRejected(t *testing.T, ctx context.Context, tx pgx.Tx, sql string, args ...any) {
	t.Helper()
	execOK(t, ctx, tx, "SAVEPOINT expected_rejection")
	_, err := tx.Exec(ctx, sql, args...)
	if err == nil {
		t.Errorf("expected SQL rejection, statement succeeded: %s", sql)
	}
	execOK(t, ctx, tx, "ROLLBACK TO SAVEPOINT expected_rejection")
	execOK(t, ctx, tx, "RELEASE SAVEPOINT expected_rejection")
}

func TestExecutionAccrualAndPaymentGuards(t *testing.T) {
	ctx, tx := openTestTx(t)
	user := insertID(t, ctx, tx, `INSERT INTO users(login,password_hash,display_name) VALUES ('stage11-'||gen_random_uuid()::text,'test-hash','Stage 11 test') RETURNING id::text`)
	customer := insertID(t, ctx, tx, `INSERT INTO customers(name) VALUES ('Stage 11 isolated customer') RETURNING id::text`)
	project := insertID(t, ctx, tx, `INSERT INTO projects(customer_id,name,created_by) VALUES ($1,'Stage 11 isolated project',$2) RETURNING id::text`, customer, user)
	revision := insertID(t, ctx, tx, `INSERT INTO project_revisions(project_id,number,created_by) VALUES ($1,1,$2) RETURNING id::text`, project, user)
	system := insertID(t, ctx, tx, `INSERT INTO project_systems(revision_id,code,name,type_code) VALUES ($1,'P-TEST','Test supply','supply') RETURNING id::text`, revision)
	worker := insertID(t, ctx, tx, `INSERT INTO workers(display_name) VALUES ('Stage 11 test worker') RETURNING id::text`)
	execOK(t, ctx, tx, `INSERT INTO project_workers(project_id,worker_id) VALUES ($1,$2)`, project, worker)
	item := insertID(t, ctx, tx, `INSERT INTO project_work_items(revision_id,system_id,name_snapshot,input_quantity,input_unit_code,calculated_quantity,result_unit_code,billable_quantity,billable_unit_code,unit_price,amount) VALUES ($1,$2,'Test duct',10,'m2',10,'m2',10,'m2',800,8000) RETURNING id::text`, revision, system)
	assign := insertID(t, ctx, tx, `INSERT INTO work_assignments(revision_id,work_item_id,worker_id) VALUES ($1,$2,$3) RETURNING id::text`, revision, item, worker)
	execOK(t, ctx, tx, `INSERT INTO worker_rate_snapshots(assignment_id,rate_per_billable_unit,unit_code) VALUES ($1,100,'m2')`, assign)
	event := insertID(t, ctx, tx, `INSERT INTO work_execution_events(assignment_id,event_type,quantity_delta,performed_on,created_by) VALUES ($1,'performed',5,CURRENT_DATE,$2) RETURNING id::text`, assign, user)
	requireRejected(t, ctx, tx, `INSERT INTO work_execution_events(assignment_id,event_type,quantity_delta,performed_on,created_by) VALUES ($1,'performed',6,CURRENT_DATE,$2)`, assign, user)
	execOK(t, ctx, tx, `INSERT INTO work_execution_approvals(event_id,approved_by) VALUES ($1,$2)`, event, user)
	execOK(t, ctx, tx, `INSERT INTO project_worker_payments(revision_id,worker_id,amount,paid_on,created_by) VALUES ($1,$2,400,CURRENT_DATE,$3)`, revision, worker, user)
	requireRejected(t, ctx, tx, `INSERT INTO project_worker_payments(revision_id,worker_id,amount,paid_on,created_by) VALUES ($1,$2,101,CURRENT_DATE,$3)`, revision, worker, user)
	correction := insertID(t, ctx, tx, `INSERT INTO work_execution_events(assignment_id,event_type,quantity_delta,performed_on,created_by) VALUES ($1,'correction',-2,CURRENT_DATE,$2) RETURNING id::text`, assign, user)
	// Cannot approve a negative correction that would drop approved wages
	// to 300 RUB when 400 RUB have already been paid.
	requireRejected(t, ctx, tx, `INSERT INTO work_execution_approvals(event_id,approved_by) VALUES ($1,$2)`, correction, user)
	requireRejected(t, ctx, tx, `UPDATE work_execution_events SET notes='edited' WHERE id=$1`, event)
	requireRejected(t, ctx, tx, `DELETE FROM project_worker_payments WHERE revision_id=$1`, revision)
}
