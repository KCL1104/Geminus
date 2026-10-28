package api_test

// Integration tests of createAnalysis and getAnalysis (T-030) through the
// whole stack: runs created through the API and run by the job loop as the
// plain worker login, analyses asked for through the handler cmd/api
// serves as the plain API login and written by the analysis job handler
// with the deterministic fake provider (TEST-024's server side, TEST-009
// for analyses; AI_SPEC §9, D-026, migration 0018).

import (
	"encoding/json/v2"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/analyses/analyzer"
	"github.com/kcl1104/geminus/internal/api"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/domainpack/softwaredelivery"
	"github.com/kcl1104/geminus/internal/proposals"
	"github.com/kcl1104/geminus/internal/runs/runner"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/worker"
	"github.com/kcl1104/geminus/internal/worker/handler"
)

type analysisView struct {
	ID            string  `json:"id"`
	RunID         string  `json:"run_id"`
	BaselineRunID *string `json:"baseline_run_id"`
	Status        string  `json:"status"`
	Analysis      *struct {
		Baseline *struct {
			Summary string `json:"summary"`
		} `json:"baseline"`
		Scenario struct {
			Summary string `json:"summary"`
		} `json:"scenario"`
		Delta []any `json:"delta"`
	} `json:"analysis"`
	Error *struct {
		Error struct {
			Code    string `json:"code"`
			Details []struct {
				Code string `json:"code"`
			} `json:"details"`
		} `json:"error"`
	} `json:"error"`
}

