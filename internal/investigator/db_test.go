package investigator

// DB-backed tests of the investigation job (H-061 §5, slice A6) through the
// real logins: the worker loop (internal/worker) runs the handler over the
// worker scope, and a stand-in for the API's executor resolves the calls
// in the investigation's acting-for scope, as A7's will. Without
// GEMINUS_TEST_DATABASE_URL they skip (NOT RUN); make db-go-test sets
// GEMINUS_REQUIRE_DB_TESTS=1, so there a skip is a failure.

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kcl1104/geminus/internal/ai/idpool"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/packs"
	"github.com/kcl1104/geminus/internal/proposals/generator"
	"github.com/kcl1104/geminus/internal/storage/dbtest"
	inv "github.com/kcl1104/geminus/internal/storage/investigations"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/storage/postgres"
	"github.com/kcl1104/geminus/internal/storage/proposals"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/tenant/workerscope"
	"github.com/kcl1104/geminus/internal/worker"
	"github.com/kcl1104/geminus/internal/worker/handler"
)

const dbIntent = "Find a change that shortens the delivery lead time."

var dbPolicy = jobs.Policy{Lease: 30 * time.Second, MaxAttempts: 5, BackoffBase: time.Millisecond, BackoffMax: time.Millisecond}

type dbEnv struct {
	admin  *pgxpool.Pool
	api    *tenant.DB
	scope  *workerscope.Worker
	org    dbtest.Org
	model  string
	base   string
	editor string
	owner  string
	idPool []byte
	f      *fake.Backend
}

func dbSetup(t *testing.T) *dbEnv {
	t.Helper()
	admin := dbtest.Admin(t)
	e := &dbEnv{admin: admin, org: dbtest.NewOrg(t, admin, "https://test.invalid", tenant.Owner, tenant.Editor)}
	ap := dbtest.Pool(t, dbtest.LoginURL(t, dbtest.APILogin), 4, true)
	if err := tenant.CheckRuntimeRole(context.Background(), ap, tenant.APIRole); err != nil {
		t.Fatal(err)
	}
	e.api = tenant.New(ap)
	wp := dbtest.Pool(t, dbtest.LoginURL(t, dbtest.WorkerLogin), 4, true)
	if err := tenant.CheckRuntimeRole(context.Background(), wp, tenant.WorkerRole); err != nil {
		t.Fatal(err)
	}
	w, err := workerscope.New(wp, []string{e.org.ID})
	if err != nil {
		t.Fatal(err)
	}
	e.scope = w
	_, e.model, e.base = dbtest.NewModel(t, admin, e.org)
	e.editor, e.owner = e.org.Members[tenant.Editor].UserID, e.org.Members[tenant.Owner].UserID
	var stored string
	if err := admin.QueryRow(context.Background(), `SELECT ir FROM twin.model_versions WHERE id=$1`, e.base).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	var ir model.ModelIR
	if err := model.Decode([]byte(stored), &ir); err != nil {
		t.Fatal(err)
	}
	p, err := idpool.New(&ir)
	if err != nil {
		t.Fatal(err)
	}
	if e.idPool, err = json.Marshal(p); err != nil {
		t.Fatal(err)
	}
	e.f = fake.New()
	return e
}

// start stores a scenario investigation of the editor with its job, as
// createInvestigation does.
func (e *dbEnv) start(t *testing.T) inv.Investigation {
	t.Helper()
	i := inv.Investigation{ID: ids.New("inv"), OrganizationID: e.org.ID, ModelID: e.model, BaseVersionID: e.base, Mode: inv.Scenario,
		Source: inv.FromRequest, Intent: ptr(dbIntent), Context: []byte(`{}`), OutputLocale: "en", ProposalModel: "muse-spark-1.3",
		PlannerModel: "muse-spark-1.3", PromptTemplateVersion: "investigator-0.1.0",
		Objective:     []byte(`{"metric_path":"/metrics/lead_time_minutes/p50","better":"lower"}`),
		RunConfig:     []byte(`{"horizon":{"value":7,"unit":"day"},"warmup":{"value":0,"unit":"day"},"replications":5,"seed":1,"trace_level":"sampled"}`),
		RunConfigHash: ptr("sha256:1111111111111111111111111111111111111111111111111111111111111111"), BaselineName: ptr("Baseline"),
		Caps: inv.CapsFor(inv.Scenario, false), CreatedBy: e.editor}
	var out inv.Investigation
	if err := e.api.Run(context.Background(), tenant.Scope{OrganizationID: e.org.ID, UserID: e.editor}, tenant.ReadWrite,
		func(ctx context.Context, tx tenant.Tx, _ tenant.Membership) error {
			var err error
			if out, err = inv.Insert(ctx, tx, i); err != nil {
				return err
			}
			j, err := jobs.Enqueue(ctx, tx, jobs.New{ID: ids.New("job"), OrganizationID: e.org.ID, Type: jobs.Investigation,
				Resource: jobs.Resource{InvestigationID: i.ID}, Payload: map[string]any{"investigation_id": i.ID, "request_id": ids.New("req")},
				MaxAttempts: inv.MaxAttempts}, dbPolicy)
			out.JobID = j.ID
			return err
		}); err != nil {
		t.Fatal(err)
	}
	return out
}

