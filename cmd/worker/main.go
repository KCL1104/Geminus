// Command worker is the composition root of the job worker (docs/ARCHITECTURE.md
// §2, §6). It owns configuration and lifecycle only, never business rules:
// it validates configuration, connects to PostgreSQL as the worker runtime
// login (refusing an unsafe one) and runs internal/worker's loop over the
// deployment's organizations until SIGINT or SIGTERM.
//
// The handlers this build registers: simulation runs (T-023,
// internal/runs/runner). Import profiling (T-016) and AI proposals and
// analyses (T-025) are not built yet; their jobs stay queued.
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
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/domainpack/softwaredelivery"
	"github.com/kcl1104/geminus/internal/runs/runner"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/storage/objects"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/tenant/workerscope"
	"github.com/kcl1104/geminus/internal/worker"
	"github.com/kcl1104/geminus/internal/worker/handler"
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
	ctx, stop := listen()
	defer stop()
	os.Exit(run(ctx, os.Getenv, os.Stderr, handlers))
}

// listen ends ctx at the first SIGINT or SIGTERM: the worker then stops its
// handler and records the attempt. The signals' default handling is
// restored before ctx ends, so a second signal ends the process at once,
// even while a handler does not return.
func listen() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-signals:
		case <-ctx.Done():
		}
		signal.Stop(signals)
		cancel()
	}()
	return ctx, func() { signal.Stop(signals); cancel() }
}

// deps is what the handlers are built from: the validated configuration.
type deps struct {
	worker  config.Worker
	objects objects.Store // nil when object storage is disabled
	log     *slog.Logger
}

// builder makes the handlers a build runs.
type builder func(deps) (map[jobs.Type]handler.Handler, error)

// handlers is the registry of the job types this build can run. Each task
// that delivers a handler (internal/worker/handler) adds it here.
func handlers(d deps) (map[jobs.Type]handler.Handler, error) {
	packs, err := domainpack.NewRegistry(softwaredelivery.Pack{})
	if err != nil {
		return nil, err
	}
	sim, err := runner.New(runner.Config{Packs: packs, Objects: d.objects, Actor: d.worker.ID, Log: d.log})
	if err != nil {
		return nil, err
	}
	return map[jobs.Type]handler.Handler{jobs.Simulation: sim}, nil
}

// objectStore opens the configured object storage (D-044): nil when it is
// disabled; the filesystem adapter only for development and tests.
func objectStore(cfg config.Config) (objects.Store, error) {
	if cfg.ObjectStorage.Provider != config.ObjectStorageFilesystem {
		return nil, nil
	}
	fs, err := objects.NewFS(cfg.ObjectStorage.Root, cfg.Env)
	if err != nil {
		return nil, err
	}
	return fs, nil
}

// run runs the loop with the handlers build makes until ctx ends.
func run(ctx context.Context, getenv func(string) string, stderr io.Writer, build builder) int {
	log := slog.New(slog.NewJSONHandler(stderr, nil)).With("component", "worker")
	cfg, err := config.Load(getenv)
	wcfg, werr := config.LoadWorker(getenv)
	if err = errors.Join(err, werr); err != nil {
		log.Error("invalid configuration", "error", err.Error())
		return exitConfig
	}
	// The whole configuration is checked before anything else: the allowlist
	// (workerscope checks it without a pool) and the connection string as
	// pgx reads it, which no connection is made for yet.
	if _, err := workerscope.New(nil, wcfg.Organizations); err != nil {
		log.Error("invalid configuration", "error", "WORKER_ORGANIZATION_IDS must list organization IDs, each once")
		return exitConfig
	}
	poolCfg, err := poolConfig(cfg.DatabaseURL)
	if err != nil {
		log.Error("invalid configuration", "error", "DATABASE_URL is not a valid PostgreSQL connection string")
		return exitConfig
	}
	store, err := objectStore(cfg)
	if err != nil {
		log.Error("invalid configuration", "error", "OBJECT_STORAGE_ROOT cannot be used: "+err.Error())
		return exitConfig
	}
	handlers, err := build(deps{worker: wcfg, objects: store, log: log})
	if err != nil {
		log.Error("invalid configuration", "error", err.Error())
		return exitConfig
	}
	if len(handlers) == 0 {
		log.Error("NOT IMPLEMENTED: this build has no job handler; refusing to start", "ai_provider", string(cfg.AIProvider))
		return exitNotImplemented
	}
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
	if ctx.Err() != nil {
		// A signal before the loop began: a stop, not a database failure.
		log.Info("stopped before starting")
		return 0
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

// poolConfig is the worker's connection pool: every connection released to
// it is checked for leftover tenant scope (tenant.Guard).
func poolConfig(databaseURL string) (*pgxpool.Config, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	cfg.AfterRelease = tenant.Guard
	return cfg, nil
}