// analysisLoop is a job loop with the analysis handler over the fake f.
func (e *runEnv) analysisLoop(t *testing.T, f *fake.Backend) *worker.Worker {
	t.Helper()
	client, err := provider.New(f, provider.DefaultBudget())
	if err != nil {
		t.Fatal(err)
	}
	packs, err := domainpack.NewRegistry(softwaredelivery.Pack{})
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h, err := analyzer.New(analyzer.Config{Provider: client, Packs: packs, Actor: "test-worker", Log: log})
	if err != nil {
		t.Fatal(err)
	}
	w, err := worker.New(e.scope, worker.Config{ID: "test-worker", Policy: fastPolicy, Heartbeat: time.Second, Idle: 20 * time.Millisecond,
		ReapLimit: 10, Handlers: map[jobs.Type]handler.Handler{jobs.Analysis: h}, Log: log})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func (e *runEnv) analyze(subject, org, key, run, body string) resp {
	return e.do("POST", "/v1/simulations/"+run+"/analyses", subject, org, map[string]string{"Idempotency-Key": key}, body)
}

func (e *runEnv) analysis(t *testing.T, subject, org, id string) analysisView {
	t.Helper()
	r := e.do("GET", "/v1/analyses/"+id, subject, org, nil, "")
	if r.status != 200 {
		t.Fatalf("get %s: %d %s", id, r.status, r.body)
	}
	valid(t, r, "http.schema.json#/$defs/AnalysisRecord")
	return decode[analysisView](t, r)
}

// completedPair is a baseline run and a two-server scenario of the
// congested queue, both run to completion.
func (e *runEnv) completedPair(t *testing.T, prefix string) (version, base, scen string) {
	t.Helper()
	editor := e.a.Members[tenant.Editor]
	_, mv := e.version(t, e.a, golden(t, "congested-queue.json"), nil)
	base = e.queued(t, editor.Subject, e.a.ID, prefix+"-base", congested(t, mv, "baseline", "[]", 1, 1, 0, "none"))
	scen = e.queued(t, editor.Subject, e.a.ID, prefix+"-scen", congested(t, mv, "two servers", twoServersPatch, 1, 1, 0, "none"))
	drain(t, e.loop(t, runner.Config{}, nil), time.Minute)
	return mv, base, scen
}

// The closed loop: an editor asks for an analysis of a completed scenario
// run beside its baseline; a viewer reads it generating, then completed,
// with the runs bound by the server; the request is idempotent; the audit
// names the analysis and its call, never the question.
func TestAnalysisClosedLoop(t *testing.T) {
	e := setupRuns(t, api.WithAI(proposals.Available))
	editor, viewer := e.a.Members[tenant.Editor], e.a.Members[tenant.Viewer]
	_, base, scen := e.completedPair(t, "ana-loop")
	body := `{"baseline_run_id":"` + base + `","question":"Why is the queue shorter?","output_locale":"zh-Hant"}`
	r := e.analyze(editor.Subject, e.a.ID, "ana-loop-1", scen, body)
	if r.status != 202 {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	valid(t, r, "http.schema.json#/$defs/AnalysisAccepted")
	accepted := decode[struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}](t, r)
	if accepted.Status != "generating" {
		t.Fatalf("accepted %+v", accepted)
	}
	// The same request again: the same answer; another body under the key: 409.
	if again := e.analyze(editor.Subject, e.a.ID, "ana-loop-1", scen, body); again.status != 202 || string(again.body) != string(r.body) {
		t.Errorf("replay: %d %s", again.status, again.body)
	}
	if other := e.analyze(editor.Subject, e.a.ID, "ana-loop-1", scen, `{"question":"Other?"}`); other.status != 409 || code(t, other) != "IDEMPOTENCY_KEY_CONFLICT" {
		t.Errorf("another body under the key: %d %s", other.status, other.body)
	}
	if v := e.analysis(t, viewer.Subject, e.a.ID, accepted.ID); v.Status != "generating" || v.Analysis != nil || v.Error != nil {
		t.Errorf("generating: %+v", v)
	}
	drain(t, e.analysisLoop(t, fake.New()), time.Minute)
	v := e.analysis(t, viewer.Subject, e.a.ID, accepted.ID)
	if v.Status != "completed" || v.RunID != scen || v.BaselineRunID == nil || *v.BaselineRunID != base || v.Analysis == nil || v.Error != nil {
		t.Fatalf("completed: %+v", v)
	}
	// The fake answers in the language asked for, with a baseline summary.
	if v.Analysis.Baseline == nil || v.Analysis.Scenario.Summary != "測試用的確定性回答器不對結果做任何判斷。" {
		t.Errorf("analysis %+v", v.Analysis)
	}
	if n := e.count(`SELECT count(*) FROM twin.audit_events WHERE organization_id=$1 AND target_id=$2 AND action IN ('analysis.create','analysis.generate')`,
		e.a.ID, accepted.ID); n != 2 {
		t.Errorf("%d audit events of the analysis", n)
	}
	if n := e.count(`SELECT count(*) FROM twin.audit_events WHERE organization_id=$1 AND metadata::text LIKE '%queue shorter%'`, e.a.ID); n != 0 {
		t.Errorf("the question is in %d audit events", n)
	}
	// Another tenant sees nothing of it.
	other := e.b.Members[tenant.Owner]
	if r := e.do("GET", "/v1/analyses/"+accepted.ID, other.Subject, e.b.ID, nil, ""); r.status != 404 {
		t.Errorf("another tenant: %d %s", r.status, r.body)
	}
	if r := e.analyze(other.Subject, e.b.ID, "ana-loop-b", scen, `{}`); r.status != 404 {
		t.Errorf("another tenant's run: %d %s", r.status, r.body)
	}
}

// An answer that invents a delta is never stored: the job fails for good
// and the analysis follows it, with the failure's code in the details.
func TestAnUngroundedAnalysisFails(t *testing.T) {
	e := setupRuns(t, api.WithAI(proposals.Available))
	editor := e.a.Members[tenant.Editor]
	_, base, scen := e.completedPair(t, "ana-bad")
	invented, err := json.Marshal(provider.AnalysisOutput{
		Baseline: &provider.AnalysisSummary{Summary: "Baseline."}, Scenario: provider.AnalysisSummary{Summary: "Throughput rose by 4242.5."},
		Delta: []provider.AnalysisDelta{}, Bottleneck: []provider.Bottleneck{}, Assumptions: []string{}, Uncertainty: []string{},
		SuggestedNextExperiments: []provider.Experiment{}})
	if err != nil {
		t.Fatal(err)
	}
	f := fake.New()
	f.Script(provider.RoleAnalyst, "Invent something", invented)
	r := e.analyze(editor.Subject, e.a.ID, "ana-bad-1", scen, `{"baseline_run_id":"`+base+`","question":"Invent something"}`)
	if r.status != 202 {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	id := decode[struct {
		ID string `json:"id"`
	}](t, r).ID
	drain(t, e.analysisLoop(t, f), time.Minute)
	v := e.analysis(t, editor.Subject, e.a.ID, id)
	if v.Status != "failed" || v.Analysis != nil || v.Error == nil || v.Error.Error.Code != "INTERNAL_ERROR" ||
		len(v.Error.Error.Details) != 1 || v.Error.Error.Details[0].Code != analyzer.Ungrounded {
		t.Fatalf("an ungrounded answer: %+v %+v", v, v.Error)
	}
	if n := e.count(`SELECT count(*) FROM twin.audit_events WHERE organization_id=$1 AND target_id=$2 AND action='analysis.attempt_failed'
		AND metadata->>'code'='ANALYSIS_UNGROUNDED' AND (metadata->>'issues')::int = 1`, e.a.ID, id); n != 1 {
		t.Errorf("%d attempt_failed events", n)
	}
}

// What is refused, before anything is stored: roles, runs that are not
// there or not completed, a baseline that is the same run or of another
// model, a question of white space, AI that is off.
func TestAnalysisRefusals(t *testing.T) {
	e := setupRuns(t, api.WithAI(proposals.Available))
	editor, viewer := e.a.Members[tenant.Editor], e.a.Members[tenant.Viewer]
	_, _, scen := e.completedPair(t, "ana-ref")
	_, otherBase, _ := e.completedPair(t, "ana-ref-other") // another model's
	_, mv2 := e.version(t, e.a, golden(t, "congested-queue.json"), nil)
	queuedRun := e.queued(t, editor.Subject, e.a.ID, "ana-ref-queued", congested(t, mv2, "queued", "[]", 1, 1, 0, "none"))
	missing := "sim_01M4YDQK00FGZZWH0PP15XPNFZ"
	for _, c := range []struct {
		name, subject, run, body string
		status                   int
		code, reason             string
	}{
		{"a viewer", viewer.Subject, scen, `{}`, 403, "FORBIDDEN", ""},
		{"no such run", editor.Subject, missing, `{}`, 404, "NOT_FOUND", "id"},
		{"no such baseline", editor.Subject, scen, `{"baseline_run_id":"` + missing + `"}`, 404, "NOT_FOUND", "/baseline_run_id"},
		{"a run not completed", editor.Subject, queuedRun, `{}`, 409, "RESULT_NOT_READY", "id"},
		{"a baseline not completed", editor.Subject, scen, `{"baseline_run_id":"` + queuedRun + `"}`, 409, "RESULT_NOT_READY", "/baseline_run_id"},
		{"a baseline of another model", editor.Subject, scen, `{"baseline_run_id":"` + otherBase + `"}`, 422, "REQUEST_VALIDATION_FAILED", ""},
		{"the same run", editor.Subject, scen, `{"baseline_run_id":"` + scen + `"}`, 422, "REQUEST_VALIDATION_FAILED", ""},
		{"white space", editor.Subject, scen, `{"question":"   "}`, 422, "REQUEST_VALIDATION_FAILED", ""},
		{"a language the contract lacks", editor.Subject, scen, `{"output_locale":"fr"}`, 422, "REQUEST_VALIDATION_FAILED", ""},
		{"a run ID of another kind", editor.Subject, "mdl_01M4YDQK00FGZZWH0PP15XPNFZ", `{}`, 400, "INVALID_REQUEST", ""},
	} {
		r := e.analyze(c.subject, e.a.ID, "ana-ref-"+strings.ReplaceAll(c.name, " ", "-"), c.run, c.body)
		if c.reason != "" {
			refusedWith(t, r, c.status, c.code, c.reason)
		} else if r.status != c.status || code(t, r) != c.code {
			t.Errorf("%s: %d %s, want %d %s", c.name, r.status, r.body, c.status, c.code)
		}
	}
	if n := e.count(`SELECT count(*) FROM twin.ai_analyses WHERE organization_id=$1`, e.a.ID); n != 0 {
		t.Errorf("%d analyses stored by refused requests", n)
	}
	// AI off: 503, nothing stored.
	off := setupRuns(t)
	_, _, s := off.completedPair(t, "ana-off")
	r := off.analyze(off.a.Members[tenant.Editor].Subject, off.a.ID, "ana-off-1", s, `{}`)
	if r.status != 503 || code(t, r) != "AI_PROVIDER_NOT_CONFIGURED" {
		t.Errorf("AI off: %d %s", r.status, r.body)
	}
}
