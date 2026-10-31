package investigations_test

// DB-backed tests of an investigation and its ledger (H-061 A3; migration
// 0019) through the real logins: the API login in a member's scope (and in
// the investigation's acting-for scope), the worker login through the
// worker scope. Without GEMINUS_TEST_DATABASE_URL they skip (NOT RUN); make
// db-go-test sets GEMINUS_REQUIRE_DB_TESTS=1, so there a skip is a failure.

import (
	"context"
	"encoding/json/v2"
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kcl1104/geminus/internal/ai/idpool"
	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/storage/dbtest"
	"github.com/kcl1104/geminus/internal/storage/investigations"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/storage/postgres"
	"github.com/kcl1104/geminus/internal/storage/proposals"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/tenant/workerscope"
)

const issuer = "https://test.invalid"

var policy = jobs.Policy{Lease: time.Minute, MaxAttempts: 3, BackoffBase: time.Hour, BackoffMax: 4 * time.Hour}

type env struct {
	admin  *pgxpool.Pool
	api    *tenant.DB
	worker *workerscope.Worker
	a, b   dbtest.Org
	model  string
	base   string
	idPool []byte
	runCfg []byte
	bModel string
	bBase  string
	editor string
	viewer string
	owner  string
	ownerB string
}

func setup(t *testing.T) *env {
	t.Helper()
	admin := dbtest.Admin(t)
	e := &env{admin: admin, a: dbtest.NewOrg(t, admin, issuer, tenant.Owner, tenant.Editor, tenant.Viewer),
		b: dbtest.NewOrg(t, admin, issuer, tenant.Owner)}
	ap := dbtest.Pool(t, dbtest.LoginURL(t, dbtest.APILogin), 4, true)
	if err := tenant.CheckRuntimeRole(context.Background(), ap, tenant.APIRole); err != nil {
		t.Fatal(err)
	}
	e.api = tenant.New(ap)
	wp := dbtest.Pool(t, dbtest.LoginURL(t, dbtest.WorkerLogin), 4, true)
	if err := tenant.CheckRuntimeRole(context.Background(), wp, tenant.WorkerRole); err != nil {
		t.Fatal(err)
	}
	w, err := workerscope.New(wp, []string{e.a.ID, e.b.ID})
	if err != nil {
		t.Fatal(err)
	}
	e.worker = w
	_, e.model, e.base = dbtest.NewModel(t, admin, e.a)
	_, e.bModel, e.bBase = dbtest.NewModel(t, admin, e.b)
	e.owner, e.editor, e.viewer = e.a.Members[tenant.Owner].UserID, e.a.Members[tenant.Editor].UserID, e.a.Members[tenant.Viewer].UserID
	e.ownerB = e.b.Members[tenant.Owner].UserID
	// The ID pool of a proposal on the base (D-046).
	var ir model.ModelIR
	var stored string
	if err := admin.QueryRow(context.Background(), `SELECT ir FROM twin.model_versions WHERE id=$1`, e.base).Scan(&stored); err != nil {
		t.Fatal(err)
	}
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
	e.runCfg = []byte(`{"horizon":{"value":7,"unit":"day"},"warmup":{"value":0,"unit":"day"},"replications":5,"seed":1,"trace_level":"sampled"}`)
	return e
}

func ptr[T any](v T) *T { return &v }

// request is a scenario-mode investigation of model A for user.
func (e *env) request(user string) investigations.Investigation {
	return investigations.Investigation{ID: ids.New("inv"), OrganizationID: e.a.ID, ModelID: e.model, BaseVersionID: e.base,
		Mode: investigations.Scenario, Source: investigations.FromRequest, Intent: ptr("Cut the lead time at ICT"),
		Context: []byte(`{"selected_entity_ids":["ict"]}`), OutputLocale: "zh-Hant", Research: true, ProposalModel: "muse-spark-1.3",
		PlannerModel: "muse-spark-1.3", PromptTemplateVersion: "investigator-0.1.0",
		Objective: []byte(`{"metric_path":"/metrics/lead_time_minutes/p50","better":"lower"}`), RunConfig: e.runCfg,
		RunConfigHash: ptr("sha256:1111111111111111111111111111111111111111111111111111111111111111"),
		BaselineName:  ptr("Baseline"), Caps: investigations.CapsFor(investigations.Scenario, false), CreatedBy: user}
}

