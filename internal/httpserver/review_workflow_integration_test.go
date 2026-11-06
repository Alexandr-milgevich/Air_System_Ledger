package httpserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Runs only against the isolated test database configured by Stage 11.4 helpers.
func TestStage126ReviewTransitionsAndImmutability(t *testing.T) {
	db := isolatedStage114(t)
	for _, name := range []string{"000006_bulk_audit.up.sql", "000007_revision_history.up.sql", "000008_review_workflow.up.sql"} {
		migration, err := os.ReadFile(filepath.Join("..", "..", "migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(context.Background(), string(migration)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	user, _, revision, system, item, _, _ := fixture114(t, db)
	ctx := context.Background()
	// A submitted revision has to be read-only, including direct SQL writes.
	if _, err := db.Exec(ctx, `UPDATE project_revisions SET status='in_review' WHERE id=$1`, revision); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE project_work_items SET unit_price=999 WHERE id=$1`, item); err == nil {
		t.Fatal("in_review work row was editable")
	}
	if _, err := db.Exec(ctx, `INSERT INTO project_systems(revision_id,code,name,type_code) VALUES($1,'extra','extra','supply')`, revision); err == nil {
		t.Fatal("in_review system insertion succeeded")
	}
	if _, err := db.Exec(ctx, `UPDATE project_revisions SET status='draft' WHERE id=$1`, revision); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE project_work_items SET unit_price=801,amount=4806 WHERE id=$1`, item); err != nil {
		t.Fatalf("draft should be editable: %v", err)
	}
	if _, err := db.Exec(ctx, `UPDATE project_revisions SET status='in_review' WHERE id=$1`, revision); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE project_revisions SET status='approved',approved_at=now(),approved_by=$2 WHERE id=$1`, revision, user); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE project_revisions SET status='draft' WHERE id=$1`, revision); err == nil {
		t.Fatal("approved revision became editable")
	}
	var n int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM revision_review_events WHERE revision_id=$1`, revision).Scan(&n); err != nil || n != 4 {
		t.Fatalf("transition journal count=%d err=%v", n, err)
	}
	if _, err := db.Exec(ctx, `DELETE FROM revision_review_events WHERE revision_id=$1`, revision); err == nil {
		t.Fatal("review audit deletion succeeded")
	}
	_ = system
}
