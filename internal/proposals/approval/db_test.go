package approval_test

// DB-backed tests of the human decision (T-026; TEST-005, TEST-006,
// TEST-007, TEST-009): models created through internal/models and
// proposals through internal/proposals as the plain API login, generated
// ready through the job loop as the plain worker login with the
// deterministic fake provider, then approved or rejected through this
// package as the plain API login. Without GEMINUS_TEST_DATABASE_URL they
// skip (NOT RUN).

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/domainpack/softwaredelivery"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/patch"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/models"
	"github.com/kcl1104/geminus/internal/proposals"
	"github.com/kcl1104/geminus/internal/proposals/approval"
	"github.com/kcl1104/geminus/internal/proposals/generator"
	"github.com/kcl1104/geminus/internal/storage/dbtest"
	"github.com/kcl1104/geminus/internal/storage/idempotency"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/storage/objects"
	"github.com/kcl1104/geminus/internal/storage/postgres"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/tenant/workerscope"
	"github.com/kcl1104/geminus/internal/worker"
	"github.com/kcl1104/geminus/internal/worker/handler"
)

const issuer = "https://test.invalid"

var fast = jobs.Policy{Lease: 3 * time.Second, MaxAttempts: 3, BackoffBase: 20 * time.Millisecond, BackoffMax: 100 * time.Millisecond}

type env struct {
	admin   *pgxpool.Pool
	org     dbtest.Org
	project string
	reg     *domainpack.Registry
	models  *models.Service
	props   *proposals.Service
	svc     *approval.Service
	fake    *fake.Backend
	w       *worker.Worker
	objects *objects.FS
	root    string
}

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func setup(t *testing.T) *env {
	t.Helper()
	admin := dbtest.Admin(t)
	org := dbtest.NewOrg(t, admin, issuer, tenant.Owner, tenant.Editor, tenant.Viewer)
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
	root := t.TempDir()
	fs, err := objects.NewFS(root, "test")
	if err != nil {
		t.Fatal(err)
	}
	scope, err := workerscope.New(wpool, []string{org.ID})
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
	w, err := worker.New(scope, worker.Config{ID: "test-worker", Policy: fast, Heartbeat: time.Second, Idle: 20 * time.Millisecond,
		ReapLimit: 10, Handlers: map[jobs.Type]handler.Handler{jobs.ModelProposal: gen}, Log: discard()})
	if err != nil {
		t.Fatal(err)
	}
	db := tenant.New(api)
	e := &env{admin: admin, org: org, reg: reg, fake: f, w: w, objects: fs, root: root,
		models: &models.Service{DB: db, Packs: reg},
		props:  &proposals.Service{DB: db, AI: proposals.Available, Policy: fast},
		svc:    &approval.Service{DB: db, Packs: reg, Objects: fs}}
	e.project = ids.New("prj")
	e.owner(t, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO twin.projects(id,organization_id,name,created_by) VALUES ($1,$2,'P',$3)`,
			e.project, org.ID, org.Members[tenant.Owner].UserID)
		return err
	})
	return e
}

func (e *env) scope(r tenant.Role) tenant.Scope {
	return tenant.Scope{OrganizationID: e.org.ID, UserID: e.org.Members[r].UserID}
}

// owner runs fn as the schema owner in the organization's scope.
func (e *env) owner(t *testing.T, fn func(ctx context.Context, tx pgx.Tx) error) {
	t.Helper()
	if err := postgres.OpenPool(e.admin).Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
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

func (e *env) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	e.owner(t, func(ctx context.Context, tx pgx.Tx) error { return tx.QueryRow(ctx, sql, args...).Scan(&n) })
	return n
}

const createBody = `{"name":"Pipeline","domain":"software_delivery","template":"default_delivery_pipeline"}`

// model creates a model from the synthetic delivery template: version 1
// and a clean draft at revision 1.
func (e *env) model(t *testing.T) models.Model {
	t.Helper()
	var req models.CreateRequest
	if err := json.Unmarshal([]byte(createBody), &req); err != nil {
		t.Fatal(err)
	}
	status, out, err := e.models.Create(context.Background(), e.scope(tenant.Editor), ids.New("req"), "key-"+ids.New("k"), e.project, []byte(createBody), req)
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

// queue asks for a proposal on base as the editor and returns its ID, still
// generating.
func (e *env) queue(t *testing.T, modelID, base, mode, intent string) string {
	t.Helper()
	req := proposals.CreateRequest{BaseVersionID: base, Mode: mode, Intent: intent}
	body, _ := json.Marshal(req)
	status, out, err := e.props.Create(context.Background(), e.scope(tenant.Editor), ids.New("req"), "key-"+ids.New("k"), modelID, body, req)
	if err != nil || status != 202 {
		t.Fatalf("create proposal: %d %v", status, err)
	}
	var a proposals.Accepted
	if err := json.Unmarshal(out, &a); err != nil {
		t.Fatal(err)
	}
	return a.ProposalID
}

// generated runs the job loop until proposal id leaves generating.
func (e *env) generated(t *testing.T, id string) proposals.Proposal {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		e.w.Pass(context.Background())
		p := e.get(t, id)
		if p.Status != "generating" {
			return p
		}
		if time.Now().After(deadline) {
			t.Fatalf("proposal %s is still generating", id)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (e *env) get(t *testing.T, id string) proposals.Proposal {
	t.Helper()
	p, err := e.props.Get(context.Background(), e.scope(tenant.Viewer), ids.New("req"), id)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(p)
	if issues, err := validate.Document(data, "proposal.schema.json", "RESPONSE"); err != nil || len(issues) > 0 {
		t.Fatalf("the proposal does not match proposal.schema.json: %v %v\n%s", issues, err, data)
	}
	return p
}

// ready is a ready proposal on base whose answer makes changes.
func (e *env) ready(t *testing.T, modelID, base, mode, changes string) proposals.Proposal {
	t.Helper()
	intent := "Change it " + ids.New("k")
	scriptAnswer(e.fake, mode, intent, answer(t, "Raise the CI runner capacity.", changes))
	p := e.generated(t, e.queue(t, modelID, base, mode, intent))
	if p.Status != "ready" {
		t.Fatalf("not ready: %+v", p)
	}
	return p
}

func (e *env) approve(role tenant.Role, id, base, key, comment string) (int, []byte, error) {
	req := approval.ApproveRequest{ExpectedBaseVersionID: base, Comment: comment}
	body, _ := json.Marshal(req)
	return e.svc.Approve(context.Background(), e.scope(role), ids.New("req"), key, id, body, req)
}

func (e *env) reject(role tenant.Role, id, key, comment string) (int, []byte, error) {
	req := approval.RejectRequest{Comment: comment}
	body, _ := json.Marshal(req)
	return e.svc.Reject(context.Background(), e.scope(role), ids.New("req"), key, id, body, req)
}

func key() string { return "key-" + ids.New("k") }

func matches(t *testing.T, ref string, out []byte) {
	t.Helper()
	if issues, err := validate.Document(out, ref, "RESPONSE"); err != nil || len(issues) > 0 {
		t.Errorf("%s does not match %s: %v %v", out, ref, issues, err)
	}
}

// snapshot is what a model holds that a decision may change: the latest
// version and the number of versions, the draft (revision, base, hash)
// and the number of scenarios and jobs.
type snapshot struct {
	latest                       string
	versions, scenarios, jobs    int
	draftRevision                int64
	draftBase, draftHash, mdName string
}

func (e *env) snap(t *testing.T, modelID string) snapshot {
	t.Helper()
	var s snapshot
	e.owner(t, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT m.latest_version_id, m.name, d.revision, d.base_version_id, d.hash,
			  (SELECT count(*) FROM twin.model_versions v WHERE v.model_id=m.id),
			  (SELECT count(*) FROM twin.scenarios s WHERE s.model_id=m.id),
			  (SELECT count(*) FROM twin.jobs j WHERE j.organization_id=m.organization_id AND j.type='simulation')
			FROM twin.models m JOIN twin.model_drafts d ON d.model_id=m.id WHERE m.id=$1`, modelID).
			Scan(&s.latest, &s.mdName, &s.draftRevision, &s.draftBase, &s.draftHash, &s.versions, &s.scenarios, &s.jobs)
	})
	return s
}

