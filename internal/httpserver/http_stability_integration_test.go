package httpserver

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"mime/multipart"
	"montage-ledger/internal/auth"
	"montage-ledger/internal/xlsxledger"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xuri/excelize/v2"
)

// Each test owns a freshly migrated PostgreSQL schema inside montage_test.
// This is essential: execution/approval/payment ledgers are append-only and
// cannot be cleaned up safely using DELETE on fixture rows.
func isolatedStage114(t *testing.T) *pgxpool.Pool {
	t.Helper()
	base := httpTestPool(t) // validates the configured AND actual database name.
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	schema := "stage114_" + hex.EncodeToString(nonce[:])
	quoted := pgx.Identifier{schema}.Sanitize()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := base.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, c := context.WithTimeout(context.Background(), 30*time.Second)
		defer c()
		if _, err := base.Exec(cleanup, "DROP SCHEMA IF EXISTS "+quoted+" CASCADE"); err != nil {
			t.Errorf("isolated schema cleanup: %v", err)
		}
	})
	dsn := os.Getenv("LEDGER_TEST_DATABASE_URL")
	cfg, err := pgxpool.ParseConfig(dsn)
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
	t.Cleanup(db.Close) // cleanup order is LIFO: close pool before DROP SCHEMA.
	for i := 1; i <= 5; i++ {
		pattern := fmt.Sprintf("../../migrations/%06d_*.up.sql", i)
		files, err := filepath.Glob(pattern)
		if err != nil || len(files) != 1 {
			t.Fatalf("migration %d not found: %v", i, err)
		}
		source, err := os.ReadFile(files[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(ctx, string(source)); err != nil {
			t.Fatalf("migration %d: %v", i, err)
		}
	}
	return db
}

func fixture114(t *testing.T, db *pgxpool.Pool) (user, project, revision, system, item, worker, assignment string) {
	t.Helper()
	user = httpTestID(t, db, `INSERT INTO users(login,display_name,password_hash) VALUES ('stage114-'||gen_random_uuid()::text,'Test', 'unused') RETURNING id::text`)
	customer := httpTestID(t, db, `INSERT INTO customers(name) VALUES ('stage114') RETURNING id::text`)
	project = httpTestID(t, db, `INSERT INTO projects(customer_id,name,created_by) VALUES($1,'stage114',$2) RETURNING id::text`, customer, user)
	revision = httpTestID(t, db, `INSERT INTO project_revisions(project_id,number,created_by) VALUES($1,1,$2) RETURNING id::text`, project, user)
	system = httpTestID(t, db, `INSERT INTO project_systems(revision_id,code,name,type_code) VALUES($1,'П-31','Supply','supply') RETURNING id::text`, revision)
	item = httpTestID(t, db, `INSERT INTO project_work_items(revision_id,system_id,name_snapshot,input_quantity,input_unit_code,calculated_quantity,result_unit_code,billable_quantity,billable_unit_code,unit_price,amount) VALUES($1,$2,'Duct',6,'m2',6,'m2',6,'m2',800,4800) RETURNING id::text`, revision, system)
	worker = httpTestID(t, db, `INSERT INTO workers(display_name) VALUES('Stage114 worker') RETURNING id::text`)
	httpTestExec(t, db, `INSERT INTO project_workers(project_id,worker_id) VALUES($1,$2)`, project, worker)
	assignment = httpTestID(t, db, `INSERT INTO work_assignments(revision_id,work_item_id,worker_id,planned_quantity) VALUES($1,$2,$3,6) RETURNING id::text`, revision, item, worker)
	httpTestExec(t, db, `INSERT INTO worker_rate_snapshots(assignment_id,rate_per_billable_unit,unit_code) VALUES($1,200,'m2')`, assignment)
	return
}

func TestStage114ApprovedImmutable(t *testing.T) {
	db := isolatedStage114(t)
	user, _, rev, sys, item, _, _ := fixture114(t, db)
	httpTestExec(t, db, `UPDATE project_revisions SET status='in_review' WHERE id=$1`, rev)
	httpTestExec(t, db, `UPDATE project_revisions SET status='approved',approved_by=$2,approved_at=now() WHERE id=$1`, rev, user)
	for _, tc := range []struct {
		name, query string
		args        []any
	}{
		{"insert system", `INSERT INTO project_systems(revision_id,code,name,type_code) VALUES($1,'В-27','Exhaust','exhaust')`, []any{rev}},
		{"update system", `UPDATE project_systems SET name='Changed' WHERE id=$1`, []any{sys}},
		{"delete item", `DELETE FROM project_work_items WHERE id=$1`, []any{item}},
		{"change item price", `UPDATE project_work_items SET unit_price=1 WHERE id=$1`, []any{item}},
		{"unapprove", `UPDATE project_revisions SET status='draft',approved_by=NULL,approved_at=NULL WHERE id=$1`, []any{rev}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, c := context.WithTimeout(context.Background(), 5*time.Second)
			defer c()
			if _, err := db.Exec(ctx, tc.query, tc.args...); err == nil {
				t.Fatal("approved revision unexpectedly changed")
			}
		})
	}
	var price, count string
	if err := db.QueryRow(context.Background(), `SELECT unit_price::text FROM project_work_items WHERE id=$1`, item).Scan(&price); err != nil || price != "800.0000" {
		t.Fatalf("item changed: price=%q err=%v", price, err)
	}
	if err := db.QueryRow(context.Background(), `SELECT count(*)::text FROM project_systems WHERE revision_id=$1`, rev).Scan(&count); err != nil || count != "1" {
		t.Fatalf("system count=%s err=%v", count, err)
	}
}

