package generator

// DB-backed tests of the model_proposal job handler through the job loop
// as the plain worker login, with proposals created through
// internal/proposals as the plain API login and the deterministic fake
// provider (TEST-003, TEST-006; T-025). Without GEMINUS_TEST_DATABASE_URL
// they skip (NOT RUN).

import (
	"context"
	"encoding/json/v2"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/ai/idpool"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/ai/review"
	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/patch"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/proposals"
	"github.com/kcl1104/geminus/internal/storage/dbtest"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/storage/postgres"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/tenant/workerscope"
	"github.com/kcl1104/geminus/internal/worker"
	"github.com/kcl1104/geminus/internal/worker/handler"
)

var fast = jobs.Policy{Lease: 3 * time.Second, MaxAttempts: 3, BackoffBase: 20 * time.Millisecond, BackoffMax: 100 * time.Millisecond}

type env struct {
	admin *postgres.DB
	org   dbtest.Org
	svc   *proposals.Service
	scope *workerscope.Worker
}

func setup(t *testing.T) *env {
	t.Helper()
	admin := dbtest.Admin(t)
	org := dbtest.NewOrg(t, admin, "https://test.invalid", tenant.Owner, tenant.Editor)
	api := dbtest.Pool(t, dbtest.LoginURL(t, dbtest.APILogin), 4, true)
	wpool := dbtest.Pool(t, dbtest.LoginURL(t, dbtest.WorkerLogin), 8, true)
	if err := tenant.CheckRuntimeRole(context.Background(), wpool, tenant.WorkerRole); err != nil {
		t.Fatal(err)
	}
	scope, err := workerscope.New(wpool, []string{org.ID})
	if err != nil {
		t.Fatal(err)
	}
	return &env{admin: postgres.OpenPool(admin), org: org, scope: scope,
		svc: &proposals.Service{DB: tenant.New(api), AI: proposals.Available, Policy: fast}}
}

// owner runs fn as the schema owner in the organization's scope.
func (e *env) owner(t *testing.T, fn func(ctx context.Context, tx pgx.Tx) error) {
	t.Helper()
	if err := e.admin.Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL ROLE twin_owner`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('app.organization_id',$1,true)`, e.org.ID); err != nil {
			return err
		}
		return fn(ctx, tx)
	}); err != nil {
		t.Fatal(err)
	}
}

