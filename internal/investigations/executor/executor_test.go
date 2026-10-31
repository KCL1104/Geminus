package executor_test

// DB-backed tests of the API's investigation executor (H-061 §6, §8, §13;
// slice A7; TEST-008, TEST-010, TEST-011, REQ-018, REQ-019, REQ-021): the
// investigation is started and stopped through internal/investigations as
// the requester; the ledger rows are written by this test as the worker's
// agent will write them (slice A6), under the investigation job's lease, as
// the plain worker login; the executor carries them out as the plain API
// login; proposals are generated and runs simulated by the job loop with
// the deterministic fake provider. Without GEMINUS_TEST_DATABASE_URL they
// skip (NOT RUN); make db-go-test sets GEMINUS_REQUIRE_DB_TESTS=1, so there
// a skip is a failure.

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kcl1104/geminus/internal/ai/investigator"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/analyses"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/domainpack/softwaredelivery"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/investigations"
	"github.com/kcl1104/geminus/internal/investigations/executor"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/models"
	"github.com/kcl1104/geminus/internal/proposals"
	"github.com/kcl1104/geminus/internal/proposals/approval"
	"github.com/kcl1104/geminus/internal/proposals/effect"
	"github.com/kcl1104/geminus/internal/proposals/generator"
	"github.com/kcl1104/geminus/internal/runs"
	"github.com/kcl1104/geminus/internal/runs/runner"
	"github.com/kcl1104/geminus/internal/simulation"
	analysisstore "github.com/kcl1104/geminus/internal/storage/analyses"
	"github.com/kcl1104/geminus/internal/storage/dbtest"
	store "github.com/kcl1104/geminus/internal/storage/investigations"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/storage/objects"
	"github.com/kcl1104/geminus/internal/storage/postgres"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/tenant/actingfor"
	"github.com/kcl1104/geminus/internal/tenant/workerscope"
	"github.com/kcl1104/geminus/internal/worker"
	"github.com/kcl1104/geminus/internal/worker/handler"
)

const issuer = "https://test.invalid"

var fast = jobs.Policy{Lease: 3 * time.Second, MaxAttempts: 3, BackoffBase: 20 * time.Millisecond, BackoffMax: 100 * time.Millisecond}

// agentLease is the investigation job's lease as this test holds it (the
// agent lane's renews it; here it simply lasts the test).
var agentLease = jobs.Policy{Lease: 10 * time.Minute, MaxAttempts: 5, BackoffBase: 20 * time.Millisecond, BackoffMax: 100 * time.Millisecond}

var runConfig = simulation.RunConfig{Horizon: simulation.Duration{Value: 1, Unit: "day"}, Warmup: simulation.Duration{Value: 0, Unit: "day"},
	Replications: 1, Seed: 7, TraceLevel: simulation.TraceNone}

const raiseCI = `[{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"replace","path":"/resources/1/capacity","value":6}]`

type env struct {
	admin    *pgxpool.Pool
	org      dbtest.Org
	other    dbtest.Org
	project  string
	db       *tenant.DB
	scope    *actingfor.Scope
	wscope   *workerscope.Worker
	svc      *investigations.Service
	ex       *executor.Executor
	cfg      executor.Config
	models   *models.Service
	props    *proposals.Service
	decide   *approval.Service
	runs     *runs.Service
	fake     *fake.Backend
	loop     *worker.Worker
	logs     *bytes.Buffer
	logsMu   *sync.Mutex
	reg      *domainpack.Registry
	fs       objects.Store
	agentJob map[string]jobs.Lease
}

type lockedWriter struct {
	w  io.Writer
	mu *sync.Mutex
}

func (l lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func setup(t *testing.T) *env {
	t.Helper()
	admin := dbtest.Admin(t)
	org := dbtest.NewOrg(t, admin, issuer, tenant.Owner, tenant.Editor, tenant.Viewer)
	other := dbtest.NewOrg(t, admin, issuer, tenant.Owner, tenant.Editor)
	api := dbtest.Pool(t, dbtest.LoginURL(t, dbtest.APILogin), 16, true)
	if err := tenant.CheckRuntimeRole(context.Background(), api, tenant.APIRole); err != nil {
		t.Fatal(err)
	}
	wpool := dbtest.Pool(t, dbtest.LoginURL(t, dbtest.WorkerLogin), 4, true)
	if err := tenant.CheckRuntimeRole(context.Background(), wpool, tenant.WorkerRole); err != nil {
		t.Fatal(err)
	}
	reg, err := domainpack.NewRegistry(softwaredelivery.Pack{})
	if err != nil {
		t.Fatal(err)
	}
	fs, err := objects.NewFS(t.TempDir(), "test")
	if err != nil {
		t.Fatal(err)
	}
	wscope, err := workerscope.New(wpool, []string{org.ID, other.ID})
	if err != nil {
		t.Fatal(err)
	}
	f := fake.New()
	client, err := provider.New(f, provider.DefaultBudget())
	if err != nil {
		t.Fatal(err)
	}
	gen, err := generator.New(generator.Config{Provider: client, Packs: reg, Objects: fs, Actor: "test-worker", Log: discard()})
	if err != nil {
		t.Fatal(err)
	}
	sim, err := runner.New(runner.Config{Packs: reg, Objects: fs, Actor: "test-worker", Log: discard()})
	if err != nil {
		t.Fatal(err)
	}
	loop, err := worker.New(wscope, worker.Config{ID: "test-worker", Policy: fast, Heartbeat: time.Second, Idle: 20 * time.Millisecond,
		ReapLimit: 10, Handlers: map[jobs.Type]handler.Handler{jobs.ModelProposal: gen, jobs.Simulation: sim}, Log: discard()})
	if err != nil {
		t.Fatal(err)
	}
	// The executor serves the first organization only; the second is
	// another deployment's (or none's).
	scope, err := actingfor.New(api, []string{org.ID})
	if err != nil {
		t.Fatal(err)
	}
	db := tenant.New(api)
	e := &env{admin: admin, org: org, other: other, db: db, scope: scope, wscope: wscope, fake: f, loop: loop, reg: reg, fs: fs,
		logs: &bytes.Buffer{}, logsMu: &sync.Mutex{}, agentJob: map[string]jobs.Lease{},
		models: &models.Service{DB: db, Packs: reg},
		props:  &proposals.Service{DB: db, AI: proposals.Available, Research: proposals.Available, Models: proposals.ProposalModels, Policy: fast},
		decide: &approval.Service{DB: db, Packs: reg, Objects: fs},
		runs:   &runs.Service{DB: db, Packs: reg, Objects: fs, Policy: fast}}
	e.svc = &investigations.Service{DB: db, On: true, Organizations: []string{org.ID, other.ID}, PlannerModels: []string{"muse-spark-1.3"},
		AI: proposals.Available, InvestigatorApproved: true, ApprovedPlanners: []string{"muse-spark-1.3"}, ProposalModels: proposals.ProposalModels,
		DefaultProposalModel: "muse-spark-1.3", Research: proposals.Available, Policy: fast}
	e.cfg = executor.Config{Scope: scope, DB: db, Proposals: e.props, Runs: e.runs, Analyses: &analyses.Service{DB: db, AI: proposals.Available, Policy: fast},
		Effects: &effect.Service{Packs: reg, Objects: fs}, Log: slog.New(slog.NewJSONHandler(lockedWriter{e.logs, e.logsMu}, nil)),
		Backoff: time.Millisecond, MaxBackoff: 5 * time.Millisecond}
	if e.ex, err = executor.New(e.cfg); err != nil {
		t.Fatal(err)
	}
	e.project = e.newProject(t, org)
	return e
}

func (e *env) newProject(t *testing.T, o dbtest.Org) string {
	t.Helper()
	id := ids.New("prj")
	e.owner(t, o.ID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO twin.projects(id,organization_id,name,created_by) VALUES ($1,$2,'P',$3)`,
			id, o.ID, o.Members[tenant.Owner].UserID)
		return err
	})
	return id
}

func (e *env) user(r tenant.Role) string { return e.org.Members[r].UserID }

func (e *env) scopeOf(r tenant.Role) tenant.Scope {
	return tenant.Scope{OrganizationID: e.org.ID, UserID: e.user(r)}
}

// owner runs fn as the schema owner in organization org's scope.
func (e *env) owner(t *testing.T, org string, fn func(ctx context.Context, tx pgx.Tx) error) {
	t.Helper()
	if err := postgres.OpenPool(e.admin).Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL ROLE twin_owner`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('app.organization_id',$1,true)`, org); err != nil {
			return err
		}
		return fn(ctx, tx)
	}); err != nil {
		t.Fatal(err)
	}
}

func (e *env) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	e.owner(t, e.org.ID, func(ctx context.Context, tx pgx.Tx) error { return tx.QueryRow(ctx, sql, args...).Scan(&n) })
	return n
}

const createBody = `{"name":"Pipeline","domain":"software_delivery","template":"default_delivery_pipeline"}`

func (e *env) model(t *testing.T) models.Model {
	t.Helper()
	var req models.CreateRequest
	if err := json.Unmarshal([]byte(createBody), &req); err != nil {
		t.Fatal(err)
	}
	status, out, err := e.models.Create(context.Background(), e.scopeOf(tenant.Editor), ids.New("req"), "key-"+ids.New("k"), e.project, []byte(createBody), req)
	if err != nil || status != 201 {
		t.Fatalf("create: %d %v", status, err)
	}
	var m models.Model
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func answer(t *testing.T, summary, changes string) []byte {
	t.Helper()
	var c any
	if err := json.Unmarshal([]byte(changes), &c); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"status": "proposed", "summary": summary, "assumptions": []string{"Service times stay."},
		"risk_level": "low", "confidence": 0.9, "changes": c, "expected_effects": []string{"Waiting may fall; a simulation will tell."},
		"requires_confirmation": true})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// start asks for an investigation of m as sc's user (createInvestigation's
// service), returning its ID.
func (e *env) start(t *testing.T, sc tenant.Scope, m models.Model, req investigations.CreateRequest) string {
	t.Helper()
	id, err := e.try(t, sc, m, req)
	if err != nil {
		t.Fatalf("createInvestigation: %v", err)
	}
	return id
}

// try asks for an investigation as start does, returning the service's
// refusal instead of failing the test.
func (e *env) try(t *testing.T, sc tenant.Scope, m models.Model, req investigations.CreateRequest) (string, error) {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	status, out, err := e.svc.Create(context.Background(), sc, ids.New("req"), "key-"+ids.New("k"), m.ID, body, req)
	if err != nil {
		return "", err
	}
	if status != 202 {
		t.Fatalf("createInvestigation: %d", status)
	}
	var a investigations.Accepted
	if err := json.Unmarshal(out, &a); err != nil {
		t.Fatal(err)
	}
	return a.InvestigationID, nil
}

func scenarioRequest(m models.Model) investigations.CreateRequest {
	cfg := runConfig
	return investigations.CreateRequest{BaseVersionID: m.LatestVersion.ID, Mode: "scenario", Intent: "Find a faster pipeline", OutputLocale: "en",
		RunConfig: &cfg, BaselineName: "Baseline for the AI"}
}

func (e *env) scenario(t *testing.T, m models.Model) string {
	t.Helper()
	return e.start(t, e.scopeOf(tenant.Editor), m, scenarioRequest(m))
}

// age moves investigation inv's clock minutes into the past (its creation
// and its deadline together): the schema owner, with its trigger set aside
// for that one statement.
func (e *env) age(t *testing.T, inv string, minutes int) {
	t.Helper()
	if err := postgres.OpenPool(e.admin).Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		for _, q := range []string{`ALTER TABLE twin.investigations DISABLE TRIGGER protect_investigation`,
			`UPDATE twin.investigations SET created_at=created_at - make_interval(mins => $1), deadline_at=deadline_at - make_interval(mins => $1)
			  WHERE id=$2`,
			`ALTER TABLE twin.investigations ENABLE TRIGGER protect_investigation`} {
			var err error
			if strings.Contains(q, "$1") {
				_, err = tx.Exec(ctx, q, minutes, inv)
			} else {
				_, err = tx.Exec(ctx, q)
			}
			if err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// statusOf reads investigation inv's status and reason as the schema owner.
func (e *env) statusOf(t *testing.T, inv string) string {
	t.Helper()
	var s string
	e.owner(t, e.org.ID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT status || ' ' || coalesce(reason,'-') FROM twin.investigations WHERE id=$1`, inv).Scan(&s)
	})
	return s
}