func TestStage114ConcurrentFactApprovalAndPayments(t *testing.T) {
	db := isolatedStage114(t)
	user, _, rev, _, _, worker, assignment := fixture114(t, db)
	// Two workers try to record 4 m² each against a 6 m² job.
	other := httpTestID(t, db, `INSERT INTO workers(display_name) VALUES('Other') RETURNING id::text`)
	var project string
	if err := db.QueryRow(context.Background(), `SELECT project_id::text FROM project_revisions WHERE id=$1`, rev).Scan(&project); err != nil {
		t.Fatal(err)
	}
	httpTestExec(t, db, `INSERT INTO project_workers(project_id,worker_id) VALUES($1,$2)`, project, other)
	second := httpTestID(t, db, `INSERT INTO work_assignments(revision_id,work_item_id,worker_id,planned_quantity) SELECT revision_id,work_item_id,$2,6 FROM work_assignments WHERE id=$1 RETURNING id::text`, assignment, other)
	httpTestExec(t, db, `INSERT INTO worker_rate_snapshots(assignment_id,rate_per_billable_unit,unit_code) VALUES($1,200,'m2')`, second)
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, a := range []string{assignment, second} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			<-start
			ctx, c := context.WithTimeout(context.Background(), 10*time.Second)
			defer c()
			_, err := db.Exec(ctx, `INSERT INTO work_execution_events(assignment_id,event_type,quantity_delta,performed_on,created_by) VALUES($1,'performed',4,current_date,$2)`, id, user)
			results <- err
		}(a)
	}
	close(start)
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("parallel overbooking: successes=%d, want 1", successes)
	}
	var total string
	if err := db.QueryRow(context.Background(), `SELECT sum(e.quantity_delta)::text FROM work_execution_events e JOIN work_assignments a ON a.id=e.assignment_id WHERE a.revision_id=$1`, rev).Scan(&total); err != nil || total != "4.00000000" {
		t.Fatalf("recorded=%q err=%v", total, err)
	}
	var event, actualWorker string
	if err := db.QueryRow(context.Background(), `SELECT e.id::text,a.worker_id::text FROM work_execution_events e JOIN work_assignments a ON a.id=e.assignment_id WHERE a.revision_id=$1`, rev).Scan(&event, &actualWorker); err != nil {
		t.Fatal(err)
	}
	_ = worker
	// Simultaneous duplicate approval uses ON CONFLICT DO NOTHING.
	start = make(chan struct{})
	results = make(chan error, 2)
	counts := make(chan int64, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ctx, c := context.WithTimeout(context.Background(), 10*time.Second)
			defer c()
			tag, err := db.Exec(ctx, `INSERT INTO work_execution_approvals(event_id,approved_by) VALUES($1,$2) ON CONFLICT(event_id) DO NOTHING`, event, user)
			if err == nil {
				counts <- tag.RowsAffected()
			}
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(counts)
	inserted := int64(0)
	for n := range counts {
		inserted += n
	}
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if inserted != 1 {
		t.Fatalf("duplicate approvals inserted %d", inserted)
	}
	// Approved accrual is 800. Two simultaneous payments of 500 must not both pass.
	start = make(chan struct{})
	results = make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ctx, c := context.WithTimeout(context.Background(), 10*time.Second)
			defer c()
			_, err := db.Exec(ctx, `INSERT INTO project_worker_payments(revision_id,worker_id,amount,paid_on,created_by) VALUES($1,$2,500,current_date,$3)`, rev, actualWorker, user)
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	successes = 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent overpayment accepted: %d successes", successes)
	}
}

