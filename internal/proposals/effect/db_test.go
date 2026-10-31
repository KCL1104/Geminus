package effect_test

// DB-backed tests of the shared effects and the services' in-transaction
// bodies (H-061 A4; TEST-008, TEST-010, REQ-018, REQ-019, REQ-021): models
// through internal/models, proposals queued through proposals.Queue in an
// AI investigation's acting-for transaction (tenant.Scope.ActingFor, set
// here by the test as the executor will set it) and generated ready by the
// job loop with the deterministic fake provider, then saved or written by
// effect.Service, all as the plain API and worker logins. Without
// GEMINUS_TEST_DATABASE_URL they skip (NOT RUN); make db-go-test sets
// GEMINUS_REQUIRE_DB_TESTS=1, so there a skip is a failure.

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
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
	"github.com/kcl1104/geminus/internal/httpapi"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/models"
	"github.com/kcl1104/geminus/internal/proposals"
	"github.com/kcl1104/geminus/internal/proposals/approval"
	"github.com/kcl1104/geminus/internal/proposals/effect"
	"github.com/kcl1104/geminus/internal/proposals/generator"
	"github.com/kcl1104/geminus/internal/runs"
	"github.com/kcl1104/geminus/internal/runs/runner"
	"github.com/kcl1104/geminus/internal/scenarios"
	"github.com/kcl1104/geminus/internal/simulation"
	"github.com/kcl1104/geminus/internal/storage/dbtest"
	"github.com/kcl1104/geminus/internal/storage/investigations"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	modelstore "github.com/kcl1104/geminus/internal/storage/models"
	"github.com/kcl1104/geminus/internal/storage/objects"
	"github.com/kcl1104/geminus/internal/storage/postgres"
	store "github.com/kcl1104/geminus/internal/storage/proposals"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/tenant/workerscope"
	"github.com/kcl1104/geminus/internal/worker"
	"github.com/kcl1104/geminus/internal/worker/handler"
)

const issuer = "https://test.invalid"

var fast = jobs.Policy{Lease: 3 * time.Second, MaxAttempts: 3, BackoffBase: 20 * time.Millisecond, BackoffMax: 100 * time.Millisecond}

// runConfig is every scenario-mode investigation's frozen run settings
// here: one replication of one day, no trace (within H-060's bounds).
var runConfig = simulation.RunConfig{Horizon: simulation.Duration{Value: 1, Unit: "day"}, Warmup: simulation.Duration{Value: 0, Unit: "day"},
	Replications: 1, Seed: 7, TraceLevel: simulation.TraceNone}

type env struct {
	admin   *pgxpool.Pool
	org     dbtest.Org
	other   dbtest.Org
	project string
	db      *tenant.DB
	reg     *domainpack.Registry
	models  *models.Service
	props   *proposals.Service
	runs    *runs.Service
	ana     *analyses.Service
	scens   *scenarios.Service
	decide  *approval.Service
	effects *effect.Service
	fake    *fake.Backend
	w       *worker.Worker
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
	scope, err := workerscope.New(wpool, []string{org.ID, other.ID})
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
	w, err := worker.New(scope, worker.Config{ID: "test-worker", Policy: fast, Heartbeat: time.Second, Idle: 20 * time.Millisecond,
		ReapLimit: 10, Handlers: map[jobs.Type]handler.Handler{jobs.ModelProposal: gen, jobs.Simulation: sim}, Log: discard()})
	if err != nil {
		t.Fatal(err)
	}
	db := tenant.New(api)
	e := &env{admin: admin, org: org, other: other, db: db, reg: reg, fake: f, w: w,
		models:  &models.Service{DB: db, Packs: reg},
		props:   &proposals.Service{DB: db, AI: proposals.Available, Policy: fast},
		runs:    &runs.Service{DB: db, Packs: reg, Objects: fs, Policy: fast},
		ana:     &analyses.Service{DB: db, AI: proposals.Available, Policy: fast},
		scens:   &scenarios.Service{DB: db, Packs: reg, Objects: fs},
		decide:  &approval.Service{DB: db, Packs: reg, Objects: fs},
		effects: &effect.Service{Packs: reg, Objects: fs}}
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

func (e *env) editor() string { return e.org.Members[tenant.Editor].UserID }

func (e *env) scope(r tenant.Role) tenant.Scope {
	return tenant.Scope{OrganizationID: e.org.ID, UserID: e.org.Members[r].UserID}
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

// model creates a model from the synthetic delivery template as user in
// org's project: version 1 and a clean draft at revision 1.
func (e *env) model(t *testing.T, sc tenant.Scope, project string) models.Model {
	t.Helper()
	var req models.CreateRequest
	if err := json.Unmarshal([]byte(createBody), &req); err != nil {
		t.Fatal(err)
	}
	status, out, err := e.models.Create(context.Background(), sc, ids.New("req"), "key-"+ids.New("k"), project, []byte(createBody), req)
	if err != nil || status != 201 {
		t.Fatalf("create: %d %v", status, err)
	}
	var m models.Model
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

const (
	raiseCI = `[{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"replace","path":"/resources/1/capacity","value":6}]`
	lowerCI = `[{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"replace","path":"/resources/1/capacity","value":2}]`
	moreQA  = `[{"op":"test","path":"/resources/2/id","value":"test_environment"},{"op":"replace","path":"/resources/2/capacity","value":2}]`
)

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

// script makes the fake answer text to a proposal in mode whose intent is
// intent, under the role the generator asks that mode as.
func (e *env) script(mode, intent string, text []byte) {
	e.fake.Script(provider.Mode(mode).Role(), intent, text)
}

// start stores an investigation of modelID at base in mode for the editor,
// with its job, in the editor's own transaction (as createInvestigation
// will, slice A7).
func (e *env) start(t *testing.T, mode investigations.Mode, modelID, base string) investigations.Investigation {
	t.Helper()
	inv := investigations.Investigation{ID: ids.New("inv"), OrganizationID: e.org.ID, ModelID: modelID, BaseVersionID: base, Mode: mode,
		Source: investigations.FromRequest, Intent: new("Find a faster line"), OutputLocale: "en", ProposalModel: "muse-spark-1.3",
		PlannerModel: "muse-spark-1.3", PromptTemplateVersion: "investigator-0.1.0", Caps: investigations.CapsFor(mode, false),
		CreatedBy: e.editor()}
	if mode == investigations.Scenario {
		cfg, err := json.Marshal(runConfig)
		if err != nil {
			t.Fatal(err)
		}
		hash, err := hashing.Config(runConfig)
		if err != nil {
			t.Fatal(err)
		}
		inv.Objective, inv.RunConfig, inv.RunConfigHash = []byte(`{"metric_path":"/metrics/lead_time_minutes/p50","better":"lower"}`), cfg, &hash
		inv.BaselineName = new("Baseline")
	}
	var out investigations.Investigation
	if err := e.db.Run(context.Background(), e.scope(tenant.Editor), tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx, _ tenant.Membership) error {
		var err error
		if out, err = investigations.Insert(ctx, tx, inv); err != nil {
			return err
		}
		_, err = jobs.Enqueue(ctx, tx, jobs.New{ID: ids.New("job"), OrganizationID: inv.OrganizationID, Type: jobs.Investigation,
			Resource: jobs.Resource{InvestigationID: inv.ID}, Payload: map[string]any{"investigation_id": inv.ID, "request_id": ids.New("req")},
			MaxAttempts: investigations.MaxAttempts}, fast)
		return err
	}); err != nil {
		t.Fatalf("start: %v", err)
	}
	return out
}

// stop ends inv as its requester's stop (a person's own transaction).
func (e *env) stop(t *testing.T, inv investigations.Investigation) {
	t.Helper()
	if err := e.db.Run(context.Background(), e.scope(tenant.Editor), tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx, _ tenant.Membership) error {
		return investigations.Stop(ctx, tx, e.org.ID, inv.ID, e.editor())
	}); err != nil {
		t.Fatal(err)
	}
}

// acting runs fn in inv's acting-for transaction for its requester, as the
// executor will (slice A7).
func (e *env) acting(inv investigations.Investigation, fn func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error) error {
	return e.db.Run(context.Background(), tenant.Scope{OrganizationID: inv.OrganizationID, UserID: inv.CreatedBy, ActingFor: inv.ID},
		tenant.ReadWrite, fn)
}

// propose queues the AI's what-if (or edit) idea intent through
// proposals.Queue in inv's scope, scripted to answer changes, and has it
// generated; it returns the proposal as getProposal shows it.
func (e *env) propose(t *testing.T, inv investigations.Investigation, intent, changes string) proposals.Proposal {
	t.Helper()
	intent += " " + ids.New("k")
	e.script(string(inv.Mode), intent, answer(t, "Raise the CI runner capacity.", changes))
	var id string
	if err := e.acting(inv, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		a, err := e.props.Queue(ctx, tx, m, ids.New("req"), inv.ModelID, proposals.CreateRequest{BaseVersionID: inv.BaseVersionID,
			Mode: string(inv.Mode), Intent: intent, OutputLocale: "en"}, proposals.FromIntent)
		id = a.ProposalID
		return err
	}); err != nil {
		t.Fatalf("queue in the scope: %v", err)
	}
	return e.generated(t, id)
}