// lease claims investigation inv's job as the agent lane would (once).
func (e *env) lease(t *testing.T, org, inv string) jobs.Lease {
	t.Helper()
	if l, ok := e.agentJob[inv]; ok {
		return l
	}
	var l jobs.Lease
	var j jobs.Job
	var ok bool
	if err := e.wscope.Run(context.Background(), org, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		j, l, ok, err = jobs.Claim(ctx, tx, org, "test-worker:agent1", []jobs.Type{jobs.Investigation}, agentLease)
		return err
	}); err != nil || !ok || j.Resource.InvestigationID != inv {
		t.Fatalf("claim %s: %+v %v %v", inv, j, ok, err)
	}
	e.agentJob[inv] = l
	return l
}

// record writes call seq of inv as the worker's agent does: the strict
// decode's canonical arguments and hash, under the job's lease.
func (e *env) record(t *testing.T, org, inv string, mode investigator.Mode, seq int, args investigator.Arguments) {
	t.Helper()
	c, err := investigator.Encode(mode, args)
	if err != nil {
		t.Fatal(err)
	}
	e.recordRaw(t, org, inv, seq, string(c.Tool), c.Canonical, c.Hash)
}

func (e *env) recordRaw(t *testing.T, org, inv string, seq int, tool string, canonical []byte, hash string) {
	t.Helper()
	l := e.lease(t, org, inv)
	note := "Trying the slowest step first."
	if err := e.wscope.Run(context.Background(), org, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := store.InsertCall(ctx, tx, store.NewCall{OrganizationID: org, InvestigationID: inv, Seq: seq, Tool: tool, Arguments: canonical,
			ArgumentsHash: &hash, Note: &note, JobID: l.JobID, LeaseToken: l.Token, Status: store.Pending})
		return err
	}); err != nil {
		t.Fatalf("record call %d: %v", seq, err)
	}
}

// observe writes what the AI was shown for call seq (the worker does).
func (e *env) observe(t *testing.T, org, inv string, seq int) {
	t.Helper()
	if err := e.wscope.Run(context.Background(), org, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		return store.Observe(ctx, tx, org, inv, seq, []byte(`{"observed":true}`), "sha256:"+strings.Repeat("3", 64))
	}); err != nil {
		t.Fatalf("observe %d: %v", seq, err)
	}
}

// call reads call seq of inv as the schema owner.
func (e *env) call(t *testing.T, org, inv string, seq int) store.Call {
	t.Helper()
	var out store.Call
	e.owner(t, org, func(ctx context.Context, tx pgx.Tx) error {
		calls, err := store.Calls(ctx, tx, org, inv)
		if err != nil {
			return err
		}
		for _, c := range calls {
			if c.Seq == seq {
				out = c
			}
		}
		return nil
	})
	if out.Seq != seq {
		t.Fatalf("no call %d of %s", seq, inv)
	}
	return out
}

func (e *env) investigation(t *testing.T, sc tenant.Scope, id string) investigations.Investigation {
	t.Helper()
	v, err := e.svc.Get(context.Background(), sc, id)
	if err != nil {
		t.Fatalf("getInvestigation: %v", err)
	}
	matches(t, "investigation.schema.json#/$defs/Investigation", v)
	return v
}

func matches(t *testing.T, ref string, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if issues, err := validate.Document(data, ref, "RESPONSE"); err != nil || len(issues) > 0 {
		t.Errorf("%s does not match %s: %v %v", data, ref, issues, err)
	}
}

// done asserts call seq is done and returns it.
func (e *env) done(t *testing.T, org, inv string, seq int) store.Call {
	t.Helper()
	c := e.call(t, org, inv, seq)
	if c.Status != store.Done || c.Error != nil || c.ResolvedAt == nil {
		t.Fatalf("call %d: %+v", seq, c)
	}
	return c
}

// refusedAs asserts call seq was refused (or failed) with code at path.
func (e *env) refusedAs(t *testing.T, org, inv string, seq int, status store.CallStatus, code investigator.Code, path string) {
	t.Helper()
	c := e.call(t, org, inv, seq)
	if c.Status != status || c.Error == nil || c.Error.Code != string(code) || c.Error.Path != path ||
		c.Effect != (store.Effect{}) {
		t.Errorf("call %d: %+v %+v, want %s %s at %q", seq, c, c.Error, status, code, path)
	}
}

