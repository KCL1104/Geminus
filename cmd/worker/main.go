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
// tests) or a live adapter, which config enables only with the owner's
// complete recorded approval, the provider's keys and GEMINUS_AI_LIVE=on:
// Claude's (T-053; H-018 is recorded, complete since the owner's retention
// confirmation, H-026) or the Meta Model API's (T-055; H-029, amended by
// H-033, with GEMINUS_MUSE_API_KEY). A live adapter is metered: every call
// is reserved for in the spend ledger before it is made and settled after
// (internal/storage/aispend). The photo path's clients (T-055: the judge
// and the SAM 3.1 segmenter, of AI_VISION_PROVIDER) are built and checked
// at start, for T-056's handler. Analyses (T-030) are not built yet; their
// jobs stay queued.
package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/anthropic"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/ai/provider/meta"
	"github.com/kcl1104/geminus/internal/ai/spend"
	"github.com/kcl1104/geminus/internal/config"
	"github.com/kcl1104/geminus/internal/config/aiapproval"
	"github.com/kcl1104/geminus/internal/import/profiler"
	"github.com/kcl1104/geminus/internal/import/validator"
	"github.com/kcl1104/geminus/internal/observability"
	"github.com/kcl1104/geminus/internal/observability/telemetry/export"
	"github.com/kcl1104/geminus/internal/packs"
	"github.com/kcl1104/geminus/internal/proposals/generator"
	"github.com/kcl1104/geminus/internal/runs/runner"
	"github.com/kcl1104/geminus/internal/storage/aispend"
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

