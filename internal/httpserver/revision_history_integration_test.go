package httpserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"montage-ledger/internal/auth"
)

// Stage 12.5 runs in a disposable schema within montage_test, never production.
func TestStage125RowAuditAndRevisionDiff(t *testing.T) {
	db := isolatedStage114(t) // creates a fully migrated, disposable test schema for 000001-000005.
	for _, name := range []string{"000006_bulk_audit.up.sql", "000007_revision_history.up.sql"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(context.Background(), string(b)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	user, project, rev, sys, item, _, _ := fixture114(t, db)
	var lineage string
	if err := db.QueryRow(context.Background(), `SELECT lineage_id::text FROM project_work_items WHERE id=$1`, item).Scan(&lineage); err != nil {
		t.Fatal(err)
	}
	if lineage == "" {
		t.Fatal("new work item has empty lineage_id")
	}
	httpTestExec(t, db, `UPDATE project_work_items SET amount=4900 WHERE id=$1`, item)
	var action string
	if err := db.QueryRow(context.Background(), `SELECT action FROM work_item_change_events WHERE item_id=$1 ORDER BY id DESC LIMIT 1`, item).Scan(&action); err != nil {
		t.Fatal(err)
	}
	if action != "UPDATE" {
		t.Fatalf("unexpected audit event %s", action)
	}
	if _, err := db.Exec(context.Background(), `DELETE FROM work_item_change_events WHERE item_id=$1`, item); err == nil {
		t.Fatal("audit delete should be denied")
	}
	var newRevision string
	if err := db.QueryRow(context.Background(), `INSERT INTO project_revisions(project_id,number,created_by) VALUES($1,2,$2) RETURNING id::text`, project, user).Scan(&newRevision); err != nil {
		t.Fatal(err)
	}
	var newSystem string
	if err := db.QueryRow(context.Background(), `INSERT INTO project_systems(revision_id,code,name,type_code) VALUES ($1,'П-31','Supply','supply') RETURNING id::text`, newRevision).Scan(&newSystem); err != nil {
		t.Fatal(err)
	}
	httpTestExec(t, db, `INSERT INTO project_work_items(revision_id,system_id,lineage_id,name_snapshot,input_quantity,input_unit_code,calculated_quantity,result_unit_code,billable_quantity,billable_unit_code,unit_price,amount) VALUES ($1,$2,$3,'Duct',6,'m2',6,'m2',6,'m2',800,4950)`, newRevision, newSystem, lineage)
	x := web{db: db, auth: auth.Store{DB: db}}
	result, err := x.loadRevisionDiff(context.Background(), rev, newRevision)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || result[0].Change != "Изменена" || result[0].BeforeAmount != "4900.00" || result[0].AfterAmount != "4950.00" {
		t.Fatalf("unexpected diff: %+v", result)
	}
	_ = sys
}