// generated runs the job loop until proposal id leaves generating.
func (e *env) generated(t *testing.T, id string) proposals.Proposal {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		e.w.Pass(context.Background())
		if p := e.get(t, id); p.Status != "generating" {
			return p
		}
		if time.Now().After(deadline) {
			t.Fatalf("proposal %s is still generating", id)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// get is getProposal as a viewer, checked against proposal.schema.json.
func (e *env) get(t *testing.T, id string) proposals.Proposal {
	t.Helper()
	p, err := e.props.Get(context.Background(), e.scope(tenant.Viewer), ids.New("req"), id)
	if err != nil {
		t.Fatal(err)
	}
	matches(t, "proposal.schema.json", p)
	return p
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

func (e *env) save(inv investigations.Investigation, id string) (effect.Saved, error) {
	var out effect.Saved
	err := e.acting(inv, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		var err error
		out, err = e.effects.SaveScenario(ctx, tx, m, ids.New("req"), id)
		return err
	})
	return out, err
}

func (e *env) write(inv investigations.Investigation, id string) (effect.Written, error) {
	var out effect.Written
	err := e.acting(inv, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		var err error
		out, err = e.effects.WriteDraft(ctx, tx, m, ids.New("req"), id)
		return err
	})
	return out, err
}

// refused asserts err is the AI effect's refusal code at path.
func refused(t *testing.T, what string, err error, code investigator.Code, path string) {
	t.Helper()
	var r *effect.Refusal
	if !errors.As(err, &r) || r.Code != code || r.Path != path {
		t.Errorf("%s: %v, want %s at %q", what, err, code, path)
	}
}

// event is an audit event as stored.
type event struct {
	action, actorType, actorID string
	meta                       map[string]any
}

// events are the audit events about target, oldest first.
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

// only is the one event of action about target.
func (e *env) only(t *testing.T, target, action string) event {
	t.Helper()
	var found []event
	for _, ev := range e.events(t, target) {
		if ev.action == action {
			found = append(found, ev)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d %s events about %s", len(found), action, target)
	}
	return found[0]
}

// markedFor asserts ev is the requester's own event, marked as the AI's
// for inv.
func markedFor(t *testing.T, ev event, inv investigations.Investigation) {
	t.Helper()
	if ev.actorType != "user" || ev.actorID != inv.CreatedBy || ev.meta["investigation_id"] != inv.ID || ev.meta["by_ai"] != true {
		t.Errorf("%s: actor %s %s, metadata %v: not the requester's event marked as the AI's", ev.action, ev.actorType, ev.actorID, ev.meta)
	}
}

// unmarked asserts ev carries no AI mark.
func unmarked(t *testing.T, ev event) {
	t.Helper()
	if _, ok := ev.meta["investigation_id"]; ok || ev.meta["by_ai"] != nil {
		t.Errorf("%s: a person's event carries the AI's mark: %v", ev.action, ev.meta)
	}
}

// The AI's what-if, end to end through the shared code: queued in its
// investigation's scope (labelled what-if 1, the event marked), generated
// ready, saved (the scenario labelled "Saved by the AI", the proposal
// applied with it, never decided, proposal.apply marked), then run (the
// baseline in slot 0, the what-if in slot 1, "Started by the AI") and
// explained (labelled); every body matches its schema, the actor is
// always the requester, and no version is created (H-061 §6.4, §15.2).
func TestTheAIsWhatIfThroughTheSharedCode(t *testing.T) {
	e := setup(t)
	m := e.model(t, e.scope(tenant.Editor), e.project)
	inv := e.start(t, investigations.Scenario, m.ID, m.LatestVersion.ID)
	versions := e.count(t, `SELECT count(*) FROM twin.model_versions WHERE model_id=$1`, m.ID)

	p := e.propose(t, inv, "Add a CI runner", raiseCI)
	if p.Status != "ready" || p.Investigation == nil || p.Investigation.ID != inv.ID || p.Investigation.Index != 1 || p.CreatedBy != e.editor() {
		t.Fatalf("the AI's proposal: %+v", p)
	}
	ev := e.only(t, p.ProposalID, "proposal.create")
	markedFor(t, ev, inv)
	if ev.meta["source"] != "intent" {
		t.Errorf("proposal.create source %v", ev.meta["source"])
	}

	saved, err := e.save(inv, p.ProposalID)
	if err != nil {
		t.Fatalf("save_scenario: %v", err)
	}
	if saved.ProposalID != p.ProposalID || saved.Name != "Raise the CI runner capacity." || !strings.HasPrefix(saved.ScenarioID, "scn_") {
		t.Errorf("saved %+v", saved)
	}
	applied := e.get(t, p.ProposalID)
	if applied.Status != "applied" || applied.ScenarioID == nil || *applied.ScenarioID != saved.ScenarioID || applied.DecidedBy != nil ||
		applied.DecidedAt != nil || applied.NewVersionID != nil || applied.AppliedDraftRevision != nil || applied.Investigation == nil {
		t.Errorf("applied: %+v", applied)
	}
	scn, err := e.scens.Get(context.Background(), e.scope(tenant.Viewer), saved.ScenarioID)
	if err != nil {
		t.Fatal(err)
	}
	matches(t, "scenario.schema.json", scn)
	if l := scn.MadeByAI; l == nil || l.InvestigationID != inv.ID || l.RequestedBy != e.editor() || l.GeneratedBy.Provider != "meta" ||
		l.GeneratedBy.Model != "muse-spark-1.3" || scn.CreatedBy != e.editor() || scn.SourceProposalID == nil || *scn.SourceProposalID != p.ProposalID ||
		scn.PatchHash != *p.PatchHash {
		t.Errorf("the AI's scenario: %+v", scn)
	}
	ev = e.only(t, p.ProposalID, "proposal.apply")
	markedFor(t, ev, inv)
	if ev.meta["scenario_id"] != saved.ScenarioID || ev.meta["mode"] != "scenario" || ev.meta["patch_hash"] != *p.PatchHash ||
		ev.meta["risk_level"] != *p.RiskLevel || ev.meta["draft_revision"] != nil {
		t.Errorf("proposal.apply: %v", ev.meta)
	}
	// Saving it again: it is applied already, and nothing more is written.
	_, err = e.save(inv, p.ProposalID)
	refused(t, "saving it twice", err, investigator.ProposalNotReady, "/proposal_id")
	if n := e.count(t, `SELECT count(*) FROM twin.scenarios WHERE source_proposal_id=$1`, p.ProposalID); n != 1 {
		t.Errorf("%d scenarios of the proposal", n)
	}

	// run_what_if through runs.Queue: the baseline (slot 0), then the
	// saved scenario (slot 1), each labelled, with the frozen settings.
	var base, run runs.Accepted
	if err := e.acting(inv, func(ctx context.Context, tx tenant.Tx, mb tenant.Membership) error {
		var err error
		if base, err = e.runs.Queue(ctx, tx, mb, ids.New("req"), runs.CreateRequest{ModelVersionID: inv.BaseVersionID, Name: "Baseline",
			ScenarioPatch: jsontext.Value(`[]`), Config: runConfig}); err != nil {
			return err
		}
		run, err = e.runs.Queue(ctx, tx, mb, ids.New("req"), runs.CreateRequest{ModelVersionID: inv.BaseVersionID, Name: saved.Name,
			ScenarioID: saved.ScenarioID, Config: runConfig})
		return err
	}); err != nil {
		t.Fatalf("run_what_if in the scope: %v", err)
	}
	for i, id := range []string{base.ID, run.ID} {
		r, err := e.runs.Get(context.Background(), e.scope(tenant.Viewer), ids.New("req"), id)
		if err != nil {
			t.Fatal(err)
		}
		matches(t, "simulation.schema.json#/$defs/Run", r)
		if l := r.MadeByAI; l == nil || l.InvestigationID != inv.ID || l.RequestedBy != e.editor() || l.GeneratedBy.Model != "muse-spark-1.3" {
			t.Errorf("run %d: %+v", i, r.MadeByAI)
		}
		markedFor(t, e.only(t, id, "simulation.create"), inv)
	}
	if n := e.count(t, `SELECT count(*) FROM twin.simulation_runs WHERE investigation_id=$1 AND investigation_slot IN (0, 1)`, inv.ID); n != 2 {
		t.Errorf("%d labelled runs in slots 0 and 1", n)
	}
	// explain_results through analyses.Queue, once both runs completed.
	deadline := time.Now().Add(time.Minute)
	for e.count(t, `SELECT count(*) FROM twin.simulation_runs WHERE investigation_id=$1 AND status='completed'`, inv.ID) < 2 {
		if time.Now().After(deadline) {
			t.Fatal("the AI's runs did not complete")
		}
		e.w.Pass(context.Background())
		time.Sleep(10 * time.Millisecond)
	}
	var explained analyses.Accepted
	if err := e.acting(inv, func(ctx context.Context, tx tenant.Tx, mb tenant.Membership) error {
		var err error
		explained, err = e.ana.Queue(ctx, tx, mb, ids.New("req"), run.ID, analyses.CreateRequest{BaselineRunID: base.ID, OutputLocale: "en"})
		return err
	}); err != nil {
		t.Fatalf("explain_results in the scope: %v", err)
	}
	rec, err := e.ana.Get(context.Background(), e.scope(tenant.Viewer), ids.New("req"), explained.ID)
	if err != nil {
		t.Fatal(err)
	}
	matches(t, "http.schema.json#/$defs/AnalysisRecord", rec)
	if rec.InvestigationID == nil || *rec.InvestigationID != inv.ID {
		t.Errorf("the AI's analysis: %+v", rec)
	}
	markedFor(t, e.only(t, explained.ID, "analysis.create"), inv)
	// Nothing the AI did created a version (it never publishes).
	if n := e.count(t, `SELECT count(*) FROM twin.model_versions WHERE model_id=$1`, m.ID); n != versions {
		t.Errorf("versions: %d, then %d", versions, n)
	}
}

// A person's own rows and events are as they were: no label in any body,
// no mark on any event (every existing test holds the bytes; this holds
// the absence of the new members), and a person cannot queue "from a
// suggestion" outside an investigation.
func TestAPersonsRowsCarryNoLabel(t *testing.T) {
	e := setup(t)
	m := e.model(t, e.scope(tenant.Editor), e.project)
	intent := "Raise it " + ids.New("k")
	e.script("scenario", intent, answer(t, "Raise the CI runner capacity.", raiseCI))
	req := proposals.CreateRequest{BaseVersionID: m.LatestVersion.ID, Mode: "scenario", Intent: intent}
	body, _ := json.Marshal(req)
	status, out, err := e.props.Create(context.Background(), e.scope(tenant.Editor), ids.New("req"), "key-"+ids.New("k"), m.ID, body, req)
	if err != nil || status != 202 {
		t.Fatalf("%d %v", status, err)
	}
	var a proposals.Accepted
	if err := json.Unmarshal(out, &a); err != nil {
		t.Fatal(err)
	}
	p := e.generated(t, a.ProposalID)
	k := "key-" + ids.New("k")
	approve := approval.ApproveRequest{ExpectedBaseVersionID: m.LatestVersion.ID}
	abody, _ := json.Marshal(approve)
	if status, _, err := e.decide.Approve(context.Background(), e.scope(tenant.Editor), ids.New("req"), k, p.ProposalID, abody, approve); err != nil || status != 201 {
		t.Fatalf("approve: %d %v", status, err)
	}
	approved := e.get(t, p.ProposalID)
	scn, err := e.scens.Get(context.Background(), e.scope(tenant.Viewer), *approved.ScenarioID)
	if err != nil {
		t.Fatal(err)
	}
	rreq := runs.CreateRequest{ModelVersionID: m.LatestVersion.ID, Name: "Mine", ScenarioPatch: jsontext.Value(`[]`), Config: runConfig}
	rbody, _ := json.Marshal(rreq)
	status, out, err = e.runs.Create(context.Background(), e.scope(tenant.Editor), ids.New("req"), "key-"+ids.New("k"), rbody, rreq)
	if err != nil || status != 202 {
		t.Fatalf("run: %d %v", status, err)
	}
	var ra runs.Accepted
	if err := json.Unmarshal(out, &ra); err != nil {
		t.Fatal(err)
	}
	r, err := e.runs.Get(context.Background(), e.scope(tenant.Viewer), ids.New("req"), ra.ID)
	if err != nil {
		t.Fatal(err)
	}
	d, err := e.models.GetDraft(context.Background(), e.scope(tenant.Viewer), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]any{"proposal": approved, "scenario": scn, "run": r, "draft": d} {
		data, _ := json.Marshal(v)
		for _, member := range []string{`"investigation"`, `"made_by_ai"`, `"ai_edit"`, `"applied_draft_revision"`, `"investigation_id"`} {
			if strings.Contains(string(data), member) {
				t.Errorf("a person's %s shows %s: %s", name, member, data)
			}
		}
	}
	for _, target := range []string{p.ProposalID, ra.ID} {
		for _, ev := range e.events(t, target) {
			if ev.actorType == "user" {
				unmarked(t, ev)
			}
		}
	}
	// "From a suggestion" is the AI's only.
	err = e.db.Run(context.Background(), e.scope(tenant.Editor), tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx, mb tenant.Membership) error {
		_, err := e.props.Queue(ctx, tx, mb, ids.New("req"), m.ID, req, proposals.FromSuggestion)
		return err
	})
	if err == nil {
		t.Error("a person queued a proposal from a suggestion")
	}
}

// What save_scenario refuses, each with its code and nothing written: a
// person's proposal, another organization's, one still generating, one a
// person decided first (approved: TEST-008, the person's scenario carries
// no label; rejected), one whose base moved (it stays ready, never made
// conflicted by the AI), one the server's check refuses now; and the
// errors that are not the AI's to react to: outside the scope, a demoted
// requester, an investigation stopped first.
func TestSaveScenarioRefusals(t *testing.T) {
	e := setup(t)
	m := e.model(t, e.scope(tenant.Editor), e.project)
	inv := e.start(t, investigations.Scenario, m.ID, m.LatestVersion.ID)
	nothing := func(what string, scenarios int) {
		t.Helper()
		if n := e.count(t, `SELECT count(*) FROM twin.scenarios WHERE model_id=$1`, m.ID); n != scenarios {
			t.Errorf("%s: %d scenarios, want %d", what, n, scenarios)
		}
		if n := e.count(t, `SELECT count(*) FROM twin.audit_events WHERE organization_id=$1 AND action='proposal.apply'`, e.org.ID); n != 0 {
			t.Errorf("%s: %d proposal.apply events", what, n)
		}
	}

	// A person's own ready proposal.
	intent := "Mine " + ids.New("k")
	e.script("scenario", intent, answer(t, "Lower the CI runner capacity.", lowerCI))
	req := proposals.CreateRequest{BaseVersionID: m.LatestVersion.ID, Mode: "scenario", Intent: intent}
	body, _ := json.Marshal(req)
	_, out, err := e.props.Create(context.Background(), e.scope(tenant.Editor), ids.New("req"), "key-"+ids.New("k"), m.ID, body, req)
	if err != nil {
		t.Fatal(err)
	}
	var mine proposals.Accepted
	_ = json.Unmarshal(out, &mine)
	e.generated(t, mine.ProposalID)
	_, err = e.save(inv, mine.ProposalID)
	refused(t, "a person's proposal", err, investigator.NotInInvestigation, "/proposal_id")
	_, err = e.save(inv, ids.New("aip"))
	refused(t, "no such proposal", err, investigator.NotInInvestigation, "/proposal_id")
	// Another organization's proposal, by its ID (RLS and the filter).
	otherModel := e.model(t, tenant.Scope{OrganizationID: e.other.ID, UserID: e.other.Members[tenant.Editor].UserID}, e.newProject(t, e.other))
	oreq := proposals.CreateRequest{BaseVersionID: otherModel.LatestVersion.ID, Mode: "scenario", Intent: "Theirs " + ids.New("k")}
	obody, _ := json.Marshal(oreq)
	_, out, err = e.props.Create(context.Background(), tenant.Scope{OrganizationID: e.other.ID, UserID: e.other.Members[tenant.Editor].UserID},
		ids.New("req"), "key-"+ids.New("k"), otherModel.ID, obody, oreq)
	if err != nil {
		t.Fatal(err)
	}
	var theirs proposals.Accepted
	_ = json.Unmarshal(out, &theirs)
	_, err = e.save(inv, theirs.ProposalID)
	refused(t, "another organization's proposal", err, investigator.NotInInvestigation, "/proposal_id")
	nothing("a proposal not the investigation's", 0)

	// Still generating: queued in the scope, its job not run yet.
	var generating string
	if err := e.acting(inv, func(ctx context.Context, tx tenant.Tx, mb tenant.Membership) error {
		a, err := e.props.Queue(ctx, tx, mb, ids.New("req"), m.ID, proposals.CreateRequest{BaseVersionID: m.LatestVersion.ID, Mode: "scenario",
			Intent: "Later " + ids.New("k"), OutputLocale: "en"}, proposals.FromIntent)
		generating = a.ProposalID
		return err
	}); err != nil {
		t.Fatal(err)
	}
	_, err = e.save(inv, generating)
	refused(t, "a proposal still generating", err, investigator.ProposalNotReady, "/proposal_id")
	e.generated(t, generating) // the fake's safe needs_input: still not ready
	_, err = e.save(inv, generating)
	refused(t, "a proposal that needs input", err, investigator.ProposalNotReady, "/proposal_id")
	nothing("a proposal not ready", 0)

	// A person approves the AI's ready what-if first (TEST-008): the
	// scenario is the person's, unlabelled; the AI's save is refused.
	p := e.propose(t, inv, "Raise it", raiseCI)
	approve := approval.ApproveRequest{ExpectedBaseVersionID: m.LatestVersion.ID}
	abody, _ := json.Marshal(approve)
	if status, _, err := e.decide.Approve(context.Background(), e.scope(tenant.Owner), ids.New("req"), "key-"+ids.New("k"), p.ProposalID, abody, approve); err != nil || status != 201 {
		t.Fatalf("a person approves the AI's proposal: %d %v", status, err)
	}
	approved := e.get(t, p.ProposalID)
	scn, err := e.scens.Get(context.Background(), e.scope(tenant.Viewer), *approved.ScenarioID)
	if err != nil || scn.MadeByAI != nil || scn.CreatedBy != e.org.Members[tenant.Owner].UserID || approved.Investigation == nil {
		t.Errorf("the person's approval of the AI's proposal: %+v %+v %v", approved, scn, err)
	}
	unmarked(t, e.only(t, p.ProposalID, "proposal.approve"))
	_, err = e.save(inv, p.ProposalID)
	refused(t, "a proposal a person approved", err, investigator.ProposalDecided, "/proposal_id")
	// ... or rejects it first.
	q := e.propose(t, inv, "Raise QA", moreQA)
	reject := approval.RejectRequest{}
	rbody, _ := json.Marshal(reject)
	if status, _, err := e.decide.Reject(context.Background(), e.scope(tenant.Editor), ids.New("req"), "key-"+ids.New("k"), q.ProposalID, rbody, reject); err != nil || status != 200 {
		t.Fatalf("reject: %d %v", status, err)
	}
	_, err = e.save(inv, q.ProposalID)
	refused(t, "a proposal a person rejected", err, investigator.ProposalDecided, "/proposal_id")
	nothing("decided by a person", 1)
	// The investigation's what-ifs are used up (3): a 4th is refused by the
	// database, by the cap's name.
	err = e.acting(inv, func(ctx context.Context, tx tenant.Tx, mb tenant.Membership) error {
		_, err := e.props.Queue(ctx, tx, mb, ids.New("req"), m.ID, proposals.CreateRequest{BaseVersionID: m.LatestVersion.ID, Mode: "scenario",
			Intent: "A fourth " + ids.New("k"), OutputLocale: "en"}, proposals.FromIntent)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "ai_proposals_investigation_cap") {
		t.Errorf("a 4th what-if: %v", err)
	}

	// A second investigation: the base moves under its ready what-if.
	inv2 := e.restart(t, inv, m.ID, m.LatestVersion.ID)
	stale := e.propose(t, inv2, "Raise it", raiseCI)
	// The server's check refuses another ready what-if now: a pack that
	// has learned a rule since its review.
	strict := e.propose(t, inv2, "Raise it more", raiseCI)
	strictEffects := &effect.Service{Packs: strictPacks{e.reg}, Objects: e.effects.Objects}
	err = e.acting(inv2, func(ctx context.Context, tx tenant.Tx, mb tenant.Membership) error {
		_, err := strictEffects.SaveScenario(ctx, tx, mb, ids.New("req"), strict.ProposalID)
		return err
	})
	var r *effect.Refusal
	if refused(t, "a proposal the check refuses now", err, investigator.RecheckFailed, "/proposal_id"); errors.As(err, &r) {
		// The refusal is the contract's ToolError and nothing more: the
		// issue codes have no place in it (H-061 §6.4's gap, recorded for
		// the owner; A4 review round 1).
		matches(t, "investigator.schema.json#/$defs/ToolError", r)
	}
	// The cause is the rule learned since the review: the shared check,
	// the approval's own, finds it now.
	if codes := e.checkCodes(t, strictPacks{e.reg}, strict.ProposalID); !slices.Contains(codes, "DOMAIN_RULE_FAILED") {
		t.Errorf("the shared check's issue codes: %v", codes)
	}
	if got := e.get(t, strict.ProposalID); got.Status != "ready" {
		t.Errorf("after the check refused it: %s", got.Status)
	}
	e.publishAnEdit(t, m.ID)
	_, err = e.save(inv2, stale.ProposalID)
	refused(t, "a proposal whose base moved", err, investigator.BaseMoved, "/proposal_id")
	if got := e.get(t, stale.ProposalID); got.Status != "ready" {
		t.Errorf("the AI made a proposal %s; it never decides", got.Status)
	}
	nothing("refused", 1)

	// Not the AI's to react to: outside the scope, a demoted requester, an
	// investigation stopped first.
	err = e.db.Run(context.Background(), e.scope(tenant.Editor), tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx, mb tenant.Membership) error {
		_, err := e.effects.SaveScenario(ctx, tx, mb, ids.New("req"), stale.ProposalID)
		return err
	})
	if !errors.Is(err, effect.ErrNotActing) {
		t.Errorf("outside the scope: %v", err)
	}
	e.setRole(t, e.editor(), tenant.Viewer)
	if _, err := e.save(inv2, stale.ProposalID); !errors.Is(err, tenant.ErrForbidden) {
		t.Errorf("a requester demoted to viewer: %v", err)
	}
	e.setRole(t, e.editor(), tenant.Editor)
	e.stop(t, inv2)
	if _, err := e.save(inv2, stale.ProposalID); !errors.Is(err, effect.ErrEnded) {
		t.Errorf("after the requester's stop: %v", err)
	}
	nothing("not the AI's", 1)
}

// restart stops inv and starts another of the editor's, on base.
func (e *env) restart(t *testing.T, inv investigations.Investigation, modelID, base string) investigations.Investigation {
	t.Helper()
	e.stop(t, inv)
	return e.start(t, inv.Mode, modelID, base)
}

// publishAnEdit makes a person's change to the model's draft and publishes
// it as the next version, so the model's latest moves.
func (e *env) publishAnEdit(t *testing.T, modelID string) models.Version {
	t.Helper()
	d, err := e.models.GetDraft(context.Background(), e.scope(tenant.Editor), modelID)
	if err != nil {
		t.Fatal(err)
	}
	d, err = e.models.PatchDraft(context.Background(), e.scope(tenant.Editor), ids.New("req"), modelID, d.Revision, []byte(`[{"op":"replace","path":"/name","value":"Moved"}]`))
	if err != nil {
		t.Fatal(err)
	}
	return e.publish(t, modelID, d)
}

func (e *env) publish(t *testing.T, modelID string, d models.Draft) models.Version {
	t.Helper()
	req := models.PublishRequest{ExpectedBaseVersionID: d.BaseVersionID}
	body, _ := json.Marshal(req)
	status, out, err := e.models.Publish(context.Background(), e.scope(tenant.Editor), ids.New("req"), "key-"+ids.New("k"), modelID, d.Revision, body, req)
	if err != nil || status != 201 {
		t.Fatalf("publish: %d %v", status, err)
	}
	var v models.Version
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatal(err)
	}
	matches(t, "http.schema.json#/$defs/ModelVersion", v)
	return v
}