// start stores inv with its job, as createInvestigation does, in user's own
// transaction; it returns the stored investigation and the job.
func (e *env) start(t *testing.T, inv investigations.Investigation) (investigations.Investigation, error) {
	t.Helper()
	var out investigations.Investigation
	err := e.api.Run(context.Background(), tenant.Scope{OrganizationID: inv.OrganizationID, UserID: inv.CreatedBy}, tenant.ReadWrite,
		func(ctx context.Context, tx tenant.Tx, _ tenant.Membership) error {
			var err error
			if out, err = investigations.Insert(ctx, tx, inv); err != nil {
				return err
			}
			j, err := jobs.Enqueue(ctx, tx, jobs.New{ID: ids.New("job"), OrganizationID: inv.OrganizationID, Type: jobs.Investigation,
				Resource: jobs.Resource{InvestigationID: inv.ID}, Payload: map[string]any{"investigation_id": inv.ID, "request_id": ids.New("req")},
				MaxAttempts: investigations.MaxAttempts}, policy)
			out.JobID = j.ID
			return err
		})
	return out, err
}

func (e *env) read(t *testing.T, org, user, id string) investigations.Investigation {
	t.Helper()
	var inv investigations.Investigation
	if err := e.api.Run(context.Background(), tenant.Scope{OrganizationID: org, UserID: user}, tenant.ReadOnly,
		func(ctx context.Context, tx tenant.Tx, _ tenant.Membership) error {
			var err error
			inv, err = investigations.Get(ctx, tx, org, id)
			return err
		}); err != nil {
		t.Fatal(err)
	}
	return inv
}

// claim takes the investigation job on an agent lane.
func (e *env) claim(t *testing.T, org string) (jobs.Job, jobs.Lease) {
	t.Helper()
	var j jobs.Job
	var l jobs.Lease
	var ok bool
	if err := e.worker.Run(context.Background(), org, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		j, l, ok, err = jobs.Claim(ctx, tx, org, "worker-a3:agent1", []jobs.Type{jobs.Investigation}, policy)
		return err
	}); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	return j, l
}

func (e *env) asWorker(t *testing.T, org string, fn func(ctx context.Context, tx pgx.Tx) error) error {
	t.Helper()
	return e.worker.Run(context.Background(), org, pgx.TxOptions{}, fn)
}

// acting runs fn in inv's acting-for transaction for its requester.
func (e *env) acting(inv investigations.Investigation, fn func(ctx context.Context, tx tenant.Tx) error) error {
	return e.api.Run(context.Background(), tenant.Scope{OrganizationID: inv.OrganizationID, UserID: inv.CreatedBy, ActingFor: inv.ID},
		tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx, _ tenant.Membership) error { return fn(ctx, tx) })
}