func TestStage114HTTPXLSXImportDatabaseExport(t *testing.T) {
	db := isolatedStage114(t)
	pwd := "stage114-password"
	hash, err := auth.HashPassword(pwd)
	if err != nil {
		t.Fatal(err)
	}
	admin := httpTestID(t, db, `INSERT INTO users(login,display_name,password_hash) VALUES ('stage114-'||gen_random_uuid()::text,'Admin',$1) RETURNING id::text`, hash)
	httpTestExec(t, db, `INSERT INTO user_roles(user_id,role_code) VALUES($1,'admin')`, admin)
	cust := httpTestID(t, db, `INSERT INTO customers(name) VALUES('Import') RETURNING id::text`)
	proj := httpTestID(t, db, `INSERT INTO projects(customer_id,name,created_by) VALUES($1,'Import project',$2) RETURNING id::text`, cust, admin)
	rev := httpTestID(t, db, `INSERT INTO project_revisions(project_id,number,created_by) VALUES($1,1,$2) RETURNING id::text`, proj, admin)
	var login string
	if err := db.QueryRow(context.Background(), `SELECT login FROM users WHERE id=$1`, admin).Scan(&login); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(db)
	call := func(method, path, contentType string, data []byte, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://localhost:8080"+path, bytes.NewReader(data))
		req.Header.Set("Origin", "http://localhost:8080")
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		for _, c := range cookies {
			req.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	l := call("POST", "/login", "application/x-www-form-urlencoded", []byte(url.Values{"login": {login}, "password": {pwd}}.Encode()))
	if l.Code != 303 {
		t.Fatalf("login %d: %s", l.Code, l.Body.String())
	}
	var session, csrf *http.Cookie
	for _, c := range l.Result().Cookies() {
		if c.Name == auth.SessionCookie {
			session = c
		}
		if c.Name == "montage_csrf" {
			csrf = c
		}
	}
	if session == nil || csrf == nil {
		t.Fatal("missing login cookies")
	}
	file, err := os.ReadFile(filepath.Join("..", "..", "testdata", "legacy_reference.xlsx"))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := xlsxledger.Parse(file)
	if err != nil || len(parsed.Items) != 124 || parsed.Total != "182607.09" {
		t.Fatalf("reference parse: %+v, %v", len(parsed.Items), err)
	}
	var body bytes.Buffer
	mp := multipart.NewWriter(&body)
	if err = mp.WriteField("csrf", csrf.Value); err != nil {
		t.Fatal(err)
	}
	part, err := mp.CreateFormFile("file", "legacy.xlsx")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.Copy(part, bytes.NewReader(file)); err != nil {
		t.Fatal(err)
	}
	if err = mp.Close(); err != nil {
		t.Fatal(err)
	}
	prefix := "/app/revisions/" + rev + "/xlsx"
	preview := call("POST", prefix+"/preview", mp.FormDataContentType(), body.Bytes(), session, csrf)
	if preview.Code != 200 {
		t.Fatalf("preview %d: %s", preview.Code, preview.Body.String())
	}
	re := regexp.MustCompile(`name="token" value="([A-Za-z0-9_-]+)"`)
	match := re.FindStringSubmatch(preview.Body.String())
	if len(match) != 2 {
		t.Fatal("preview confirmation token missing")
	}
	confirmation := url.Values{"csrf": {csrf.Value}, "token": {match[1]}}
	committed := call("POST", prefix+"/commit", "application/x-www-form-urlencoded", []byte(confirmation.Encode()), session, csrf)
	if committed.Code != 303 {
		t.Fatalf("commit %d: %s", committed.Code, committed.Body.String())
	}
	var n int
	var total string
	err = db.QueryRow(context.Background(), `SELECT count(*),COALESCE(SUM(amount),0)::text FROM project_work_items WHERE revision_id=$1`, rev).Scan(&n, &total)
	if err != nil || n != 124 || total != "182607.09" {
		t.Fatalf("database rows=%d sum=%s err=%v", n, total, err)
	}
	exported := call("GET", prefix+"/export", "", nil, session)
	if exported.Code != 200 {
		t.Fatalf("export %d: %s", exported.Code, exported.Body.String())
	}
	xf, err := excelize.OpenReader(bytes.NewReader(exported.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer xf.Close()
	rows, err := xf.GetRows("Ведомость")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	sum := ""
	for _, row := range rows {
		if len(row) > 1 && row[1] == "Итого по ведомости" && len(row) > 7 {
			sum = row[7]
		}
		if len(row) > 0 && regexp.MustCompile(`^[0-9]+$`).MatchString(strings.TrimSpace(row[0])) {
			count++
		}
	}
	if count != 124 {
		t.Fatalf("export lines=%d want 124", count)
	}
	// GetRows returns the formatted display value. Excel may include a
	// thousands separator (e.g. "182,607.09") despite identical numeric data.
	// The reference workbook uses a dot as decimal separator.
	normalizedTotal := strings.ReplaceAll(strings.TrimSpace(sum), ",", "")
	if normalizedTotal != "182607.09" {
		t.Fatalf("export grand total=%q (normalized=%q)", sum, normalizedTotal)
	}
	// A second import must not overwrite the existing 124 rows.
	again := call("POST", prefix+"/commit", "application/x-www-form-urlencoded", []byte(confirmation.Encode()), session, csrf)
	if again.Code == 303 {
		t.Fatal("duplicate confirmation was accepted")
	}
}