// version is a version as stored, with the proposal it came from.
type version struct {
	id, parent, source, hash, ir, createdBy string
	number                                  int64
	comment                                 *string
	manifest                                string
}

func (e *env) version(t *testing.T, id string) version {
	t.Helper()
	var v version
	e.owner(t, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id, coalesce(parent_version_id,''), coalesce(source_proposal_id,''), hash, ir, created_by, version,
			  comment, dataset_bindings::text FROM twin.model_versions WHERE id=$1`, id).
			Scan(&v.id, &v.parent, &v.source, &v.hash, &v.ir, &v.createdBy, &v.number, &v.comment, &v.manifest)
	})
	return v
}

// TEST-007, REQ-009, AI_SPEC §6: an editor approves a ready edit proposal:
// exactly one new version, the next after the base, with the base and the
// reviewed changes as its IR, its comment and the base's manifest; the
// model's latest; the clean draft moves to it; the proposal is approved
// with its decision record; one audit event without the comment.
func TestApproveEdit(t *testing.T) {
	e := setup(t)
	m := e.model(t)
	p := e.ready(t, m.ID, m.LatestVersion.ID, "edit", raiseCI)
	k := key()
	status, out, err := e.approve(tenant.Editor, p.ProposalID, m.LatestVersion.ID, k, "Reviewed the diff and the risk.")
	if err != nil || status != 201 {
		t.Fatalf("approve: %d %v %s", status, err, out)
	}
	matches(t, "http.schema.json#/$defs/Approval", out)
	var a approval.EditApproval
	if err := json.Unmarshal(out, &a); err != nil {
		t.Fatal(err)
	}
	if a.Status != "approved" || a.Mode != "edit" || a.Version != 2 || a.PatchHash != *p.PatchHash || a.DraftOutOfDate || a.ProposalID != p.ProposalID {
		t.Errorf("approval %+v", a)
	}
	v := e.version(t, a.NewVersionID)
	base := e.version(t, m.LatestVersion.ID)
	applied, err := patch.Apply([]byte(base.ir), p.Changes, patch.AIEdit)
	if err != nil || !applied.Valid() {
		t.Fatal(err)
	}
	want, _ := hashing.Canonical(applied.Candidate)
	if v.parent != m.LatestVersion.ID || v.source != p.ProposalID || v.number != 2 || v.ir != string(want) || v.hash != hashing.Sum(want) ||
		v.createdBy != e.org.Members[tenant.Editor].UserID || v.comment == nil || *v.comment != "Reviewed the diff and the risk." || v.manifest != "[]" {
		t.Errorf("the new version %+v", v)
	}
	s := e.snap(t, m.ID)
	if s.latest != v.id || s.versions != 2 || s.draftBase != v.id || s.draftRevision != 2 || s.draftHash != v.hash || s.scenarios != 0 {
		t.Errorf("after approval %+v", s)
	}
	got := e.get(t, p.ProposalID)
	if got.Status != "approved" || got.NewVersionID == nil || *got.NewVersionID != v.id || got.ScenarioID != nil || got.DecidedBy == nil ||
		*got.DecidedBy != e.org.Members[tenant.Editor].UserID || got.DecidedAt == nil || got.DecisionComment == nil ||
		*got.DecisionComment != "Reviewed the diff and the risk." || string(got.Changes) != string(p.Changes) {
		t.Errorf("the approved proposal %+v", got)
	}
	if n := e.count(t, `SELECT count(*) FROM twin.audit_events WHERE target_id=$1 AND action='proposal.approve' AND actor_type='user'
		AND actor_id=$2 AND metadata->>'new_version_id'=$3 AND metadata->>'mode'='edit' AND metadata->>'draft_out_of_date'='false'
		AND metadata->>'patch_hash'=$4 AND NOT metadata ? 'comment' AND position('Reviewed' in metadata::text)=0`,
		p.ProposalID, e.org.Members[tenant.Editor].UserID, v.id, *p.PatchHash); n != 1 {
		t.Errorf("%d proposal.approve events", n)
	}
	// The same key and body replay the same answer, and nothing more is
	// written (D-011).
	again, out2, err := e.approve(tenant.Editor, p.ProposalID, m.LatestVersion.ID, k, "Reviewed the diff and the risk.")
	if err != nil || again != 201 || !bytes.Equal(out, out2) {
		t.Errorf("replay: %d %v %s", again, err, out2)
	}
	if s := e.snap(t, m.ID); s.versions != 2 || s.draftRevision != 2 {
		t.Errorf("the replay wrote: %+v", s)
	}
	if n := e.count(t, `SELECT count(*) FROM twin.audit_events WHERE target_id=$1 AND action='proposal.approve'`, p.ProposalID); n != 1 {
		t.Errorf("the replay audited again: %d", n)
	}
}

// D-003: a dirty draft is kept byte for byte and is out of date; it is not
// rebased, and publishing it later is a conflict.
func TestApproveKeepsADirtyDraft(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	m := e.model(t)
	if _, err := e.models.PatchDraft(ctx, e.scope(tenant.Editor), ids.New("req"), m.ID, 1, []byte(`[{"op":"replace","path":"/name","value":"Unpublished edit"}]`)); err != nil {
		t.Fatal(err)
	}
	before, err := e.models.GetDraft(ctx, e.scope(tenant.Viewer), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	p := e.ready(t, m.ID, m.LatestVersion.ID, "edit", raiseCI)
	status, out, err := e.approve(tenant.Owner, p.ProposalID, m.LatestVersion.ID, key(), "")
	if err != nil || status != 201 {
		t.Fatalf("approve: %d %v", status, err)
	}
	var a approval.EditApproval
	if err := json.Unmarshal(out, &a); err != nil || !a.DraftOutOfDate {
		t.Fatalf("%s %v", out, err)
	}
	after, err := e.models.GetDraft(ctx, e.scope(tenant.Viewer), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision || after.Hash != before.Hash || string(after.IR) != string(before.IR) ||
		after.BaseVersionID != m.LatestVersion.ID || !after.OutOfDate {
		t.Errorf("the dirty draft changed: %+v", after)
	}
	if v := e.version(t, a.NewVersionID); v.comment != nil {
		t.Errorf("an empty comment is kept: %q", *v.comment)
	}
	if got := e.get(t, p.ProposalID); got.DecisionComment != nil {
		t.Errorf("an empty comment is kept on the proposal: %q", *got.DecisionComment)
	}
	// Publishing the stale draft is refused (D-003, ADR-022).
	body := fmt.Sprintf(`{"expected_base_version_id":%q}`, m.LatestVersion.ID)
	if _, _, err := e.models.Publish(ctx, e.scope(tenant.Editor), ids.New("req"), key(), m.ID, after.Revision, []byte(body),
		models.PublishRequest{ExpectedBaseVersionID: m.LatestVersion.ID}); !errors.Is(err, models.ErrConflict) {
		t.Errorf("publishing the stale draft: %v", err)
	}
}

// D-004, AI_SPEC §6–§7, TEST-008's core: a scenario approval creates
// exactly one immutable scenario on the base with the reviewed changes,
// no version, no draft change, no run.
func TestApproveScenario(t *testing.T) {
	e := setup(t)
	m := e.model(t)
	p := e.ready(t, m.ID, m.LatestVersion.ID, "scenario", raiseCI)
	before := e.snap(t, m.ID)
	status, out, err := e.approve(tenant.Editor, p.ProposalID, m.LatestVersion.ID, key(), "Try it.")
	if err != nil || status != 201 {
		t.Fatalf("approve: %d %v %s", status, err, out)
	}
	matches(t, "http.schema.json#/$defs/Approval", out)
	var a approval.ScenarioApproval
	if err := json.Unmarshal(out, &a); err != nil || a.Mode != "scenario" || a.PatchHash != *p.PatchHash || a.ScenarioID == "" {
		t.Fatalf("%s %v", out, err)
	}
	after := e.snap(t, m.ID)
	if after.latest != before.latest || after.versions != before.versions || after.draftRevision != before.draftRevision ||
		after.draftHash != before.draftHash || after.jobs != before.jobs || after.scenarios != 1 {
		t.Errorf("a scenario approval changed %+v into %+v", before, after)
	}
	var base, name, patchText, hash, source string
	e.owner(t, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT base_version_id, name, patch::text, patch_hash, source_proposal_id FROM twin.scenarios WHERE id=$1`, a.ScenarioID).
			Scan(&base, &name, &patchText, &hash, &source)
	})
	canonical, _ := hashing.Canonical([]byte(patchText))
	if base != m.LatestVersion.ID || name != "Raise the CI runner capacity." || string(canonical) != string(p.Changes) || hash != *p.PatchHash || source != p.ProposalID {
		t.Errorf("the scenario %s %s %s %s %s", base, name, canonical, hash, source)
	}
	if got := e.get(t, p.ProposalID); got.Status != "approved" || got.ScenarioID == nil || *got.ScenarioID != a.ScenarioID || got.NewVersionID != nil {
		t.Errorf("the approved proposal %+v", got)
	}
}

