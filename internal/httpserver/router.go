package httpserver

import (
	"context"
	"encoding/json"
	"montage-ledger/internal/auth"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type DatabasePinger interface{ Ping(context.Context) error }

type statusResponse struct {
	Status  string `json:"status"`
	Service string `json:"service"`
}

// NewHandler exposes liveness independently of PostgreSQL readiness.
// A nil pinger is treated as unavailable, never as healthy.
func NewHandler(db DatabasePinger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /assets/app.css", appCSSHandler)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		sendJSON(w, http.StatusOK, statusResponse{Status: "alive", Service: "montage-ledger"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if db == nil {
			sendJSON(w, http.StatusServiceUnavailable, statusResponse{Status: "database_unavailable", Service: "montage-ledger"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := db.Ping(ctx); err != nil {
			// Never leak database errors (including connection details) in HTTP responses.
			sendJSON(w, http.StatusServiceUnavailable, statusResponse{Status: "database_unavailable", Service: "montage-ledger"})
			return
		}
		sendJSON(w, http.StatusOK, statusResponse{Status: "ready", Service: "montage-ledger"})
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/app", http.StatusSeeOther)
	})
	if pool, ok := db.(*pgxpool.Pool); ok && pool != nil {
		x := web{db: pool, auth: auth.Store{DB: pool}}
		mux.HandleFunc("GET /login", x.loginGet)
		mux.HandleFunc("POST /login", x.loginPost)
		mux.HandleFunc("POST /logout", x.requireUser(x.logout))
		mux.HandleFunc("GET /app", x.requireUser(x.home))
		mux.HandleFunc("GET /app/customers", x.requireUser(x.customersGet))
		mux.HandleFunc("POST /app/customers", x.requireUser(x.customersPost))
		mux.HandleFunc("GET /app/projects", x.requireUser(x.projectsGet))
		mux.HandleFunc("POST /app/projects", x.requireUser(x.projectsPost))
		mux.HandleFunc("GET /app/projects/{id}", x.requireUser(x.projectGet))
		mux.HandleFunc("POST /app/projects/{id}/revisions", x.requireUser(x.revisionsPost))
		mux.HandleFunc("POST /app/projects/{id}/members", x.requireUser(x.membersPost))
		mux.HandleFunc("POST /app/projects/{id}/workers", x.requireUser(x.projectWorkersPost))
		mux.HandleFunc("GET /app/revisions/{id}", x.requireUser(x.revisionGet))
		mux.HandleFunc("GET /app/revisions/{id}/review", x.requireUser(x.reviewGet))
		mux.HandleFunc("POST /app/revisions/{id}/review/submit", x.requireUser(x.reviewSubmit))
		mux.HandleFunc("POST /app/revisions/{id}/review/decide", x.requireUser(x.reviewDecide))
		mux.HandleFunc("POST /app/revisions/{id}/review/exception/request", x.requireUser(x.reviewExceptionRequest))
		mux.HandleFunc("POST /app/revisions/{id}/review/exception/accept", x.requireUser(x.reviewExceptionAccept))
		mux.HandleFunc("GET /app/revisions/{id}/review/approved.xlsx", x.requireUser(x.approvedXLSX))
		mux.HandleFunc("GET /app/revisions/{id}/ledger", x.requireUser(x.ledgerGet))
		mux.HandleFunc("POST /app/revisions/{id}/clone", x.requireUser(x.revisionClone))
		mux.HandleFunc("GET /app/revisions/{id}/compare", x.requireUser(x.revisionCompare))
		mux.HandleFunc("GET /app/revisions/{id}/history", x.requireUser(x.revisionHistory))
		mux.HandleFunc("POST /app/revisions/{id}/items", x.requireUser(x.itemCreate))
		mux.HandleFunc("POST /app/revisions/{id}/bulk/preview", x.requireUser(x.bulkPreview))
		mux.HandleFunc("POST /app/revisions/{id}/bulk/commit", x.requireUser(x.bulkCommit))
		mux.HandleFunc("GET /app/revisions/{id}/bulk/history", x.requireUser(x.bulkHistoryGet))
		mux.HandleFunc("POST /app/revisions/{id}/bulk/{operation}/undo", x.requireUser(x.bulkUndo))
		mux.HandleFunc("POST /app/revisions/{id}/items/{item}", x.requireUser(x.itemUpdate))
		mux.HandleFunc("POST /app/revisions/{id}/items/{item}/delete", x.requireUser(x.itemDelete))
		mux.HandleFunc("POST /app/revisions/{id}/systems", x.requireUser(x.systemsPost))
		mux.HandleFunc("POST /app/revisions/{id}/locations", x.requireUser(x.locationsPost))
		mux.HandleFunc("GET /app/revisions/{id}/xlsx", x.requireUser(x.xlsxGet))
		mux.HandleFunc("POST /app/revisions/{id}/xlsx/preview", x.requireUser(x.xlsxPreview))
		mux.HandleFunc("POST /app/revisions/{id}/xlsx/commit", x.requireUser(x.xlsxCommit))
		mux.HandleFunc("GET /app/revisions/{id}/xlsx/export", x.requireUser(x.xlsxExport))
		mux.HandleFunc("GET /app/revisions/{id}/execution", x.requireUser(x.executionGet))
		mux.HandleFunc("GET /app/revisions/{id}/execution/progress", x.requireUser(x.executionProgressGet))
		mux.HandleFunc("GET /app/revisions/{id}/execution/schedule", x.requireUser(x.executionScheduleGet))
		mux.HandleFunc("GET /app/revisions/{id}/execution/workload", x.requireUser(x.executionWorkloadGet))
		mux.HandleFunc("POST /app/revisions/{id}/execution/{assignment}/allocation", x.requireUser(x.executionAllocationSave))
		mux.HandleFunc("POST /app/revisions/{id}/execution/{assignment}/schedule", x.requireUser(x.executionScheduleSave))
		mux.HandleFunc("POST /app/revisions/{id}/execution/assign", x.requireUser(x.executionAssign))
		mux.HandleFunc("POST /app/revisions/{id}/execution/{event}/approve", x.requireUser(x.executionApprove))
		mux.HandleFunc("POST /app/revisions/{id}/execution/{assignment}/record", x.requireUser(x.executionRecord))
		mux.HandleFunc("GET /app/revisions/{id}/execution/export", x.requireUser(x.executionExport))
		mux.HandleFunc("GET /app/revisions/{id}/reports", x.requireUser(x.reportGet))
		mux.HandleFunc("GET /app/revisions/{id}/reports/export", x.requireUser(x.reportExport))
		mux.HandleFunc("POST /app/revisions/{id}/reports/payments", x.requireUser(x.paymentPost))
		mux.HandleFunc("GET /app/admin/users", x.requireUser(x.adminUsersGet))
		mux.HandleFunc("POST /app/admin/users", x.requireUser(x.adminUsersCreate))
		mux.HandleFunc("POST /app/admin/users/{id}/update", x.requireUser(x.adminUsersUpdate))
		mux.HandleFunc("POST /app/admin/users/{id}/password", x.requireUser(x.adminUsersPassword))
		mux.HandleFunc("POST /app/admin/users/{id}/status", x.requireUser(x.adminUsersStatus))
		mux.HandleFunc("POST /app/admin/access", x.requireUser(x.adminProjectAccess))
		mux.HandleFunc("GET /app/workers", x.requireUser(x.workersGet))
		mux.HandleFunc("POST /app/workers", x.requireUser(x.workersPost))
	}
	return mux
}

func sendJSON(w http.ResponseWriter, code int, response statusResponse) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(response)
}
