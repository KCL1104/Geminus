package api_test

// The AI investigation routes through the whole stack (H-061 slice A7;
// TEST-009, TEST-010, TEST-011, REQ-019): the handler cmd/api serves, a TLS
// test OIDC provider and the disposable database as the plain API login.
// Off (the default), every route answers 503 and changes nothing; on, a
// person's own requests start, read, list and stop an investigation, with
// the contract's answers and refusals.

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/kcl1104/geminus/internal/api"
	"github.com/kcl1104/geminus/internal/auth"
	"github.com/kcl1104/geminus/internal/auth/authtest"
	"github.com/kcl1104/geminus/internal/proposals"
	"github.com/kcl1104/geminus/internal/storage/dbtest"
	"github.com/kcl1104/geminus/internal/tenant"
)

// setupServing is setup for routes whose options name the organizations:
// the organizations first, then the handler built with serve(a, b)'s
// options (one handler per call of serve: rebuild for other settings).
type serving struct {
	*env
	verifier auth.Verifier
	db       *tenant.DB
}

func setupServing(t *testing.T) *serving {
	t.Helper()
	admin := dbtest.Admin(t)
	p := authtest.New(t)
	v, err := auth.NewOIDCVerifier(p.Config())
	if err != nil {
		t.Fatal(err)
	}
	pool := dbtest.Pool(t, dbtest.LoginURL(t, dbtest.APILogin), 8, true)
	if err := tenant.CheckRuntimeRole(context.Background(), pool, tenant.APIRole); err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, p: p, admin: admin, logs: &bytes.Buffer{}, logsMu: &sync.Mutex{}}
	e.a = dbtest.NewOrg(t, admin, p.Issuer, tenant.Owner, tenant.Editor, tenant.Viewer)
	e.b = dbtest.NewOrg(t, admin, p.Issuer, tenant.Owner)
	return &serving{env: e, verifier: v, db: tenant.New(pool)}
}

