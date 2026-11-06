package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// This test creates an isolated schema inside montage_test; never points at montage.
func stage128Pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	base := httpTestPool(t) // validates actual database name contains "test".
	schema := "stage128_" + newTestSuffix128(t)
	quoted := pgx.Identifier{schema}.Sanitize()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if _, err := base.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, c := context.WithTimeout(context.Background(), 20*time.Second)
		defer c()
		if _, e := base.Exec(cleanup, "DROP SCHEMA IF EXISTS "+quoted+" CASCADE"); e != nil {
			t.Errorf("cleanup: %v", e)
		}
	})
	cfg, err := pgxpool.ParseConfig(os.Getenv("LEDGER_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = make(map[string]string)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	for n := 1; n <= 9; n++ {
		matches, e := filepath.Glob(fmt.Sprintf("../../migrations/%06d_*.up.sql", n))
		if e != nil || len(matches) != 1 {
			t.Fatalf("migration %d: files %v err %v", n, matches, e)
		}
		sql, e := os.ReadFile(matches[0])
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(ctx, string(sql)); e != nil {
			t.Fatalf("migration %d failed: %v", n, e)
		}
	}
	return db
}

func TestStage128ExceptionSnapshotInvalidation(t *testing.T) {
	db := stage128Pool(t)
	user, _, revision, _, item, _, _ := fixture114(t, db)
	ctx := context.Background()
	// A legacy XLSX line with an inconsistent original amount.
	_, err := db.Exec(ctx, `UPDATE project_work_items SET rule_snapshot='{"mode":"legacy_snapshot","imported_from":"xlsx"}'::jsonb,amount=92.80 WHERE id=$1`, item)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO review_amount_exceptions(revision_id,work_item_id,calculation_snapshot,reason,requested_by)
      SELECT revision_id,id,review_item_snapshot(i),'Сохраняем оригинальную стоимость из XLSX', $2
      FROM project_work_items i WHERE id=$1`, item, user)
	if err != nil {
		t.Fatal(err)
	}
	var invalid int
	err = db.QueryRow(ctx, `SELECT count(*) FROM review_amount_exceptions e JOIN project_work_items i ON i.id=e.work_item_id
      WHERE i.revision_id=$1 AND e.calculation_snapshot=review_item_snapshot(i) AND e.status='pending'`, revision).Scan(&invalid)
	if err != nil || invalid != 1 {
		t.Fatalf("valid exception: count=%d err=%v", invalid, err)
	}
	// Pending historical exception permits submission, never approval.
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	count, e := outstandingReviewAmountCount(ctx, tx, revision, false)
	if e != nil || count != 0 {
		t.Fatalf("submission outstanding=%d err=%v", count, e)
	}
	count, e = outstandingReviewAmountCount(ctx, tx, revision, true)
	if e != nil || count != 1 {
		t.Fatalf("approval outstanding=%d err=%v", count, e)
	}
	if e = tx.Rollback(ctx); e != nil {
		t.Fatal(e)
	}
	// Change only a relevant billable parameter. Trigger must invalidate the authorization.
	_, err = db.Exec(ctx, `UPDATE project_work_items SET unit_price=801 WHERE id=$1`, item)
	if err != nil {
		t.Fatal(err)
	}
	var status string
	if err = db.QueryRow(ctx, `SELECT status FROM review_amount_exceptions WHERE work_item_id=$1`, item).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "invalidated" {
		t.Fatalf("status after price change = %q", status)
	}
	var actions int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM review_amount_exception_events WHERE work_item_id=$1`, item).Scan(&actions); err != nil {
		t.Fatal(err)
	}
	if actions != 2 {
		t.Fatalf("audit events=%d, want 2", actions)
	}
	_, err = db.Exec(ctx, `DELETE FROM review_amount_exception_events WHERE work_item_id=$1`, item)
	if err == nil {
		t.Fatal("immutable audit history was deleted")
	}
}

func newTestSuffix128(t *testing.T) string {
	t.Helper()
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b[:])
}