// generated runs the job loop until proposal id leaves generating.
func (e *env) generated(t *testing.T, id string) proposals.Proposal {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		e.loop.Pass(context.Background())
		p, err := e.props.Get(context.Background(), e.scopeOf(tenant.Viewer), ids.New("req"), id)
		if err != nil {
			t.Fatal(err)
		}
		if p.Status != "generating" {
			return p
		}
		if time.Now().After(deadline) {
			t.Fatalf("proposal %s is still generating", id)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// event is an audit event as stored.
type event struct {
	action, actorType, actorID string
	meta                       map[string]any
}

func (e *env) events(t *testing.T, target string) []event {
	t.Helper()
	var out []event
	e.owner(t, e.org.ID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT action, actor_type, actor_id, metadata::text FROM twin.audit_events WHERE target_id=$1 ORDER BY created_at, id`, target)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var ev event
			var meta string
			if err := rows.Scan(&ev.action, &ev.actorType, &ev.actorID, &meta); err != nil {
				return err
			}
			if err := json.Unmarshal([]byte(meta), &ev.meta); err != nil {
				return err
			}
			out = append(out, ev)
		}
		return rows.Err()
	})
	return out
}

func (e *env) toolCalls(t *testing.T, inv string) []event {
	t.Helper()
	var out []event
	for _, ev := range e.events(t, inv) {
		if ev.action == "investigation.tool_call" {
			out = append(out, ev)
		}
	}
	return out
}

func (e *env) setRole(t *testing.T, org, user string, role tenant.Role) {
	t.Helper()
	e.owner(t, org, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE twin.memberships SET role=$3 WHERE organization_id=$1 AND user_id=$2`, org, user, string(role))
		return err
	})
}

// The scenario loop through the executor (H-061 §6.4): propose_what_if,
// save_scenario, run_what_if and explain_results each carried out once,
// in the requester's name, labelled as the AI's, with the AI's mark on
// every event; the investigation shows its steps, what it made and the
// server's comparison of its what-if run against its baseline; nothing it
// did published a version.
func TestTheScenarioLoop(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	m := e.model(t)
	versions := e.count(t, `SELECT count(*) FROM twin.model_versions WHERE model_id=$1`, m.ID)
	inv := e.scenario(t, m)
	org := e.org.ID
	got := e.investigation(t, e.scopeOf(tenant.Viewer), inv)
	if got.Status != "running" || got.Activity.State != investigations.Queued || got.Used != (investigations.Used{}) ||
		got.Planner.Model != "muse-spark-1.3" || got.Planner.Provider != "meta" || got.ProposalModel != "muse-spark-1.3" ||
		got.Objective == nil || got.Objective.MetricPath != "/metrics/lead_time_minutes/p50" || got.Objective.Better != "lower" {
		t.Fatalf("started: %+v", got)
	}

	// Call 1: propose_what_if, carried out once.
	idea := "Add a CI runner so builds stop queuing " + ids.New("k")
	e.fake.Script(provider.Mode("scenario").Role(), idea, answer(t, "Raise the CI runner capacity.", raiseCI))
	e.record(t, org, inv, investigator.Scenario, 1, investigator.ProposeWhatIfArguments{Idea: idea})
	if got := e.investigation(t, e.scopeOf(tenant.Viewer), inv); got.Activity.State != investigations.CarryingOut {
		t.Errorf("a pending call: %s", got.Activity.State)
	}
	e.ex.Pass(ctx)
	c1 := e.done(t, org, inv, 1)
	if c1.Effect.ProposalID == nil {
		t.Fatalf("call 1 made no proposal: %+v", c1)
	}
	e.ex.Pass(ctx) // a second pass finds nothing pending
	if n := e.count(t, `SELECT count(*) FROM twin.ai_proposals WHERE investigation_id=$1`, inv); n != 1 {
		t.Fatalf("%d proposals after two passes", n)
	}
	p := e.generated(t, *c1.Effect.ProposalID)
	if p.Status != "ready" || p.Investigation == nil || p.Investigation.ID != inv || p.Investigation.Index != 1 || p.CreatedBy != e.user(tenant.Editor) {
		t.Fatalf("the AI's proposal: %+v", p)
	}
	if got := e.investigation(t, e.scopeOf(tenant.Viewer), inv); got.Activity.State != investigations.Waiting {
		t.Errorf("a call not observed yet: %s", got.Activity.State)
	}
	e.observe(t, org, inv, 1)

	// Call 2: save_scenario.
	e.record(t, org, inv, investigator.Scenario, 2, investigator.SaveScenarioArguments{ProposalID: p.ProposalID})
	e.ex.Pass(ctx)
	c2 := e.done(t, org, inv, 2)
	if c2.Effect.ScenarioID == nil || e.count(t, `SELECT count(*) FROM twin.scenarios WHERE id=$1 AND investigation_id=$2 AND created_by=$3`,
		*c2.Effect.ScenarioID, inv, e.user(tenant.Editor)) != 1 {
		t.Fatalf("call 2: %+v", c2)
	}
	e.observe(t, org, inv, 2)

	// Call 3: run_what_if: the baseline (slot 0) and the what-if (slot 1),
	// with the frozen settings and D-007's limits.
	e.record(t, org, inv, investigator.Scenario, 3, investigator.RunWhatIfArguments{ScenarioID: *c2.Effect.ScenarioID})
	e.ex.Pass(ctx)
	c3 := e.done(t, org, inv, 3)
	if c3.Effect.BaselineRunID == nil || c3.Effect.RunID == nil {
		t.Fatalf("call 3: %+v", c3)
	}
	if n := e.count(t, `SELECT count(*) FROM twin.simulation_runs WHERE investigation_id=$1 AND ((id=$2 AND investigation_slot=0 AND name='Baseline for the AI'
		AND scenario_id IS NULL) OR (id=$3 AND investigation_slot=1 AND scenario_id=$4)) AND execution_limits->>'wall_timeout_seconds'='300'
		AND created_by=$5`, inv, *c3.Effect.BaselineRunID, *c3.Effect.RunID, *c2.Effect.ScenarioID, e.user(tenant.Editor)); n != 2 {
		t.Errorf("%d of the two runs as asked", n)
	}
	deadline := time.Now().Add(time.Minute)
	for e.count(t, `SELECT count(*) FROM twin.simulation_runs WHERE investigation_id=$1 AND status='completed'`, inv) < 2 {
		if time.Now().After(deadline) {
			t.Fatal("the AI's runs did not complete")
		}
		e.loop.Pass(ctx)
		time.Sleep(10 * time.Millisecond)
	}
	e.observe(t, org, inv, 3)

	// Call 4: explain_results, against the baseline.
	q := "Why is it faster?"
	e.record(t, org, inv, investigator.Scenario, 4, investigator.ExplainResultsArguments{RunID: *c3.Effect.RunID, Question: &q})
	e.ex.Pass(ctx)
	c4 := e.done(t, org, inv, 4)
	if c4.Effect.AnalysisID == nil || e.count(t, `SELECT count(*) FROM twin.ai_analyses WHERE id=$1 AND investigation_id=$2 AND investigation_index=1
		AND run_id=$3 AND baseline_run_id=$4`, *c4.Effect.AnalysisID, inv, *c3.Effect.RunID, *c3.Effect.BaselineRunID) != 1 {
		t.Fatalf("call 4: %+v", c4)
	}
	e.observe(t, org, inv, 4)

	// Call 5: the same scenario again is ALREADY_RUN; call 6: a suggestion
	// of an analysis the investigation has nothing to do with.
	e.record(t, org, inv, investigator.Scenario, 5, investigator.RunWhatIfArguments{ScenarioID: *c2.Effect.ScenarioID})
	e.ex.Pass(ctx)
	e.refusedAs(t, org, inv, 5, store.Refused, investigator.AlreadyRun, "/scenario_id")
	e.observe(t, org, inv, 5)
	e.record(t, org, inv, investigator.Scenario, 6, investigator.ProposeWhatIfArguments{Idea: "Try the second idea",
		FromSuggestion: &investigator.SuggestionRef{AnalysisID: ids.New("ana"), Index: 0}})
	e.ex.Pass(ctx)
	e.refusedAs(t, org, inv, 6, store.Refused, investigator.NotInInvestigation, "/from_suggestion/analysis_id")

	// What the person sees: the steps, the counts, the candidate with the
	// server's comparison of its pair.
	v := e.investigation(t, e.scopeOf(tenant.Viewer), inv)
	if len(v.Steps) != 6 || v.Used != (investigations.Used{WhatIfs: 1, Runs: 2, Explanations: 1, Steps: 6}) || len(v.Candidates) != 1 ||
		v.BaselineRunID != nil || v.ClosingLine != nil {
		t.Fatalf("the investigation: %+v", v)
	}
	cand := v.Candidates[0]
	if cand.Index != 1 || cand.Source != "idea" || cand.Idea != idea || cand.ProposalStatus != "applied" || cand.ScenarioID == nil ||
		*cand.ScenarioID != *c2.Effect.ScenarioID || cand.RunID == nil || *cand.RunID != *c3.Effect.RunID || len(cand.AnalysisIDs) != 1 ||
		cand.Comparison == nil || !cand.Comparison.Comparable || cand.Comparison.Objective == nil ||
		cand.Comparison.Objective.MetricPath != "/metrics/lead_time_minutes/p50" {
		t.Errorf("the candidate: %+v %+v", cand, cand.Comparison)
	}
	if v.Steps[0].Note == nil || v.Steps[4].Error == nil || v.Steps[4].Error.Code != "ALREADY_RUN" || string(v.Steps[0].Arguments) == "null" {
		t.Errorf("steps %+v", v.Steps)
	}
	// Every carried-out call is a user event naming the requester, marked
	// as the AI's, with its outcome and code; never its arguments.
	calls := e.toolCalls(t, inv)
	if len(calls) != 6 {
		t.Fatalf("%d investigation.tool_call events", len(calls))
	}
	for i, ev := range calls {
		if ev.actorType != "user" || ev.actorID != e.user(tenant.Editor) || ev.meta["investigation_id"] != inv || ev.meta["by_ai"] != true ||
			ev.meta["seq"] != float64(i+1) || ev.meta["idea"] != nil {
			t.Errorf("event %d: %+v", i+1, ev)
		}
	}
	if calls[4].meta["outcome"] != "refused" || calls[4].meta["code"] != "ALREADY_RUN" || calls[0].meta["outcome"] != "done" || calls[0].meta["code"] != nil {
		t.Errorf("outcomes: %v %v", calls[0].meta, calls[4].meta)
	}
	if n := e.count(t, `SELECT count(*) FROM twin.audit_events WHERE metadata->>'investigation_id'=$1 AND (actor_type<>'user' OR actor_id<>$2)`,
		inv, e.user(tenant.Editor)); n != 0 {
		t.Errorf("%d marked events not by the requester", n)
	}
	// The log names IDs and codes, never the AI's idea or note.
	e.logsMu.Lock()
	logs := e.logs.String()
	e.logsMu.Unlock()
	if !strings.Contains(logs, `"msg":"investigation call carried out"`) || strings.Contains(logs, idea) || strings.Contains(logs, "slowest step") {
		t.Errorf("logs: %s", logs)
	}
	// Nothing the AI did published a version.
	if n := e.count(t, `SELECT count(*) FROM twin.model_versions WHERE model_id=$1`, m.ID); n != versions {
		t.Errorf("versions: %d, then %d", versions, n)
	}

	// The requester stops it: the closing line is the server's.
	status, out, err := e.svc.Stop(ctx, e.scopeOf(tenant.Editor), ids.New("req"), "key-"+ids.New("k"), inv, []byte(`{}`))
	if err != nil || status != 200 {
		t.Fatalf("stop: %d %v", status, err)
	}
	var stopped investigations.Investigation
	if err := json.Unmarshal(out, &stopped); err != nil {
		t.Fatal(err)
	}
	matches(t, "investigation.schema.json#/$defs/Investigation", stopped)
	if stopped.Status != "stopped" || stopped.Reason == nil || *stopped.Reason != "stopped_by_person" || stopped.Stop == nil ||
		stopped.Stop.By != e.user(tenant.Editor) || stopped.ClosingLine == nil || stopped.Activity.State != investigations.Ended {
		t.Errorf("stopped: %+v", stopped)
	}
	// Its explanation's job was still queued (this job loop explains
	// nothing): the stop leaves it queued and the analysis generating, as
	// stopInvestigation's contract says (the API may not change an
	// analysis, 0018); the worker's analyzer is to fail it with
	// JOB_CANCELLED before its paid call (slice A6).
	if n := e.count(t, `SELECT count(*) FROM twin.ai_analyses a JOIN twin.jobs j ON j.organization_id=a.organization_id AND j.ai_analysis_id=a.id
		WHERE a.id=$1 AND a.status='generating' AND j.status='queued'`, *c4.Effect.AnalysisID); n != 1 {
		t.Errorf("the queued explanation after the stop: %d", n)
	}
	if _, _, err := e.svc.Stop(ctx, e.scopeOf(tenant.Editor), ids.New("req"), "key-"+ids.New("k"), inv, []byte(`{}`)); !errors.Is(err, investigations.ErrEnded) {
		t.Errorf("a second stop: %v", err)
	}
}

