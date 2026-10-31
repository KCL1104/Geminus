package api_test

// Integration tests of the proposal routes (T-025) through the whole
// stack: the handler cmd/api serves as the plain API login, and the job
// loop cmd/worker runs as the plain worker login with the proposal
// generator over the deterministic fake provider (TEST-003, TEST-006,
// TEST-007, TEST-009 for proposals). Without GEMINUS_TEST_DATABASE_URL they
// skip (NOT RUN).

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/api"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/domainpack/softwaredelivery"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/proposals"
	"github.com/kcl1104/geminus/internal/proposals/generator"
	"github.com/kcl1104/geminus/internal/storage/dbtest"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/tenant/workerscope"
	"github.com/kcl1104/geminus/internal/worker"
	"github.com/kcl1104/geminus/internal/worker/handler"
)

type proposalEnv struct {
	*env
	fake *fake.Backend
	w    *worker.Worker
}

// setupProposals is setup with AI available (the fake) and the worker side:
// a scope over both organizations as the worker login and the generator.
func setupProposals(t *testing.T, opts ...api.Option) *proposalEnv {
	t.Helper()
	e := setup(t, append([]api.Option{api.WithAI(proposals.Available)}, opts...)...)
	pool := dbtest.Pool(t, dbtest.LoginURL(t, dbtest.WorkerLogin), 8, true)
	if err := tenant.CheckRuntimeRole(context.Background(), pool, tenant.WorkerRole); err != nil {
		t.Fatal(err)
	}
	scope, err := workerscope.New(pool, []string{e.a.ID, e.b.ID})
	if err != nil {
		t.Fatal(err)
	}
	f := fake.New()
	client, err := provider.New(f, provider.DefaultBudget())
	if err != nil {
		t.Fatal(err)
	}
	packs, err := domainpack.NewRegistry(softwaredelivery.Pack{})
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h, err := generator.New(generator.Config{Provider: client, Packs: packs, Actor: "test-worker", Log: log})
	if err != nil {
		t.Fatal(err)
	}
	w, err := worker.New(scope, worker.Config{ID: "test-worker", Policy: fastPolicy, Heartbeat: time.Second, Idle: 20 * time.Millisecond,
		ReapLimit: 10, Handlers: map[jobs.Type]handler.Handler{jobs.ModelProposal: h}, Log: log})
	if err != nil {
		t.Fatal(err)
	}
	return &proposalEnv{env: e, fake: f, w: w}
}

func proposalBody(base, mode, intent string) string {
	data, _ := json.Marshal(map[string]any{"base_version_id": base, "mode": mode, "intent": intent})
	return string(data)
}

func (e *env) propose(subject, org, modelID, key, body string) resp {
	return e.do("POST", "/v1/models/"+modelID+"/ai-proposals", subject, org, map[string]string{"Idempotency-Key": key}, body)
}

type proposalView struct {
	ProposalID      string         `json:"proposal_id"`
	Status          string         `json:"status"`
	Mode            string         `json:"mode"`
	CreatedBy       string         `json:"created_by"`
	ServerRiskLevel *string        `json:"server_risk_level"`
	RiskLevel       *string        `json:"risk_level"`
	PatchHash       *string        `json:"patch_hash"`
	Changes         jsontext.Value `json:"changes"`
	Questions       []string       `json:"questions"`
	// RequestedModel is the model the request named (H-065), "" for none.
	RequestedModel string `json:"requested_model"`
	Error          *struct {
		Error struct {
			Code    string `json:"code"`
			Details []struct {
				Code string `json:"code"`
			} `json:"details"`
		} `json:"error"`
	} `json:"error"`
}

func (e *env) proposal(t *testing.T, subject, org, id string) proposalView {
	t.Helper()
	r := e.do("GET", "/v1/ai-proposals/"+id, subject, org, nil, "")
	if r.status != 200 {
		t.Fatalf("get: %d %s", r.status, r.body)
	}
	valid(t, r, "proposal.schema.json")
	return decode[proposalView](t, r)
}