// setRole changes user's role in the organization (the controlled
// membership path, as the schema owner).
func (e *env) setRole(t *testing.T, user string, role tenant.Role) {
	t.Helper()
	e.owner(t, e.org.ID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE twin.memberships SET role=$3 WHERE organization_id=$1 AND user_id=$2`, e.org.ID, user, string(role))
		return err
	})
}

// checkCodes are the codes of the issues the shared check (effect.Check,
// the approval's own) finds in proposal id now, with the domain packs of
// packs: what a person's approval would answer in its 422.
func (e *env) checkCodes(t *testing.T, packs effect.PackLookup, id string) []string {
	t.Helper()
	var codes []string
	err := e.db.Run(context.Background(), e.scope(tenant.Viewer), tenant.ReadOnly, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		p, err := store.Get(ctx, tx, m.OrganizationID, id)
		if err != nil {
			return err
		}
		base, err := modelstore.GetVersion(ctx, tx, m.OrganizationID, p.BaseVersionID)
		if err != nil {
			return err
		}
		pack, err := packs.Lookup("software_delivery")
		if err != nil {
			return err
		}
		_, err = effect.Check(ctx, tx, m.OrganizationID, e.effects.Objects, pack, p, base)
		var invalid *effect.InvalidError
		if !errors.As(err, &invalid) {
			return fmt.Errorf("the shared check passes or fails otherwise: %v", err)
		}
		for _, is := range invalid.Issues {
			codes = append(codes, is.Code)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return codes
}

// strictPacks is the registry with a rule learned since the review: a CI
// runner capacity above 4 is an error.
type strictPacks struct{ reg *domainpack.Registry }

func (s strictPacks) Lookup(domain string) (domainpack.DomainPack, error) {
	p, err := s.reg.Lookup(domain)
	return strictPack{p}, err
}

type strictPack struct{ domainpack.DomainPack }

func (s strictPack) Validate(ir *model.ModelIR) []model.ValidationIssue {
	issues := s.DomainPack.Validate(ir)
	for i, r := range ir.Resources {
		if r.ID == "ci_runner" && r.Capacity > 4 {
			issues = append(issues, model.NewIssue("DOMAIN_RULE_FAILED", "/resources/"+itoa(i)+"/capacity", "the CI runners are at most 4", model.SeverityError))
		}
	}
	return issues
}

func itoa(i int) string { return string(rune('0' + i)) }

// The AI's edit, end to end: written into the clean draft (labelled with
// the proposal, its investigation, the revision and the operation count;
// model.draft.update and proposal.apply marked, the requester the actor),
// the proposal applied at that revision, no version; a person's later
// write keeps the label, a person's publish carries it into the version
// ("Includes a change the AI wrote", contains_ai_edit) and the draft that
// follows no longer names it; a reset clears it too.
func TestTheAIsEditThroughTheSharedCode(t *testing.T) {
	e := setup(t)
	m := e.model(t, e.scope(tenant.Editor), e.project)
	inv := e.start(t, investigations.Edit, m.ID, m.LatestVersion.ID)
	p := e.propose(t, inv, "Add a CI runner", raiseCI)
	if p.Status != "ready" || p.Mode != "edit" || p.Investigation == nil || p.Investigation.Index != 1 {
		t.Fatalf("the AI's edit proposal: %+v", p)
	}
	written, err := e.write(inv, p.ProposalID)
	if err != nil {
		t.Fatalf("write_to_draft: %v", err)
	}
	if written.DraftRevision != 2 || written.OperationCount != 2 {
		t.Errorf("written %+v", written)
	}
	d, err := e.models.GetDraft(context.Background(), e.scope(tenant.Viewer), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	matches(t, "http.schema.json#/$defs/Draft", d)
	if a := d.AIEdit; a == nil || a.ProposalID != p.ProposalID || a.InvestigationID != inv.ID || a.Revision != 2 || a.Operations != 2 || d.Revision != 2 {
		t.Fatalf("the draft after the AI's edit: %+v %+v", d, d.AIEdit)
	}
	var ir model.ModelIR
	if err := model.Decode(d.IR, &ir); err != nil || ir.Resources[1].ID != "ci_runner" || ir.Resources[1].Capacity != 6 {
		t.Errorf("the draft does not hold the edit: %v", err)
	}
	applied := e.get(t, p.ProposalID)
	if applied.Status != "applied" || applied.AppliedDraftRevision == nil || *applied.AppliedDraftRevision != 2 || applied.ScenarioID != nil ||
		applied.NewVersionID != nil || applied.DecidedBy != nil {
		t.Errorf("applied: %+v", applied)
	}
	markedFor(t, e.only(t, p.ProposalID, "proposal.apply"), inv)
	if ev := e.only(t, p.ProposalID, "proposal.apply"); ev.meta["draft_revision"] != float64(2) || ev.meta["scenario_id"] != nil {
		t.Errorf("proposal.apply: %v", ev.meta)
	}
	updates := e.events(t, m.ID)
	if last := updates[len(updates)-1]; last.action != "model.draft.update" {
		t.Errorf("the last event about the model: %s", last.action)
	} else {
		markedFor(t, last, inv)
	}
	if n := e.count(t, `SELECT count(*) FROM twin.model_versions WHERE model_id=$1`, m.ID); n != 1 {
		t.Errorf("%d versions after the AI's edit", n)
	}
	// Written once: the proposal is applied, and the draft is not clean.
	_, err = e.write(inv, p.ProposalID)
	refused(t, "writing it twice", err, investigator.ProposalNotReady, "/proposal_id")

	// A person's next write keeps the label (unmarked).
	d, err = e.models.PatchDraft(context.Background(), e.scope(tenant.Editor), ids.New("req"), m.ID, d.Revision, []byte(`[{"op":"replace","path":"/name","value":"Checked"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if a := d.AIEdit; a == nil || a.Revision != 2 || d.Revision != 3 {
		t.Errorf("after a person's write: %+v %+v", d, d.AIEdit)
	}
	updates = e.events(t, m.ID)
	unmarked(t, updates[len(updates)-1])
	// A person publishes it: the version names the AI's edit; the draft
	// that follows does not.
	v := e.publish(t, m.ID, d)
	if v.AIEdit == nil || v.AIEdit.ProposalID != p.ProposalID || v.AIEdit.InvestigationID != inv.ID {
		t.Errorf("the published version: %+v", v.AIEdit)
	}
	if got, err := e.models.GetVersion(context.Background(), e.scope(tenant.Viewer), v.ID); err != nil || got.AIEdit == nil || got.AIEdit.ProposalID != p.ProposalID {
		t.Errorf("the version read back: %+v %v", got.AIEdit, err)
	}
	publish := e.only(t, v.ID, "model.publish")
	unmarked(t, publish)
	if publish.meta["contains_ai_edit"] != true || publish.actorID != e.editor() {
		t.Errorf("model.publish: %v", publish.meta)
	}
	if after, err := e.models.GetDraft(context.Background(), e.scope(tenant.Viewer), m.ID); err != nil || after.AIEdit != nil {
		t.Errorf("the draft after the publish: %+v %v", after.AIEdit, err)
	}
	// A version published without the AI's edit names none.
	plain := e.publishAnEdit(t, m.ID)
	if plain.AIEdit != nil {
		t.Errorf("a person's version: %+v", plain.AIEdit)
	}
	if ev := e.only(t, plain.ID, "model.publish"); ev.meta["contains_ai_edit"] != nil {
		t.Errorf("a person's publish: %v", ev.meta)
	}

	// Another edit investigation on the latest: written, then reset.
	m2 := e.model(t, e.scope(tenant.Editor), e.project)
	inv2 := e.restart(t, inv, m2.ID, m2.LatestVersion.ID)
	p2 := e.propose(t, inv2, "Add a tester", moreQA)
	if _, err := e.write(inv2, p2.ProposalID); err != nil {
		t.Fatal(err)
	}
	d2, err := e.models.GetDraft(context.Background(), e.scope(tenant.Editor), m2.ID)
	if err != nil || d2.AIEdit == nil {
		t.Fatalf("%+v %v", d2, err)
	}
	reset, err := e.models.ResetDraft(context.Background(), e.scope(tenant.Editor), ids.New("req"), m2.ID, d2.Revision,
		models.ResetRequest{ExpectedLatestVersionID: m2.LatestVersion.ID, ConfirmDiscard: true})
	if err != nil || reset.AIEdit != nil || reset.Hash == d2.Hash {
		t.Errorf("after a reset: %+v %v", reset.AIEdit, err)
	}
	// The proposal stays applied: what the AI did is recorded.
	if got := e.get(t, p2.ProposalID); got.Status != "applied" || got.AppliedDraftRevision == nil {
		t.Errorf("after the reset: %+v", got)
	}
}

// What write_to_draft refuses, with nothing written: a draft with a
// person's change (DRAFT_CHANGED), a base that moved (BASE_MOVED), a
// scenario's tool on an edit (MODE_MISMATCH); and Approve and Reject
// refuse an applied proposal (409 LIFECYCLE_CONFLICT: applied is final).
func TestWriteDraftRefusals(t *testing.T) {
	e := setup(t)
	m := e.model(t, e.scope(tenant.Editor), e.project)
	inv := e.start(t, investigations.Edit, m.ID, m.LatestVersion.ID)
	p := e.propose(t, inv, "Add a CI runner", raiseCI)
	_, err := e.save(inv, p.ProposalID)
	refused(t, "save_scenario on an edit", err, investigator.ModeMismatch, "")
	// A person's change in the draft first.
	d, err := e.models.GetDraft(context.Background(), e.scope(tenant.Editor), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	dirty, err := e.models.PatchDraft(context.Background(), e.scope(tenant.Editor), ids.New("req"), m.ID, d.Revision, []byte(`[{"op":"replace","path":"/name","value":"Mine"}]`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.write(inv, p.ProposalID)
	refused(t, "a draft with a person's change", err, investigator.DraftChanged, "")
	if now, _ := e.models.GetDraft(context.Background(), e.scope(tenant.Viewer), m.ID); now.Revision != dirty.Revision || now.Hash != dirty.Hash || now.AIEdit != nil {
		t.Errorf("the refused write changed the draft: %+v", now)
	}
	if got := e.get(t, p.ProposalID); got.Status != "ready" {
		t.Errorf("after the refusal: %s", got.Status)
	}
	// The person publishes their change: the base moved.
	e.publish(t, m.ID, dirty)
	_, err = e.write(inv, p.ProposalID)
	refused(t, "a base that moved", err, investigator.BaseMoved, "/proposal_id")
	if n := e.count(t, `SELECT count(*) FROM twin.audit_events WHERE organization_id=$1 AND action='proposal.apply'`, e.org.ID); n != 0 {
		t.Errorf("%d proposal.apply events", n)
	}

	// Approve and Reject refuse an applied proposal.
	m2 := e.model(t, e.scope(tenant.Editor), e.project)
	inv2 := e.restart(t, inv, m2.ID, m2.LatestVersion.ID)
	p2 := e.propose(t, inv2, "Add a CI runner", raiseCI)
	if _, err := e.write(inv2, p2.ProposalID); err != nil {
		t.Fatal(err)
	}
	approve := approval.ApproveRequest{ExpectedBaseVersionID: m2.LatestVersion.ID}
	abody, _ := json.Marshal(approve)
	_, _, err = e.decide.Approve(context.Background(), e.scope(tenant.Owner), ids.New("req"), "key-"+ids.New("k"), p2.ProposalID, abody, approve)
	var state *approval.StateError
	var he *httpapi.Error
	if !errors.As(err, &state) || state.Status != "applied" || !errors.As(approval.Refused(err), &he) || he.Code != httpapi.LifecycleConflict {
		t.Errorf("approving an applied proposal: %v", err)
	}
	reject := approval.RejectRequest{}
	rbody, _ := json.Marshal(reject)
	_, _, err = e.decide.Reject(context.Background(), e.scope(tenant.Owner), ids.New("req"), "key-"+ids.New("k"), p2.ProposalID, rbody, reject)
	if !errors.As(err, &state) || state.Status != "applied" {
		t.Errorf("rejecting an applied proposal: %v", err)
	}
	if got := e.get(t, p2.ProposalID); got.Status != "applied" || got.DecidedBy != nil {
		t.Errorf("after the refused decisions: %+v", got)
	}
	if n := e.count(t, `SELECT count(*) FROM twin.model_versions WHERE model_id=$1`, m2.ID); n != 1 {
		t.Errorf("%d versions: an applied proposal was approved", n)
	}
}

// A person's draft write and the AI's race for the same clean draft: both
// take the model row first, so one waits for the other, and exactly one
// writes: the AI's, after which the person's If-Match revision is stale
// (409), or the person's, after which the draft is no longer clean
// (DRAFT_CHANGED). Never both, never a lost update (TEST-005). Each
// iteration gives one side a head start, so both orders are seen.
func TestThePersonAndTheAIRaceForTheDraft(t *testing.T) {
	e := setup(t)
	outcomes := map[string]int{}
	for i := range 6 {
		m := e.model(t, e.scope(tenant.Editor), e.project)
		inv := e.restartOn(t, m)
		p := e.propose(t, inv, "Add a CI runner", raiseCI)
		d, err := e.models.GetDraft(context.Background(), e.scope(tenant.Editor), m.ID)
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		var aiErr, personErr error
		start := make(chan struct{})
		// Odd iterations: the person starts 200 ms later; even ones: the AI.
		// The iterations with no head start for either are 0 and 1 too:
		// whichever takes the model row first wins.
		late := func(side int) {
			if i >= 2 && i%2 == side {
				time.Sleep(200 * time.Millisecond)
			}
		}
		wg.Go(func() { <-start; late(0); _, aiErr = e.write(inv, p.ProposalID) })
		wg.Go(func() {
			<-start
			late(1)
			_, personErr = e.models.PatchDraft(context.Background(), e.scope(tenant.Editor), ids.New("req"), m.ID, d.Revision,
				[]byte(`[{"op":"replace","path":"/name","value":"Mine"}]`))
		})
		close(start)
		wg.Wait()
		after, err := e.models.GetDraft(context.Background(), e.scope(tenant.Viewer), m.ID)
		if err != nil {
			t.Fatal(err)
		}
		var r *effect.Refusal
		switch {
		case aiErr == nil && errors.Is(personErr, models.ErrConflict):
			if after.AIEdit == nil || after.Revision != 2 {
				t.Errorf("the AI wrote first, then: %+v", after)
			}
			outcomes["ai"]++
		case personErr == nil && errors.As(aiErr, &r) && r.Code == investigator.DraftChanged:
			if after.AIEdit != nil || after.Revision != 2 || e.get(t, p.ProposalID).Status != "ready" {
				t.Errorf("the person wrote first, then: %+v", after)
			}
			outcomes["person"]++
		default:
			t.Errorf("race %d: the AI %v, the person %v", i, aiErr, personErr)
		}
	}
	if outcomes["ai"] == 0 || outcomes["person"] == 0 {
		t.Errorf("first writer: %v; with a head start each side should have written first at least once", outcomes)
	}
	t.Logf("first writer: %v", outcomes)
}

// restartOn stops the editor's running investigation and starts an edit
// one on m.
func (e *env) restartOn(t *testing.T, m models.Model) investigations.Investigation {
	t.Helper()
	if err := e.db.Run(context.Background(), e.scope(tenant.Editor), tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx, _ tenant.Membership) error {
		inv, ok, err := investigations.RunningOf(ctx, tx, e.org.ID, e.editor())
		if err != nil || !ok {
			return err
		}
		return investigations.Stop(ctx, tx, e.org.ID, inv.ID, e.editor())
	}); err != nil {
		t.Fatal(err)
	}
	return e.start(t, investigations.Edit, m.ID, m.LatestVersion.ID)
}