// TEST-007, D-011, D-030: the same key and body, however many race, give
// one effect and the same answer; another key is 409 already decided; the
// key on another proposal is a key conflict; a replay still needs the role.
func TestApprovalIdempotency(t *testing.T) {
	e := setup(t)
	m := e.model(t)
	p := e.ready(t, m.ID, m.LatestVersion.ID, "edit", raiseCI)
	other := e.model(t)
	q := e.ready(t, other.ID, other.LatestVersion.ID, "edit", raiseCI)
	k := key()
	const n = 8
	answers := make([]string, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			status, out, err := e.approve(tenant.Editor, p.ProposalID, m.LatestVersion.ID, k, "same")
			answers[i] = fmt.Sprint(status, string(out), err)
		})
	}
	wg.Wait()
	for _, a := range answers {
		if a != answers[0] || !strings.HasPrefix(a, "201") {
			t.Errorf("an answer %s, the first %s", a, answers[0])
		}
	}
	if s := e.snap(t, m.ID); s.versions != 2 {
		t.Errorf("%d versions after %d racing replays", s.versions, n)
	}
	var decided *approval.DecidedError
	if _, _, err := e.approve(tenant.Editor, p.ProposalID, m.LatestVersion.ID, key(), "same"); !errors.As(err, &decided) || decided.Status != "approved" {
		t.Errorf("another key: %v", err)
	}
	if _, _, err := e.approve(tenant.Owner, p.ProposalID, m.LatestVersion.ID, key(), ""); !errors.As(err, &decided) {
		t.Errorf("another person: %v", err)
	}
	if _, _, err := e.reject(tenant.Editor, p.ProposalID, key(), ""); !errors.As(err, &decided) {
		t.Errorf("rejecting an approved proposal: %v", err)
	}
	if _, _, err := e.approve(tenant.Editor, q.ProposalID, other.LatestVersion.ID, k, "same"); err == nil || !strings.Contains(err.Error(), "different request") {
		t.Errorf("the key on another proposal: %v", err)
	}
	if _, _, err := e.approve(tenant.Editor, p.ProposalID, m.LatestVersion.ID, k, "another comment"); err == nil {
		t.Error("the key with another body was replayed")
	}
	if s := e.snap(t, m.ID); s.versions != 2 {
		t.Errorf("%d versions", s.versions)
	}
	// The replay needs the role now, not the one the first request had.
	e.setRole(t, tenant.Editor, tenant.Viewer)
	if _, _, err := e.approve(tenant.Editor, p.ProposalID, m.LatestVersion.ID, k, "same"); !errors.Is(err, tenant.ErrForbidden) {
		t.Errorf("a replay after the role was removed: %v", err)
	}
}