// An investigation is stored as asked, running, with its 20 minutes and
// its job; it is read back by itself, as the person's running one and in
// its model's list, and only in its organization (the application filter
// holds without RLS too).
func TestStartAndRead(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	asked := e.request(e.editor)
	got, err := e.start(t, asked)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != investigations.Running || !got.DeadlineAt.Equal(got.CreatedAt.Add(20*time.Minute)) || got.JobID == "" {
		t.Errorf("stored %+v", got)
	}
	read := e.read(t, e.a.ID, e.viewer, asked.ID)
	if read.ID != asked.ID || read.Mode != investigations.Scenario || read.Source != investigations.FromRequest || *read.Intent != *asked.Intent ||
		read.OutputLocale != "zh-Hant" || !read.Research || read.ProposalModel != "muse-spark-1.3" || read.PlannerModel != "muse-spark-1.3" ||
		read.PromptTemplateVersion != "investigator-0.1.0" || read.Caps != investigations.CapsFor(investigations.Scenario, false) ||
		*read.RunConfigHash != *asked.RunConfigHash || *read.BaselineName != "Baseline" || read.BaselineRunID != nil || read.Reason != nil ||
		read.AIOutcome != nil || read.FinishedAt != nil || read.JobID != got.JobID || read.CreatedBy != e.editor ||
		!read.DeadlineAt.Equal(got.DeadlineAt) {
		t.Errorf("read %+v", read)
	}
	var cfg, obj, wantCfg map[string]any
	if json.Unmarshal(read.RunConfig, &cfg) != nil || json.Unmarshal(read.Objective, &obj) != nil || json.Unmarshal(e.runCfg, &wantCfg) != nil ||
		cfg["replications"] != wantCfg["replications"] || obj["better"] != "lower" {
		t.Errorf("run config %s, objective %s", read.RunConfig, read.Objective)
	}
	if err := e.api.Run(ctx, tenant.Scope{OrganizationID: e.a.ID, UserID: e.editor}, tenant.ReadOnly, func(ctx context.Context, tx tenant.Tx, _ tenant.Membership) error {
		inv, ok, err := investigations.RunningOf(ctx, tx, e.a.ID, e.editor)
		if err != nil || !ok || inv.ID != asked.ID {
			t.Errorf("running of the editor: %v %v %v", inv.ID, ok, err)
		}
		if _, ok, err := investigations.RunningOf(ctx, tx, e.a.ID, e.owner); err != nil || ok {
			t.Errorf("running of the owner: %v %v", ok, err)
		}
		list, err := investigations.List(ctx, tx, e.a.ID, e.model, nil, 10)
		if err != nil || len(list) != 1 || list[0].ID != asked.ID {
			t.Errorf("list: %+v %v", list, err)
		}
		after, err := investigations.List(ctx, tx, e.a.ID, e.model, &investigations.Position{CreatedAt: list[0].CreatedAt, ID: list[0].ID}, 10)
		if err != nil || len(after) != 0 {
			t.Errorf("after the last: %+v %v", after, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// The organization filter by itself: as the provisioning superuser.
	if err := postgres.OpenPool(e.admin).Tx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := investigations.Get(ctx, tx, e.b.ID, asked.ID); !errors.Is(err, investigations.ErrNotFound) {
			t.Errorf("a's investigation in b: %v", err)
		}
		if list, err := investigations.List(ctx, tx, e.b.ID, e.model, nil, 10); err != nil || len(list) != 0 {
			t.Errorf("a's model in b: %+v %v", list, err)
		}
		if calls, err := investigations.Calls(ctx, tx, e.b.ID, asked.ID); err != nil || len(calls) != 0 {
			t.Errorf("a's calls in b: %+v %v", calls, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Under RLS, b's owner sees none of it either.
	var n int
	if err := e.api.Run(ctx, tenant.Scope{OrganizationID: e.b.ID, UserID: e.ownerB}, tenant.ReadOnly, func(ctx context.Context, tx tenant.Tx, _ tenant.Membership) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM twin.investigations`).Scan(&n)
	}); err != nil || n != 0 {
		t.Errorf("b sees %d investigations (%v)", n, err)
	}
}

// One running investigation per person per organization (409), whoever
// races; a viewer starts none; an edit investigation has its own caps.
func TestOneRunningPerPerson(t *testing.T) {
	e := setup(t)
	results := make([]error, 4)
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() { _, results[i] = e.start(t, e.request(e.editor)) })
	}
	wg.Wait()
	ok := 0
	for _, err := range results {
		switch {
		case err == nil:
			ok++
		case !errors.Is(err, investigations.ErrRunning):
			t.Errorf("a racing start: %v", err)
		}
	}
	if ok != 1 {
		t.Errorf("%d of 4 racing starts of one person ran", ok)
	}
	if _, err := e.start(t, e.request(e.viewer)); !errors.Is(err, postgres.ErrPermission) {
		t.Errorf("a viewer's start: %v", err)
	}
	edit := e.request(e.owner)
	edit.Mode, edit.Objective, edit.RunConfig, edit.RunConfigHash, edit.BaselineName = investigations.Edit, nil, nil, nil, nil
	edit.Caps = investigations.CapsFor(investigations.Edit, false)
	if got, err := e.start(t, edit); err != nil || got.Caps.Runs != 0 || got.Caps.WhatIfs != 1 {
		t.Errorf("the owner's edit investigation: %+v %v", got.Caps, err)
	}
	// Another person's caps are the database's: anything else is refused.
	other := e.request(e.owner)
	other.Caps.Steps = 20
	if _, err := e.start(t, other); !errors.Is(err, postgres.ErrConstraint) {
		t.Errorf("an investigation with other caps: %v", err)
	}
}

// At most 10 investigations per organization per UTC day (429), counted
// under the organization's lock, ended ones included.
func TestDailyLimit(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	for i := range 10 {
		inv, err := e.start(t, e.request(e.owner))
		if err != nil {
			t.Fatalf("investigation %d: %v", i+1, err)
		}
		if err := e.api.Run(ctx, tenant.Scope{OrganizationID: e.a.ID, UserID: e.owner}, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx, _ tenant.Membership) error {
			if _, err := jobs.CancelQueued(ctx, tx, e.a.ID, inv.JobID); err != nil {
				return err
			}
			return investigations.Stop(ctx, tx, e.a.ID, inv.ID, e.owner)
		}); err != nil {
			t.Fatalf("stopping %d: %v", i+1, err)
		}
	}
	if _, err := e.start(t, e.request(e.editor)); !errors.Is(err, investigations.ErrDailyLimit) {
		t.Errorf("the 11th of the day: %v", err)
	}
	// Another organization's count is its own.
	inB := e.request(e.ownerB)
	inB.OrganizationID, inB.ModelID, inB.BaseVersionID = e.b.ID, e.bModel, e.bBase
	if _, err := e.start(t, inB); err != nil {
		t.Errorf("b's first of the day: %v", err)
	}
}

// The ledger round trip: the worker records a call under the job's lease;
// the executor claims it once (SKIP LOCKED) in the acting-for scope and
// records its result once; the worker records what the AI was shown, once;
// a snapshot reads it all with the database's clock.
func TestLedgerRoundTrip(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	inv, err := e.start(t, e.request(e.editor))
	if err != nil {
		t.Fatal(err)
	}
	_, lease := e.claim(t, e.a.ID)
	call := func(seq int, tool string, args string, status investigations.CallStatus, te *investigations.ToolError) (investigations.Call, error) {
		var c investigations.Call
		err := e.asWorker(t, e.a.ID, func(ctx context.Context, tx pgx.Tx) error {
			var a []byte
			var h *string
			if args != "" {
				a, h = []byte(args), ptr("sha256:2222222222222222222222222222222222222222222222222222222222222222")
			}
			var err error
			c, err = investigations.InsertCall(ctx, tx, investigations.NewCall{OrganizationID: e.a.ID, InvestigationID: inv.ID, Seq: seq, Tool: tool,
				Arguments: a, ArgumentsHash: h, Note: ptr("Testing the bottleneck first."), JobID: lease.JobID, LeaseToken: lease.Token,
				Status: status, Error: te})
			return err
		})
		return c, err
	}
	// Refusals first: a stale token, a skipped seq.
	stale := lease
	stale.Token++
	if err := e.asWorker(t, e.a.ID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := investigations.InsertCall(ctx, tx, investigations.NewCall{OrganizationID: e.a.ID, InvestigationID: inv.ID, Seq: 1,
			Tool: investigations.InvalidCall, JobID: stale.JobID, LeaseToken: stale.Token, Status: investigations.Refused,
			Error: &investigations.ToolError{Code: "UNKNOWN_TOOL", Path: ""}})
		return err
	}); !errors.Is(err, postgres.ErrImmutable) {
		t.Errorf("a stale lease: %v", err)
	}
	if _, err := call(2, "propose_what_if", `{"idea":"x","from_suggestion":null}`, investigations.Pending, nil); !errors.Is(err, postgres.ErrImmutable) {
		t.Errorf("a skipped seq: %v", err)
	}
	// Call 1: invalid output, refused at once.
	c1, err := call(1, investigations.InvalidCall, "", investigations.Refused, &investigations.ToolError{Code: "INVALID_ARGUMENTS", Path: "/idea"})
	if err != nil || c1.Status != investigations.Refused || c1.ResolvedAt == nil || c1.Arguments != nil || c1.Error.Path != "/idea" {
		t.Fatalf("call 1: %+v %v", c1, err)
	}
	if _, err := call(2, "propose_what_if", `{"idea":"x","from_suggestion":null}`, investigations.Pending, nil); !errors.Is(err, postgres.ErrImmutable) {
		t.Errorf("call 2 before call 1 was observed: %v", err)
	}
	observe := func(seq int) error {
		return e.asWorker(t, e.a.ID, func(ctx context.Context, tx pgx.Tx) error {
			return investigations.Observe(ctx, tx, e.a.ID, inv.ID, seq, []byte(`{"is_error":true}`),
				"sha256:3333333333333333333333333333333333333333333333333333333333333333")
		})
	}
	if err := observe(1); err != nil {
		t.Fatal(err)
	}
	if err := observe(1); !errors.Is(err, investigations.ErrObserved) {
		t.Errorf("observed twice: %v", err)
	}
	// Call 2: propose_what_if, pending; the executor carries it out once.
	c2, err := call(2, "propose_what_if", `{"idea":"Add a second tester at ICT","from_suggestion":null}`, investigations.Pending, nil)
	if err != nil || c2.Status != investigations.Pending || c2.ResolvedAt != nil {
		t.Fatalf("call 2: %+v %v", c2, err)
	}
	if err := observe(2); !errors.Is(err, investigations.ErrObserved) {
		t.Errorf("observed before it was carried out: %v", err)
	}
	// Carried out only in the acting-for scope, for the requester.
	if err := e.api.Run(ctx, tenant.Scope{OrganizationID: e.a.ID, UserID: e.editor}, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx, _ tenant.Membership) error {
		return investigations.Resolve(ctx, tx, e.a.ID, inv.ID, 2, investigations.Resolution{Status: investigations.Refused,
			Error: &investigations.ToolError{Code: "LIMIT_REACHED", Path: ""}})
	}); !errors.Is(err, postgres.ErrPermission) {
		t.Errorf("a call resolved outside its acting-for scope: %v", err)
	}
	if err := e.api.Run(ctx, tenant.Scope{OrganizationID: e.a.ID, UserID: e.owner, ActingFor: inv.ID}, tenant.ReadWrite,
		func(ctx context.Context, tx tenant.Tx, _ tenant.Membership) error {
			return investigations.Resolve(ctx, tx, e.a.ID, inv.ID, 2, investigations.Resolution{Status: investigations.Refused,
				Error: &investigations.ToolError{Code: "LIMIT_REACHED", Path: ""}})
		}); !errors.Is(err, postgres.ErrPermission) {
		t.Errorf("a call resolved for another person: %v", err)
	}
	// Two executors: the first holds the call, the second skips it.
	held, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	proposal := ids.New("aip")
	go func() {
		done <- e.acting(inv, func(ctx context.Context, tx tenant.Tx) error {
			c, ok, err := investigations.ClaimCall(ctx, tx, e.a.ID, inv.ID, 2)
			if err != nil || !ok || c.Tool != "propose_what_if" {
				return errors.Join(err, errors.New("the first executor could not claim the call"))
			}
			close(held)
			<-release
			if _, err := proposals.Insert(ctx, tx, proposals.Proposal{ID: proposal, OrganizationID: e.a.ID, ModelID: e.model, BaseVersionID: e.base,
				Mode: "scenario", Intent: "Add a second tester at ICT", OutputLocale: "en", IDPool: e.idPool, CreatedBy: e.editor}); err != nil {
				return err
			}
			return investigations.Resolve(ctx, tx, e.a.ID, inv.ID, 2, investigations.Resolution{Status: investigations.Done,
				Effect: investigations.Effect{ProposalID: &proposal}})
		})
	}()
	<-held
	if err := e.acting(inv, func(ctx context.Context, tx tenant.Tx) error {
		if _, ok, err := investigations.ClaimCall(ctx, tx, e.a.ID, inv.ID, 2); err != nil || ok {
			t.Errorf("the second executor claimed a held call: %v %v", ok, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := e.acting(inv, func(ctx context.Context, tx tenant.Tx) error {
		if _, ok, err := investigations.ClaimCall(ctx, tx, e.a.ID, inv.ID, 2); err != nil || ok {
			t.Errorf("a carried-out call was claimed again: %v %v", ok, err)
		}
		return investigations.Resolve(ctx, tx, e.a.ID, inv.ID, 2, investigations.Resolution{Status: investigations.Failed,
			Error: &investigations.ToolError{Code: "INTERNAL", Path: ""}})
	}); !errors.Is(err, investigations.ErrResolved) {
		t.Errorf("resolved twice: %v", err)
	}
	if err := observe(2); err != nil {
		t.Fatal(err)
	}
	// The proposal is the investigation's: labelled, what-if 1.
	var label string
	var index int
	if err := e.admin.QueryRow(ctx, `SELECT investigation_id, investigation_index FROM twin.ai_proposals WHERE id=$1`, proposal).Scan(&label, &index); err != nil ||
		label != inv.ID || index != 1 {
		t.Errorf("the proposal's label %q, index %d (%v)", label, index, err)
	}
	// The snapshot.
	if err := e.asWorker(t, e.a.ID, func(ctx context.Context, tx pgx.Tx) error {
		got, calls, now, err := investigations.Snapshot(ctx, tx, e.a.ID, inv.ID)
		if err != nil {
			return err
		}
		if got.ID != inv.ID || len(calls) != 2 || now.IsZero() || !now.Before(got.DeadlineAt) {
			t.Errorf("snapshot: %+v %d calls at %v", got, len(calls), now)
		}
		seqs := []int{}
		for _, c := range calls {
			seqs = append(seqs, c.Seq)
		}
		if !slices.Equal(seqs, []int{1, 2}) || calls[1].Status != investigations.Done || *calls[1].Effect.ProposalID != proposal ||
			calls[1].ObservedAt == nil || string(calls[1].Observation) != `{"is_error": true}` || *calls[1].Note != "Testing the bottleneck first." {
			t.Errorf("calls: %+v", calls)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// Ends: a person's stop (once); the worker's end, only with its job's end;
// the time limit, only 2 minutes after the deadline with no worker on it.
func TestEnds(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	inv, err := e.start(t, e.request(e.editor))
	if err != nil {
		t.Fatal(err)
	}
	// The worker's end without its job's end is refused.
	j, lease := e.claim(t, e.a.ID)
	if err := e.asWorker(t, e.a.ID, func(ctx context.Context, tx pgx.Tx) error {
		return investigations.End(ctx, tx, e.a.ID, inv.ID, investigations.LimitReached, nil)
	}); !errors.Is(err, postgres.ErrImmutable) {
		t.Errorf("an end while the job runs: %v", err)
	}
	// The Finish: the job, then the investigation with the AI's claim.
	if err := e.asWorker(t, e.a.ID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := jobs.Lock(ctx, tx, lease); err != nil {
			return err
		}
		if err := jobs.Complete(ctx, tx, lease); err != nil {
			return err
		}
		return investigations.End(ctx, tx, e.a.ID, inv.ID, investigations.FinishedByAI, ptr("no_improvement"))
	}); err != nil {
		t.Fatalf("the worker's finish of %s: %v", j.ID, err)
	}
	got := e.read(t, e.a.ID, e.editor, inv.ID)
	if got.Status != investigations.Finished || *got.Reason != investigations.FinishedByAI || *got.AIOutcome != "no_improvement" || got.FinishedAt == nil {
		t.Errorf("finished: %+v", got)
	}
	if err := e.api.Run(ctx, tenant.Scope{OrganizationID: e.a.ID, UserID: e.editor}, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx, _ tenant.Membership) error {
		return investigations.Stop(ctx, tx, e.a.ID, inv.ID, e.editor)
	}); !errors.Is(err, investigations.ErrEnded) {
		t.Errorf("a stop after the end: %v", err)
	}
	// A person's stop: the requester, or an owner, in their own name.
	second, err := e.start(t, e.request(e.editor))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.api.Run(ctx, tenant.Scope{OrganizationID: e.a.ID, UserID: e.editor}, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx, _ tenant.Membership) error {
		return investigations.Stop(ctx, tx, e.a.ID, second.ID, e.owner)
	}); !errors.Is(err, postgres.ErrPermission) {
		t.Errorf("a stop in another's name: %v", err)
	}
	if err := e.api.Run(ctx, tenant.Scope{OrganizationID: e.a.ID, UserID: e.owner}, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx, _ tenant.Membership) error {
		if _, err := jobs.CancelQueued(ctx, tx, e.a.ID, second.JobID); err != nil {
			return err
		}
		return investigations.Stop(ctx, tx, e.a.ID, second.ID, e.owner)
	}); err != nil {
		t.Fatalf("an owner's stop: %v", err)
	}
	got = e.read(t, e.a.ID, e.viewer, second.ID)
	if got.Status != investigations.Stopped || *got.Reason != investigations.StoppedByPerson || *got.StopRequestedBy != e.owner {
		t.Errorf("stopped: %+v", got)
	}
	// EndStale: nothing before the time limit.
	third, err := e.start(t, e.request(e.editor))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.api.Run(ctx, tenant.Scope{OrganizationID: e.a.ID, UserID: e.editor}, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx, _ tenant.Membership) error {
		ended, err := investigations.EndStale(ctx, tx, e.a.ID, third.ID)
		if err != nil || ended {
			t.Errorf("a fresh investigation ended at its time limit: %v %v", ended, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// H-060's caps, the end statuses and the contract's reasons.
func TestCapsFor(t *testing.T) {
	if got := investigations.CapsFor(investigations.Scenario, true); got.Runs != 3 || got.WhatIfs != 3 || got.Explanations != 4 {
		t.Errorf("with a reused baseline: %+v", got)
	}
	for _, r := range investigations.Reasons {
		want := investigations.Stopped
		if r == investigations.FinishedByAI || r == investigations.DraftWritten || r == investigations.LimitReached {
			want = investigations.Finished
		}
		if investigations.StatusOf(r) != want {
			t.Errorf("%s ends %s", r, investigations.StatusOf(r))
		}
	}
	// The reasons are the contract's, in its order.
	var common struct {
		Defs map[string]struct {
			Enum []string `json:"enum"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "schemas", "common.schema.json")), &common); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range investigations.Reasons {
		got = append(got, string(r))
	}
	if want := common.Defs["InvestigationReason"].Enum; !slices.Equal(got, want) {
		t.Errorf("reasons %v, the contract's %v", got, want)
	}
}
