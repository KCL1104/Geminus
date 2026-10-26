package main

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/api"
	"github.com/kcl1104/geminus/internal/config"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/import/profiler"
	"github.com/kcl1104/geminus/internal/import/validator"
	"github.com/kcl1104/geminus/internal/observability"
	"github.com/kcl1104/geminus/internal/proposals/generator"
	"github.com/kcl1104/geminus/internal/runs/runner"
	"github.com/kcl1104/geminus/internal/storage/dbtest"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/storage/postgres"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/worker/handler"
)

var validEnv = map[string]string{
	"DATABASE_URL":            "postgresql://twin_worker:db-pass-123@/twin?host=/nonexistent-geminus-socket",
	"WORKER_ID":               "worker-0",
	"WORKER_ORGANIZATION_IDS": ids.New("org"),
}

func with(kv ...string) func(string) string {
	m := map[string]string{}
	for k, v := range validEnv {
		m[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return func(k string) string { return m[k] }
}

// someHandler stands for the handlers later tasks register.
var someHandler = fixed(map[jobs.Type]handler.Handler{jobs.ImportProfile: handler.Func(func(context.Context, handler.Run) error { return nil })})

// fixed is a build whose handlers are hs.
func fixed(hs map[jobs.Type]handler.Handler) builder {
	return func(deps) (map[jobs.Type]handler.Handler, error) { return hs, nil }
}

// T-035 (D-093 (a)): every line run writes is a JSON line with the
// component, and the filter keeps a registered field (ai_provider). Before
// T-035 this held too; TestTheWorkersLoggerIsFiltered is the filter's test.
func TestTheWorkersLinesAreJSON(t *testing.T) {
	var stderr bytes.Buffer
	if code := run(context.Background(), with("WORKER_ID", "worker-0"), &stderr, fixed(nil)); code != exitNotImplemented {
		t.Fatalf("exit = %d: %s", code, stderr.String())
	}
	for _, l := range strings.Split(strings.TrimSpace(stderr.String()), "\n") {
		var line map[string]any
		if err := json.Unmarshal([]byte(l), &line); err != nil || line["component"] != "worker" {
			t.Errorf("%v: %s", err, l)
		}
		if line["ai_provider"] == observability.Redacted {
			t.Errorf("a registered field was redacted: %s", l)
		}
	}
}

// T-035 (D-094): the worker's logger filters what a line carries: an
// unregistered key, a credential, a URL's password and a quoted value.
func TestTheWorkersLoggerIsFiltered(t *testing.T) {
	var b bytes.Buffer
	newLogger(&b).Error("recording the failure failed", "error", `connect postgres://twin_worker:pw-SENTINEL@db/twin: cell "CASE-SENTINEL"`,
		"payload", `{"prompt":"SENTINEL"}`, "worker_id", "sk-ant-api03-SENTINELsentinel0123456789")
	var line map[string]any
	if err := json.Unmarshal(b.Bytes(), &line); err != nil || line["component"] != "worker" || line["payload"] != observability.Redacted ||
		line["worker_id"] != observability.Redacted {
		t.Errorf("%v %s", err, b.String())
	}
	if strings.Contains(b.String(), "SENTINEL") {
		t.Errorf("the log holds what it must not: %s", b.String())
	}
}

// T-035 (D-095 (b)): a telemetry setting that is not one stops the worker
// with the variable's name, never its value.
func TestRunRefusesABadTelemetrySetting(t *testing.T) {
	for _, c := range [][2]string{
		{"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "https://c/v1/traces?token=otel-SENTINEL"},
		{"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "http://c-SENTINEL/v1/traces"}, // review L6
		{"OTEL_EXPORTER_OTLP_TRACES_HEADERS", "authorization:otel-SENTINEL"},  // review M1
	} {
		var stderr bytes.Buffer
		if code := run(context.Background(), with(c[0], c[1]), &stderr, someHandler); code != exitConfig {
			t.Fatalf("%s: exit = %d: %s", c[0], code, stderr.String())
		}
		if !strings.Contains(stderr.String(), c[0]) || strings.Contains(stderr.String(), "SENTINEL") {
			t.Errorf("%s", stderr.String())
		}
	}
}

func TestRunRejectsInvalidConfiguration(t *testing.T) {
	var stderr bytes.Buffer
	if code := run(context.Background(), func(string) string { return "" }, &stderr, someHandler); code != exitConfig {
		t.Fatalf("exit = %d, want %d", code, exitConfig)
	}
	for _, v := range []string{"DATABASE_URL", "WORKER_ID", "WORKER_ORGANIZATION_IDS"} {
		if !strings.Contains(stderr.String(), v) {
			t.Errorf("stderr does not name %s: %s", v, stderr.String())
		}
	}
}

// A build without any job handler refuses to start before it connects
// anywhere.
func TestRunRefusesToStartWithoutHandlers(t *testing.T) {
	var stderr bytes.Buffer
	if code := run(context.Background(), with(), &stderr, fixed(nil)); code != exitNotImplemented {
		t.Fatalf("exit = %d, want %d", code, exitNotImplemented)
	}
	out := stderr.String()
	if !strings.Contains(out, "NOT IMPLEMENTED") || !strings.Contains(out, "no job handler") {
		t.Fatalf("stderr does not explain what is missing: %s", out)
	}
	if strings.Contains(out, "db-pass-123") || strings.Contains(out, "database") {
		t.Fatalf("stderr leaks the password or shows a connection attempt: %s", out)
	}
}

// This build runs simulation jobs (T-023), import profiling (T-016),
// import validation (T-017) and AI proposals (T-025); nothing else yet
// (analyses are T-030's).
func TestTheBuildRunsSimulations(t *testing.T) {
	hs, err := handlers(deps{worker: config.Worker{ID: "worker-0"}, log: slog.New(slog.DiscardHandler)})
	if err != nil || len(hs) != 4 || hs[jobs.Simulation] == nil {
		t.Fatalf("%v %v", hs, err)
	}
	if _, ok := hs[jobs.ImportProfile].(*profiler.Handler); !ok {
		t.Errorf("the import_profile handler is a %T", hs[jobs.ImportProfile])
	}
	if _, ok := hs[jobs.ImportValidate].(*validator.Handler); !ok {
		t.Errorf("the import_validate handler is a %T", hs[jobs.ImportValidate])
	}
	if _, ok := hs[jobs.ModelProposal].(*generator.Handler); !ok {
		t.Errorf("the model_proposal handler is a %T", hs[jobs.ModelProposal])
	}
}

// T-025: the provider's mode is the configuration's, and without the
// owner's recorded approval no configuration reaches a live provider:
// disabled is the default, anthropic without an approval answers
// policy_not_approved (and config refuses an approval this build does not
// record), the fake answers only where config allows it.
func TestTheProviderIsTheConfigured(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name string
		env  []string
		want provider.Code
	}{
		{"default", nil, provider.CodeNotConfigured},
		{"disabled", []string{"AI_PROVIDER", "disabled"}, provider.CodeNotConfigured},
		{"live without approval", []string{"AI_PROVIDER", "anthropic"}, provider.CodePolicyNotApproved},
		// The fake is reached: it refuses this unsealed prompt as any
		// backend's client would.
		{"fake", []string{"AI_PROVIDER", "fake", "APP_ENV", "test"}, provider.CodeInvalidRequest},
	} {
		t.Run(c.name, func(t *testing.T) {
			ai, err := config.LoadAI(with(c.env...))
			if err != nil {
				t.Fatal(err)
			}
			client, budget, err := aiClient(ai, nil, "test-worker")
			if err != nil || budget.Validate() != nil {
				t.Fatalf("%v %v", err, budget.Validate())
			}
			_, err = client.GenerateModelProposal(ctx, provider.ProposalContext{OrganizationID: ids.New("org"), Mode: provider.ModeEdit})
			if got := provider.CodeOf(err); got != c.want {
				t.Fatalf("code %q, want %q", got, c.want)
			}
		})
	}
	if _, err := config.LoadAI(with("AI_PROVIDER", "anthropic", "AI_POLICY_APPROVAL", "owner-approved-2026")); err == nil {
		t.Fatal("an approval this build does not record was accepted")
	}
	// T-053: H-018 is recorded and complete (H-026). Everything live AI
	// needs but the switch still answers policy_not_approved. With the
	// switch, live AI is on, and the worker builds the live client only
	// over the spend ledger: without one it refuses (no client, so no
	// request). No live client is called here.
	h018 := []string{"AI_PROVIDER", "anthropic", "AI_POLICY_APPROVAL", "H-018", "AI_MODEL", "claude-opus-5-5",
		"GEMINUS_ANTHROPIC_API_KEY_A", "sk-ant-test-fake-aaaaaaaaaaaa", "GEMINUS_ANTHROPIC_API_KEY_B", "sk-ant-test-fake-bbbbbbbbbbbb"}
	ai, err := config.LoadAI(with(h018...))
	if err != nil || ai.LiveEnabled() || ai.LiveOff != config.LiveOffSwitch {
		t.Fatalf("H-018 without the switch: %+v %v", ai, err)
	}
	client, _, err := aiClient(ai, nil, "test-worker")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.GenerateModelProposal(ctx, provider.ProposalContext{OrganizationID: ids.New("org"), Mode: provider.ModeEdit}); provider.CodeOf(err) != provider.CodePolicyNotApproved {
		t.Errorf("H-018 without the switch: %v", err)
	}
	live, err := config.LoadAI(with(append(h018, "GEMINUS_AI_LIVE", "on")...))
	if err != nil || !live.LiveEnabled() {
		t.Fatalf("H-018 with the switch: %+v %v", live, err)
	}
	if c, _, err := aiClient(live, nil, "test-worker"); err == nil || c != nil {
		t.Error("the worker built a live client without the spend ledger")
	}
	// The budget a deployment sets reaches the client and the context.
	ai, err = config.LoadAI(with("AI_PROVIDER", "fake", "APP_ENV", "test", "AI_MAX_PROMPT_BYTES", "65536", "AI_MAX_RETRIES", "0"))
	if err != nil {
		t.Fatal(err)
	}
	if _, budget, err := aiClient(ai, nil, "test-worker"); err != nil || budget.MaxPromptBytes != 65536 || budget.MaxRetries != 0 {
		t.Fatalf("%+v %v", budget, err)
	}
}

// T-056: the proposal handler is built with T-055's photo path (deps.photo:
// the judge and the segmenter of AI_VISION_PROVIDER), whatever its mode,
// and without one (a photo proposal then fails AI_PROVIDER_NOT_CONFIGURED;
// internal/proposals/generator TestPhotoFailures).
func TestTheProposalHandlerTakesThePhotoPath(t *testing.T) {
	for name, env := range map[string][]string{
		"fake":     {"AI_PROVIDER", "fake", "APP_ENV", "test"},
		"disabled": {"AI_PROVIDER", "disabled"},
	} {
		ai, err := config.LoadAI(with(env...))
		if err != nil {
			t.Fatal(err)
		}
		p, err := photoPath(ai, provider.DefaultBudget(), nil, "test-worker")
		if err != nil || p.Judge == nil || p.Segmenter == nil {
			t.Fatalf("%s: %+v %v", name, p, err)
		}
		for _, pic := range []photo{p, {}} {
			hs, err := handlers(deps{worker: config.Worker{ID: "worker-0"}, ai: ai, log: slog.New(slog.DiscardHandler), photo: pic})
			if err != nil || hs[jobs.ModelProposal] == nil {
				t.Errorf("%s: %v", name, err)
			}
		}
	}
}

// T-023 review L2: the worker runs the domains the API accepts runs of,
// no more and no fewer: both build their packs with internal/packs, and
// the simulation handler this build registers serves exactly the API's
// domains. Otherwise the API would accept runs the worker fails for good
// (RUN_INPUT_INVALID).
func TestTheWorkerServesTheAPIsPacks(t *testing.T) {
	hs, err := handlers(deps{worker: config.Worker{ID: "worker-0"}, log: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	sim, ok := hs[jobs.Simulation].(*runner.Handler)
	if !ok {
		t.Fatalf("the simulation handler is a %T", hs[jobs.Simulation])
	}
	apiDomains, err := api.Domains()
	if err != nil {
		t.Fatal(err)
	}
	if got := sim.Domains(); len(got) == 0 || !slices.Equal(got, apiDomains) {
		t.Errorf("the worker serves %v, the API %v", got, apiDomains)
	}
}

// The simulation handler names the worker in audit events, so a WORKER_ID
// that cannot be an audit actor is a configuration error, as is an object
// storage root the adapter cannot use; both before any connection.
func TestRunRefusesWhatTheHandlersCannotUse(t *testing.T) {
	for name, getenv := range map[string]func(string) string{
		"a worker ID that is no actor": with("WORKER_ID", "0worker/a"),
		"a missing storage root":       with("OBJECT_STORAGE_PROVIDER", "filesystem", "APP_ENV", "test", "OBJECT_STORAGE_ROOT", "/nonexistent-geminus-objects"),
	} {
		var stderr bytes.Buffer
		if code := run(context.Background(), getenv, &stderr, handlers); code != exitConfig || strings.Contains(stderr.String(), "database") {
			t.Errorf("%s: exit %d: %s", name, code, stderr.String())
		}
	}
	var stderr bytes.Buffer
	if code := run(context.Background(), with("OBJECT_STORAGE_PROVIDER", "filesystem", "APP_ENV", "production", "OBJECT_STORAGE_ROOT", t.TempDir()), &stderr, handlers); code != exitConfig ||
		!strings.Contains(stderr.String(), "OBJECT_STORAGE_PROVIDER") {
		t.Errorf("the filesystem adapter in production: exit %d: %s", code, stderr.String())
	}
}

// T-039: the worker opens the same object storage as the API: the
// S3-compatible bucket of a deployment (production included), none when
// disabled; a half-configured bucket stops it before any connection.
func TestTheObjectStoreIsTheConfigured(t *testing.T) {
	bucket := []string{"APP_ENV", "production", "OBJECT_STORAGE_PROVIDER", "s3", "OBJECT_STORAGE_S3_ENDPOINT", "https://storage.example",
		"OBJECT_STORAGE_S3_BUCKET", "geminus-demo-1a2b", "OBJECT_STORAGE_S3_REGION", "auto", "OBJECT_STORAGE_S3_ACCESS_KEY_ID", "AKID",
		"OBJECT_STORAGE_S3_SECRET_ACCESS_KEY", "SECRET-SENTINEL"}
	cfg, err := config.Load(with(bucket...))
	if err != nil {
		t.Fatal(err)
	}
	if s, err := objectStore(cfg); s == nil || err != nil {
		t.Errorf("s3: %v %v", s, err)
	}
	cfg, err = config.Load(with())
	if err != nil {
		t.Fatal(err)
	}
	if s, err := objectStore(cfg); s != nil || err != nil {
		t.Errorf("disabled: %v %v", s, err)
	}
	var stderr bytes.Buffer
	half := append(slices.Clone(bucket), "OBJECT_STORAGE_S3_BUCKET", "")
	if code := run(context.Background(), with(half...), &stderr, handlers); code != exitConfig ||
		!strings.Contains(stderr.String(), "OBJECT_STORAGE_S3_BUCKET") || strings.Contains(stderr.String(), "SECRET-SENTINEL") ||
		strings.Contains(stderr.String(), "database") {
		t.Errorf("a bucket without its name: exit %d: %s", code, stderr.String())
	}
}

// The allowlist is checked with the rest of the configuration, before the
// handlers and before any connection (T-019 review L4).
func TestRunRefusesABadAllowlist(t *testing.T) {
	for _, orgs := range []string{"org_bad", ids.New("usr"), validEnv["WORKER_ORGANIZATION_IDS"] + "," + validEnv["WORKER_ORGANIZATION_IDS"], ids.New("org") + ",", "not-an-id,,,"} {
		for name, hs := range map[string]builder{"with a handler": someHandler, "the build's": handlers} {
			var stderr bytes.Buffer
			if code := run(context.Background(), with("WORKER_ORGANIZATION_IDS", orgs), &stderr, hs); code != exitConfig || !strings.Contains(stderr.String(), "WORKER_ORGANIZATION_IDS") {
				t.Errorf("%q %s: exit %d: %s", orgs, name, code, stderr.String())
			}
		}
	}
}

// A connection string pgx refuses is a configuration error too, before the
// handler check and before any connection (T-019 round 2 I5).
func TestRunRefusesABadConnectionString(t *testing.T) {
	bad := "postgresql://twin_worker:db-pass-123@/twin?host=/nonexistent-geminus-socket&connect_timeout=soon"
	for name, hs := range map[string]builder{"with a handler": someHandler, "the build's": handlers} {
		var stderr bytes.Buffer
		if code := run(context.Background(), with("DATABASE_URL", bad), &stderr, hs); code != exitConfig || !strings.Contains(stderr.String(), "DATABASE_URL") || strings.Contains(stderr.String(), "db-pass-123") {
			t.Errorf("%s: exit %d: %s", name, code, stderr.String())
		}
	}
}

// Every connection the pool hands back is checked for leftover tenant
// scope (tenant.Guard).
func TestPoolGuardsReleasedConnections(t *testing.T) {
	cfg, err := poolConfig(validEnv["DATABASE_URL"])
	if err != nil || cfg.AfterRelease == nil {
		t.Fatalf("the pool has no release guard (%v)", err)
	}
	if _, err := poolConfig("not a url"); err == nil {
		t.Error("an invalid DATABASE_URL was accepted")
	}
}

func TestRunNeedsTheDatabase(t *testing.T) {
	var stderr bytes.Buffer
	// Bounded, so a worker that wrongly started fails the test instead of
	// hanging it (T-019 review I1).
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if code := run(ctx, with(), &stderr, someHandler); code != exitDatabase {
		t.Fatalf("exit = %d, want %d: %s", code, exitDatabase, stderr.String())
	}
	if out := stderr.String(); !strings.Contains(out, "database unavailable") || strings.Contains(out, "db-pass-123") {
		t.Fatalf("stderr: %s", out)
	}
}

// A signal before the loop began (the context already ended) is a stop,
// not a database failure.
func TestRunStopsBeforeStarting(t *testing.T) {
	var stderr bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if code := run(ctx, with(), &stderr, someHandler); code != 0 {
		t.Fatalf("exit = %d, want 0: %s", code, stderr.String())
	}
	if out := stderr.String(); !strings.Contains(out, "stopped before starting") || strings.Contains(out, "database unavailable") {
		t.Fatalf("stderr: %s", out)
	}
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuffer) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// Against the disposable database: the worker refuses a superuser login,
// runs a job of an assigned organization as the worker login, and stops
// when signalled.
func TestRunWorksAndStops(t *testing.T) {
	admin := dbtest.Admin(t) // skips unless the disposable database is configured; creates the worker login
	o := dbtest.NewOrg(t, admin, "https://test.invalid", tenant.Owner)
	var stderr syncBuffer
	// Bounded, so a worker that wrongly accepted the login stops and the
	// test fails instead of hanging.
	refuse, cancelRefuse := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelRefuse()
	if code := run(refuse, with("DATABASE_URL", dbtest.AdminURL(t), "WORKER_ORGANIZATION_IDS", o.ID), &stderr, someHandler); code != exitDatabase ||
		!strings.Contains(stderr.String(), "refusing the database login") {
		t.Fatalf("a superuser login: exit %d: %s", code, stderr.String())
	}
	imp := dbtest.NewImport(t, admin, o)
	var job jobs.Job
	if err := postgres.OpenPool(admin).Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		job, err = jobs.Enqueue(ctx, tx, jobs.New{ID: ids.New("job"), OrganizationID: o.ID, Type: jobs.ImportProfile,
			Resource: jobs.Resource{ImportID: imp}, Payload: map[string]any{"import_id": imp}}, jobs.DefaultPolicy)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	ran := make(chan struct{}, 1)
	handlers := map[jobs.Type]handler.Handler{jobs.ImportProfile: handler.Func(func(ctx context.Context, run handler.Run) error {
		finish := run.Finish
		if err := finish(ctx, jobs.Completed, func(context.Context, tenant.Tx) error { return nil }); err != nil {
			return err
		}
		ran <- struct{}{}
		return nil
	})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, with("DATABASE_URL", dbtest.LoginURL(t, dbtest.WorkerLogin), "WORKER_ORGANIZATION_IDS", o.ID), &stderr, fixed(handlers))
	}()
	select {
	case <-ran:
	case code := <-done:
		t.Fatalf("exited %d: %s", code, stderr.String())
	case <-time.After(20 * time.Second):
		t.Fatalf("the job did not run: %s", stderr.String())
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit = %d: %s", code, stderr.String())
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the worker did not stop")
	}
	var status jobs.Status
	if err := admin.QueryRow(context.Background(), `SELECT status FROM twin.jobs WHERE id=$1`, job.ID).Scan(&status); err != nil || status != jobs.Completed {
		t.Errorf("the job is %s (%v)", status, err)
	}
	if out := stderr.String(); !strings.Contains(out, `"msg":"running"`) || !strings.Contains(out, `"msg":"stopped"`) {
		t.Errorf("stderr: %s", out)
	}
}