// setRole changes the role of the organization's member who had from.
func (e *env) setRole(t *testing.T, from, to tenant.Role) {
	t.Helper()
	e.owner(t, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE twin.memberships SET role=$3 WHERE organization_id=$1 AND user_id=$2`,
			e.org.ID, e.org.Members[from].UserID, string(to))
		return err
	})
}

// TEST-007: concurrent approvals of one proposal with different keys, and
// an approval racing a rejection: exactly one decision wins, the others
// are 409 already decided, and there is one effect or none.
func TestDecisionRaces(t *testing.T) {
	e := setup(t)
	m := e.model(t)
	p := e.ready(t, m.ID, m.LatestVersion.ID, "edit", raiseCI)
	const n = 6
	errs := make([]error, n)
	statuses := make([]int, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			statuses[i], _, errs[i] = e.approve(tenant.Editor, p.ProposalID, m.LatestVersion.ID, key(), "")
		})
	}
	wg.Wait()
	won := 0
	for i := range n {
		var decided *approval.DecidedError
		switch {
		case errs[i] == nil && statuses[i] == 201:
			won++
		case errors.As(errs[i], &decided):
		default:
			t.Errorf("an approval: %d %v", statuses[i], errs[i])
		}
	}
	if won != 1 || e.snap(t, m.ID).versions != 2 {
		t.Errorf("%d approvals won, %d versions", won, e.snap(t, m.ID).versions)
	}

	latest := e.snap(t, m.ID).latest
	q := e.ready(t, m.ID, latest, "edit", moreQA)
	var approveErr, rejectErr error
	var approveStatus int
	wg.Go(func() { approveStatus, _, approveErr = e.approve(tenant.Editor, q.ProposalID, latest, key(), "") })
	wg.Go(func() { _, _, rejectErr = e.reject(tenant.Owner, q.ProposalID, key(), "No.") })
	wg.Wait()
	got := e.get(t, q.ProposalID)
	var decided *approval.DecidedError
	switch {
	case approveErr == nil && approveStatus == 201 && errors.As(rejectErr, &decided):
		if got.Status != "approved" || e.snap(t, m.ID).versions != 3 {
			t.Errorf("the approval won but %s with %d versions", got.Status, e.snap(t, m.ID).versions)
		}
	case rejectErr == nil && errors.As(approveErr, &decided):
		if got.Status != "rejected" || e.snap(t, m.ID).versions != 2 {
			t.Errorf("the rejection won but %s with %d versions", got.Status, e.snap(t, m.ID).versions)
		}
	default:
		t.Errorf("approve %d %v, reject %v", approveStatus, approveErr, rejectErr)
	}
}

// TEST-007, AI_SPEC §6: two proposals on one base, one approved; the other
// is conflicted by its approval (409 MODEL_VERSION_CONFLICT, the key's
// stored answer), never rebased; another key is then already decided; a
// publish that moved the base conflicts an approval the same way; an
// approval that names another base changes nothing.
func TestApprovalOnAMovedBase(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	m := e.model(t)
	first := e.ready(t, m.ID, m.LatestVersion.ID, "edit", raiseCI)
	second := e.ready(t, m.ID, m.LatestVersion.ID, "edit", moreQA)
	scenario := e.ready(t, m.ID, m.LatestVersion.ID, "scenario", moreQA)
	if status, _, err := e.approve(tenant.Editor, first.ProposalID, m.LatestVersion.ID, key(), ""); err != nil || status != 201 {
		t.Fatalf("the first approval: %d %v", status, err)
	}
	moved := e.snap(t, m.ID)
	for _, p := range []proposals.Proposal{second, scenario} {
		k := key()
		status, out, err := e.approve(tenant.Owner, p.ProposalID, m.LatestVersion.ID, k, "too late")
		if err != nil || status != 409 {
			t.Fatalf("%s on the old base: %d %v %s", p.Mode, status, err, out)
		}
		matches(t, "common.schema.json#/$defs/Error", out)
		if !strings.Contains(string(out), `"code":"MODEL_VERSION_CONFLICT"`) || !strings.Contains(string(out), `"path":"/expected_base_version_id"`) {
			t.Errorf("%s", out)
		}
		got := e.get(t, p.ProposalID)
		if got.Status != "conflicted" || got.DecidedBy == nil || *got.DecidedBy != e.org.Members[tenant.Owner].UserID || got.DecisionComment != nil ||
			got.NewVersionID != nil || got.ScenarioID != nil || got.BaseVersionID != m.LatestVersion.ID {
			t.Errorf("%s: %+v", p.Mode, got)
		}
		if s := e.snap(t, m.ID); s != moved {
			t.Errorf("%s: the conflict changed the model: %+v", p.Mode, s)
		}
		// The key's answer is stored: a retry gets it again, naming the
		// retry's own request (D-069 (a), review L1).
		retry := ids.New("req")
		retryReq := approval.ApproveRequest{ExpectedBaseVersionID: m.LatestVersion.ID, Comment: "too late"}
		retryBody, _ := json.Marshal(retryReq)
		again, out2, err := e.svc.Approve(ctx, e.scope(tenant.Owner), retry, k, p.ProposalID, retryBody, retryReq)
		if err != nil || again != 409 || !bytes.Equal(out2, renamed(t, out, retry)) || bytes.Equal(out2, out) {
			t.Errorf("%s: the conflict's replay: %d %v\n%s\n%s", p.Mode, again, err, out, out2)
		}
		var decided *approval.DecidedError
		if _, _, err := e.approve(tenant.Owner, p.ProposalID, moved.latest, key(), ""); !errors.As(err, &decided) || decided.Status != "conflicted" {
			t.Errorf("%s: a new key on the latest base: %v", p.Mode, err)
		}
		if _, _, err := e.reject(tenant.Owner, p.ProposalID, key(), ""); !errors.As(err, &decided) {
			t.Errorf("%s: rejecting a conflicted proposal: %v", p.Mode, err)
		}
		if n := e.count(t, `SELECT count(*) FROM twin.audit_events WHERE target_id=$1 AND action='proposal.conflict'
			AND metadata->>'latest_version_id'=$2`, p.ProposalID, moved.latest); n != 1 {
			t.Errorf("%s: %d proposal.conflict events", p.Mode, n)
		}
	}

	// A publish moves the base the same way.
	third := e.ready(t, m.ID, moved.latest, "edit", lowerCI)
	d, err := e.models.GetDraft(ctx, e.scope(tenant.Editor), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.models.PatchDraft(ctx, e.scope(tenant.Editor), ids.New("req"), m.ID, d.Revision, []byte(`[{"op":"replace","path":"/name","value":"Published"}]`)); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"expected_base_version_id":%q}`, moved.latest)
	if status, _, err := e.models.Publish(ctx, e.scope(tenant.Editor), ids.New("req"), key(), m.ID, d.Revision+1, []byte(body),
		models.PublishRequest{ExpectedBaseVersionID: moved.latest}); err != nil || status != 201 {
		t.Fatalf("publish: %d %v", status, err)
	}
	if status, _, err := e.approve(tenant.Editor, third.ProposalID, moved.latest, key(), ""); err != nil || status != 409 {
		t.Errorf("after a publish: %d %v", status, err)
	}
	if got := e.get(t, third.ProposalID); got.Status != "conflicted" {
		t.Errorf("after a publish: %s", got.Status)
	}

	// The request must name the proposal's base: another one is refused
	// and nothing changes, not even the proposal's state.
	latest := e.snap(t, m.ID)
	// The latest version already has raiseCI's capacity (the first
	// approval), and a patch that leaves the model as it is is invalid.
	fourth := e.ready(t, m.ID, latest.latest, "edit", lowerCI)
	for _, wrong := range []string{m.LatestVersion.ID, ids.New("mv")} {
		if _, _, err := e.approve(tenant.Editor, fourth.ProposalID, wrong, key(), ""); !errors.Is(err, approval.ErrOtherBase) {
			t.Errorf("naming %s: %v", wrong, err)
		}
	}
	if got := e.get(t, fourth.ProposalID); got.Status != "ready" || got.DecidedBy != nil {
		t.Errorf("a wrong base changed the proposal: %+v", got)
	}
	if s := e.snap(t, m.ID); s != latest {
		t.Errorf("a wrong base changed the model: %+v", s)
	}
	if status, _, err := e.approve(tenant.Editor, fourth.ProposalID, latest.latest, key(), ""); err != nil || status != 201 {
		t.Errorf("then with the right base: %d %v", status, err)
	}
}