func (e *dbEnv) handler(t *testing.T) *Handler {
	t.Helper()
	registry, err := packs.Registry()
	if err != nil {
		t.Fatal(err)
	}
	client, err := provider.New(e.f, provider.DefaultBudget())
	if err != nil {
		t.Fatal(err)
	}
	h, err := New(Config{Planner: client, Packs: registry, Actor: "worker-test:agent1", Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Poll: 10 * time.Millisecond, ChildPoll: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func (e *dbEnv) loop(t *testing.T, hs map[jobs.Type]handler.Handler) *worker.Worker {
	t.Helper()
	w, err := worker.New(e.scope, worker.Config{ID: "worker-test:agent1", Policy: dbPolicy, Heartbeat: 5 * time.Second, Idle: 10 * time.Millisecond,
		ReapLimit: 10, Handlers: hs, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// acting runs fn in the investigation's acting-for scope (the executor's).
func (e *dbEnv) acting(i inv.Investigation, fn func(ctx context.Context, tx tenant.Tx) error) error {
	return e.api.Run(context.Background(), tenant.Scope{OrganizationID: i.OrganizationID, UserID: i.CreatedBy, ActingFor: i.ID},
		tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx, _ tenant.Membership) error { return fn(ctx, tx) })
}

// executor resolves the investigation's pending calls with carry until
// stop is closed.
func (e *dbEnv) executor(t *testing.T, i inv.Investigation, carry func(ctx context.Context, tx tenant.Tx, c inv.Call) inv.Resolution,
	stop <-chan struct{}) *sync.WaitGroup {
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Millisecond):
			}
			_ = e.acting(i, func(ctx context.Context, tx tenant.Tx) error {
				calls, err := inv.Calls(ctx, tx, i.OrganizationID, i.ID)
				if err != nil {
					return err
				}
				for _, c := range calls {
					if c.Status != inv.Pending {
						continue
					}
					claimed, ok, err := inv.ClaimCall(ctx, tx, i.OrganizationID, i.ID, c.Seq)
					if err != nil || !ok {
						return err
					}
					return inv.Resolve(ctx, tx, i.OrganizationID, i.ID, claimed.Seq, carry(ctx, tx, claimed))
				}
				return nil
			})
		}
	})
	return &wg
}

// runUntil runs the loop's passes until done, for at most a minute (a
// pass that would wait longer is stopped, as at a SIGTERM).
func runUntil(t *testing.T, ctx context.Context, w *worker.Worker, done func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	for !done() {
		if ctx.Err() != nil {
			t.Fatal("the job did not end")
		}
		w.Pass(ctx)
	}
}