// deps is what the handlers are built from: the validated configuration
// and the worker's scope, which the live AI meter runs its transactions in.
type deps struct {
	worker  config.Worker
	ai      config.AI
	objects objects.Store // nil when object storage is disabled
	log     *slog.Logger
	spend   aispend.Runner
	// photo is the photo path's clients (T-055), built and checked at
	// start; T-056's handler takes them.
	photo photo
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
	// The packs' data-quality rules of imported events (D-059, T-052).
	validate, err := validator.New(validator.Config{Objects: d.objects, Actor: d.worker.ID, Log: d.log, Packs: served})
	if err != nil {
		return nil, err
	}
	client, budget, err := aiClient(d.ai, d.spend, d.worker.ID)
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
// provider that is not on, answer every call with their code and read no
// key; the fake is for development and tests (config refuses it
// elsewhere); the live adapter (Claude's, T-053, or Meta's, T-055) takes
// the approval token and the keys config read, by label (D-018), and is
// metered by the spend ledger under the configured limits, its
// transactions run by spendRun as the worker (T-053).
func aiClient(ai config.AI, spendRun aispend.Runner, actor string) (*provider.Client, provider.Budget, error) {
	budget := aiBudget(ai.Budget)
	var backend provider.Backend
	switch {
	case ai.Provider == config.AIProviderFake:
		backend = fake.New()
	case ai.LiveEnabled():
		live, err := liveBackend(ai.Provider, ai.Approval, ai.Model, ai.Credentials)
		if err != nil {
			return nil, provider.Budget{}, err
		}
		meter, err := ledger(spendRun, actor, ai.Limits)
		if err != nil {
			return nil, provider.Budget{}, err
		}
		client, err := provider.NewMetered(live, budget, meter)
		if err != nil {
			return nil, provider.Budget{}, err
		}
		return client, budget, nil
	case ai.Provider == config.AIProviderAnthropic || ai.Provider == config.AIProviderMeta:
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

// aiBudget is the provider's default budget with what the deployment set.
func aiBudget(b config.AIBudget) provider.Budget {
	budget := provider.DefaultBudget()
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
	return budget
}

// liveBackend is provider p's live adapter over the approval, model and
// keys config read. Each adapter takes only its own provider's approval
// (anthropic.New and meta.New check it too).
func liveBackend(p config.AIProvider, a aiapproval.Approval, model string, creds []config.AICredential) (provider.Billable, error) {
	switch p {
	case config.AIProviderAnthropic:
		cs := make([]anthropic.Credential, len(creds))
		for i, c := range creds {
			cs[i] = anthropic.Credential{Label: c.Label, Key: c.Key}
		}
		b, err := anthropic.New(anthropic.Config{Approval: a, Model: model, Credentials: cs})
		if err != nil {
			return nil, err // never a typed nil in the interface
		}
		return b, nil
	case config.AIProviderMeta:
		if len(creds) != 1 {
			return nil, errors.New("live AI on the Meta Model API takes one key")
		}
		b, err := meta.New(meta.Config{Approval: a, Model: model, Credential: meta.Credential{Label: creds[0].Label, Key: creds[0].Key}})
		if err != nil {
			return nil, err
		}
		return b, nil
	}
	return nil, errors.New("no live adapter for this provider")
}

// ledger is the spend ledger's meter of the live clients (T-053): one
// ledger, whichever path and provider a call is of.
func ledger(spendRun aispend.Runner, actor string, limits spend.Limits) (*aispend.Meter, error) {
	if spendRun == nil {
		return nil, errors.New("live AI needs the spend ledger")
	}
	return aispend.New(aispend.Config{Run: spendRun, Actor: actor, Limits: limits})
}

// photo is the photo path (T-055, for T-056's handler; H-033): the judge,
// a checked client whose backend reads a prompt's images, and the
// segmenter (SAM 3.1), both of the provider config.AIVision names. Off,
// they answer every call with the code a proposal would.
type photo struct {
	Judge     *provider.Client
	Segmenter *provider.SegmentClient
}

// photoBackends are the photo path's backends: the fakes (development and
// tests), the live Meta adapters once the photo path is on (the judge
// Muse Spark, the segmenter the approval's segmentation model), Disabled
// otherwise. This build's Claude adapter reads no image, and no approval
// of it names one (H-018), so config never turns the photo path on for
// it; were it on, the worker would refuse to start.
func photoBackends(v config.AIVision) (provider.Backend, provider.SegmentBackend, error) {
	switch {
	case v.Provider == config.AIProviderFake:
		return fake.New(), fake.NewSegmenter(), nil
	case v.LiveEnabled() && v.Provider == config.AIProviderMeta:
		judge, err := liveBackend(v.Provider, v.Approval, v.Model, v.Credentials)
		if err != nil {
			return nil, nil, err
		}
		var seg provider.SegmentBackend = provider.Disabled{Code: provider.CodeNotConfigured}
		if v.SegmentationModel != "" {
			c := v.Credentials[0]
			s, err := meta.NewSegmenter(meta.Config{Approval: v.Approval, Model: v.SegmentationModel,
				Credential: meta.Credential{Label: c.Label, Key: c.Key}})
			if err != nil {
				return nil, nil, err
			}
			seg = s
		}
		return judge, seg, nil
	case v.LiveEnabled():
		return nil, nil, errors.New("the photo path's provider has no adapter that reads images in this build")
	case v.Provider == config.AIProviderAnthropic || v.Provider == config.AIProviderMeta:
		off := provider.Disabled{Code: provider.CodePolicyNotApproved}
		return off, off, nil
	}
	off := provider.Disabled{Code: provider.CodeNotConfigured}
	return off, off, nil
}

// photoPath builds the photo path's clients over photoBackends: a
// billable backend only through the ledger, under the proposals' budget.
func photoPath(ai config.AI, budget provider.Budget, spendRun aispend.Runner, actor string) (photo, error) {
	judge, seg, err := photoBackends(ai.Vision)
	if err != nil {
		return photo{}, err
	}
	var p photo
	var meter *aispend.Meter
	if b, ok := judge.(provider.Billable); ok {
		if meter, err = ledger(spendRun, actor, ai.Limits); err != nil {
			return photo{}, err
		}
		p.Judge, err = provider.NewMetered(b, budget, meter)
	} else {
		p.Judge, err = provider.New(judge, budget)
	}
	if err != nil {
		return photo{}, err
	}
	if s, ok := seg.(provider.BillableSegmenter); ok {
		if meter == nil {
			if meter, err = ledger(spendRun, actor, ai.Limits); err != nil {
				return photo{}, err
			}
		}
		p.Segmenter, err = provider.NewMeteredSegmentClient(s, budget, meter)
	} else {
		p.Segmenter, err = provider.NewSegmentClient(seg, budget)
	}
	if err != nil {
		return photo{}, err
	}
	return p, nil
}

// objectStore opens the configured object storage (D-044): nil when it is
// disabled; the filesystem adapter only for development and tests; the
// S3-compatible bucket of a deployment (T-039).
func objectStore(cfg config.Config) (objects.Store, error) {
	switch o := cfg.ObjectStorage; o.Provider {
	case config.ObjectStorageFilesystem:
		fs, err := objects.NewFS(o.Root, cfg.Env)
		if err != nil {
			return nil, err
		}
		return fs, nil
	case config.ObjectStorageS3:
		s3, err := objects.NewS3(objects.S3Config{Endpoint: o.S3.Endpoint, Bucket: o.S3.Bucket, Region: o.S3.Region,
			AccessKeyID: o.S3.AccessKeyID, SecretAccessKey: o.S3.SecretAccessKey, PathStyle: o.S3.PathStyle})
		if err != nil {
			return nil, err
		}
		return s3, nil
	}
	return nil, nil
}

// run runs the loop with the handlers build makes until ctx ends.
func run(ctx context.Context, getenv func(string) string, stderr io.Writer, build builder) int {
	log := newLogger(stderr)
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
		log.Error("invalid configuration", "error", "the object storage cannot be used: "+err.Error())
		return exitConfig
	}
	// The pool connects lazily: nothing is dialled before the handlers are
	// built and the runtime role is checked.
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
	spendRun := func(ctx context.Context, org string, fn func(ctx context.Context, tx tenant.Tx) error) error {
		return scope.Run(ctx, org, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error { return fn(ctx, tx) })
	}
	pic, err := photoPath(ai, aiBudget(ai.Budget), spendRun, wcfg.ID)
	if err != nil {
		log.Error("invalid configuration", "error", err.Error())
		return exitConfig
	}
	handlers, err := build(deps{worker: wcfg, ai: ai, objects: store, log: log, spend: spendRun, photo: pic})
	if err != nil {
		log.Error("invalid configuration", "error", err.Error())
		return exitConfig
	}
	if len(handlers) == 0 {
		log.Error("NOT IMPLEMENTED: this build has no job handler; refusing to start", "ai_provider", string(cfg.AIProvider))
		return exitNotImplemented
	}
	// Whether live AI is on, and the typed reason when it is off (T-053),
	// for the proposals and the photo path (T-055); never a key.
	if live := ai.Provider == config.AIProviderAnthropic || ai.Provider == config.AIProviderMeta; live && ai.LiveEnabled() {
		log.Info("live AI is on", "provider", string(ai.Provider), "model", ai.Model, "code", "LIVE_AI_ON")
	} else if live {
		log.Info("live AI is off", "provider", string(ai.Provider), "code", strings.ToUpper(ai.LiveOff))
	}
	if v := ai.Vision; v.Provider == config.AIProviderAnthropic || v.Provider == config.AIProviderMeta {
		if v.LiveEnabled() {
			log.Info("the photo path is on", "provider", string(v.Provider), "model", v.Model, "code", "PHOTO_PATH_ON")
		} else {
			log.Info("the photo path is off", "provider", string(v.Provider), "code", strings.ToUpper(v.LiveOff))
		}
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

// newLogger is the worker's log (T-035, D-093, D-094): JSON lines on
// stderr, every attribute through the privacy filter, every line of a job
// with its correlation.
func newLogger(stderr io.Writer) *slog.Logger { return observability.NewLogger(stderr, "worker") }

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