// serve builds the handler with the usual AI options and opts, and returns
// an env on it (the same organizations, database and identities).
func (s *serving) serve(opts ...api.Option) *env {
	s.t.Helper()
	log := slog.New(slog.NewJSONHandler(lockedWriter{s.logs, s.logsMu}, nil))
	h, err := api.Handler(log, s.verifier, s.db, opts...)
	if err != nil {
		s.t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	s.t.Cleanup(srv.Close)
	e := *s.env
	e.srv = srv
	return &e
}

// withAI are the AI options the investigation tests serve with (the fake's).
func withAI(opts ...api.Option) []api.Option {
	return append([]api.Option{api.WithAI(proposals.Available), api.WithResearchAI(proposals.Available),
		api.WithProposalModels(proposals.ProposalModels, nil)}, opts...)
}

func scenarioBody(base string) string {
	return `{"base_version_id":"` + base + `","mode":"scenario","intent":"Find a faster pipeline","output_locale":"en",` +
		`"run_config":{"horizon":{"value":1,"unit":"day"},"warmup":{"value":0,"unit":"day"},"replications":1,"seed":7,"trace_level":"none"},` +
		`"baseline_name":"Baseline for the AI"}`
}

// investigationModel is a model of org a made by its editor through the
// routes, with its first version.
func (e *env) investigationModel(t *testing.T) (modelID, base string) {
	t.Helper()
	owner, editor := e.a.Members[tenant.Owner], e.a.Members[tenant.Editor]
	prj := decode[project](t, e.create(owner.Subject, e.a.ID, "key-inv-prj-"+t.Name(), `{"name":"Investigations"}`))
	r := e.do("POST", "/v1/projects/"+prj.ID+"/models", editor.Subject, e.a.ID, map[string]string{"Idempotency-Key": "key-inv-mdl-" + t.Name()}, createModelBody)
	if r.status != 201 {
		t.Fatalf("create model: %d %s", r.status, r.body)
	}
	m := decode[modelSummary](t, r)
	return m.ID, m.LatestVersion.ID
}

func (e *env) startInvestigation(subject, org, modelID, key, body string) resp {
	return e.do("POST", "/v1/models/"+modelID+"/investigations", subject, org, map[string]string{"Idempotency-Key": key}, body)
}

func (e *env) stopInvestigation(subject, org, id, key string) resp {
	return e.do("POST", "/v1/investigations/"+id+"/stop", subject, org, map[string]string{"Idempotency-Key": key}, `{}`)
}

// detail is an error's first detail code and path.
func detail(t *testing.T, r resp) (code, path string) {
	t.Helper()
	d := decode[struct {
		Error struct {
			Details []struct {
				Path string `json:"path"`
				Code string `json:"code"`
			} `json:"details"`
		} `json:"error"`
	}](t, r)
	if len(d.Error.Details) == 0 {
		return "", ""
	}
	return d.Error.Details[0].Code, d.Error.Details[0].Path
}

func on(org string, more ...func(*api.Investigations)) api.Option {
	i := api.Investigations{On: true, Organizations: []string{org}, PlannerModels: []string{"muse-spark-1.3"}, InvestigatorApproved: true,
		ApprovedPlanners: []string{"muse-spark-1.3"}, DefaultProposalModel: "muse-spark-1.3"}
	for _, f := range more {
		f(&i)
	}
	return api.WithInvestigations(i)
}

// Off (GEMINUS_INVESTIGATIONS unset), every investigation route answers 503
// SERVICE_UNAVAILABLE with INVESTIGATIONS_OFF, whatever the body, and
// nothing is stored.
func TestInvestigationsAreOffByDefault(t *testing.T) {
	e := setup(t, api.WithAI(proposals.Available))
	editor := e.a.Members[tenant.Editor]
	modelID, base := e.investigationModel(t)
	inv := "inv_01M4YDQK00B8S5WQXJ3TQ7ZK2A"
	for _, r := range []resp{
		e.startInvestigation(editor.Subject, e.a.ID, modelID, "key-off-1", scenarioBody(base)),
		e.startInvestigation(editor.Subject, e.a.ID, modelID, "key-off-2", `{"not":"a request"}`),
		e.do("GET", "/v1/models/"+modelID+"/investigations", editor.Subject, e.a.ID, nil, ""),
		e.do("GET", "/v1/investigations/"+inv, editor.Subject, e.a.ID, nil, ""),
		e.stopInvestigation(editor.Subject, e.a.ID, inv, "key-off-3"),
	} {
		if r.status != 503 || code(t, r) != "SERVICE_UNAVAILABLE" {
			t.Errorf("off: %d %s", r.status, r.body)
		}
		if c, _ := detail(t, r); c != "INVESTIGATIONS_OFF" {
			t.Errorf("off: detail %s", c)
		}
	}
	if n := e.countInvestigations(t); n != 0 {
		t.Errorf("%d investigations stored while off", n)
	}
}

func (e *env) countInvestigations(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.admin.QueryRow(t.Context(), `SELECT count(*) FROM twin.investigations WHERE organization_id IN ($1, $2)`, e.a.ID, e.b.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// On: an editor starts one (202, its job and its event), replays it, cannot
// start a second; any member reads and lists it; another organization does
// not find it; a viewer or another editor cannot stop it, the requester
// can, once.
func TestInvestigationRoutes(t *testing.T) {
	srv := setupServing(t)
	// Investigations serve organization a only.
	e := srv.serve(withAI(on(srv.a.ID))...)
	owner, editor, viewer := e.a.Members[tenant.Owner], e.a.Members[tenant.Editor], e.a.Members[tenant.Viewer]
	other := dbtest.NewUser(t, e.admin, e.p.Issuer)
	dbtest.Join(t, e.admin, e.a.ID, other.UserID, tenant.Editor)
	modelID, base := e.investigationModel(t)

	// Refusals first: nothing is stored for any of them.
	if r := e.startInvestigation(viewer.Subject, e.a.ID, modelID, "key-inv-viewer", scenarioBody(base)); r.status != 403 || code(t, r) != "FORBIDDEN" {
		t.Errorf("a viewer: %d %s", r.status, r.body)
	}
	if r := e.startInvestigation(e.b.Members[tenant.Owner].Subject, e.b.ID, modelID, "key-inv-b", scenarioBody(base)); r.status != 503 {
		t.Errorf("an organization not served: %d %s", r.status, r.body)
	}
	wonder := strings.Replace(scenarioBody(base), `"output_locale":"en",`, `"output_locale":"en","planner_model":"muse-wonder-1.0",`, 1)
	if r := e.startInvestigation(editor.Subject, e.a.ID, modelID, "key-inv-wonder", wonder); r.status != 422 || code(t, r) != "REQUEST_VALIDATION_FAILED" {
		t.Errorf("a planner not offered: %d %s", r.status, r.body)
	} else if c, p := detail(t, r); c != "INVESTIGATION_PLANNER_NOT_OFFERED" || p != "/planner_model" {
		t.Errorf("a planner not offered: %s at %s", c, p)
	}
	research := strings.Replace(scenarioBody(base), `"output_locale":"en",`, `"output_locale":"en","research":true,"model":"muse-wonder-1.0",`, 1)
	if r := e.startInvestigation(editor.Subject, e.a.ID, modelID, "key-inv-research", research); r.status != 422 {
		t.Errorf("Muse Wonder research: %d %s", r.status, r.body)
	} else if c, p := detail(t, r); c != "INVESTIGATION_RESEARCH_OVER_CEILING" || p != "/research" {
		t.Errorf("Muse Wonder research: %s at %s", c, p)
	}
	editWithRuns := strings.Replace(scenarioBody(base), `"mode":"scenario"`, `"mode":"edit"`, 1)
	if r := e.startInvestigation(editor.Subject, e.a.ID, modelID, "key-inv-edit-runs", editWithRuns); r.status != 422 {
		t.Errorf("an edit with run settings: %d %s", r.status, r.body)
	}
	warm := strings.Replace(scenarioBody(base), `"warmup":{"value":0,"unit":"day"}`, `"warmup":{"value":1,"unit":"day"}`, 1)
	if r := e.startInvestigation(editor.Subject, e.a.ID, modelID, "key-inv-warmup", warm); r.status != 422 {
		t.Errorf("a warmup as long as the horizon: %d %s", r.status, r.body)
	} else if _, p := detail(t, r); p != "/run_config/warmup" {
		t.Errorf("a warmup as long as the horizon: at %s", p)
	}
	if n := e.countInvestigations(t); n != 0 {
		t.Fatalf("%d investigations stored by refusals", n)
	}

	// Started.
	r := e.startInvestigation(editor.Subject, e.a.ID, modelID, "key-inv-1", scenarioBody(base))
	if r.status != 202 {
		t.Fatalf("start: %d %s", r.status, r.body)
	}
	valid(t, r, "investigation.schema.json#/$defs/InvestigationAccepted")
	acc := decode[struct {
		ID     string `json:"investigation_id"`
		Status string `json:"status"`
	}](t, r)
	if again := e.startInvestigation(editor.Subject, e.a.ID, modelID, "key-inv-1", scenarioBody(base)); again.status != 202 || string(again.body) != string(r.body) {
		t.Errorf("replay: %d %s", again.status, again.body)
	}
	otherIntent := strings.Replace(scenarioBody(base), "Find a faster pipeline", "Find a cheaper pipeline", 1)
	if conflict := e.startInvestigation(editor.Subject, e.a.ID, modelID, "key-inv-1", otherIntent); conflict.status != 409 ||
		code(t, conflict) != "IDEMPOTENCY_KEY_CONFLICT" {
		t.Errorf("the key with another body: %d %s", conflict.status, conflict.body)
	}
	second := e.startInvestigation(editor.Subject, e.a.ID, modelID, "key-inv-2", scenarioBody(base))
	if second.status != 409 || code(t, second) != "LIFECYCLE_CONFLICT" {
		t.Errorf("a second running: %d %s", second.status, second.body)
	} else if c, _ := detail(t, second); c != "INVESTIGATION_RUNNING" {
		t.Errorf("a second running: %s", c)
	}
	var jobs, events int
	if err := e.admin.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM twin.jobs WHERE investigation_id=$1 AND type='investigation' AND max_attempts=5),
		(SELECT count(*) FROM twin.audit_events WHERE target_id=$1 AND action='investigation.create' AND actor_id=$2 AND NOT metadata ? 'intent')`,
		acc.ID, editor.UserID).Scan(&jobs, &events); err != nil || jobs != 1 || events != 1 {
		t.Errorf("job %d, events %d, %v", jobs, events, err)
	}

	// Read and listed by any member, in its organization only.
	g := e.do("GET", "/v1/investigations/"+acc.ID, viewer.Subject, e.a.ID, nil, "")
	if g.status != 200 {
		t.Fatalf("get: %d %s", g.status, g.body)
	}
	valid(t, g, "investigation.schema.json#/$defs/Investigation")
	l := e.do("GET", "/v1/models/"+modelID+"/investigations", viewer.Subject, e.a.ID, nil, "")
	if l.status != 200 || !strings.Contains(string(l.body), acc.ID) {
		t.Fatalf("list: %d %s", l.status, l.body)
	}
	valid(t, l, "investigation.schema.json#/$defs/Investigations")
	if r := e.do("GET", "/v1/investigations/"+acc.ID, e.b.Members[tenant.Owner].Subject, e.b.ID, nil, ""); r.status != 503 && r.status != 404 {
		t.Errorf("another organization: %d %s", r.status, r.body)
	}
	if r := e.do("GET", "/v1/investigations/not-an-id", viewer.Subject, e.a.ID, nil, ""); r.status != 400 {
		t.Errorf("a malformed ID: %d", r.status)
	}

	// Stopped: never by a viewer or another editor; by the requester once.
	if r := e.stopInvestigation(viewer.Subject, e.a.ID, acc.ID, "key-stop-v"); r.status != 403 {
		t.Errorf("a viewer's stop: %d %s", r.status, r.body)
	}
	if r := e.stopInvestigation(other.Subject, e.a.ID, acc.ID, "key-stop-o"); r.status != 403 || code(t, r) != "FORBIDDEN" {
		t.Errorf("another editor's stop: %d %s", r.status, r.body)
	}
	s := e.stopInvestigation(editor.Subject, e.a.ID, acc.ID, "key-stop-1")
	if s.status != 200 {
		t.Fatalf("stop: %d %s", s.status, s.body)
	}
	valid(t, s, "investigation.schema.json#/$defs/Investigation")
	if !strings.Contains(string(s.body), `"reason":"stopped_by_person"`) || !strings.Contains(string(s.body), `"by":"`+editor.UserID+`"`) {
		t.Errorf("stopped: %s", s.body)
	}
	// Its queued job was cancelled with it, and investigation.stop names
	// the requester (not an owner's stop), with a count.
	var job string
	var stops int
	if err := e.admin.QueryRow(t.Context(), `SELECT (SELECT status FROM twin.jobs WHERE investigation_id=$1),
		(SELECT count(*) FROM twin.audit_events WHERE target_id=$1 AND action='investigation.stop' AND actor_id=$2
		   AND metadata->>'reason'='stopped_by_person' AND NOT metadata ? 'by_owner' AND metadata ? 'steps')`, acc.ID, editor.UserID).Scan(&job, &stops); err != nil ||
		job != "cancelled" || stops != 1 {
		t.Errorf("the stop's job %q, %d events, %v", job, stops, err)
	}
	if again := e.stopInvestigation(editor.Subject, e.a.ID, acc.ID, "key-stop-1"); again.status != 200 || string(again.body) != string(s.body) {
		t.Errorf("stop replay: %d", again.status)
	}
	if ended := e.stopInvestigation(owner.Subject, e.a.ID, acc.ID, "key-stop-2"); ended.status != 409 || code(t, ended) != "LIFECYCLE_CONFLICT" {
		t.Errorf("a stop of an ended one: %d %s", ended.status, ended.body)
	}

	// An owner may stop another person's investigation.
	r = e.startInvestigation(other.Subject, e.a.ID, modelID, "key-inv-other", scenarioBody(base))
	if r.status != 202 {
		t.Fatalf("another editor starts: %d %s", r.status, r.body)
	}
	byOther := decode[struct {
		ID string `json:"investigation_id"`
	}](t, r)
	if s := e.stopInvestigation(owner.Subject, e.a.ID, byOther.ID, "key-stop-owner"); s.status != 200 || !strings.Contains(string(s.body), `"by":"`+owner.UserID+`"`) {
		t.Errorf("an owner's stop: %d %s", s.status, s.body)
	}

	// The base moved: 409 at /base_version_id.
	stale := e.do("GET", "/v1/models/"+modelID+"/draft", editor.Subject, e.a.ID, nil, "")
	if stale.status != 200 {
		t.Fatal(stale.status)
	}
	if p := e.patch(editor.Subject, modelID, stale.header.Get("ETag"), `[{"op":"replace","path":"/name","value":"Moved"}]`); p.status != 200 {
		t.Fatalf("patch: %d %s", p.status, p.body)
	}
	pub := fmt.Sprintf(`{"expected_base_version_id":%q}`, base)
	if p := e.publish(editor.Subject, modelID, "key-inv-pub", `"draft-revision-2"`, pub); p.status != 201 {
		t.Fatalf("publish: %d %s", p.status, p.body)
	}
	if r := e.startInvestigation(editor.Subject, e.a.ID, modelID, "key-inv-stale", scenarioBody(base)); r.status != 409 || code(t, r) != "MODEL_VERSION_CONFLICT" {
		t.Errorf("a stale base: %d %s", r.status, r.body)
	} else if _, p := detail(t, r); p != "/base_version_id" {
		t.Errorf("a stale base at %s", p)
	}
}

// The 11th investigation of an organization's UTC day is 429 RATE_LIMITED
// with Retry-After; a deployment whose approval lacks the investigator role
// answers 503 AI_POLICY_NOT_APPROVED, and one without AI 503
// AI_PROVIDER_NOT_CONFIGURED.
func TestInvestigationLimitsAndApproval(t *testing.T) {
	s := setupServing(t)
	e := s.serve(withAI(on(s.a.ID))...)
	editor := e.a.Members[tenant.Editor]
	modelID, base := e.investigationModel(t)
	for i := 1; i <= 10; i++ {
		r := e.startInvestigation(editor.Subject, e.a.ID, modelID, "key-day-"+strconv.Itoa(i), scenarioBody(base))
		if r.status != 202 {
			t.Fatalf("investigation %d: %d %s", i, r.status, r.body)
		}
		id := decode[struct {
			ID string `json:"investigation_id"`
		}](t, r).ID
		if s := e.stopInvestigation(editor.Subject, e.a.ID, id, "key-day-stop-"+strconv.Itoa(i)); s.status != 200 {
			t.Fatalf("stop %d: %d %s", i, s.status, s.body)
		}
	}
	r := e.startInvestigation(editor.Subject, e.a.ID, modelID, "key-day-11", scenarioBody(base))
	if r.status != 429 || code(t, r) != "RATE_LIMITED" {
		t.Fatalf("the 11th: %d %s", r.status, r.body)
	}
	if c, _ := detail(t, r); c != "INVESTIGATION_DAILY_LIMIT" {
		t.Errorf("the 11th: %s", c)
	}
	if ra, err := strconv.Atoi(r.header.Get("Retry-After")); err != nil || ra < 1 || ra > 86400 {
		t.Errorf("Retry-After %q", r.header.Get("Retry-After"))
	}

	unapproved := s.serve(withAI(on(s.a.ID, func(i *api.Investigations) { i.InvestigatorApproved, i.ApprovedPlanners = false, nil }))...)
	r = unapproved.startInvestigation(editor.Subject, e.a.ID, modelID, "key-unapproved", scenarioBody(base))
	if r.status != 503 || code(t, r) != "AI_POLICY_NOT_APPROVED" {
		t.Errorf("no investigator role: %d %s", r.status, r.body)
	} else if c, _ := detail(t, r); c != "AI_ROLE_NOT_APPROVED" {
		t.Errorf("no investigator role: %s", c)
	}
	noAI := s.serve(on(s.a.ID))
	r = noAI.startInvestigation(editor.Subject, e.a.ID, modelID, "key-no-ai", scenarioBody(base))
	if r.status != 503 || code(t, r) != "AI_PROVIDER_NOT_CONFIGURED" {
		t.Errorf("no AI: %d %s", r.status, r.body)
	}
}