// TEST-009, SECURITY §2: a viewer cannot decide; another organization sees
// nothing (404); a role removed after the person read the proposal is
// refused at approval, and a role change in flight is waited for.
func TestDecisionRoles(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	m := e.model(t)
	p := e.ready(t, m.ID, m.LatestVersion.ID, "edit", raiseCI)
	before := e.snap(t, m.ID)
	if _, _, err := e.approve(tenant.Viewer, p.ProposalID, m.LatestVersion.ID, key(), ""); !errors.Is(err, tenant.ErrForbidden) {
		t.Errorf("a viewer approves: %v", err)
	}
	if _, _, err := e.reject(tenant.Viewer, p.ProposalID, key(), ""); !errors.Is(err, tenant.ErrForbidden) {
		t.Errorf("a viewer rejects: %v", err)
	}
	stranger := dbtest.NewOrg(t, e.admin, issuer, tenant.Owner)
	in := tenant.Scope{OrganizationID: stranger.ID, UserID: stranger.Members[tenant.Owner].UserID}
	req := approval.ApproveRequest{ExpectedBaseVersionID: m.LatestVersion.ID}
	body, _ := json.Marshal(req)
	if _, _, err := e.svc.Approve(ctx, in, ids.New("req"), key(), p.ProposalID, body, req); !errors.Is(err, approval.ErrNotFound) {
		t.Errorf("another organization approves: %v", err)
	}
	if _, _, err := e.svc.Reject(ctx, in, ids.New("req"), key(), p.ProposalID, []byte(`{}`), approval.RejectRequest{}); !errors.Is(err, approval.ErrNotFound) {
		t.Errorf("another organization rejects: %v", err)
	}
	cross := tenant.Scope{OrganizationID: e.org.ID, UserID: stranger.Members[tenant.Owner].UserID}
	if _, _, err := e.svc.Approve(ctx, cross, ids.New("req"), key(), p.ProposalID, body, req); !errors.Is(err, tenant.ErrNoMembership) {
		t.Errorf("a non-member choosing the organization: %v", err)
	}
	if _, _, err := e.approve(tenant.Editor, ids.New("aip"), m.LatestVersion.ID, key(), ""); !errors.Is(err, approval.ErrNotFound) {
		t.Errorf("no such proposal: %v", err)
	}

	// The editor read the proposal, then lost the role: refused.
	e.get(t, p.ProposalID)
	e.setRole(t, tenant.Editor, tenant.Viewer)
	if _, _, err := e.approve(tenant.Editor, p.ProposalID, m.LatestVersion.ID, key(), ""); !errors.Is(err, tenant.ErrForbidden) {
		t.Errorf("approving after the role was removed: %v", err)
	}
	e.setRole(t, tenant.Editor, tenant.Editor)
	if s := e.snap(t, m.ID); s != before || e.get(t, p.ProposalID).Status != "ready" {
		t.Errorf("refused approvals changed something: %+v", s)
	}

	// A role change in flight: the approval waits for it (the membership
	// row is share-locked by every write, T-009 review L3), then sees the
	// new role and is refused.
	tx, err := e.admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE twin.memberships SET role='viewer' WHERE organization_id=$1 AND user_id=$2`,
		e.org.ID, e.org.Members[tenant.Editor].UserID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := e.approve(tenant.Editor, p.ProposalID, m.LatestVersion.ID, key(), "")
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("the approval did not wait for the role change: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, tenant.ErrForbidden) {
			t.Errorf("after the role change committed: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the approval is still waiting")
	}
	if s := e.snap(t, m.ID); s != before || e.get(t, p.ProposalID).Status != "ready" {
		t.Errorf("the refused approval changed something: %+v", s)
	}
}

// D-003's lock order: a decision waits for the model row another write
// holds (a publish, a draft edit), so the two run one after the other.
func TestDecisionsWaitForTheModelRow(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	m := e.model(t)
	approveMe := e.ready(t, m.ID, m.LatestVersion.ID, "scenario", raiseCI)
	rejectMe := e.ready(t, m.ID, m.LatestVersion.ID, "edit", raiseCI)
	for _, w := range []struct {
		name  string
		write func() error
	}{
		{"approve", func() error {
			_, _, err := e.approve(tenant.Editor, approveMe.ProposalID, m.LatestVersion.ID, key(), "")
			return err
		}},
		{"reject", func() error { _, _, err := e.reject(tenant.Editor, rejectMe.ProposalID, key(), ""); return err }},
	} {
		tx, err := e.admin.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `SELECT 1 FROM twin.models WHERE id=$1 FOR UPDATE`, m.ID); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- w.write() }()
		select {
		case err := <-done:
			t.Errorf("%s did not wait for the model row: %v", w.name, err)
		case <-time.After(500 * time.Millisecond):
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("%s after the row was released: %v", w.name, err)
			}
		case <-time.After(30 * time.Second):
			t.Fatalf("%s still waiting", w.name)
		}
	}
}

// Review L3, D-003's lock order: a decision takes the model row before the
// draft, as a publish and a draft edit do, so it never holds the draft
// while it waits for the model (which would deadlock with them). While
// another transaction holds the draft row, the decision waiting for it
// already holds the model row: the holder cannot take it (55P03).
func TestDecisionsTakeTheModelRowFirst(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	m := e.model(t)
	approveMe := e.ready(t, m.ID, m.LatestVersion.ID, "edit", raiseCI)
	rejectMe := e.ready(t, m.ID, m.LatestVersion.ID, "edit", moreQA)
	for _, w := range []struct {
		name  string
		write func() error
	}{
		{"approve", func() error {
			_, _, err := e.approve(tenant.Editor, approveMe.ProposalID, m.LatestVersion.ID, key(), "")
			return err
		}},
		{"reject", func() error { _, _, err := e.reject(tenant.Editor, rejectMe.ProposalID, key(), ""); return err }},
	} {
		holder, err := e.admin.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := holder.Exec(ctx, `SELECT 1 FROM twin.model_drafts WHERE model_id=$1 FOR UPDATE`, m.ID); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- w.write() }()
		e.waitsForALock(t, done, "twin.model_drafts")
		_, err = holder.Exec(ctx, `SELECT 1 FROM twin.models WHERE id=$1 FOR UPDATE NOWAIT`, m.ID)
		var pe *pgconn.PgError
		if !errors.As(err, &pe) || pe.Code != "55P03" {
			t.Errorf("%s: the model row is free while the decision waits for the draft: %v", w.name, err)
		}
		if err := holder.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("%s after the draft was released: %v", w.name, err)
			}
		case <-time.After(30 * time.Second):
			t.Fatalf("%s still waiting", w.name)
		}
	}
}

// waitsForALock returns once a backend waits for a row lock in a
// statement that locks table, and fails the test if the decision ends
// first or none does within 10 s.
func (e *env) waitsForALock(t *testing.T, done <-chan error, table string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var n int
		if err := e.admin.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity
			WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%FROM '||$1::text||'%FOR UPDATE%'`, table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			return
		}
		select {
		case err := <-done:
			t.Fatalf("the decision did not wait for the held row: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("no statement on %s waits for a lock", table)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Review M1, D-069 (a), API_DATABASE §7: a key is scoped to the
// organization, the actor and the operation, not the proposal, so the
// fingerprint (the proposal and the normalized body) is what tells the
// same key with the same body on another proposal of the same model and
// base apart: 409 IDEMPOTENCY_KEY_CONFLICT, never the first proposal's
// answer, and the second proposal stays as it was. {} and {"comment":""}
// are one request (D-068, review I3).
func TestTheKeyNamesTheProposal(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	m := e.model(t)
	base := m.LatestVersion.ID
	first := e.ready(t, m.ID, base, "edit", raiseCI)
	second := e.ready(t, m.ID, base, "edit", moreQA)
	k := key()
	if status, out, err := e.approve(tenant.Editor, first.ProposalID, base, k, "Same."); err != nil || status != 201 {
		t.Fatalf("approve the first: %d %v %s", status, err, out)
	}
	moved := e.snap(t, m.ID)
	if status, out, err := e.approve(tenant.Editor, second.ProposalID, base, k, "Same."); !errors.Is(err, idempotency.ErrConflict) {
		t.Errorf("the key on the second proposal: %d %v %s", status, err, out)
	}
	if got := e.get(t, second.ProposalID); got.Status != "ready" || got.DecidedBy != nil {
		t.Errorf("the second proposal changed: %+v", got)
	}
	if s := e.snap(t, m.ID); s != moved {
		t.Errorf("the refused approval changed %+v into %+v", moved, s)
	}

	// Two rejections with an empty body.
	third := e.ready(t, m.ID, moved.latest, "edit", lowerCI)
	fourth := e.ready(t, m.ID, moved.latest, "edit", moreQA)
	k = key()
	rejected, out, err := e.svc.Reject(ctx, e.scope(tenant.Editor), ids.New("req"), k, third.ProposalID, []byte(`{}`), approval.RejectRequest{})
	if err != nil || rejected != 200 {
		t.Fatalf("reject the third: %d %v", rejected, err)
	}
	if status, again, err := e.svc.Reject(ctx, e.scope(tenant.Editor), ids.New("req"), k, fourth.ProposalID, []byte(`{}`), approval.RejectRequest{}); !errors.Is(err, idempotency.ErrConflict) {
		t.Errorf("the key on the fourth proposal: %d %v %s", status, err, again)
	}
	if got := e.get(t, fourth.ProposalID); got.Status != "ready" || got.DecidedBy != nil {
		t.Errorf("the fourth proposal changed: %+v", got)
	}
	// The same rejection with an empty comment replays the first answer.
	if status, again, err := e.svc.Reject(ctx, e.scope(tenant.Editor), ids.New("req"), k, third.ProposalID, []byte(`{"comment":""}`), approval.RejectRequest{}); err != nil ||
		status != 200 || !bytes.Equal(again, out) {
		t.Errorf(`{"comment":""} after {}: %d %v %s`, status, err, again)
	}
	if n := e.count(t, `SELECT count(*) FROM twin.audit_events WHERE action='proposal.reject' AND organization_id=$1`, e.org.ID); n != 1 {
		t.Errorf("%d rejections audited", n)
	}
}

// TEST-006: a rejection changes no model, draft, version or scenario; a
// proposal that asked for input can be rejected; the answer is the
// proposal; the key replays it; generating, invalid and failed proposals
// cannot be decided, and approval needs ready.
func TestRejectAndStates(t *testing.T) {
	e := setup(t)
	m := e.model(t)
	before := e.snap(t, m.ID)
	p := e.ready(t, m.ID, m.LatestVersion.ID, "edit", raiseCI)
	k := key()
	status, out, err := e.reject(tenant.Editor, p.ProposalID, k, "Not now.")
	if err != nil || status != 200 {
		t.Fatalf("reject: %d %v", status, err)
	}
	matches(t, "proposal.schema.json", out)
	var got proposals.Proposal
	if err := json.Unmarshal(out, &got); err != nil || got.Status != "rejected" || got.DecisionComment == nil || *got.DecisionComment != "Not now." ||
		got.DecidedBy == nil || *got.DecidedBy != e.org.Members[tenant.Editor].UserID || string(got.Changes) != string(p.Changes) {
		t.Errorf("%s %v", out, err)
	}
	if after := e.snap(t, m.ID); after != before {
		t.Errorf("a rejection changed %+v into %+v", before, after)
	}
	if again, out2, err := e.reject(tenant.Editor, p.ProposalID, k, "Not now."); err != nil || again != 200 || !bytes.Equal(out, out2) {
		t.Errorf("replay: %d %v", again, err)
	}
	var decided *approval.DecidedError
	if _, _, err := e.approve(tenant.Editor, p.ProposalID, m.LatestVersion.ID, key(), ""); !errors.As(err, &decided) || decided.Status != "rejected" {
		t.Errorf("approving a rejected proposal: %v", err)
	}
	if n := e.count(t, `SELECT count(*) FROM twin.audit_events WHERE target_id=$1 AND action='proposal.reject' AND metadata->>'from'='ready'
		AND position('Not now' in metadata::text)=0`, p.ProposalID); n != 1 {
		t.Errorf("%d proposal.reject events", n)
	}

	// needs_input (the fake's answer to an intent it has no script for).
	asked := e.generated(t, e.queue(t, m.ID, m.LatestVersion.ID, "edit", "Something vague."))
	if asked.Status != "needs_input" {
		t.Fatalf("%+v", asked)
	}
	var state *approval.StateError
	if _, _, err := e.approve(tenant.Editor, asked.ProposalID, m.LatestVersion.ID, key(), ""); !errors.As(err, &state) || state.Status != "needs_input" {
		t.Errorf("approving needs_input: %v", err)
	}
	if status, out, err := e.reject(tenant.Editor, asked.ProposalID, key(), ""); err != nil || status != 200 {
		t.Errorf("rejecting needs_input: %d %v", status, err)
	} else {
		matches(t, "proposal.schema.json", out)
	}

	undecidable := func(id, want string) {
		t.Helper()
		if _, _, err := e.approve(tenant.Editor, id, m.LatestVersion.ID, key(), ""); !errors.As(err, &state) || string(state.Status) != want || state.Decision != "approve" {
			t.Errorf("approving %s: %v", want, err)
		}
		if _, _, err := e.reject(tenant.Editor, id, key(), ""); !errors.As(err, &state) || string(state.Status) != want || state.Decision != "reject" {
			t.Errorf("rejecting %s: %v", want, err)
		}
	}
	// generating: its job has not run (no pass of the loop before the
	// checks).
	undecidable(e.queue(t, m.ID, m.LatestVersion.ID, "edit", "Raise it later."), "generating")
	// invalid: the answer sets a capacity of 0; failed: the provider refuses.
	e.fake.Script(provider.RoleModeler, "Zero runners.", answer(t, "No runners.",
		`[{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"replace","path":"/resources/1/capacity","value":0}]`))
	e.fake.Fail(provider.RoleModeler, "Refuse.", provider.NewError(provider.CodeRefused, 0, ""))
	for intent, want := range map[string]string{"Zero runners.": "invalid", "Refuse.": "failed"} {
		got := e.generated(t, e.queue(t, m.ID, m.LatestVersion.ID, "edit", intent))
		if got.Status != want {
			t.Fatalf("%s: %s", want, got.Status)
		}
		undecidable(got.ProposalID, want)
	}
	if after := e.snap(t, m.ID); after != before {
		t.Errorf("refused decisions changed %+v into %+v", before, after)
	}
}

// SECURITY §5, AI_SPEC §6, D-069 (b): the approval reviews the stored
// content again and trusts none of the stored results. A patch rewritten
// after review (its hash kept), a stored risk lowered below the server's,
// and a rule the domain pack has gained since review are all refused, and
// nothing changes.
func TestApprovalReviewsAgain(t *testing.T) {
	e := setup(t)
	m := e.model(t)
	before := e.snap(t, m.ID)
	tamper := e.tamper(t)
	var ie *approval.InvalidError
	var re *approval.RiskChangedError
	var decided *approval.DecidedError
	var state *approval.StateError

	rewritten := e.ready(t, m.ID, m.LatestVersion.ID, "edit", raiseCI)
	tamper(rewritten.ProposalID, `patch='`+lowerCI+`'`)
	if _, _, err := e.approve(tenant.Editor, rewritten.ProposalID, m.LatestVersion.ID, key(), ""); err == nil ||
		errors.As(err, &ie) || errors.As(err, &re) || errors.As(err, &decided) || errors.As(err, &state) {
		t.Errorf("a patch its hash does not cover: %v", err)
	}

	lowered := e.ready(t, m.ID, m.LatestVersion.ID, "edit", lowerCI)
	if *lowered.ServerRiskLevel != "medium" {
		t.Fatalf("lowering a capacity is %s", *lowered.ServerRiskLevel)
	}
	tamper(lowered.ProposalID, `server_risk_level='low', risk_level='low'`)
	if _, _, err := e.approve(tenant.Editor, lowered.ProposalID, m.LatestVersion.ID, key(), ""); !errors.As(err, &re) || re.Now != "medium" {
		t.Errorf("a stored risk below the server's: %v", err)
	}

	strict := e.ready(t, m.ID, m.LatestVersion.ID, "edit", raiseCI)
	svc := *e.svc
	svc.Packs = strictLookup{e.reg}
	req := approval.ApproveRequest{ExpectedBaseVersionID: m.LatestVersion.ID}
	body, _ := json.Marshal(req)
	if _, _, err := svc.Approve(context.Background(), e.scope(tenant.Editor), ids.New("req"), key(), strict.ProposalID, body, req); !errors.As(err, &ie) {
		t.Errorf("a rule the pack gained since review: %v", err)
	}

	for _, p := range []proposals.Proposal{rewritten, lowered, strict} {
		if got := e.get(t, p.ProposalID); got.Status != "ready" {
			t.Errorf("a refused approval moved the proposal to %s", got.Status)
		}
	}
	if s := e.snap(t, m.ID); s != before {
		t.Errorf("refused approvals changed %+v into %+v", before, s)
	}
	if n := e.count(t, `SELECT count(*) FROM twin.audit_events WHERE action='proposal.approve' AND organization_id=$1`, e.org.ID); n != 0 {
		t.Errorf("%d approvals audited", n)
	}
}

// tamper rewrites a stored proposal behind the API's back, as the schema
// owner with the user triggers off for the statement: what an approval
// must not trust.
func (e *env) tamper(t *testing.T) func(id, set string) {
	return func(id, set string) {
		t.Helper()
		e.owner(t, func(ctx context.Context, tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `ALTER TABLE twin.ai_proposals DISABLE TRIGGER USER`); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE twin.ai_proposals SET `+set+` WHERE id=$1`, id); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `ALTER TABLE twin.ai_proposals ENABLE TRIGGER USER`)
			return err
		})
	}
}

// typed reports whether err is one of the refusals the service names
// (each its own status and code); anything else is a server error (500).
func typed(err error) bool {
	var decided *approval.DecidedError
	var state *approval.StateError
	var risk *approval.RiskChangedError
	var invalid *approval.InvalidError
	var storage *approval.DatasetStorageError
	return errors.As(err, &decided) || errors.As(err, &state) || errors.As(err, &risk) || errors.As(err, &invalid) ||
		errors.As(err, &storage) || errors.Is(err, approval.ErrNotFound) || errors.Is(err, approval.ErrOtherBase) ||
		errors.Is(err, approval.ErrInvalidComment) || errors.Is(err, approval.ErrDatasetsUnavailable) ||
		errors.Is(err, idempotency.ErrConflict) || errors.Is(err, idempotency.ErrInProgress) ||
		errors.Is(err, tenant.ErrForbidden) || errors.Is(err, tenant.ErrNoMembership)
}

// strictLookup serves a pack that refuses a capacity above 5: a rule the
// pack did not have when the proposals were reviewed.
type strictLookup struct{ reg *domainpack.Registry }

func (l strictLookup) Lookup(domain string) (domainpack.DomainPack, error) {
	p, err := l.reg.Lookup(domain)
	return strictPack{p}, err
}

type strictPack struct{ domainpack.DomainPack }

func (p strictPack) Validate(ir *model.ModelIR) []model.ValidationIssue {
	issues := p.DomainPack.Validate(ir)
	for i, r := range ir.Resources {
		if r.Capacity > 5 {
			issues = append(issues, model.NewIssue("DOMAIN_MISMATCH", fmt.Sprintf("/resources/%d/capacity", i), "too many", model.SeverityError))
		}
	}
	return issues
}

// D-062 (g), TEST-014: the approved version freezes the base's whole
// dataset manifest; the snapshots are read and verified again at the
// approval: without object storage it is refused (503), with bytes that no
// longer match their hash it is invalid; nothing changes either time.
func TestApprovalFreezesTheManifest(t *testing.T) {
	e := setup(t)
	m := e.model(t)
	base, binding := e.datasetVersion(t, m)
	p := e.ready(t, m.ID, base, "edit", raiseCI)
	var bindings []model.DatasetBinding
	if err := json.Unmarshal(mustJSON(t, p.DatasetBindings), &bindings); err != nil || len(bindings) != 1 || bindings[0] != binding {
		t.Fatalf("the proposal binds %+v", p.DatasetBindings)
	}
	before := e.snap(t, m.ID)

	noStorage := *e.svc
	noStorage.Objects = nil
	req := approval.ApproveRequest{ExpectedBaseVersionID: base}
	body, _ := json.Marshal(req)
	if _, _, err := noStorage.Approve(context.Background(), e.scope(tenant.Editor), ids.New("req"), key(), p.ProposalID, body, req); !errors.Is(err, approval.ErrDatasetsUnavailable) {
		t.Errorf("without object storage: %v", err)
	}
	// Review I2: object storage that cannot be read is 503
	// DATASET_STORAGE_FAILED, as in the generator (D-061 (c)), not 500.
	broken := *e.svc
	broken.Objects = brokenObjects{e.objects}
	var storage *approval.DatasetStorageError
	if _, _, err := broken.Approve(context.Background(), e.scope(tenant.Editor), ids.New("req"), key(), p.ProposalID, body, req); !errors.As(err, &storage) {
		t.Errorf("object storage that fails: %v", err)
	}
	// Review L2, D-069 (b): a proposal that holds another manifest than
	// its base's whole manifest is the server's fault: a server error,
	// never approved, nothing changed.
	other := e.ready(t, m.ID, base, "edit", moreQA)
	e.tamper(t)(other.ProposalID, `candidate_dataset_bindings='[]'`)
	if _, _, err := e.approve(tenant.Editor, other.ProposalID, base, key(), ""); err == nil || typed(err) {
		t.Errorf("a proposal without its base's manifest: %v", err)
	}
	if got := e.get(t, other.ProposalID); got.Status != "ready" || got.DecidedBy != nil {
		t.Errorf("a refused approval moved the proposal: %+v", got)
	}
	if s := e.snap(t, m.ID); s != before {
		t.Errorf("refused approvals changed %+v into %+v", before, s)
	}

	key0, _ := objects.NewKey(e.org.ID, objects.Datasets, binding.DatasetVersionID+".json")
	path := filepath.Join(e.root, filepath.FromSlash(key0))
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"tampered":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var ie *approval.InvalidError
	if _, _, err := e.approve(tenant.Editor, p.ProposalID, base, key(), ""); !errors.As(err, &ie) ||
		!slices.ContainsFunc(ie.Issues, func(is model.ValidationIssue) bool { return is.Code == validate.DatasetBindingInvalid }) {
		t.Errorf("a snapshot that no longer verifies: %v", err)
	}
	if s := e.snap(t, m.ID); s != before || e.get(t, p.ProposalID).Status != "ready" {
		t.Errorf("refused approvals changed something: %+v", s)
	}

	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	status, out, err := e.approve(tenant.Editor, p.ProposalID, base, key(), "")
	if err != nil || status != 201 {
		t.Fatalf("approve: %d %v", status, err)
	}
	var a approval.EditApproval
	if err := json.Unmarshal(out, &a); err != nil {
		t.Fatal(err)
	}
	v := e.version(t, a.NewVersionID)
	var frozen []model.DatasetBinding
	if err := json.Unmarshal([]byte(v.manifest), &frozen); err != nil || len(frozen) != 1 || frozen[0] != binding {
		t.Errorf("the new version freezes %s", v.manifest)
	}
	if n := e.count(t, `SELECT count(*) FROM twin.model_version_datasets WHERE model_version_id=$1 AND dataset_version_id=$2`,
		a.NewVersionID, binding.DatasetVersionID); n != 1 {
		t.Errorf("%d binding rows", n)
	}
	// The clean draft moved to the new version, its datasets checked again.
	if s := e.snap(t, m.ID); s.draftBase != a.NewVersionID || a.DraftOutOfDate {
		t.Errorf("the draft %+v, out of date %v", s, a.DraftOutOfDate)
	}
}

// renamed is the error answer out as request requestID gets it.
func renamed(t *testing.T, out []byte, requestID string) []byte {
	t.Helper()
	var e proposals.ErrorEnvelope
	if err := json.Unmarshal(out, &e, json.RejectUnknownMembers(true)); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	e.Error.RequestID = requestID
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := hashing.Canonical(data)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

// brokenObjects is object storage whose reads fail.
type brokenObjects struct{ objects.Store }

func (brokenObjects) Get(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("the object store did not answer")
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// datasetVersion gives model m a version 2 whose code review takes an
// empirical service time from a dataset snapshot in object storage, with
// the draft moved to it (clean), and returns the version and its binding.
func (e *env) datasetVersion(t *testing.T, m models.Model) (string, model.DatasetBinding) {
	t.Helper()
	ctx := context.Background()
	v1 := e.version(t, m.LatestVersion.ID)
	var doc map[string]any
	if err := json.Unmarshal([]byte(v1.ir), &doc); err != nil {
		t.Fatal(err)
	}
	nodes := doc["nodes"].([]any)
	nodes[1].(map[string]any)["service_time"] = map[string]any{"distribution": map[string]any{"type": "empirical", "samples_ref": "dataset:review_minutes"}}
	raw, _ := json.Marshal(doc)
	ir, err := hashing.Canonical(raw)
	if err != nil {
		t.Fatal(err)
	}
	ds, dsv, mv := ids.New("ds"), ids.New("dsv"), ids.New("mv")
	snap, _ := json.Marshal(map[string]any{"dataset_version_id": dsv, "organization_id": e.org.ID, "logical_ref": "dataset:review_minutes",
		"unit": "minute", "samples": []float64{20, 30, 45}})
	objKey, err := objects.NewKey(e.org.ID, objects.Datasets, dsv+".json")
	if err != nil {
		t.Fatal(err)
	}
	obj, err := e.objects.Put(ctx, objKey, bytes.NewReader(snap), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	b := model.DatasetBinding{LogicalRef: "dataset:review_minutes", DatasetVersionID: dsv, ContentHash: obj.SHA256, Unit: "minute", SampleCount: 3}
	manifest := mustJSON(t, []model.DatasetBinding{b})
	owner := e.org.Members[tenant.Owner].UserID
	e.owner(t, func(ctx context.Context, tx pgx.Tx) error {
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO twin.datasets(id,organization_id,project_id,kind,name,created_by) VALUES ($1,$2,$3,'empirical_samples','review minutes',$4)`,
				[]any{ds, e.org.ID, e.project, owner}},
			{`INSERT INTO twin.dataset_versions(id,organization_id,dataset_id,version,object_key,content_hash,content_type,row_count,unit,created_by)
				VALUES ($1,$2,$3,1,$4,$5,'application/json',3,'minute',$6)`, []any{dsv, e.org.ID, ds, objKey, obj.SHA256, owner}},
			{`INSERT INTO twin.model_versions(id,organization_id,model_id,version,schema_version,parent_version_id,ir,hash,dataset_bindings,created_by)
				VALUES ($1,$2,$3,2,'0.1',$4,$5,$6,$7,$8)`, []any{mv, e.org.ID, m.ID, m.LatestVersion.ID, string(ir), hashing.Sum(ir), string(manifest), owner}},
			{`INSERT INTO twin.model_version_datasets(organization_id,model_id,model_version_id,logical_ref,dataset_version_id,content_hash,unit,sample_count)
				VALUES ($1,$2,$3,$4,$5,$6,'minute',3)`, []any{e.org.ID, m.ID, mv, b.LogicalRef, dsv, obj.SHA256}},
			{`UPDATE twin.models SET latest_version_id=$1 WHERE id=$2`, []any{mv, m.ID}},
			{`UPDATE twin.model_drafts SET base_version_id=$1, revision=revision+1, ir=$2, hash=$3, issues='[]' WHERE model_id=$4`,
				[]any{mv, string(ir), hashing.Sum(ir), m.ID}},
		} {
			if _, err := tx.Exec(ctx, q.sql, q.args...); err != nil {
				return err
			}
		}
		return nil
	})
	return mv, b
}
