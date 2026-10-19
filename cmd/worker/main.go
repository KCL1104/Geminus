// Command worker is the composition root of the job worker (docs/ARCHITECTURE.md
// §2, §6). It owns configuration and lifecycle only, never business rules:
// it validates configuration, connects to PostgreSQL as the worker runtime
// login (refusing an unsafe one) and runs internal/worker's loop over the
// deployment's organizations until SIGINT or SIGTERM.
//
// No job handler exists yet (import profiling is T-016, simulation T-023,
// AI proposals and analyses T-025), so after validating configuration it
// exits with status 2 instead of claiming jobs it cannot run.
package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kcl1104/geminus/internal/config"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/tenant/workerscope"
	"github.com/kcl1104/geminus/internal/worker"
)

const (
	exitConfig         = 1
	exitNotImplemented = 2
	exitDatabase       = 3
)

// The loop's timing (ADR-024): the queue's default lease, renewed every
// third of it; a pause between passes that found no job; and how many
// expired leases one pass takes back per organization.
const (
	heartbeat = 10 * time.Second
	idle      = time.Second
	reapLimit = 100
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Getenv, os.Stderr, handlers()))
}

// handlers is the registry of the job types this build can run. Each task
// that delivers a handler adds it here; until then it is empty and the
// worker refuses to start.
func handlers() map[jobs.Type]worker.Handler { return nil }

// run runs the loop with handlers until ctx ends.
func run(ctx context.Context, getenv func(string) string, stderr io.Writer, handlers map[jobs.Type]worker.Handler) int {
	log := slog.New(slog.NewJSONHandler(stderr, nil)).With("component", "worker")
	cfg, err := config.Load(getenv)
	wcfg, werr := config.LoadWorker(getenv)
	if err = errors.Join(err, werr); err != nil {
		log.Error("invalid configuration", "error", err.Error())
		return exitConfig
	}
	if len(handlers) == 0 {
		log.Error("NOT IMPLEMENTED: no job handler exists yet (T-016, T-023, T-025); refusing to start",
			"ai_provider", string(cfg.AIProvider))
		return exitNotImplemented
	}
	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		log.Error("invalid configuration", "error", "DATABASE_URL is not a valid PostgreSQL connection string")
		return exitConfig
	}
	// Every connection released to the pool is checked for leftover tenant
	// scope.
	poolCfg.AfterRelease = tenant.Guard
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		log.Error("database unavailable", "error", "the connection pool could not be created")
		return exitDatabase
	}
	defer pool.Close()
	scope, err := workerscope.New(pool, wcfg.Organizations)
	if err != nil {
		log.Error("invalid configuration", "error", "WORKER_ORGANIZATION_IDS must list organization IDs, each once")
		return exitConfig
	}
	w, err := worker.New(scope, worker.Config{ID: wcfg.ID, Policy: jobs.DefaultPolicy, Heartbeat: heartbeat,
		Idle: idle, ReapLimit: reapLimit, Handlers: handlers, Log: log})
	if err != nil {
		log.Error("invalid configuration", "error", err.Error())
		return exitConfig
	}
	checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	err = tenant.CheckRuntimeRole(checkCtx, pool, tenant.WorkerRole)
	cancel()
	if errors.Is(err, tenant.ErrUnsafeRole) {
		// The error names roles and attributes, never the password.
		log.Error("refusing the database login", "error", err.Error())
		return exitDatabase
	}
	if err != nil {
		// pgx names the host, user and database, never the password.
		log.Error("database unavailable", "error", err.Error())
		return exitDatabase
	}
	log.Info("running", "worker_id", wcfg.ID, "organizations", len(wcfg.Organizations))
	w.Run(ctx)
	log.Info("stopped")
	return 0
}
