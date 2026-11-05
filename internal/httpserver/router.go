package httpserver

import (
	"encoding/json"
	"net/http"
)

type statusResponse struct {
	Status  string `json:"status"`
	Service string `json:"service"`
}

// NewHandler provides only the bootstrap endpoints. /readyz intentionally
// returns 503 until a database-backed readiness probe is implemented.
func NewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		sendJSON(w, http.StatusOK, statusResponse{Status: "alive", Service: "montage-ledger"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		sendJSON(w, http.StatusServiceUnavailable, statusResponse{Status: "database_not_connected", Service: "montage-ledger"})
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write([]byte(`<!doctype html><html lang="ru"><meta charset="utf-8"><title>Монтажные ведомости</title><body><h1>Монтажные ведомости ОВиК</h1><p>Стартовый сервер. Работа с данными будет доступна после подключения PostgreSQL и авторизации.</p></body></html>`))
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
