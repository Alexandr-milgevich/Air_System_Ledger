package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
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
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write([]byte(`<!doctype html><html lang="ru"><meta charset="utf-8"><title>Монтажные ведомости</title><body><h1>Монтажные ведомости ОВиК</h1><p>Базовый сервер. Каталоги и редактор ведомостей пока не реализованы.</p></body></html>`))
	})
	return mux
}

func sendJSON(w http.ResponseWriter, code int, response statusResponse) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(response)
}
