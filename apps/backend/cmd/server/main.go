// Command server is the actilens backend HTTP entrypoint.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"actilens/backend/internal/config"
	"actilens/backend/internal/db"
	"actilens/backend/internal/filestore"
	"actilens/backend/internal/exporter"
	"actilens/backend/internal/lifecycle"
	"actilens/backend/internal/obs"
	"actilens/backend/internal/retention"
	"actilens/backend/internal/server"
	"actilens/backend/internal/store"

	"github.com/getsentry/sentry-go"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	if err := obs.Init(cfg.Environment, obs.FileConfig{
		Dir:        cfg.LogDir,
		MaxSizeMB:  cfg.LogMaxSizeMB,
		MaxBackups: cfg.LogMaxBackups,
		MaxAgeDays: cfg.LogMaxAgeDays,
	}); err != nil {
		log.Printf("log file unavailable, logging to stdout only: %v", err)
	}
	defer obs.Close()

	// Sentry error reporting. Empty DSN ⇒ disabled (no-op), so local dev stays quiet.
	if cfg.SentryDSN != "" {
		if err := sentry.Init(sentry.ClientOptions{
			Dsn:              cfg.SentryDSN,
			Environment:      cfg.Environment,
			TracesSampleRate: 0.0,
		}); err != nil {
			log.Printf("sentry init: %v", err)
		} else {
			defer sentry.Flush(2 * time.Second)
		}
	}

	if err := os.MkdirAll(cfg.StorageDir+"/screenshots", 0o755); err != nil {
		log.Fatalf("create storage dir: %v", err)
	}

	// Run migrations before opening the runtime pool so the schema is ready.
	if err := db.Migrate(cfg.DatabaseURL); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	ctx := context.Background()
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer pool.Close()

	st := store.New(pool)
	files := filestore.New(cfg.StorageDir)
	ret := retention.New(st, files)
	exp := exporter.New(st, files)
	life := lifecycle.New(st, files)

	// Hourly retention sweep (plus one on startup).
	sweepCtx, stopSweeper := context.WithCancel(ctx)
	defer stopSweeper()
	ret.StartSweeper(sweepCtx, time.Hour)
	exp.StartWorker(sweepCtx, 2*time.Second)
	life.StartWorker(sweepCtx, time.Minute)

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: server.New(cfg, st, files, ret),
	}

	go func() {
		log.Printf("listening on :%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server: %v", err)
		}
	}()

	// Graceful shutdown on SIGINT/SIGTERM.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
	log.Println("stopped")
}
