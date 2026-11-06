package httpserver

import (
	"context"
	"montage-ledger/internal/auth"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// HTTP integration tests are opt-in. Never connect these tests to DATABASE_URL.
// They write only to a database whose actual PostgreSQL database name contains "test".
func httpTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("LEDGER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("LEDGER_TEST_DATABASE_URL is unset; HTTP integration test skipped")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse test DSN: %v", err)
	}
	if !strings.Contains(strings.ToLower(cfg.ConnConfig.Database), "test") {
		t.Fatal("refusing HTTP integration test: configured database name must contain 'test'")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("open test pool: %v", err)
	}
	t.Cleanup(pool.Close)
	var database string
	if err := pool.QueryRow(ctx, `SELECT current_database()`).Scan(&database); err != nil {
		t.Fatalf("query database: %v", err)
	}
	if !strings.Contains(strings.ToLower(database), "test") {
		t.Fatalf("refusing writes to database %q", database)
	}
	return pool
}

func httpTestID(t *testing.T, db *pgxpool.Pool, query string, args ...any) string {
	t.Helper()
	var id string
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.QueryRow(ctx, query, args...).Scan(&id); err != nil {
		t.Fatalf("fixture insert failed: %v", err)
	}
	return id
}
func httpTestExec(t *testing.T, db *pgxpool.Pool, query string, args ...any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		t.Fatalf("fixture SQL failed: %v", err)
	}
}