func (e *dbEnv) state(t *testing.T, id string) (inv.Investigation, []inv.Call, string) {
	t.Helper()
	var i inv.Investigation
	var calls []inv.Call
	var job string
	if err := postgres.OpenPool(e.admin).Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if i, err = inv.Get(ctx, tx, e.org.ID, id); err != nil {
			return err
		}
		if calls, err = inv.Calls(ctx, tx, e.org.ID, id); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT status FROM twin.jobs WHERE organization_id=$1 AND investigation_id=$2`, e.org.ID, id).Scan(&job)
	}); err != nil {
		t.Fatal(err)
	}
	return i, calls, job
}

func toolTurn(id, name, input string) fake.ToolAnswer {
	return fake.ToolAnswer{Calls: []provider.ToolCall{{ID: id, Name: name, Input: jsontext.Value(input)}}}
}

// The job on the database: a refused call shown its error, a what-if the
// executor queued in the person's name and labelled, waited for until the
// proposal left generating, observed once; the planner's finish recorded
// with the end (finished_by_ai, its claim), the job completed in the same
// transaction, and one worker event investigation.end.
func TestTheJobOnTheDatabase(t *testing.T) {
	e := dbSetup(t)
	i := e.start(t)
	e.f.ScriptTools(provider.RoleInvestigator, dbIntent,
		toolTurn("toolu_01", "run_what_if", `{"scenario_id":"`+ids.New("scn")+`"}`),
		toolTurn("toolu_02", "propose_what_if", `{"idea":"Add a second reviewer.","from_suggestion":null}`),
		toolTurn("toolu_03", "finish", `{"outcome":"needs_person","best_scenario_id":null}`))
	proposal := ids.New("aip")
	stop := make(chan struct{})
	wg := e.executor(t, i, func(ctx context.Context, tx tenant.Tx, c inv.Call) inv.Resolution {
		if c.Tool != "propose_what_if" {
			return inv.Resolution{Status: inv.Refused, Error: &inv.ToolError{Code: "NOT_IN_INVESTIGATION", Path: "/scenario_id"}}
		}
		if _, err := proposals.Insert(ctx, tx, proposals.Proposal{ID: proposal, OrganizationID: e.org.ID, ModelID: e.model, BaseVersionID: e.base,
			Mode: "scenario", Intent: "Add a second reviewer.", OutputLocale: "en", IDPool: e.idPool, CreatedBy: e.editor}); err != nil {
			t.Error(err)
		}
		return inv.Resolution{Status: inv.Done, Effect: inv.Effect{ProposalID: &proposal}}
	}, stop)
	// The proposal's own job generates it a little later (the test as its
	// pipeline: a job, then the content, as the owner).
	go func() {
		for {
			time.Sleep(20 * time.Millisecond)
			var n int
			if err := e.admin.QueryRow(context.Background(), `SELECT count(*) FROM twin.ai_proposals WHERE id=$1`, proposal).Scan(&n); err != nil || n == 0 {
				continue
			}
			job := ids.New("job")
			if err := postgres.OpenPool(e.admin).Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
				for _, q := range []struct {
					sql  string
					args []any
				}{
					{`INSERT INTO twin.jobs(id,organization_id,type,status,payload,ai_proposal_id) VALUES ($1,$2,'model_proposal','queued','{}',$3)`,
						[]any{job, e.org.ID, proposal}},
					{`UPDATE twin.jobs SET status='running', attempt=1, lease_token=lease_token+1, locked_at=now(), locked_by='w',
						lease_expires_at=now()+interval '30 seconds' WHERE organization_id=$1 AND id=$2`, []any{e.org.ID, job}},
					{`UPDATE twin.ai_proposals SET status='ready', summary='A second reviewer.', patch='[{"op":"test","path":"/name","value":"M"}]',
						patch_hash='sha256:1111111111111111111111111111111111111111111111111111111111111111', validation='{"valid":true,"issues":[]}',
						confidence=0.9, server_risk_level='low', provider_risk_claim='low', risk_level='low' WHERE organization_id=$1 AND id=$2`,
						[]any{e.org.ID, proposal}},
					{`UPDATE twin.jobs SET status='completed', locked_at=NULL, locked_by=NULL, lease_expires_at=NULL WHERE organization_id=$1 AND id=$2`,
						[]any{e.org.ID, job}},
				} {
					if _, err := tx.Exec(ctx, q.sql, q.args...); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				t.Error(err)
			}
			return
		}
	}()
	w := e.loop(t, map[jobs.Type]handler.Handler{jobs.Investigation: e.handler(t)})
	runUntil(t, context.Background(), w, func() bool { _, _, job := e.state(t, i.ID); return job == "completed" })
	close(stop)
	wg.Wait()
	got, calls, _ := e.state(t, i.ID)
	if got.Status != inv.Finished || got.Reason == nil || *got.Reason != inv.FinishedByAI || got.AIOutcome == nil || *got.AIOutcome != "needs_person" {
		t.Fatalf("investigation %+v", got)
	}
	if len(calls) != 3 || calls[0].Status != inv.Refused || calls[1].Status != inv.Done || calls[2].Tool != "finish" || calls[2].Status != inv.Done {
		t.Fatalf("calls %+v", calls)
	}
	if o0, o1 := string(canonical(calls[0].Observation)), string(canonical(calls[1].Observation)); !strings.Contains(o0, `"NOT_IN_INVESTIGATION"`) ||
		!strings.Contains(o1, `"status":"ready"`) || !strings.Contains(o1, `"index":1`) || calls[1].ObservationHash == nil ||
		*calls[1].ObservationHash != hashOf(canonical(calls[1].Observation)) || calls[2].Observation != nil {
		t.Fatalf("observations %s / %s", calls[0].Observation, calls[1].Observation)
	}
	var actor, reason string
	var steps, turns int
	if err := e.admin.QueryRow(context.Background(), `SELECT actor_id, metadata->>'reason', (metadata->>'steps')::int, (metadata->>'planner_turns')::int
		FROM twin.audit_events WHERE organization_id=$1 AND action='investigation.end' AND target_id=$2`, e.org.ID, i.ID).
		Scan(&actor, &reason, &steps, &turns); err != nil {
		t.Fatal(err)
	}
	if actor != "worker-test:agent1" || reason != "finished_by_ai" || steps != 3 || turns != 3 {
		t.Fatalf("investigation.end: %s %s %d %d", actor, reason, steps, turns)
	}
}

// A stopping worker (SIGTERM) leaves the pending call recorded and ends its
// attempt; the next attempt waits for that call (never records it again)
// and asks the planner the next step only.
func TestAResumeSendsNoCallTwice(t *testing.T) {
	e := dbSetup(t)
	i := e.start(t)
	e.f.ScriptTools(provider.RoleInvestigator, dbIntent,
		toolTurn("toolu_01", "run_what_if", `{"scenario_id":"`+ids.New("scn")+`"}`),
		toolTurn("toolu_02", "finish", `{"outcome":"out_of_ideas","best_scenario_id":null}`))
	w := e.loop(t, map[jobs.Type]handler.Handler{jobs.Investigation: e.handler(t)})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); w.Pass(ctx) }()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, calls, _ := e.state(t, i.ID); len(calls) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no call was recorded")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if got, calls, job := e.state(t, i.ID); got.Status != inv.Running || len(calls) != 1 || calls[0].Status != inv.Pending || job != "queued" {
		t.Fatalf("after the stop: %+v %+v %s", got, calls, job)
	}
	turnsBefore := len(e.f.Requests())
	stop := make(chan struct{})
	wg := e.executor(t, i, func(context.Context, tenant.Tx, inv.Call) inv.Resolution {
		return inv.Resolution{Status: inv.Refused, Error: &inv.ToolError{Code: "NOT_IN_INVESTIGATION", Path: "/scenario_id"}}
	}, stop)
	runUntil(t, context.Background(), w, func() bool { _, _, job := e.state(t, i.ID); return job == "completed" })
	close(stop)
	wg.Wait()
	got, calls, _ := e.state(t, i.ID)
	if got.Reason == nil || *got.Reason != inv.FinishedByAI || len(calls) != 2 || calls[0].ObservedAt == nil || calls[1].Tool != "finish" {
		t.Fatalf("after the resume: %+v %+v", got, calls)
	}
	if n := len(e.f.Requests()) - turnsBefore; n != 1 {
		t.Fatalf("the resume asked %d turns, want 1", n)
	}
}

// staleRun is a handler.Run over the worker scope whose job carries a
// lease token that is no longer the job's.
type staleRun struct {
	e   *dbEnv
	job jobs.Job
}

func (r staleRun) Job() jobs.Job { return r.job }
func (r staleRun) Checkpoint(ctx context.Context, fn func(context.Context, tenant.Tx) error) error {
	return r.e.scope.Run(ctx, r.job.OrganizationID, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error { return fn(ctx, tx) })
}
func (r staleRun) Finish(context.Context, jobs.Status, func(context.Context, tenant.Tx) error) error {
	return nil
}

// The lease fence: a call recorded under a token that is not the running
// job's current one is refused by the database, whatever the worker
// thinks it holds.
func TestAStaleLeaseCannotRecordACall(t *testing.T) {
	e := dbSetup(t)
	i := e.start(t)
	var job jobs.Job
	if err := e.scope.Run(context.Background(), e.org.ID, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		j, _, ok, err := jobs.Claim(ctx, tx, e.org.ID, "worker-test:agent1", []jobs.Type{jobs.Investigation}, dbPolicy)
		if err == nil && !ok {
			t.Fatal("no job claimed")
		}
		job = j
		return err
	}); err != nil {
		t.Fatal(err)
	}
	stale := job
	stale.LeaseToken--
	l := &dbLedger{run: staleRun{e: e, job: stale}, org: e.org.ID, id: i.ID}
	args := []byte(`{"from_suggestion":null,"idea":"Add a reviewer."}`)
	hash := hashOf(args)
	if err := l.Insert(context.Background(), inv.NewCall{Seq: 1, Tool: "propose_what_if", Arguments: args, ArgumentsHash: &hash, Status: inv.Pending}, nil); err == nil {
		t.Fatal("a stale lease recorded a call")
	}
	l.run = staleRun{e: e, job: job}
	if err := l.Insert(context.Background(), inv.NewCall{Seq: 1, Tool: "propose_what_if", Arguments: args, ArgumentsHash: &hash, Status: inv.Pending}, nil); err != nil {
		t.Fatalf("the current lease: %v", err)
	}
}

// H-061 §12: a proposal an investigation asked for fails fast with
// JOB_CANCELLED, before its paid call, once the investigation has ended.
func TestTheGeneratorFailsFastOnAnEndedInvestigation(t *testing.T) {
	e := dbSetup(t)
	i := e.start(t)
	proposal, pjob := ids.New("aip"), ids.New("job")
	// The executor's propose_what_if (A7): the labelled proposal and its job.
	if err := e.scope.Run(context.Background(), e.org.ID, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		_, _, _, err := jobs.Claim(ctx, tx, e.org.ID, "worker-test:agent1", []jobs.Type{jobs.Investigation}, dbPolicy)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var lease int64
	if err := e.admin.QueryRow(context.Background(), `SELECT id, lease_token FROM twin.jobs WHERE investigation_id=$1`, i.ID).Scan(&i.JobID, &lease); err != nil {
		t.Fatal(err)
	}
	args := []byte(`{"from_suggestion":null,"idea":"Add a reviewer."}`)
	if err := e.scope.Run(context.Background(), e.org.ID, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := inv.InsertCall(ctx, tx, inv.NewCall{OrganizationID: e.org.ID, InvestigationID: i.ID, Seq: 1, Tool: "propose_what_if",
			Arguments: args, ArgumentsHash: ptr(hashOf(args)), JobID: i.JobID, LeaseToken: lease, Status: inv.Pending})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	req := ids.New("req")
	if err := e.acting(i, func(ctx context.Context, tx tenant.Tx) error {
		if _, err := proposals.Insert(ctx, tx, proposals.Proposal{ID: proposal, OrganizationID: e.org.ID, ModelID: e.model, BaseVersionID: e.base,
			Mode: "scenario", Intent: "Add a reviewer.", OutputLocale: "en", IDPool: e.idPool, CreatedBy: e.editor}); err != nil {
			return err
		}
		if _, err := jobs.Enqueue(ctx, tx, jobs.New{ID: pjob, OrganizationID: e.org.ID, Type: jobs.ModelProposal,
			Resource: jobs.Resource{AIProposalID: proposal}, Payload: map[string]any{"proposal_id": proposal, "request_id": req}}, dbPolicy); err != nil {
			return err
		}
		return inv.Resolve(ctx, tx, e.org.ID, i.ID, 1, inv.Resolution{Status: inv.Done, Effect: inv.Effect{ProposalID: &proposal}})
	}); err != nil {
		t.Fatal(err)
	}
	// The worker's lease lapses; the requester stops the investigation.
	if _, err := e.admin.Exec(context.Background(), `UPDATE twin.jobs SET lease_expires_at=now()-interval '1 second' WHERE id=$1`, i.JobID); err != nil {
		t.Fatal(err)
	}
	if err := e.api.Run(context.Background(), tenant.Scope{OrganizationID: e.org.ID, UserID: e.editor}, tenant.ReadWrite,
		func(ctx context.Context, tx tenant.Tx, _ tenant.Membership) error {
			return inv.Stop(ctx, tx, e.org.ID, i.ID, e.editor)
		}); err != nil {
		t.Fatal(err)
	}
	registry, err := packs.Registry()
	if err != nil {
		t.Fatal(err)
	}
	client, err := provider.New(e.f, provider.DefaultBudget())
	if err != nil {
		t.Fatal(err)
	}
	g, err := generator.New(generator.Config{Provider: client, Budget: provider.DefaultBudget(), Packs: registry, Actor: "worker-test:rest",
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	w := e.loop(t, map[jobs.Type]handler.Handler{jobs.ModelProposal: g})
	runUntil(t, context.Background(), w, func() bool {
		var s string
		_ = e.admin.QueryRow(context.Background(), `SELECT status FROM twin.jobs WHERE id=$1`, pjob).Scan(&s)
		return s == "failed"
	})
	var status, code string
	if err := e.admin.QueryRow(context.Background(), `SELECT status, error->>'code' FROM twin.ai_proposals WHERE id=$1`, proposal).Scan(&status, &code); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || code != generator.JobCancelled || len(e.f.Requests()) != 0 {
		t.Fatalf("proposal %s %s, %d provider requests", status, code, len(e.f.Requests()))
	}
}