// Edit mode (H-061 §6.4): the AI's one edit proposal, then write_to_draft
// into the clean draft; the closing line says it was written; no version.
func TestTheEditLoop(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	m := e.model(t)
	inv := e.start(t, e.scopeOf(tenant.Editor), m, investigations.CreateRequest{BaseVersionID: m.LatestVersion.ID, Mode: "edit",
		Intent: "Give CI more runners", OutputLocale: "en"})
	org := e.org.ID
	idea := "Raise the CI runner capacity to six " + ids.New("k")
	e.fake.Script(provider.Mode("edit").Role(), idea, answer(t, "Raise the CI runner capacity.", raiseCI))
	e.record(t, org, inv, investigator.Edit, 1, investigator.ProposeModelEditArguments{Idea: idea})
	e.ex.Pass(ctx)
	p := e.generated(t, *e.done(t, org, inv, 1).Effect.ProposalID)
	if p.Status != "ready" || p.Mode != "edit" {
		t.Fatalf("the AI's edit: %+v", p)
	}
	e.observe(t, org, inv, 1)
	e.record(t, org, inv, investigator.Edit, 2, investigator.WriteToDraftArguments{ProposalID: p.ProposalID})
	e.ex.Pass(ctx)
	c2 := e.done(t, org, inv, 2)
	if c2.Effect.DraftRevision == nil || *c2.Effect.DraftRevision != 2 {
		t.Fatalf("write_to_draft: %+v", c2)
	}
	d, err := e.models.GetDraft(ctx, e.scopeOf(tenant.Viewer), m.ID)
	if err != nil || d.Revision != 2 || d.AIEdit == nil || d.AIEdit.ProposalID != p.ProposalID {
		t.Fatalf("the draft: %+v %v", d, err)
	}
	if n := e.count(t, `SELECT count(*) FROM twin.model_versions WHERE model_id=$1`, m.ID); n != 1 {
		t.Errorf("%d versions: the AI never publishes", n)
	}
	// The worker ends it with draft_written (A6); here the person stops it.
	if _, _, err := e.svc.Stop(ctx, e.scopeOf(tenant.Editor), ids.New("req"), "key-"+ids.New("k"), inv, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	v := e.investigation(t, e.scopeOf(tenant.Viewer), inv)
	if v.ClosingLine == nil || v.ClosingLine.Key != investigations.DraftWritten || v.ClosingLine.DraftRevision == nil ||
		*v.ClosingLine.DraftRevision != 2 || len(v.Candidates) != 1 || v.Candidates[0].AppliedDraftRevision == nil {
		t.Errorf("the ended edit: %+v %+v", v.ClosingLine, v.Candidates)
	}
}

// Refusals (H-061 §7): a person's proposal, scenario or run is
// NOT_IN_INVESTIGATION; a base that moved is BASE_MOVED; the 4th what-if is
// LIMIT_REACHED (the database's cap); arguments that do not decode the
// second time are INVALID_ARGUMENTS; arguments that do not hash to the
// recorded hash are failed INTERNAL. Nothing is made for any of them.
func TestRefusals(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	m := e.model(t)
	org := e.org.ID
	// A person's own proposal and run.
	intent := "A person's idea " + ids.New("k")
	e.fake.Script(provider.Mode("scenario").Role(), intent, answer(t, "Raise the CI runner capacity.", raiseCI))
	body, _ := json.Marshal(proposals.CreateRequest{BaseVersionID: m.LatestVersion.ID, Mode: "scenario", Intent: intent, OutputLocale: "en"})
	var preq proposals.CreateRequest
	_ = json.Unmarshal(body, &preq)
	_, out, err := e.props.Create(ctx, e.scopeOf(tenant.Editor), ids.New("req"), "key-"+ids.New("k"), m.ID, body, preq)
	if err != nil {
		t.Fatal(err)
	}
	var mine proposals.Accepted
	_ = json.Unmarshal(out, &mine)
	inv := e.scenario(t, m)
	seq := 0
	next := func(args investigator.Arguments) int {
		seq++
		e.record(t, org, inv, investigator.Scenario, seq, args)
		e.ex.Pass(ctx)
		return seq
	}
	s := next(investigator.SaveScenarioArguments{ProposalID: mine.ProposalID})
	e.refusedAs(t, org, inv, s, store.Refused, investigator.NotInInvestigation, "/proposal_id")
	e.observe(t, org, inv, s)
	s = next(investigator.RunWhatIfArguments{ScenarioID: ids.New("scn")})
	e.refusedAs(t, org, inv, s, store.Refused, investigator.NotInInvestigation, "/scenario_id")
	e.observe(t, org, inv, s)
	s = next(investigator.ExplainResultsArguments{RunID: ids.New("sim")})
	e.refusedAs(t, org, inv, s, store.Refused, investigator.NotInInvestigation, "/run_id")
	e.observe(t, org, inv, s)
	// The second decode: an idea that is not NFC (the worker's decoder
	// refuses it too; recorded here as if it had not).
	raw := []byte(`{"from_suggestion":null,"idea":"Café line"}`)
	seq++
	e.recordRaw(t, org, inv, seq, "propose_what_if", raw, "sha256:"+strings.Repeat("4", 64))
	e.ex.Pass(ctx)
	e.refusedAs(t, org, inv, seq, store.Refused, investigator.InvalidArguments, "/idea")
	e.observe(t, org, inv, seq)
	// Arguments that are not the ones hashed: failed, never carried out.
	c, err := investigator.Encode(investigator.Scenario, investigator.ProposeWhatIfArguments{Idea: "A forged call"})
	if err != nil {
		t.Fatal(err)
	}
	seq++
	e.recordRaw(t, org, inv, seq, "propose_what_if", c.Canonical, "sha256:"+strings.Repeat("5", 64))
	e.ex.Pass(ctx)
	e.refusedAs(t, org, inv, seq, store.Failed, investigator.Internal, "")
	e.observe(t, org, inv, seq)
	if n := e.count(t, `SELECT count(*) FROM twin.ai_proposals WHERE investigation_id=$1`, inv); n != 0 {
		t.Fatalf("%d proposals after the refusals", n)
	}
	// Three what-ifs, then the 4th: LIMIT_REACHED by the database's cap.
	for i := 1; i <= 3; i++ {
		s := next(investigator.ProposeWhatIfArguments{Idea: "What-if " + string(rune('0'+i))})
		e.done(t, org, inv, s)
		e.observe(t, org, inv, s)
	}
	s = next(investigator.ProposeWhatIfArguments{Idea: "What-if 4"})
	e.refusedAs(t, org, inv, s, store.Refused, investigator.LimitReached, "")
	if n := e.count(t, `SELECT count(*) FROM twin.ai_proposals WHERE investigation_id=$1`, inv); n != 3 {
		t.Errorf("%d proposals", n)
	}
	// Every refusal is the contract's ToolError and shows in the steps.
	v := e.investigation(t, e.scopeOf(tenant.Viewer), inv)
	if len(v.Steps) != seq || v.Used.WhatIfs != 3 {
		t.Errorf("the investigation: %d steps, %+v", len(v.Steps), v.Used)
	}

	// The base moves (a person publishes): the AI's next what-if is
	// BASE_MOVED, in a new investigation on the old base... which the API
	// refuses to start (409), so the move happens while it runs. The stop
	// cancels the three queued proposal jobs first: their proposals fail
	// with JOB_CANCELLED, before any call to the provider.
	if _, _, err := e.svc.Stop(ctx, e.scopeOf(tenant.Editor), ids.New("req"), "key-"+ids.New("k"), inv, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if n := e.count(t, `SELECT count(*) FROM twin.ai_proposals p JOIN twin.jobs j ON j.organization_id=p.organization_id AND j.ai_proposal_id=p.id
		WHERE p.investigation_id=$1 AND p.status='failed' AND p.error->>'code'='JOB_CANCELLED' AND j.status='cancelled'`, inv); n != 3 {
		t.Errorf("%d of the 3 queued proposals cancelled by the stop", n)
	}
	inv2 := e.scenario(t, m)
	e.publishAnEdit(t, m.ID)
	e.record(t, org, inv2, investigator.Scenario, 1, investigator.ProposeWhatIfArguments{Idea: "After the move"})
	e.ex.Pass(ctx)
	e.refusedAs(t, org, inv2, 1, store.Refused, investigator.BaseMoved, "")
}

func (e *env) publishAnEdit(t *testing.T, modelID string) {
	t.Helper()
	ctx := context.Background()
	d, err := e.models.GetDraft(ctx, e.scopeOf(tenant.Editor), modelID)
	if err != nil {
		t.Fatal(err)
	}
	d, err = e.models.PatchDraft(ctx, e.scopeOf(tenant.Editor), ids.New("req"), modelID, d.Revision, []byte(`[{"op":"replace","path":"/name","value":"Moved"}]`))
	if err != nil {
		t.Fatal(err)
	}
	req := models.PublishRequest{ExpectedBaseVersionID: d.BaseVersionID}
	body, _ := json.Marshal(req)
	if status, _, err := e.models.Publish(ctx, e.scopeOf(tenant.Editor), ids.New("req"), "key-"+ids.New("k"), modelID, d.Revision, body, req); err != nil || status != 201 {
		t.Fatalf("publish: %d %v", status, err)
	}
}

// Two executors (two API processes in a deploy overlap) pass at once over
// one pending call: it is carried out exactly once (SKIP LOCKED and the
// single pending -> done move), with one proposal and one event.
func TestTwoExecutorsCarryOutACallOnce(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	m := e.model(t)
	org := e.org.ID
	other, err := executor.New(e.cfg)
	if err != nil {
		t.Fatal(err)
	}
	for round := 1; round <= 3; round++ {
		inv := e.scenario(t, m)
		e.record(t, org, inv, investigator.Scenario, 1, investigator.ProposeWhatIfArguments{Idea: "Round " + string(rune('0'+round))})
		var wg sync.WaitGroup
		for _, x := range []*executor.Executor{e.ex, other} {
			wg.Go(func() { x.Pass(ctx) })
		}
		wg.Wait()
		e.done(t, org, inv, 1)
		if n := e.count(t, `SELECT count(*) FROM twin.ai_proposals WHERE investigation_id=$1`, inv); n != 1 {
			t.Errorf("round %d: %d proposals", round, n)
		}
		if n := len(e.toolCalls(t, inv)); n != 1 {
			t.Errorf("round %d: %d events", round, n)
		}
		if _, _, err := e.svc.Stop(ctx, e.scopeOf(tenant.Editor), ids.New("req"), "key-"+ids.New("k"), inv, []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
}

// A stop racing a call (H-061 §13): whichever commits first, the other
// sees it. Either the call is done (then its proposal exists, labelled) or
// it was not carried out (pending, nothing made); the investigation ends
// stopped either way, and nothing is made after the stop.
func TestAStopRacingACall(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	m := e.model(t)
	org := e.org.ID
	outcomes := map[store.CallStatus]int{}
	for round := 1; round <= 8; round++ {
		inv := e.scenario(t, m)
		e.record(t, org, inv, investigator.Scenario, 1, investigator.ProposeWhatIfArguments{Idea: "Race " + string(rune('0'+round))})
		var wg sync.WaitGroup
		var stopErr error
		start := make(chan struct{})
		// Round 1: the call first, then the stop; round 2 the stop first;
		// the others at once, the stop held back a little more each round.
		lead := time.Duration(round-3) * 4 * time.Millisecond
		wg.Go(func() {
			<-start
			if round == 2 {
				time.Sleep(200 * time.Millisecond)
			}
			e.ex.Pass(ctx)
		})
		wg.Go(func() {
			<-start
			switch {
			case round == 1:
				time.Sleep(500 * time.Millisecond)
			case lead > 0:
				time.Sleep(lead)
			}
			_, _, stopErr = e.svc.Stop(ctx, e.scopeOf(tenant.Editor), ids.New("req"), "key-"+ids.New("k"), inv, []byte(`{}`))
		})
		close(start)
		wg.Wait()
		if stopErr != nil {
			t.Fatalf("round %d: stop: %v", round, stopErr)
		}
		c := e.call(t, org, inv, 1)
		made := e.count(t, `SELECT count(*) FROM twin.ai_proposals WHERE investigation_id=$1`, inv)
		switch {
		case c.Status == store.Done && made == 1:
		case c.Status == store.Pending && made == 0:
		default:
			t.Errorf("round %d: call %s with %d proposals", round, c.Status, made)
		}
		outcomes[c.Status]++
		// After the stop nothing more is carried out.
		e.ex.Pass(ctx)
		if after := e.call(t, org, inv, 1); after.Status != c.Status {
			t.Errorf("round %d: the call moved after the stop: %s", round, after.Status)
		}
		if v := e.investigation(t, e.scopeOf(tenant.Viewer), inv); v.Status != "stopped" {
			t.Errorf("round %d: %s", round, v.Status)
		}
	}
	t.Logf("outcomes %v", outcomes)
	if outcomes[store.Done] == 0 || outcomes[store.Pending] == 0 {
		t.Errorf("both orders were not seen: %v", outcomes)
	}
}

// The requester loses owner or editor (H-061 §8, §13): the call is not
// carried out and the investigation stops with role_lost at the next pass;
// a removed membership the same. Nothing is made.
func TestTheRequesterLosesTheRole(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	m := e.model(t)
	org := e.org.ID
	inv := e.scenario(t, m)
	e.record(t, org, inv, investigator.Scenario, 1, investigator.ProposeWhatIfArguments{Idea: "Before the demotion"})
	e.setRole(t, org, e.user(tenant.Editor), tenant.Viewer)
	e.ex.Pass(ctx)
	if c := e.call(t, org, inv, 1); c.Status != store.Pending {
		t.Errorf("a demoted requester's call: %s", c.Status)
	}
	v := e.investigation(t, e.scopeOf(tenant.Viewer), inv)
	if v.Status != "stopped" || v.Reason == nil || *v.Reason != "role_lost" || v.Steps[0].Status != "pending" {
		t.Errorf("after the demotion: %s %v", v.Status, v.Reason)
	}
	if n := e.count(t, `SELECT count(*) FROM twin.ai_proposals WHERE investigation_id=$1`, inv); n != 0 {
		t.Errorf("%d proposals", n)
	}
	// Restored, the requester starts again; then the membership is removed.
	e.setRole(t, org, e.user(tenant.Editor), tenant.Editor)
	inv2 := e.scenario(t, m)
	e.record(t, org, inv2, investigator.Scenario, 1, investigator.ProposeWhatIfArguments{Idea: "Before the removal"})
	e.owner(t, org, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM twin.memberships WHERE organization_id=$1 AND user_id=$2`, org, e.user(tenant.Editor))
		return err
	})
	e.ex.Pass(ctx)
	if c := e.call(t, org, inv2, 1); c.Status != store.Pending {
		t.Errorf("a removed requester's call: %s", c.Status)
	}
	var reason string
	e.owner(t, org, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT coalesce(reason,'') FROM twin.investigations WHERE id=$1`, inv2).Scan(&reason)
	})
	if reason != "role_lost" {
		t.Errorf("after the removal: %q", reason)
	}
}

// After the deadline (H-061 §8, §12) a pending call is closed as
// LIMIT_REACHED and nothing is made; the database refuses any labelled
// write then anyway.
func TestTheDeadline(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	m := e.model(t)
	org := e.org.ID
	inv := e.scenario(t, m)
	e.record(t, org, inv, investigator.Scenario, 1, investigator.ProposeWhatIfArguments{Idea: "Too late"})
	// Twenty minutes pass: the schema owner moves the clock of this one
	// investigation.
	e.age(t, inv, 21)
	e.ex.Pass(ctx)
	e.refusedAs(t, org, inv, 1, store.Refused, investigator.LimitReached, "")
	if n := e.count(t, `SELECT count(*) FROM twin.ai_proposals WHERE investigation_id=$1`, inv); n != 0 {
		t.Errorf("%d proposals after the deadline", n)
	}
}

// The stale self-cleanup (H-061 §13 "Ends", slice A7): a running
// investigation whose clock ran out with no worker to end it is ended with
// time_limit by its requester's next createInvestigation, in their own
// transaction, and the new one starts (202). Within the 2 minutes after the
// deadline, or while a worker runs its job, it is still running and the
// start is 409 (ErrRunning).
func TestTheStaleSelfCleanup(t *testing.T) {
	e := setup(t)
	m := e.model(t)
	org := e.org.ID
	// A worker runs the job (claimed, as the agent lane does): 23 minutes
	// on, the deadline 3 minutes past, the start is still refused.
	owner := e.scopeOf(tenant.Owner)
	held := e.start(t, owner, m, scenarioRequest(m))
	e.lease(t, org, held)
	e.age(t, held, 23)
	if _, err := e.try(t, owner, m, scenarioRequest(m)); !errors.Is(err, investigations.ErrRunning) {
		t.Errorf("a start while a worker runs the stale one: %v", err)
	}
	if s := e.statusOf(t, held); s != "running -" {
		t.Errorf("the investigation a worker runs: %s", s)
	}

	// Its job queued (no worker): 21 minutes on, within the 2 minutes'
	// grace, still running and the start refused.
	editor := e.scopeOf(tenant.Editor)
	stale := e.scenario(t, m)
	e.age(t, stale, 21)
	if _, err := e.try(t, editor, m, scenarioRequest(m)); !errors.Is(err, investigations.ErrRunning) {
		t.Errorf("a start within the grace: %v", err)
	}
	if s := e.statusOf(t, stale); s != "running -" {
		t.Errorf("within the grace: %s", s)
	}
	// 23 minutes on: ended at its time limit, and the new one runs.
	e.age(t, stale, 2)
	next, err := e.try(t, editor, m, scenarioRequest(m))
	if err != nil {
		t.Fatalf("a start past the grace: %v", err)
	}
	old := e.investigation(t, e.scopeOf(tenant.Viewer), stale)
	if old.Status != "stopped" || old.Reason == nil || *old.Reason != "time_limit" || old.Stop != nil || old.FinishedAt == nil ||
		old.Activity.State != investigations.Ended {
		t.Errorf("the stale investigation: %s %v %+v", old.Status, old.Reason, old.Stop)
	}
	if v := e.investigation(t, editor, next); v.Status != "running" || v.ID == stale {
		t.Errorf("the new investigation: %s", v.Status)
	}
	if n := e.count(t, `SELECT count(*) FROM twin.investigations WHERE organization_id=$1 AND created_by=$2 AND status='running'`,
		org, e.user(tenant.Editor)); n != 1 {
		t.Errorf("%d running investigations of the requester", n)
	}
}

// A stop asks the investigation's runs to cancel (H-061 §13 step 3):
// stopped right after run_what_if, before the job loop simulates them, both
// labelled runs carry the request, and the job loop then ends them
// cancelled, with no result.
func TestTheStopCancelsItsRuns(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	m := e.model(t)
	org := e.org.ID
	inv := e.scenario(t, m)
	idea := "Add a CI runner " + ids.New("k")
	e.fake.Script(provider.Mode("scenario").Role(), idea, answer(t, "Raise the CI runner capacity.", raiseCI))
	e.record(t, org, inv, investigator.Scenario, 1, investigator.ProposeWhatIfArguments{Idea: idea})
	e.ex.Pass(ctx)
	p := e.generated(t, *e.done(t, org, inv, 1).Effect.ProposalID)
	e.observe(t, org, inv, 1)
	e.record(t, org, inv, investigator.Scenario, 2, investigator.SaveScenarioArguments{ProposalID: p.ProposalID})
	e.ex.Pass(ctx)
	scn := e.done(t, org, inv, 2).Effect.ScenarioID
	e.observe(t, org, inv, 2)
	e.record(t, org, inv, investigator.Scenario, 3, investigator.RunWhatIfArguments{ScenarioID: *scn})
	e.ex.Pass(ctx)
	c3 := e.done(t, org, inv, 3)
	const pair = `SELECT count(*) FROM twin.simulation_runs WHERE investigation_id=$1 AND id IN ($2,$3) AND `
	if n := e.count(t, pair+`status='queued' AND NOT cancellation_requested`, inv, *c3.Effect.BaselineRunID, *c3.Effect.RunID); n != 2 {
		t.Fatalf("%d of the two runs queued before the stop", n)
	}
	if status, _, err := e.svc.Stop(ctx, e.scopeOf(tenant.Editor), ids.New("req"), "key-"+ids.New("k"), inv, []byte(`{}`)); err != nil || status != 200 {
		t.Fatalf("stop: %d %v", status, err)
	}
	if n := e.count(t, pair+`status='queued' AND cancellation_requested`, inv, *c3.Effect.BaselineRunID, *c3.Effect.RunID); n != 2 {
		t.Errorf("%d of the two runs asked to cancel by the stop", n)
	}
	deadline := time.Now().Add(time.Minute)
	for e.count(t, pair+`status IN ('queued','running')`, inv, *c3.Effect.BaselineRunID, *c3.Effect.RunID) > 0 {
		if time.Now().After(deadline) {
			t.Fatal("the stopped investigation's runs did not end")
		}
		e.loop.Pass(ctx)
		time.Sleep(10 * time.Millisecond)
	}
	if n := e.count(t, pair+`status='cancelled' AND NOT EXISTS (SELECT 1 FROM twin.simulation_results s WHERE s.run_id=simulation_runs.id)`,
		inv, *c3.Effect.BaselineRunID, *c3.Effect.RunID); n != 2 {
		t.Errorf("%d of the two runs ended cancelled, with no result", n)
	}
}

// The executor works only in the organizations the deployment assigns
// (D-008 as amended (c)): a call in another organization is never carried
// out, and another organization's investigation is not found.
func TestOnlyAssignedOrganizations(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	// A model and an investigation in the second organization.
	otherProject := e.newProject(t, e.other)
	var req models.CreateRequest
	_ = json.Unmarshal([]byte(createBody), &req)
	sc := tenant.Scope{OrganizationID: e.other.ID, UserID: e.other.Members[tenant.Editor].UserID}
	_, out, err := e.models.Create(ctx, sc, ids.New("req"), "key-"+ids.New("k"), otherProject, []byte(createBody), req)
	if err != nil {
		t.Fatal(err)
	}
	var m models.Model
	_ = json.Unmarshal(out, &m)
	cfg := runConfig
	inv := e.start(t, sc, m, investigations.CreateRequest{BaseVersionID: m.LatestVersion.ID, Mode: "scenario", Intent: "Elsewhere",
		OutputLocale: "en", RunConfig: &cfg, BaselineName: "Baseline"})
	e.record(t, e.other.ID, inv, investigator.Scenario, 1, investigator.ProposeWhatIfArguments{Idea: "Not assigned"})
	e.ex.Pass(ctx)
	if c := e.call(t, e.other.ID, inv, 1); c.Status != store.Pending {
		t.Errorf("a call in an organization not assigned: %s", c.Status)
	}
	if _, err := e.svc.Get(ctx, e.scopeOf(tenant.Owner), inv); !errors.Is(err, investigations.ErrNotFound) {
		t.Errorf("another organization's investigation: %v", err)
	}
}

// The acting-for scope refuses everything no tool does (H-061 §8, §11 deny
// triggers, reached through the services a person uses): publishing,
// deciding a proposal, cancelling a run, a person's keyed create; nothing
// changes.
func TestTheScopeRefusesWhatNoToolDoes(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	m := e.model(t)
	org := e.org.ID
	inv := e.scenario(t, m)
	acting := tenant.Scope{OrganizationID: org, UserID: e.user(tenant.Editor), ActingFor: inv}
	versions := e.count(t, `SELECT count(*) FROM twin.model_versions WHERE model_id=$1`, m.ID)
	d, err := e.models.GetDraft(ctx, e.scopeOf(tenant.Editor), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	preq := models.PublishRequest{ExpectedBaseVersionID: d.BaseVersionID}
	pbody, _ := json.Marshal(preq)
	if _, _, err := e.models.Publish(ctx, acting, ids.New("req"), "key-"+ids.New("k"), m.ID, d.Revision, pbody, preq); !errors.Is(err, postgres.ErrPermission) {
		t.Errorf("a publish in the scope: %v", err)
	}
	// The AI's own ready proposal, decided in the scope.
	idea := "An idea to decide " + ids.New("k")
	e.fake.Script(provider.Mode("scenario").Role(), idea, answer(t, "Raise the CI runner capacity.", raiseCI))
	e.record(t, org, inv, investigator.Scenario, 1, investigator.ProposeWhatIfArguments{Idea: idea})
	e.ex.Pass(ctx)
	p := e.generated(t, *e.done(t, org, inv, 1).Effect.ProposalID)
	areq := approval.ApproveRequest{ExpectedBaseVersionID: p.BaseVersionID}
	abody, _ := json.Marshal(areq)
	if _, _, err := e.decide.Approve(ctx, acting, ids.New("req"), "key-"+ids.New("k"), p.ProposalID, abody, areq); !errors.Is(err, postgres.ErrPermission) {
		t.Errorf("an approval in the scope: %v", err)
	}
	if _, _, err := e.decide.Reject(ctx, acting, ids.New("req"), "key-"+ids.New("k"), p.ProposalID, []byte(`{}`), approval.RejectRequest{}); !errors.Is(err, postgres.ErrPermission) {
		t.Errorf("a rejection in the scope: %v", err)
	}
	cbody, _ := json.Marshal(proposals.CreateRequest{BaseVersionID: m.LatestVersion.ID, Mode: "scenario", Intent: "A keyed create", OutputLocale: "en"})
	var creq proposals.CreateRequest
	_ = json.Unmarshal(cbody, &creq)
	if _, _, err := e.props.Create(ctx, acting, ids.New("req"), "key-"+ids.New("k"), m.ID, cbody, creq); !errors.Is(err, postgres.ErrPermission) {
		t.Errorf("a person's keyed create in the scope: %v", err)
	}
	if n := e.count(t, `SELECT count(*) FROM twin.model_versions WHERE model_id=$1`, m.ID); n != versions {
		t.Errorf("versions: %d, then %d", versions, n)
	}
	if got, err := e.props.Get(ctx, e.scopeOf(tenant.Viewer), ids.New("req"), p.ProposalID); err != nil || got.Status != "ready" {
		t.Errorf("the proposal after the refused decisions: %+v %v", got.Status, err)
	}
	// A person may still approve the AI's ready proposal (TEST-008): the
	// scenario is the person's, unlabelled, and the AI may neither save the
	// proposal again (PROPOSAL_DECIDED) nor run the person's scenario
	// (NOT_IN_INVESTIGATION), though its source is the AI's own what-if.
	status, out, err := e.decide.Approve(ctx, e.scopeOf(tenant.Editor), ids.New("req"), "key-"+ids.New("k"), p.ProposalID, abody, areq)
	if err != nil || status != 201 {
		t.Fatalf("a person's approval: %d %v", status, err)
	}
	var approved struct {
		ScenarioID string `json:"scenario_id"`
	}
	if err := json.Unmarshal(out, &approved); err != nil || approved.ScenarioID == "" {
		t.Fatalf("the approval's scenario: %s %v", out, err)
	}
	e.observe(t, org, inv, 1)
	e.record(t, org, inv, investigator.Scenario, 2, investigator.SaveScenarioArguments{ProposalID: p.ProposalID})
	e.ex.Pass(ctx)
	e.refusedAs(t, org, inv, 2, store.Refused, investigator.ProposalDecided, "/proposal_id")
	e.observe(t, org, inv, 2)
	e.record(t, org, inv, investigator.Scenario, 3, investigator.RunWhatIfArguments{ScenarioID: approved.ScenarioID})
	e.ex.Pass(ctx)
	e.refusedAs(t, org, inv, 3, store.Refused, investigator.NotInInvestigation, "/scenario_id")
	if n := e.count(t, `SELECT count(*) FROM twin.simulation_runs WHERE investigation_id=$1`, inv); n != 0 {
		t.Errorf("%d runs", n)
	}
}

// Research (H-061 §6.4): only the first what-if asks for it, and only when
// the person did; the later ones never do.
func TestResearchOnTheFirstWhatIfOnly(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	m := e.model(t)
	org := e.org.ID
	cfg := runConfig
	inv := e.start(t, e.scopeOf(tenant.Editor), m, investigations.CreateRequest{BaseVersionID: m.LatestVersion.ID, Mode: "scenario",
		Intent: "Research the line first", OutputLocale: "en", Research: true, RunConfig: &cfg, BaselineName: "Baseline"})
	for seq := 1; seq <= 2; seq++ {
		e.record(t, org, inv, investigator.Scenario, seq, investigator.ProposeWhatIfArguments{Idea: "What-if " + string(rune('0'+seq))})
		e.ex.Pass(ctx)
		e.done(t, org, inv, seq)
		e.observe(t, org, inv, seq)
	}
	var first, second bool
	e.owner(t, org, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT
			coalesce((SELECT (j.payload->>'research')::boolean FROM twin.jobs j JOIN twin.ai_proposals p ON p.id=j.ai_proposal_id
			  WHERE p.investigation_id=$1 AND p.investigation_index=1), false),
			coalesce((SELECT (j.payload->>'research')::boolean FROM twin.jobs j JOIN twin.ai_proposals p ON p.id=j.ai_proposal_id
			  WHERE p.investigation_id=$1 AND p.investigation_index=2), false)`, inv).Scan(&first, &second)
	})
	if !first || second {
		t.Errorf("research: first %v, second %v", first, second)
	}
}

// Run stops when its context ends and drains: a pass in progress finishes,
// and Run returns.
func TestRunStops(t *testing.T) {
	e := setup(t)
	cfg := e.cfg
	cfg.Interval = 10 * time.Millisecond
	ex, err := executor.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m := e.model(t)
	inv := e.scenario(t, m)
	e.record(t, e.org.ID, inv, investigator.Scenario, 1, investigator.ProposeWhatIfArguments{Idea: "In the background"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); ex.Run(ctx) }()
	deadline := time.Now().Add(20 * time.Second)
	for e.call(t, e.org.ID, inv, 1).Status == store.Pending {
		if time.Now().After(deadline) {
			t.Fatal("the background executor did not carry out the call")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(executor.DefaultDrain + 5*time.Second):
		t.Fatal("Run did not return after its context ended")
	}
}

// personRun queues a person's run of m (createSimulation's service).
func (e *env) personRun(t *testing.T, m models.Model, name, patch string, cfg simulation.RunConfig) string {
	t.Helper()
	req := runs.CreateRequest{ModelVersionID: m.LatestVersion.ID, Name: name, ScenarioPatch: jsontext.Value(patch), Config: cfg}
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	status, out, err := e.runs.Create(context.Background(), e.scopeOf(tenant.Editor), ids.New("req"), "key-"+ids.New("k"), body, req)
	if err != nil || status != 202 {
		t.Fatalf("createSimulation: %d %v", status, err)
	}
	var a runs.Accepted
	if err := json.Unmarshal(out, &a); err != nil {
		t.Fatal(err)
	}
	return a.ID
}

// personAnalysis is a person's analysis of run against baseline, completed
// by the worker (its job claimed and completed with it) with one suggested
// experiment.
func (e *env) personAnalysis(t *testing.T, run, baseline string) string {
	t.Helper()
	ctx := context.Background()
	ana := &analyses.Service{DB: e.db, AI: proposals.Available, Policy: fast}
	req := analyses.CreateRequest{BaselineRunID: baseline, OutputLocale: "en"}
	body, _ := json.Marshal(req)
	status, out, err := ana.Create(ctx, e.scopeOf(tenant.Editor), ids.New("req"), "key-"+ids.New("k"), run, body, req)
	if err != nil || status != 202 {
		t.Fatalf("createAnalysis: %d %v", status, err)
	}
	var a analyses.Accepted
	if err := json.Unmarshal(out, &a); err != nil {
		t.Fatal(err)
	}
	if err := e.wscope.Run(ctx, e.org.ID, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		_, l, ok, err := jobs.Claim(ctx, tx, e.org.ID, "test-worker:rest", []jobs.Type{jobs.Analysis}, fast)
		if err != nil || !ok {
			return errors.Join(errors.New("no analysis job to claim"), err)
		}
		if err := analysisstore.Complete(ctx, tx, e.org.ID, a.ID, []byte(`{"suggested_next_experiments":[{"name":"More CI",`+
			`"hypothesis":"Builds wait for the one CI runner.","requires_confirmation":true}]}`), nil); err != nil {
			return err
		}
		return jobs.Complete(ctx, tx, l)
	}); err != nil {
		t.Fatalf("completing the analysis: %v", err)
	}
	return a.ID
}

// Entry B (H-061 §10): from a person's completed analysis of a what-if
// against a baseline with no patch, the investigation reuses that baseline
// (its settings frozen, 3 runs left), and its first what-if may be one of
// the analysis's suggested experiments, asked for in the server's words;
// its run is started against the person's baseline, which is not run again.
// An analysis whose baseline's settings are over the caps (11
// replications) cannot be reused.
func TestEntryB(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	m := e.model(t)
	org := e.org.ID
	base := e.personRun(t, m, "A person's baseline", "[]", runConfig)
	scen := e.personRun(t, m, "A person's what-if", raiseCI, runConfig)
	many := runConfig
	many.Replications = 11
	bigBase := e.personRun(t, m, "A person's baseline of 11 replications", "[]", many)
	bigScen := e.personRun(t, m, "A person's what-if of 11 replications", raiseCI, many)
	deadline := time.Now().Add(time.Minute)
	for e.count(t, `SELECT count(*) FROM twin.simulation_runs WHERE id IN ($1, $2, $3, $4) AND status='completed'`, base, scen, bigBase, bigScen) < 4 {
		if time.Now().After(deadline) {
			t.Fatal("the person's runs did not complete")
		}
		e.loop.Pass(ctx)
		time.Sleep(10 * time.Millisecond)
	}
	// A baseline over the caps: not reusable (422 at /from_analysis_id).
	over := e.personAnalysis(t, bigScen, bigBase)
	req := investigations.CreateRequest{FromAnalysisID: over, OutputLocale: "en"}
	body, _ := json.Marshal(req)
	_, _, err := e.svc.Create(ctx, e.scopeOf(tenant.Editor), ids.New("req"), "key-"+ids.New("k"), m.ID, body, req)
	var re *investigations.RequestError
	if !errors.As(err, &re) || len(re.Issues) != 1 || re.Issues[0].Path != "/from_analysis_id" {
		t.Errorf("a baseline over the caps: %v", err)
	}
	ana := e.personAnalysis(t, scen, base)
	inv := e.start(t, e.scopeOf(tenant.Editor), m, investigations.CreateRequest{FromAnalysisID: ana, OutputLocale: "zh-Hant"})
	v := e.investigation(t, e.scopeOf(tenant.Viewer), inv)
	if v.Source != "analysis" || v.SourceAnalysisID == nil || *v.SourceAnalysisID != ana || v.BaselineRunID == nil || *v.BaselineRunID != base ||
		v.Caps.Runs != 3 || v.RunConfig == nil || *v.RunConfig != runConfig || v.OutputLocale != "zh-Hant" {
		t.Fatalf("entry B: %+v", v)
	}
	// Another completed analysis of the person's (not the one it started
	// from), a suggestion it does not have, then the one it has.
	e.record(t, org, inv, investigator.Scenario, 1, investigator.ProposeWhatIfArguments{Idea: "Another analysis's idea",
		FromSuggestion: &investigator.SuggestionRef{AnalysisID: over, Index: 0}})
	e.ex.Pass(ctx)
	e.refusedAs(t, org, inv, 1, store.Refused, investigator.NotInInvestigation, "/from_suggestion/analysis_id")
	e.observe(t, org, inv, 1)
	e.record(t, org, inv, investigator.Scenario, 2, investigator.ProposeWhatIfArguments{Idea: "The fifth idea",
		FromSuggestion: &investigator.SuggestionRef{AnalysisID: ana, Index: 5}})
	e.ex.Pass(ctx)
	e.refusedAs(t, org, inv, 2, store.Refused, investigator.NotInInvestigation, "/from_suggestion/index")
	e.observe(t, org, inv, 2)
	intent := "More CI: Builds wait for the one CI runner."
	e.fake.Script(provider.Mode("scenario").Role(), intent, answer(t, "Raise the CI runner capacity.", raiseCI))
	e.record(t, org, inv, investigator.Scenario, 3, investigator.ProposeWhatIfArguments{Idea: "Try the analysis's first idea",
		FromSuggestion: &investigator.SuggestionRef{AnalysisID: ana, Index: 0}})
	e.ex.Pass(ctx)
	pid := *e.done(t, org, inv, 3).Effect.ProposalID
	var asked string
	e.owner(t, org, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT intent FROM twin.ai_proposals WHERE id=$1`, pid).Scan(&asked)
	})
	if asked != intent {
		t.Errorf("the suggestion's intent %q", asked)
	}
	if n := len(e.events(t, pid)); n == 0 || e.events(t, pid)[0].meta["source"] != "suggestion" {
		t.Errorf("proposal.create: %+v", e.events(t, pid))
	}
	p := e.generated(t, pid)
	e.observe(t, org, inv, 3)
	e.record(t, org, inv, investigator.Scenario, 4, investigator.SaveScenarioArguments{ProposalID: p.ProposalID})
	e.ex.Pass(ctx)
	sc := *e.done(t, org, inv, 4).Effect.ScenarioID
	e.observe(t, org, inv, 4)
	e.record(t, org, inv, investigator.Scenario, 5, investigator.RunWhatIfArguments{ScenarioID: sc})
	e.ex.Pass(ctx)
	c4 := e.done(t, org, inv, 5)
	if *c4.Effect.BaselineRunID != base || e.count(t, `SELECT count(*) FROM twin.simulation_runs WHERE investigation_id=$1`, inv) != 1 {
		t.Errorf("the reused baseline: %+v", c4.Effect)
	}
	if v := e.investigation(t, e.scopeOf(tenant.Viewer), inv); len(v.Candidates) != 1 || v.Candidates[0].Source != "suggestion" || v.Used.Runs != 1 {
		t.Errorf("entry B's candidate: %+v %+v", v.Candidates, v.Used)
	}
}