// generated passes the loop until proposal id leaves generating.
func (e *proposalEnv) generated(t *testing.T, subject, org, id string) proposalView {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		e.w.Pass(context.Background())
		if p := e.proposal(t, subject, org, id); p.Status != "generating" {
			return p
		}
		if time.Now().After(deadline) {
			t.Fatalf("proposal %s is still generating", id)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

const raiseCI = `{"status":"proposed","summary":"Raise the CI runner capacity.","assumptions":[],"risk_level":"low","confidence":0.9,` +
	`"changes":[{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"replace","path":"/resources/1/capacity","value":6}],` +
	`"expected_effects":["Waiting may fall; a simulation will tell."],"requires_confirmation":true}`

// The closed loop (REQ-008, TEST-006): the request answers 202
// generating; the worker generates it ready with the server's risk; the
// model, its draft and its versions are untouched; it is listed.
func TestProposalClosedLoop(t *testing.T) {
	e := setupProposals(t)
	editor, viewer := e.a.Members[tenant.Editor], e.a.Members[tenant.Viewer]
	m := e.newModel(t)
	before := e.do("GET", "/v1/models/"+m.ID+"/draft", editor.Subject, e.a.ID, nil, "")
	e.fake.Script(provider.RoleModeler, "Raise the CI runners to 6.", []byte(raiseCI))
	r := e.propose(editor.Subject, e.a.ID, m.ID, "prop-loop-0001", proposalBody(m.LatestVersion.ID, "edit", "Raise the CI runners to 6."))
	if r.status != 202 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	valid(t, r, "http.schema.json#/$defs/ProposalAccepted")
	accepted := decode[proposals.Accepted](t, r)
	if p := e.proposal(t, viewer.Subject, e.a.ID, accepted.ProposalID); p.Status != "generating" || p.CreatedBy != editor.UserID || p.Changes != nil {
		t.Fatalf("%+v", p)
	}
	p := e.generated(t, viewer.Subject, e.a.ID, accepted.ProposalID)
	if p.Status != "ready" || *p.ServerRiskLevel != "low" || *p.RiskLevel != "low" || p.PatchHash == nil {
		t.Fatalf("%+v", p)
	}
	after := e.do("GET", "/v1/models/"+m.ID+"/draft", editor.Subject, e.a.ID, nil, "")
	if string(before.body) != string(after.body) || before.header.Get("ETag") != after.header.Get("ETag") {
		t.Errorf("the draft changed:\n%s\n%s", before.body, after.body)
	}
	if n := e.count(`SELECT count(*) FROM twin.model_versions WHERE model_id=$1`, m.ID); n != 1 {
		t.Errorf("%d versions", n)
	}
	list := e.do("GET", "/v1/models/"+m.ID+"/ai-proposals?limit=10", viewer.Subject, e.a.ID, nil, "")
	valid(t, list, "http.schema.json#/$defs/Proposals")
	if !strings.Contains(string(list.body), accepted.ProposalID) {
		t.Errorf("the list misses the proposal: %s", list.body)
	}
	// A provider that keeps failing fails the proposal, with the code.
	e.fake.Fail(provider.RoleModeler, "Refuse.", provider.NewError(provider.CodeRefused, 0, ""))
	r = e.propose(editor.Subject, e.a.ID, m.ID, "prop-loop-0002", proposalBody(m.LatestVersion.ID, "edit", "Refuse."))
	if r.status != 202 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	p = e.generated(t, viewer.Subject, e.a.ID, decode[proposals.Accepted](t, r).ProposalID)
	if p.Status != "failed" || p.Error.Error.Code != "INTERNAL_ERROR" || p.Error.Error.Details[0].Code != "AI_REFUSED" {
		t.Errorf("%+v", p.Error)
	}
}

// TEST-009 (proposals): editors and owners create, any member reads; a
// member of another organization sees 404 for the model and the proposal;
// no token is 401.
func TestProposalRoleMatrix(t *testing.T) {
	e := setupProposals(t)
	owner, editor, viewer := e.a.Members[tenant.Owner], e.a.Members[tenant.Editor], e.a.Members[tenant.Viewer]
	stranger := e.b.Members[tenant.Owner]
	m := e.newModel(t)
	body := proposalBody(m.LatestVersion.ID, "scenario", "What if there were 6 runners?")
	if r := e.propose(viewer.Subject, e.a.ID, m.ID, "prop-role-0001", body); r.status != 403 || code(t, r) != "FORBIDDEN" {
		t.Errorf("viewer: %d %s", r.status, r.body)
	}
	if r := e.propose(stranger.Subject, e.b.ID, m.ID, "prop-role-0002", body); r.status != 404 || code(t, r) != "NOT_FOUND" {
		t.Errorf("another organization: %d %s", r.status, r.body)
	}
	if r := e.propose(stranger.Subject, e.a.ID, m.ID, "prop-role-0003", body); r.status != 404 {
		t.Errorf("a non-member choosing the organization: %d %s", r.status, r.body)
	}
	if r := e.propose("", e.a.ID, m.ID, "prop-role-0004", body); r.status != 401 {
		t.Errorf("no token: %d %s", r.status, r.body)
	}
	var id string
	for i, s := range []string{owner.Subject, editor.Subject} {
		r := e.propose(s, e.a.ID, m.ID, fmt.Sprintf("prop-role-01%02d", i), body)
		if r.status != 202 {
			t.Fatalf("%d %s", r.status, r.body)
		}
		id = decode[proposals.Accepted](t, r).ProposalID
	}
	e.proposal(t, viewer.Subject, e.a.ID, id)
	if r := e.do("GET", "/v1/ai-proposals/"+id, stranger.Subject, e.b.ID, nil, ""); r.status != 404 || code(t, r) != "NOT_FOUND" {
		t.Errorf("another organization reads it: %d %s", r.status, r.body)
	}
	if r := e.do("GET", "/v1/models/"+m.ID+"/ai-proposals", stranger.Subject, e.b.ID, nil, ""); r.status != 404 {
		t.Errorf("another organization lists them: %d %s", r.status, r.body)
	}
	if r := e.do("GET", "/v1/ai-proposals/not-an-id", viewer.Subject, e.a.ID, nil, ""); r.status != 400 {
		t.Errorf("a bad ID: %d %s", r.status, r.body)
	}
}

// What the request cannot be: refused before anything is stored.
func TestProposalRequestRefusals(t *testing.T) {
	e := setupProposals(t)
	editor := e.a.Members[tenant.Editor]
	m := e.newModel(t)
	other := e.newModelIn(t, "-other")
	for _, c := range []struct {
		name, model, body string
		headers           map[string]string
		status            int
		code              string
	}{
		{"no key", m.ID, proposalBody(m.LatestVersion.ID, "edit", "x"), map[string]string{}, 400, "IDEMPOTENCY_KEY_REQUIRED"},
		{"bad JSON", m.ID, `{"base_version_id":`, nil, 400, "INVALID_REQUEST"},
		{"unknown member", m.ID, `{"base_version_id":"` + m.LatestVersion.ID + `","mode":"edit","intent":"x","organization_id":"` + e.a.ID + `"}`, nil, 422, "REQUEST_VALIDATION_FAILED"},
		{"unknown mode", m.ID, proposalBody(m.LatestVersion.ID, "publish", "x"), nil, 422, "REQUEST_VALIDATION_FAILED"},
		{"white space", m.ID, proposalBody(m.LatestVersion.ID, "edit", " \t\n"), nil, 422, "REQUEST_VALIDATION_FAILED"},
		{"another model's version", m.ID, proposalBody(other.LatestVersion.ID, "edit", "x"), nil, 422, "REQUEST_VALIDATION_FAILED"},
		{"no such version", m.ID, proposalBody(ids.New("mv"), "edit", "x"), nil, 404, "NOT_FOUND"},
		{"no such model", ids.New("mdl"), proposalBody(m.LatestVersion.ID, "edit", "x"), nil, 404, "NOT_FOUND"},
		{"a bad locale", m.ID, `{"base_version_id":"` + m.LatestVersion.ID + `","mode":"edit","intent":"x","output_locale":"zh-TW"}`, nil, 422, "REQUEST_VALIDATION_FAILED"},
	} {
		h := c.headers
		if h == nil {
			h = map[string]string{"Idempotency-Key": "prop-refuse-" + strings.ReplaceAll(c.name, " ", "-")}
		}
		r := e.do("POST", "/v1/models/"+c.model+"/ai-proposals", editor.Subject, e.a.ID, h, c.body)
		if r.status != c.status || code(t, r) != c.code {
			t.Errorf("%s: %d %s", c.name, r.status, r.body)
		}
	}
	// A stale base (a newer version was published): 409, no auto-rebase.
	if r := e.patch(editor.Subject, m.ID, etag(1), `[{"op":"replace","path":"/name","value":"Renamed"}]`); r.status != 200 {
		t.Fatalf("patch: %d %s", r.status, r.body)
	}
	publish := e.publish(editor.Subject, m.ID, "prop-refuse-pub", etag(2), `{"expected_base_version_id":"`+m.LatestVersion.ID+`"}`)
	if publish.status != 201 {
		t.Fatalf("publish: %d %s", publish.status, publish.body)
	}
	if r := e.propose(editor.Subject, e.a.ID, m.ID, "prop-refuse-stale", proposalBody(m.LatestVersion.ID, "edit", "x")); r.status != 409 || code(t, r) != "MODEL_VERSION_CONFLICT" {
		t.Errorf("stale base: %d %s", r.status, r.body)
	}
	if n := e.count(`SELECT count(*) FROM twin.ai_proposals WHERE organization_id=$1`, e.a.ID); n != 0 {
		t.Errorf("%d proposals stored by refused requests", n)
	}
}

// AI_SPEC §2: with AI off the request is refused with its code and
// nothing is stored.
func TestProposalsNeedAI(t *testing.T) {
	for _, c := range []struct {
		opts []api.Option
		code string
	}{
		{nil, "AI_PROVIDER_NOT_CONFIGURED"},
		{[]api.Option{api.WithAI(proposals.PolicyNotApproved)}, "AI_POLICY_NOT_APPROVED"},
	} {
		e := setup(t, c.opts...)
		editor := e.a.Members[tenant.Editor]
		m := e.newModel(t)
		r := e.propose(editor.Subject, e.a.ID, m.ID, "prop-ai-off-01", proposalBody(m.LatestVersion.ID, "edit", "x"))
		if r.status != 503 || code(t, r) != c.code {
			t.Errorf("%s: %d %s", c.code, r.status, r.body)
		}
		if n := e.count(`SELECT count(*) FROM twin.ai_proposals WHERE organization_id=$1`, e.a.ID); n != 0 {
			t.Errorf("%s: %d proposals", c.code, n)
		}
		if n := e.count(`SELECT count(*) FROM twin.jobs WHERE organization_id=$1 AND type='model_proposal'`, e.a.ID); n != 0 {
			t.Errorf("%s: %d jobs", c.code, n)
		}
	}
}

// TEST-007 for creation: the same key and body answer the same, however
// many race; another body or model with the key is 409; one proposal and
// one job per accepted request.
func TestProposalIdempotency(t *testing.T) {
	e := setupProposals(t)
	editor, owner := e.a.Members[tenant.Editor], e.a.Members[tenant.Owner]
	m := e.newModel(t)
	other := e.newModelIn(t, "-other")
	body := proposalBody(m.LatestVersion.ID, "edit", "Raise the CI runners to 6.")
	const n = 12
	answers := make([]string, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			r := e.propose(editor.Subject, e.a.ID, m.ID, "prop-idem-0001", body)
			answers[i] = fmt.Sprint(r.status, string(r.body))
		})
	}
	wg.Wait()
	for _, a := range answers[1:] {
		if a != answers[0] || !strings.HasPrefix(a, "202") {
			t.Errorf("a retry answered %s, the first %s", a, answers[0])
		}
	}
	if r := e.propose(editor.Subject, e.a.ID, m.ID, "prop-idem-0001", proposalBody(m.LatestVersion.ID, "scenario", "Raise the CI runners to 6.")); r.status != 409 || code(t, r) != "IDEMPOTENCY_KEY_CONFLICT" {
		t.Errorf("same key, other body: %d %s", r.status, r.body)
	}
	if r := e.propose(editor.Subject, e.a.ID, other.ID, "prop-idem-0001", proposalBody(other.LatestVersion.ID, "edit", "Raise the CI runners to 6.")); r.status != 409 {
		t.Errorf("same key, other model: %d %s", r.status, r.body)
	}
	if r := e.propose(owner.Subject, e.a.ID, m.ID, "prop-idem-0001", body); r.status != 202 {
		t.Errorf("another user's same key: %d %s", r.status, r.body)
	}
	if c := e.count(`SELECT count(*) FROM twin.ai_proposals WHERE organization_id=$1`, e.a.ID); c != 2 {
		t.Errorf("%d proposals, want 2 (one per user)", c)
	}
	if c := e.count(`SELECT count(*) FROM twin.jobs WHERE organization_id=$1 AND type='model_proposal'`, e.a.ID); c != 2 {
		t.Errorf("%d jobs, want 2", c)
	}
}