// The cleanup is deliberately scoped to generated IDs, never TRUNCATE or DROP.
// The fresh fixture is not run in parallel and always runs against the isolated test DB.
func TestHTTPAuthenticationRBACCSRFAndReports(t *testing.T) {
	db := httpTestPool(t)
	password := "stage11-3-test-password-only"
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	admin := httpTestID(t, db, `INSERT INTO users(login,display_name,password_hash) VALUES ('stage113admin-' || gen_random_uuid()::text,'Stage11 admin',$1) RETURNING id::text`, hash)
	engineer := httpTestID(t, db, `INSERT INTO users(login,display_name,password_hash) VALUES ('stage113eng-' || gen_random_uuid()::text,'Stage11 engineer',$1) RETURNING id::text`, hash)
	outsider := httpTestID(t, db, `INSERT INTO users(login,display_name,password_hash) VALUES ('stage113outsider-' || gen_random_uuid()::text,'Stage11 outsider',$1) RETURNING id::text`, hash)
	customer := httpTestID(t, db, `INSERT INTO customers(name) VALUES ('Stage 11.3 HTTP fixture') RETURNING id::text`)
	project := httpTestID(t, db, `INSERT INTO projects(customer_id,name,created_by) VALUES ($1,'Stage 11.3 project',$2) RETURNING id::text`, customer, admin)
	revision := httpTestID(t, db, `INSERT INTO project_revisions(project_id,number,created_by) VALUES ($1,1,$2) RETURNING id::text`, project, admin)
	httpTestExec(t, db, `INSERT INTO user_roles(user_id,role_code) VALUES ($1,'admin'),($2,'engineer'),($3,'installer')`, admin, engineer, outsider)
	httpTestExec(t, db, `INSERT INTO project_members(project_id,user_id,permission) VALUES ($1,$2,'read')`, project, engineer)
	// Cleanup runs even if an assertion fails (including after login creates sessions).
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		for _, op := range []struct {
			sql  string
			args []any
		}{
			{`DELETE FROM user_sessions WHERE user_id IN ($1,$2,$3)`, []any{admin, engineer, outsider}},
			{`DELETE FROM project_members WHERE project_id=$1`, []any{project}},
			{`DELETE FROM project_revisions WHERE id=$1`, []any{revision}},
			{`DELETE FROM projects WHERE id=$1`, []any{project}},
			{`DELETE FROM customers WHERE id=$1`, []any{customer}},
			{`DELETE FROM user_roles WHERE user_id IN ($1,$2,$3)`, []any{admin, engineer, outsider}},
			{`DELETE FROM users WHERE id IN ($1,$2,$3)`, []any{admin, engineer, outsider}},
		} {
			if _, e := db.Exec(ctx, op.sql, op.args...); e != nil {
				t.Errorf("fixture cleanup failed: %v", e)
			}
		}
	})

	var adminLogin, engineerLogin, outsiderLogin string
	for _, v := range []struct {
		id   string
		into *string
	}{{admin, &adminLogin}, {engineer, &engineerLogin}, {outsider, &outsiderLogin}} {
		var login string
		if err := db.QueryRow(context.Background(), `SELECT login FROM users WHERE id=$1`, v.id).Scan(&login); err != nil {
			t.Fatal(err)
		}
		*v.into = login
	}
	handler := NewHandler(db)
	request := func(method, path string, form url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		var body *strings.Reader
		if form == nil {
			body = strings.NewReader("")
		} else {
			body = strings.NewReader(form.Encode())
		}
		req := httptest.NewRequest(method, "http://localhost:8080"+path, body)
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		req.Header.Set("Origin", "http://localhost:8080")
		for _, c := range cookies {
			req.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	login := func(user string) (*http.Cookie, *http.Cookie) {
		t.Helper()
		rec := request(http.MethodPost, "/login", url.Values{"login": {user}, "password": {password}})
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("login %s returned %d: %s", user, rec.Code, rec.Body.String())
		}
		var session, csrf *http.Cookie
		for _, c := range rec.Result().Cookies() {
			if c.Name == auth.SessionCookie {
				session = c
			}
			if c.Name == "montage_csrf" {
				csrf = c
			}
		}
		if session == nil || csrf == nil || session.Value == "" || csrf.Value == "" {
			t.Fatal("login did not issue session and CSRF cookies")
		}
		if !session.HttpOnly || !csrf.HttpOnly {
			t.Fatal("session cookies must be HttpOnly")
		}
		return session, csrf
	}

	t.Run("unauthenticated redirects", func(t *testing.T) {
		for _, path := range []string{"/app/projects/" + project, "/app/revisions/" + revision + "/reports"} {
			if got := request(http.MethodGet, path, nil).Code; got != http.StatusSeeOther {
				t.Errorf("%s status %d, want 303", path, got)
			}
		}
	})
	t.Run("invalid password", func(t *testing.T) {
		rec := request(http.MethodPost, "/login", url.Values{"login": {adminLogin}, "password": {"wrong-password"}})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("got %d, want 401", rec.Code)
		}
	})
	adminSession, adminCSRF := login(adminLogin)
	engineerSession, engineerCSRF := login(engineerLogin)
	outsiderSession, _ := login(outsiderLogin)
	t.Run("role restrictions and project membership", func(t *testing.T) {
		for _, tc := range []struct {
			name, path string
			cookie     *http.Cookie
			code       int
		}{
			{"admin project", "/app/projects/" + project, adminSession, 200},
			{"reader project", "/app/projects/" + project, engineerSession, 200},
			{"outsider project", "/app/projects/" + project, outsiderSession, 404},
			{"reader customers", "/app/customers", engineerSession, 403},
			{"outsider report", "/app/revisions/" + revision + "/reports", outsiderSession, 404},
			{"reader report", "/app/revisions/" + revision + "/reports", engineerSession, 200},
			{"admin report", "/app/revisions/" + revision + "/reports", adminSession, 200},
		} {
			t.Run(tc.name, func(t *testing.T) {
				r := request(http.MethodGet, tc.path, nil, tc.cookie)
				if r.Code != tc.code {
					t.Fatalf("got %d, want %d: %s", r.Code, tc.code, r.Body.String())
				}
			})
		}
	})
	t.Run("read-only cannot create revision", func(t *testing.T) {
		r := request(http.MethodPost, "/app/projects/"+project+"/revisions", url.Values{"csrf": {engineerCSRF.Value}}, engineerSession, engineerCSRF)
		if r.Code != http.StatusForbidden {
			t.Fatalf("got %d want 403", r.Code)
		}
	})
	t.Run("CSRF and origin rejected", func(t *testing.T) {
		r := request(http.MethodPost, "/app/projects/"+project+"/revisions", url.Values{"csrf": {"invalid"}}, adminSession, adminCSRF)
		if r.Code != http.StatusForbidden {
			t.Fatalf("invalid csrf got %d", r.Code)
		}
		before := httpTestID(t, db, `SELECT COUNT(*)::text FROM project_revisions WHERE project_id=$1`, project)
		if before != "1" {
			t.Fatalf("CSRF-invalid request mutated revisions: count=%s", before)
		}
		req := httptest.NewRequest(http.MethodPost, "http://localhost:8080/app/projects/"+project+"/revisions", strings.NewReader(url.Values{"csrf": {adminCSRF.Value}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", "http://evil.example")
		req.AddCookie(adminSession)
		req.AddCookie(adminCSRF)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("cross-origin got %d", rec.Code)
		}
	})
	t.Run("report export is XLSX", func(t *testing.T) {
		rec := request(http.MethodGet, "/app/revisions/"+revision+"/reports/export", nil, adminSession)
		if rec.Code != 200 {
			t.Fatalf("export got %d: %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Header().Get("Content-Type"), "spreadsheet") {
			t.Fatalf("unexpected export type %q", rec.Header().Get("Content-Type"))
		}
		if len(rec.Body.Bytes()) < 4 || string(rec.Body.Bytes()[:2]) != "PK" {
			t.Fatal("XLSX is not a ZIP file")
		}
	})
	t.Run("logout revokes session", func(t *testing.T) {
		rec := request(http.MethodPost, "/logout", url.Values{"csrf": {adminCSRF.Value}}, adminSession, adminCSRF)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("logout got %d", rec.Code)
		}
		if got := request(http.MethodGet, "/app", nil, adminSession).Code; got != http.StatusSeeOther {
			t.Fatalf("revoked session accepted: %d", got)
		}
	})
}
