// Command registry serves the template marketplace's public read API on its
// own, against the same database, for deployments that put the registry on a
// separate host from Multica. Operator writes stay in Multica's /admin.
//
// Environment: DATABASE_URL (required), PORT (default 8090), and
// REGISTRY_PUBLIC_URL, the absolute origin bundle URLs are built from.
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

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/registry"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		slog.Error("DATABASE_URL is required")
		os.Exit(1)
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8090"
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		slog.Error("connect database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	public := &registry.Public{
		Service: registry.NewService(db.New(pool)),
		BaseURL: os.Getenv("REGISTRY_PUBLIC_URL"),
	}
	router := chi.NewRouter()
	router.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	router.Route("/registry/v1", public.Routes)

	server := &http.Server{Addr: ":" + port, Handler: router, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	slog.Info("registry listening", "port", port)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("registry server", "error", err)
		os.Exit(1)
	}
}
