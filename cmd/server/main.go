package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"montage-ledger/internal/config"
	"montage-ledger/internal/database"
	"montage-ledger/internal/httpserver"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := config.LoadDotEnvIfPresent(".env"); err != nil {
		logger.Error("load local environment", "error", err)
		os.Exit(1)
	}
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := database.Open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		// Do not log the potentially credential-bearing driver error.
		logger.Error("postgres connection failed; check DATABASE_URL and PostgreSQL availability")
		os.Exit(1)
	}
	defer pool.Close()
	logger.Info("postgres connection verified")
	server := &http.Server{
		Addr: addr, Handler: httpserver.NewHandler(pool),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("shutdown", "error", err)
		}
	}()
	logger.Info("starting HTTP server", "addr", addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("http server", "error", err)
		os.Exit(1)
	}
	logger.Info("server stopped")
}
