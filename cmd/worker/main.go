// Command worker is the composition root of the job worker (docs/ARCHITECTURE.md
// §2, §6). It owns configuration and lifecycle only, never business rules:
// it validates configuration, connects to PostgreSQL as the worker runtime
// login (refusing an unsafe one) and runs internal/worker's loop over the
// deployment's organizations until SIGINT or SIGTERM.
//
// The handlers this build registers: simulation runs (T-023,
// internal/runs/runner), import profiling (T-016, internal/import/profiler),
// import validation (T-017, internal/import/validator) and AI proposals
// (T-025, internal/proposals/generator), which ask the provider of the
// configured mode: disabled (the default; every proposal fails
// AI_PROVIDER_NOT_CONFIGURED), the deterministic fake (development and
// tests) or the live adapter, which config enables only with the owner's
// recorded approval (none is recorded: H-001, H-017). Analyses (T-030) are
// not built yet; their jobs stay queued.
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

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/anthropic"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/config"
	"github.com/kcl1104/geminus/internal/import/profiler"
	"github.com/kcl1104/geminus/internal/import/validator"
	"github.com/kcl1104/geminus/internal/observability"
	"github.com/kcl1104/geminus/internal/observability/telemetry/export"
	"github.com/kcl1104/geminus/internal/packs"
	"github.com/kcl1104/geminus/internal/proposals/generator"
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
	ai      config.AI
	objects objects.Store // nil when object storage is disabled
	log     *slog.Logger
}

// builder makes the handlers a build runs.
type builder func(deps) (map[jobs.Type]handler.Handler, error)

// handlers is the registry of the job types this build can run. Each task
// that delivers a handler (internal/worker/handler) adds it here.
func handlers(d deps) (map[jobs.Type]handler.Handler, error) {
	served, err := packs.Registry() // the API's packs (T-023 review L2)
	if err != nil {
		return nil, err
	}
	sim, err := runner.New(runner.Config{Packs: served, Objects: d.objects, Actor: d.worker.ID, Log: d.log})
	if err != nil {
		return nil, err
	}
	profile, err := profiler.New(profiler.Config{Objects: d.objects, Actor: d.worker.ID, Log: d.log})
	if err != nil {
		return nil, err
	}
	validate, err := validator.New(validator.Config{Objects: d.objects, Actor: d.worker.ID, Log: d.log})
	if err != nil {
		return nil, err
	}
	client, budget, err := aiClient(d.ai)
	if err != nil {
		return nil, err
	}
	proposals, err := generator.New(generator.Config{Provider: client, Budget: budget, Packs: served, Objects: d.objects,
		Actor: d.worker.ID, Log: d.log})
	if err != nil {
		return nil, err
	}
	return map[jobs.Type]handler.Handler{jobs.Simulation: sim, jobs.ImportProfile: profile, jobs.ImportValidate: validate,
		jobs.ModelProposal: proposals}, nil
}

// aiClient is the checked provider client of the configured mode (T-024,
// ADR-027): every mode goes through provider.Client. Disabled, and a live
// provider without the owner's recorded approval, answer every call with
// their code and read no key; the fake is for development and tests
// (config refuses it elsewhere); the live adapter takes the approval token
// and the keys config read, by label (D-018).
func aiClient(ai config.AI) (*provider.Client, provider.Budget, error) {
	budget := provider.DefaultBudget()
	b := ai.Budget
	if b.MaxPromptBytes > 0 {
		budget.MaxPromptBytes = b.MaxPromptBytes
	}
	if b.MaxOutputTokens > 0 {
		budget.MaxOutputTokens = b.MaxOutputTokens
	}
	if b.TimeoutSeconds > 0 {
		budget.Timeout = time.Duration(b.TimeoutSeconds) * time.Second
	}
	if b.AttemptTimeoutSeconds > 0 {
		budget.AttemptTimeout = time.Duration(b.AttemptTimeoutSeconds) * time.Second
	}
	if b.MaxRetries != nil {
		budget.MaxRetries = *b.MaxRetries
	}
	if b.MaxConcurrentPerOrganization > 0 {
		budget.MaxConcurrentPerOrganization = b.MaxConcurrentPerOrganization
	}
	var backend provider.Backend
	switch {
	case ai.Provider == config.AIProviderFake:
		backend = fake.New()
	case ai.LiveEnabled():
		creds := make([]anthropic.Credential, len(ai.Credentials))
		for i, c := range ai.Credentials {
			creds[i] = anthropic.Credential{Label: c.Label, Key: c.Key}
		}
		live, err := anthropic.New(anthropic.Config{Approval: ai.Approval, Model: ai.Model, Credentials: creds})
		if err != nil {
			return nil, provider.Budget{}, err
		}
		backend = live
	case ai.Provider == config.AIProviderAnthropic:
		backend = provider.Disabled{Code: provider.CodePolicyNotApproved}
	default:
		backend = provider.Disabled{Code: provider.CodeNotConfigured}
	}
	client, err := provider.New(backend, budget)
	if err != nil {
		return nil, provider.Budget{}, err
	}
	return client, budget, nil
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
	// JSON lines through the privacy filter; a job's lines carry its
	// correlation (T-035, D-093, D-094).
	log := observability.NewLogger(stderr, "worker")
	cfg, err := config.Load(getenv)
	wcfg, werr := config.LoadWorker(getenv)
	// Load checked the AI configuration with the rest; this is its whole
	// value (mode, approval, budget, credentials by label).
	ai, aerr := config.LoadAI(getenv)
	if err == nil {
		err = aerr
	}
	tel, terr := config.LoadTelemetry(getenv)
	if err = errors.Join(err, werr, terr); err != nil {
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
	handlers, err := build(deps{worker: wcfg, ai: ai, objects: store, log: log})
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
	// Traces and metrics leave the process only to an OTLP endpoint the
	// operator named (D-095 (b), ADR-028); otherwise this installs nothing.
	stopTelemetry, err := export.Setup(ctx, tel, "geminus-worker", log)
	if err != nil {
		log.Error("invalid configuration", "error", "the telemetry exporter cannot be created: "+err.Error())
		return exitConfig
	}
	defer func() {
		fctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := stopTelemetry(fctx); err != nil {
			log.Warn("flushing the telemetry failed", "error", err.Error())
		}
	}()
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