// model commits a model whose version 1 (its latest) is the synthetic
// default pipeline, and returns the model and version IDs.
func (e *env) model(t *testing.T) (string, string) {
	t.Helper()
	var ir map[string]any
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata", "golden-models", "default-delivery.json")), &ir); err != nil {
		t.Fatal(err)
	}
	prj, mdl, mv := ids.New("prj"), ids.New("mdl"), ids.New("mv")
	ir["model_id"] = mdl
	raw, _ := json.Marshal(ir)
	canonical, err := hashing.Canonical(raw)
	if err != nil {
		t.Fatal(err)
	}
	user := e.org.Members[tenant.Owner].UserID
	e.owner(t, func(ctx context.Context, tx pgx.Tx) error {
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO twin.projects(id,organization_id,name,created_by) VALUES ($1,$2,'P',$3)`, []any{prj, e.org.ID, user}},
			{`INSERT INTO twin.models(id,organization_id,project_id,name,domain,created_by) VALUES ($1,$2,$3,'M','software_delivery',$4)`, []any{mdl, e.org.ID, prj, user}},
			{`INSERT INTO twin.model_versions(id,organization_id,model_id,version,schema_version,ir,hash,created_by) VALUES ($1,$2,$3,1,'0.1',$4,$5,$6)`,
				[]any{mv, e.org.ID, mdl, string(canonical), hashing.Sum(canonical), user}},
			{`UPDATE twin.models SET latest_version_id=$1 WHERE id=$2`, []any{mv, mdl}},
		} {
			if _, err := tx.Exec(ctx, q.sql, q.args...); err != nil {
				return err
			}
		}
		return nil
	})
	return mdl, mv
}

func (e *env) editor() tenant.Scope {
	return tenant.Scope{OrganizationID: e.org.ID, UserID: e.org.Members[tenant.Editor].UserID}
}

// create queues a proposal as the organization's editor.
func (e *env) create(t *testing.T, mdl, mv, mode, intent string) string {
	t.Helper()
	req := proposals.CreateRequest{BaseVersionID: mv, Mode: mode, Intent: intent}
	body, _ := json.Marshal(req)
	status, out, err := e.svc.Create(context.Background(), e.editor(), ids.New("req"), ids.New("key"), mdl, body, req)
	if err != nil || status != 202 {
		t.Fatalf("create: %d %v", status, err)
	}
	var a proposals.Accepted
	if err := json.Unmarshal(out, &a); err != nil || a.Status != "generating" {
		t.Fatalf("%s %v", out, err)
	}
	return a.ProposalID
}

func (e *env) pool(t *testing.T, id string) idpool.Pool {
	t.Helper()
	var raw []byte
	e.owner(t, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id_pool FROM twin.ai_proposals WHERE id=$1`, id).Scan(&raw)
	})
	var p idpool.Pool
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// loop is the job loop with the generator over backend.
func (e *env) loop(t *testing.T, backend provider.Backend) *worker.Worker {
	t.Helper()
	h, err := New(Config{Provider: client(t, backend), Packs: packs(t), Actor: "test-worker", Log: discard()})
	if err != nil {
		t.Fatal(err)
	}
	w, err := worker.New(e.scope, worker.Config{ID: "test-worker", Policy: fast, Heartbeat: time.Second, Idle: 20 * time.Millisecond,
		ReapLimit: 10, Handlers: map[jobs.Type]handler.Handler{jobs.ModelProposal: h}, Log: discard()})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// until runs passes until proposal id leaves generating and returns it as
// the API shows it.
func (e *env) until(t *testing.T, w *worker.Worker, id string) proposals.Proposal {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		w.Pass(context.Background())
		p, err := e.svc.Get(context.Background(), e.editor(), ids.New("req"), id)
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

func (e *env) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	e.owner(t, func(ctx context.Context, tx pgx.Tx) error { return tx.QueryRow(ctx, sql, args...).Scan(&n) })
	return n
}

func issueCodes(t *testing.T, p proposals.Proposal) map[string][]string {
	t.Helper()
	var v review.Validation
	if err := json.Unmarshal(p.Validation, &v); err != nil {
		t.Fatalf("validation %s: %v", p.Validation, err)
	}
	out := map[string][]string{}
	for _, i := range v.Issues {
		out[i.Path] = append(out[i.Path], i.Code)
	}
	return out
}

func answer(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func proposedAnswer(changes string) map[string]any {
	var c any
	_ = json.Unmarshal([]byte(changes), &c)
	return map[string]any{"status": "proposed", "summary": "Raise the CI runner capacity.", "assumptions": []string{"Service times stay."},
		"risk_level": "low", "confidence": 0.9, "changes": c, "expected_effects": []string{"Waiting may fall; a simulation will tell."},
		"requires_confirmation": true}
}

const raiseRunners = `[{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"replace","path":"/resources/1/capacity","value":6}]`

// TEST-006, REQ-008: the AI request creates only the proposal; the job
// generates it ready, with the server's review, in one transaction with
// the job's completion and its audit event, and the model is untouched.
func TestAReadyProposal(t *testing.T) {
	e := setup(t)
	mdl, mv := e.model(t)
	f := fake.New()
	f.Script(provider.RoleModeler, "Raise the CI runners to 6.", answer(t, proposedAnswer(raiseRunners)))
	id := e.create(t, mdl, mv, "edit", "Raise the CI runners to 6.")
	p := e.until(t, e.loop(t, f), id)
	if p.Status != "ready" || *p.ServerRiskLevel != "low" || *p.ProviderRiskClaim != "low" || *p.RiskLevel != "low" || *p.Confidence != 0.9 {
		t.Fatalf("%+v", p)
	}
	canonical, _ := hashing.Canonical([]byte(raiseRunners))
	if string(p.Changes) != string(canonical) {
		t.Errorf("changes %s", p.Changes)
	}
	if want, _ := hashing.Patch(canonical); *p.PatchHash != want {
		t.Errorf("patch_hash %s, want %s", *p.PatchHash, want)
	}
	if n := e.count(t, `SELECT count(*) FROM twin.jobs WHERE ai_proposal_id=$1 AND status='completed'`, id); n != 1 {
		t.Errorf("%d completed jobs", n)
	}
	if n := e.count(t, `SELECT count(*) FROM twin.model_versions WHERE model_id=$1`, mdl); n != 1 {
		t.Errorf("generating a proposal made %d versions", n)
	}
	if n := e.count(t, `SELECT count(*) FROM twin.models WHERE id=$1 AND latest_version_id=$2`, mdl, mv); n != 1 {
		t.Error("generating a proposal moved the model's latest version")
	}
	if n := e.count(t, `SELECT count(*) FROM twin.audit_events WHERE target_id=$1 AND action='proposal.create' AND actor_type='user'
		AND NOT metadata ? 'intent'`, id); n != 1 {
		t.Errorf("%d proposal.create events without the intent", n)
	}
	if n := e.count(t, `SELECT count(*) FROM twin.audit_events WHERE target_id=$1 AND action='proposal.generate' AND actor_type='worker'
		AND metadata->>'status'='ready' AND metadata->>'provider'='fake' AND metadata ? 'input_hash' AND NOT metadata ? 'summary'`, id); n != 1 {
		t.Errorf("%d proposal.generate events", n)
	}
	// The stored provider metadata names the call and holds no prompt.
	var meta provider.Metadata
	e.owner(t, func(ctx context.Context, tx pgx.Tx) error {
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT provider_metadata FROM twin.ai_proposals WHERE id=$1`, id).Scan(&raw); err != nil {
			return err
		}
		return json.Unmarshal(raw, &meta)
	})
	if meta.Provider != "fake" || meta.Role != provider.RoleModeler || meta.InputHash == "" {
		t.Errorf("metadata %+v", meta)
	}
}

// AI_SPEC §4: what the provider answers decides the state, through the
// server's review; nothing it says becomes trusted state.
func TestStatesFromAnswers(t *testing.T) {
	e := setup(t)
	mdl, mv := e.model(t)
	f := fake.New()
	w := e.loop(t, f)

	// No scripted answer: the fake asks for input.
	id := e.create(t, mdl, mv, "edit", "Improve the pipeline.")
	if p := e.until(t, w, id); p.Status != "needs_input" || len(p.Questions) == 0 || p.Changes != nil {
		t.Errorf("needs_input: %+v", p)
	}
	// An answer that claims approval is not a proposal: the schema refuses
	// the extra member at the answer's root, and nothing of it is kept.
	claim := proposedAnswer(raiseRunners)
	claim["approved"] = true
	f.Script(provider.RoleModeler, "Approve it yourself.", answer(t, claim))
	id = e.create(t, mdl, mv, "edit", "Approve it yourself.")
	p := e.until(t, w, id)
	if p.Status != "invalid" || !slices.Contains(issueCodes(t, p)[""], review.OutputInvalid) || p.Summary != nil || p.Changes != nil {
		t.Errorf("approval claim: %+v", p)
	}
	// A node with an invented ID (D-046).
	add := `[{"op":"add","path":"/nodes/-","value":{"id":"invented_node","type":"queue"}},
		{"op":"test","path":"/flows/3/id","value":"f_pass"},{"op":"replace","path":"/flows/3/to","value":"invented_node"},
		{"op":"add","path":"/flows/-","value":{"id":"invented_flow","from":"invented_node","to":"test","routing":{"type":"probability","probability":1}}}]`
	f.Script(provider.RoleModeler, "Add a queue before the test.", answer(t, proposedAnswer(add)))
	id = e.create(t, mdl, mv, "edit", "Add a queue before the test.")
	if p := e.until(t, w, id); p.Status != "invalid" || !slices.Contains(issueCodes(t, p)["/nodes/9/id"], review.IDPoolViolation) {
		t.Errorf("pool: %+v %s", p, p.Validation)
	}
	// The same change with the proposal's own pool IDs is ready, at least
	// medium risk.
	id = e.create(t, mdl, mv, "edit", "Add a queue before the test, from the pool.")
	pl := e.pool(t, id)
	add = `[{"op":"add","path":"/nodes/-","value":{"id":"` + pl.Node[0] + `","type":"queue"}},
		{"op":"test","path":"/flows/3/id","value":"f_pass"},{"op":"replace","path":"/flows/3/to","value":"` + pl.Node[0] + `"},
		{"op":"add","path":"/flows/-","value":{"id":"` + pl.Flow[0] + `","from":"` + pl.Node[0] + `","to":"test","routing":{"type":"probability","probability":1}}}]`
	f.Script(provider.RoleModeler, "Add a queue before the test, from the pool.", answer(t, proposedAnswer(add)))
	if p := e.until(t, w, id); p.Status != "ready" || *p.ServerRiskLevel != "medium" || *p.RiskLevel != "medium" {
		t.Errorf("pool IDs: %+v %s", p, p.Validation)
	}
	// An empirical distribution with no binding in the base's manifest.
	unbound := `[{"op":"test","path":"/nodes/2/id","value":"ci"},{"op":"replace","path":"/nodes/2/service_time/distribution","value":{"type":"empirical","samples_ref":"dataset:ci_times"}}]`
	f.Script(provider.RoleModeler, "Use the CI samples.", answer(t, proposedAnswer(unbound)))
	id = e.create(t, mdl, mv, "edit", "Use the CI samples.")
	if p := e.until(t, w, id); p.Status != "invalid" || !slices.Contains(issueCodes(t, p)["/nodes/2/service_time/distribution/samples_ref"], validate.DatasetBindingInvalid) {
		t.Errorf("unbound dataset: %+v %s", p, p.Validation)
	}
	// A scenario never removes a node (D-021); one that raises a capacity
	// is ready and creates no version.
	remove := `[{"op":"test","path":"/nodes/2/id","value":"ci"},{"op":"remove","path":"/nodes/2"}]`
	f.Script(provider.RoleScenario, "Without CI.", answer(t, proposedAnswer(remove)))
	id = e.create(t, mdl, mv, "scenario", "Without CI.")
	if p := e.until(t, w, id); p.Status != "invalid" || !slices.Contains(issueCodes(t, p)["/changes/1/path"], patch.PathForbidden) {
		t.Errorf("scenario scope: %+v %s", p, p.Validation)
	}
	f.Script(provider.RoleScenario, "What if there were 6 runners?", answer(t, proposedAnswer(raiseRunners)))
	id = e.create(t, mdl, mv, "scenario", "What if there were 6 runners?")
	if p := e.until(t, w, id); p.Status != "ready" || p.Mode != "scenario" {
		t.Errorf("scenario: %+v", p)
	}
	if n := e.count(t, `SELECT count(*) FROM twin.model_versions WHERE model_id=$1`, mdl); n != 1 {
		t.Errorf("proposals made %d versions", n)
	}
}

// A provider failure is the job's: retried while the provider may
// recover, failed for good after the last attempt or at once; the
// proposal follows its job to failed with the failure (0012), each
// attempt's call audited.
func TestProviderFailuresFailTheProposal(t *testing.T) {
	e := setup(t)
	mdl, mv := e.model(t)
	f := fake.New()
	w := e.loop(t, f)
	f.Fail(provider.RoleModeler, "Busy.", provider.NewError(provider.CodeRateLimited, 429, "org-a"))
	id := e.create(t, mdl, mv, "edit", "Busy.")
	p := e.until(t, w, id)
	if p.Status != "failed" || p.Error.Error.Code != "RATE_LIMITED" || p.Error.Error.Details[0].Code != "AI_RATE_LIMITED" {
		t.Fatalf("%+v", p.Error)
	}
	if n := e.count(t, `SELECT count(*) FROM twin.audit_events WHERE target_id=$1 AND action='proposal.attempt_failed'`, id); n != fast.MaxAttempts {
		t.Errorf("%d attempts audited, want %d", n, fast.MaxAttempts)
	}
	f.Fail(provider.RoleModeler, "Refuse.", provider.NewError(provider.CodeRefused, 0, "org-a"))
	id = e.create(t, mdl, mv, "edit", "Refuse.")
	p = e.until(t, w, id)
	if p.Status != "failed" || p.Error.Error.Details[0].Code != "AI_REFUSED" || p.Error.Error.Code != "INTERNAL_ERROR" {
		t.Fatalf("%+v", p.Error)
	}
	if n := e.count(t, `SELECT attempt FROM twin.jobs WHERE ai_proposal_id=$1`, id); n != 1 {
		t.Errorf("a refusal was tried %d times", n)
	}
	// Disabled AI in the worker (an API that thought otherwise).
	id = e.create(t, mdl, mv, "edit", "Anything.")
	p = e.until(t, e.loop(t, provider.Disabled{Code: provider.CodeNotConfigured}), id)
	if p.Status != "failed" || p.Error.Error.Code != "AI_PROVIDER_NOT_CONFIGURED" {
		t.Fatalf("%+v", p.Error)
	}
	if n := e.count(t, `SELECT count(*) FROM twin.ai_proposals WHERE id=$1 AND summary IS NULL AND patch IS NULL AND risk_level IS NULL`, id); n != 1 {
		t.Error("a failed proposal holds generated content")
	}
}

// H-003 review M2: an answer holding U+0000 (in its text, a patch value or
// a member name), which PostgreSQL text and jsonb cannot store, becomes
// invalid in one attempt: the job completes, and nothing of the answer is
// stored. H-057: in that attempt the provider is asked twice, the second
// time for the one repair (the fake repeats the answer, refused again), and
// the stored metadata says the repair came to invalid.
func TestUnstorableAnswersAreInvalid(t *testing.T) {
	e := setup(t)
	mdl, mv := e.model(t)
	for name, c := range map[string]struct {
		edit func(map[string]any)
		path string
	}{
		"summary": {func(a map[string]any) { a["summary"] = "a\x00b" }, "/summary"},
		"patch value": {func(a map[string]any) {
			var ops any
			_ = json.Unmarshal([]byte(`[{"op":"replace","path":"/name","value":"x\u0000y"}]`), &ops)
			a["changes"] = ops
		}, "/changes/0/value"},
		"member name": {func(a map[string]any) {
			var ops any
			_ = json.Unmarshal([]byte(`[{"op":"test","path":"/nodes/2/id","value":"ci"},`+
				`{"op":"replace","path":"/nodes/2/service_time/distribution","value":{"type":"constant","value":1,"a\u0000b":1}}]`), &ops)
			a["changes"] = ops
		}, "/changes/1/value"},
	} {
		t.Run(name, func(t *testing.T) {
			a := proposedAnswer(raiseRunners)
			c.edit(a)
			f := fake.New()
			intent := "Store this " + name + "."
			f.Script(provider.RoleModeler, intent, answer(t, a))
			id := e.create(t, mdl, mv, "edit", intent)
			p := e.until(t, e.loop(t, f), id)
			if p.Status != "invalid" || !slices.Contains(issueCodes(t, p)[c.path], review.OutputInvalid) || p.Summary != nil || p.Changes != nil {
				t.Fatalf("%+v %s", p, p.Validation)
			}
			if reqs := f.Requests(); len(reqs) != 2 || reqs[0].Prompt.Revision != nil || reqs[1].Prompt.Revision == nil {
				t.Errorf("the provider was asked %d times", len(reqs))
			}
			if n := e.count(t, `SELECT count(*) FROM twin.jobs WHERE ai_proposal_id=$1 AND status='completed' AND attempt=1`, id); n != 1 {
				t.Error("the job did not complete in its first attempt")
			}
			if n := e.count(t, `SELECT count(*) FROM twin.ai_proposals WHERE id=$1 AND provider_metadata->'repair'->>'outcome'='invalid'
				AND position('\u0000' in provider_metadata::text)=0`, id); n != 1 {
				t.Error("the stored metadata does not say the repair came to invalid")
			}
		})
	}
}
