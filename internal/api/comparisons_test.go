package api_test

// Integration tests of compareSimulations (T-029) through the whole stack:
// runs created through the API and run by the job loop as the plain worker
// login, compared through the handler cmd/api serves as the plain API login
// (TEST-022 and TEST-023 through the API, TEST-009 for comparisons; D-088,
// D-089 PROPOSED).

import (
	"encoding/json/v2"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/api"
	"github.com/kcl1104/geminus/internal/results/compare"
	"github.com/kcl1104/geminus/internal/runs/runner"
	"github.com/kcl1104/geminus/internal/simulation"
	"github.com/kcl1104/geminus/internal/tenant"
)

const twoServersPatch = `[{"op":"test","path":"/resources/0/id","value":"server"},{"op":"replace","path":"/resources/0/capacity","value":2}]`

func (e *runEnv) compareRuns(subject, org, baseline, scenario string) resp {
	return e.do("GET", "/v1/simulation-comparisons?baseline_run_id="+baseline+"&scenario_run_id="+scenario, subject, org, nil, "")
}

// congested is the run request of the congested-queue golden (SIMULATION_SPEC
// §10: one server, a wait mean of 47.5 minutes) with patch, seed,
// replications, warmup (minutes) and trace level.
func congested(t *testing.T, version, name, patch string, seed, replications, warmup int, trace string) string {
	t.Helper()
	var ops []any
	if err := json.Unmarshal([]byte(patch), &ops); err != nil {
		t.Fatal(err)
	}
	return runRequest(version, replications, trace, map[string]any{"name": name, "scenario_patch": ops, "config": map[string]any{
		"horizon": map[string]any{"value": 205, "unit": "minute"}, "warmup": map[string]any{"value": warmup, "unit": "minute"},
		"replications": replications, "seed": seed, "trace_level": trace}})
}

func (e *runEnv) queued(t *testing.T, subject, org, key, body string) string {
	t.Helper()
	r := e.createRun(subject, org, key, body)
	if r.status != 202 {
		t.Fatalf("create %s: %d %s", key, r.status, r.body)
	}
	return decode[runBody](t, r).ID
}

func (e *runEnv) comparison(t *testing.T, subject, org, baseline, scenario string) (compare.Comparison, resp) {
	t.Helper()
	r := e.compareRuns(subject, org, baseline, scenario)
	if r.status != 200 {
		t.Fatalf("compare %s %s: %d %s", baseline, scenario, r.status, r.body)
	}
	valid(t, r, "http.schema.json#/$defs/Comparison")
	return decode[compare.Comparison](t, r), r
}

func (e *runEnv) result(t *testing.T, subject, org, id string) *simulation.Result {
	t.Helper()
	r := e.do("GET", "/v1/simulations/"+id+"/results", subject, org, nil, "")
	if r.status != 200 {
		t.Fatalf("results %s: %d %s", id, r.status, r.body)
	}
	return decode[*simulation.Result](t, r)
}

// refusedWith checks an error response's status, code and the parameter
// details[0].reason names.
func refusedWith(t *testing.T, r resp, status int, wantCode, param string) {
	t.Helper()
	if r.status != status || code(t, r) != wantCode {
		t.Errorf("%d %s, want %d %s", r.status, r.body, status, wantCode)
		return
	}
	var e struct {
		Error struct {
			Details []struct {
				Path   string `json:"path"`
				Reason string `json:"reason"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(r.body, &e); err != nil || len(e.Error.Details) != 1 || e.Error.Details[0].Reason != param || e.Error.Details[0].Path != "" {
		t.Errorf("%s: details %+v, want the parameter %s", r.body, e.Error.Details, param)
	}
}

// The closed loop (REQ-014, TEST-022): a baseline run and a two-server
// scenario of the congested queue, compared by a viewer once both are
// completed. Every value is the one each run's Result holds at the metric
// path, every delta is recomputed here from those values, a baseline of 0
// has no relative delta, the software-delivery pack's DORA metrics stay
// unavailable (TEST-023), and the answer is the same bytes every time and
// writes no audit event.
func TestComparisonClosedLoop(t *testing.T) {
	e := setupRuns(t)
	editor, viewer, owner := e.a.Members[tenant.Editor], e.a.Members[tenant.Viewer], e.a.Members[tenant.Owner]
	_, mv := e.version(t, e.a, golden(t, "congested-queue.json"), nil)
	base := e.queued(t, editor.Subject, e.a.ID, "cmp-loop-base", congested(t, mv, "baseline", "[]", 1, 1, 0, "none"))
	scen := e.queued(t, editor.Subject, e.a.ID, "cmp-loop-scen", congested(t, mv, "two servers", twoServersPatch, 1, 1, 0, "none"))
	// Not completed yet: 409, the baseline named first (D-089 (b)).
	refusedWith(t, e.compareRuns(viewer.Subject, e.a.ID, base, scen), 409, "RESULT_NOT_READY", "baseline_run_id")
	drain(t, e.loop(t, runner.Config{}, nil), time.Minute)
	audits := e.count(`SELECT count(*) FROM twin.audit_events WHERE organization_id=$1`, e.a.ID)
	c, first := e.comparison(t, viewer.Subject, e.a.ID, base, scen)
	if !c.Comparable || len(c.IncomparableReasons) != 0 || c.BaselineRunID != base || c.ScenarioRunID != scen {
		t.Fatalf("comparison %+v", c)
	}
	if strings.Join(c.Warnings, ",") != compare.WarningSingleReplication {
		t.Errorf("warnings %v", c.Warnings)
	}
	b, s := e.result(t, viewer.Subject, e.a.ID, base), e.result(t, viewer.Subject, e.a.ID, scen)
	stats, err := compare.Statistics(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Metrics) != len(stats) {
		t.Errorf("%d metrics, the baseline has %d statistics", len(c.Metrics), len(stats))
	}
	for _, m := range c.Metrics {
		bv, berr := compare.Resolve(b, m.MetricPath)
		sv, serr := compare.Resolve(s, m.MetricPath)
		if berr != nil || serr != nil || !same(m.Baseline, bv) || !same(m.Scenario, sv) {
			t.Errorf("%s: compared %v %v, results %v %v (%v %v)", m.MetricPath, m.Baseline, m.Scenario, bv, sv, berr, serr)
			continue
		}
		switch {
		case bv == nil || sv == nil:
			if m.Status != compare.Unavailable || m.AbsoluteDelta != nil || m.RelativeDelta != nil {
				t.Errorf("%s: an unavailable value has %+v", m.MetricPath, m)
			}
		case *bv == 0:
			if m.Status != compare.Available || m.AbsoluteDelta == nil || *m.AbsoluteDelta != *sv || m.RelativeDelta != nil ||
				m.Reason == nil || *m.Reason != compare.ReasonBaselineZero {
				t.Errorf("%s: a zero baseline has %+v", m.MetricPath, m)
			}
		default:
			abs := *sv - *bv
			if m.Status != compare.Available || m.Reason != nil || m.AbsoluteDelta == nil || *m.AbsoluteDelta != abs ||
				m.RelativeDelta == nil || *m.RelativeDelta != abs / *bv {
				t.Errorf("%s: %+v, want %v and %v", m.MetricPath, m, abs, abs / *bv)
			}
		}
	}
	waits, ok := c.Metric("/metrics/queues/service/wait_mean_minutes")
	if !ok || waits.Baseline == nil || *waits.Baseline != 47.5 || waits.Scenario == nil || *waits.Scenario >= 47.5 || waits.RelativeDelta == nil || *waits.RelativeDelta >= 0 {
		t.Errorf("queue waits %+v: the baseline is 47.5 minutes (SIMULATION_SPEC §10), two servers wait less", waits)
	}
	dora := 0
	for _, m := range c.Metrics {
		if strings.HasPrefix(m.MetricPath, "/metrics/dora/") {
			dora++
			if m.Status != compare.Unavailable || m.Reason == nil || *m.Reason != compare.ReasonBothUnavailable || m.Baseline != nil || m.Scenario != nil {
				t.Errorf("DORA %+v", m)
			}
		}
	}
	if dora == 0 || dora%3 != 0 {
		t.Errorf("%d DORA statistics", dora)
	}
	if f, ok := c.Metric("/metrics/failure_count/mean"); !ok || f.Reason == nil || *f.Reason != compare.ReasonBaselineZero || f.RelativeDelta != nil {
		t.Errorf("failure count %+v (no failure node: 0 in both)", f)
	}
	// Deterministic: the same bytes for another member, and again.
	for _, subject := range []string{viewer.Subject, editor.Subject, owner.Subject} {
		if _, again := e.comparison(t, subject, e.a.ID, base, scen); string(again.body) != string(first.body) {
			t.Errorf("%s: another answer", subject)
		}
	}
	if n := e.count(`SELECT count(*) FROM twin.audit_events WHERE organization_id=$1`, e.a.ID); n != audits {
		t.Errorf("comparing wrote %d audit events", n-audits)
	}
	// The roles swapped: the "baseline" has a patch, so the runs are not
	// comparable; both values stay side by side, without a delta.
	swapped, _ := e.comparison(t, viewer.Subject, e.a.ID, scen, base)
	if swapped.Comparable || strings.Join(swapped.IncomparableReasons, ",") != compare.BaselinePatchNotEmpty ||
		!strings.Contains(strings.Join(swapped.Warnings, ","), compare.WarningScenarioPatchEmpty) {
		t.Errorf("swapped: %v %v", swapped.IncomparableReasons, swapped.Warnings)
	}
	for _, m := range swapped.Metrics {
		if m.Status != compare.Incomparable || m.AbsoluteDelta != nil || m.RelativeDelta != nil {
			t.Errorf("swapped %+v", m)
		}
	}
	if m, ok := swapped.Metric("/metrics/queues/service/wait_mean_minutes"); !ok || m.Scenario == nil || *m.Scenario != 47.5 {
		t.Errorf("swapped waits %+v", m)
	}
}

func same(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b && math.Signbit(*a) == math.Signbit(*b)
}

// SIMULATION_SPEC §7: another seed, replication count, warmup or model
// version makes the runs incomparable (200, comparable=false, every reason);
// another trace level does not (it changes no metric, §9).
func TestComparisonIncomparable(t *testing.T) {
	e := setupRuns(t)
	editor := e.a.Members[tenant.Editor]
	_, mv := e.version(t, e.a, golden(t, "congested-queue.json"), nil)
	_, other := e.version(t, e.a, golden(t, "congested-queue.json"), nil)
	base := e.queued(t, editor.Subject, e.a.ID, "cmp-inc-base", congested(t, mv, "baseline", "[]", 1, 1, 0, "none"))
	runs := map[string]string{
		"seed":         e.queued(t, editor.Subject, e.a.ID, "cmp-inc-seed", congested(t, mv, "seed 2", twoServersPatch, 2, 1, 0, "none")),
		"replications": e.queued(t, editor.Subject, e.a.ID, "cmp-inc-reps", congested(t, mv, "two replications", twoServersPatch, 1, 2, 0, "none")),
		"warmup":       e.queued(t, editor.Subject, e.a.ID, "cmp-inc-warm", congested(t, mv, "warmup", twoServersPatch, 1, 1, 5, "none")),
		"model":        e.queued(t, editor.Subject, e.a.ID, "cmp-inc-model", congested(t, other, "another model", twoServersPatch, 1, 1, 0, "none")),
		"trace":        e.queued(t, editor.Subject, e.a.ID, "cmp-inc-trace", congested(t, mv, "sampled", twoServersPatch, 1, 1, 0, "sampled")),
	}
	drain(t, e.loop(t, runner.Config{}, nil), time.Minute)
	for name, want := range map[string]string{
		"seed": compare.SeedMismatch, "replications": compare.ReplicationsMismatch, "warmup": compare.WarmupMismatch,
		"model": compare.ModelVersionMismatch, "trace": "",
	} {
		c, _ := e.comparison(t, editor.Subject, e.a.ID, base, runs[name])
		if got := strings.Join(c.IncomparableReasons, ","); got != want || c.Comparable != (want == "") {
			t.Errorf("%s: comparable %v, reasons %q, want %q", name, c.Comparable, got, want)
		}
		for _, m := range c.Metrics {
			if want != "" && (m.Status != compare.Incomparable || m.Reason == nil || *m.Reason != compare.ReasonRunsNotComparable ||
				m.AbsoluteDelta != nil || m.RelativeDelta != nil) {
				t.Errorf("%s: %+v", name, m)
			}
		}
		if len(c.Metrics) == 0 {
			t.Errorf("%s: no metrics", name)
		}
	}
}

// TEST-009 for comparisons: every member compares; a member of another
// organization, an authenticated stranger, and a run of another
// organization see 404, a run of another organization exactly like a run
// that does not exist; an unknown identity is 403; no token is 401.
func TestComparisonRoleMatrix(t *testing.T) {
	e := setupRuns(t)
	owner, editor, viewer := e.a.Members[tenant.Owner], e.a.Members[tenant.Editor], e.a.Members[tenant.Viewer]
	outsider := e.b.Members[tenant.Owner]
	_, mv := e.version(t, e.a, golden(t, "congested-queue.json"), nil)
	_, mvB := e.version(t, e.b, golden(t, "congested-queue.json"), nil)
	base := e.queued(t, editor.Subject, e.a.ID, "cmp-role-base", congested(t, mv, "baseline", "[]", 1, 1, 0, "none"))
	scen := e.queued(t, editor.Subject, e.a.ID, "cmp-role-scen", congested(t, mv, "two servers", twoServersPatch, 1, 1, 0, "none"))
	theirs := e.queued(t, outsider.Subject, e.b.ID, "cmp-role-b", congested(t, mvB, "theirs", twoServersPatch, 1, 1, 0, "none"))
	drain(t, e.loop(t, runner.Config{}, nil), time.Minute)
	for _, s := range []string{owner.Subject, editor.Subject, viewer.Subject} {
		e.comparison(t, s, e.a.ID, base, scen)
	}
	for _, c := range []struct {
		name, subject, org, baseline, scenario string
		status                                 int
		code, param                            string
	}{
		{"member of another organization", outsider.Subject, e.a.ID, base, scen, 404, "NOT_FOUND", ""},
		{"another organization's runs", outsider.Subject, e.b.ID, base, scen, 404, "NOT_FOUND", "baseline_run_id"},
		{"another organization's scenario", viewer.Subject, e.a.ID, base, theirs, 404, "NOT_FOUND", "scenario_run_id"},
		{"another organization's baseline", outsider.Subject, e.b.ID, base, theirs, 404, "NOT_FOUND", "baseline_run_id"},
		{"no such run", viewer.Subject, e.a.ID, base, "sim_01M4YDQK00FGZZWH0PP15XPNFZ", 404, "NOT_FOUND", "scenario_run_id"},
		{"identity without a user", "sub-nobody", e.a.ID, base, scen, 403, "FORBIDDEN", ""},
		{"no token", "", e.a.ID, base, scen, 401, "UNAUTHENTICATED", ""},
	} {
		r := e.compareRuns(c.subject, c.org, c.baseline, c.scenario)
		if c.param != "" {
			refusedWith(t, r, c.status, c.code, c.param)
		} else if r.status != c.status || code(t, r) != c.code {
			t.Errorf("%s: %d %s", c.name, r.status, r.body)
		}
	}
	// Another organization's run and no run at all are refused alike.
	other := e.compareRuns(viewer.Subject, e.a.ID, base, theirs)
	none := e.compareRuns(viewer.Subject, e.a.ID, base, "sim_01M4YDQK00FGZZWH0PP15XPNFZ")
	var x, y struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Details any    `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(other.body, &x); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(none.body, &y); err != nil {
		t.Fatal(err)
	}
	if !equalJSON(t, x, y) {
		t.Errorf("another organization's run %s, no run %s", other.body, none.body)
	}
}

// The request's own checks come first (400 naming the parameter); a run
// that is not completed (queued, cancelled, failed) is 409 naming it, and a
// missing run is 404 before a run that is not ready is 409.
func TestComparisonRefusals(t *testing.T) {
	e := setupRuns(t)
	editor, viewer := e.a.Members[tenant.Editor], e.a.Members[tenant.Viewer]
	_, mv := e.version(t, e.a, golden(t, "congested-queue.json"), nil)
	base := e.queued(t, editor.Subject, e.a.ID, "cmp-ref-base", congested(t, mv, "baseline", "[]", 1, 1, 0, "none"))
	scen := e.queued(t, editor.Subject, e.a.ID, "cmp-ref-scen", congested(t, mv, "two servers", twoServersPatch, 1, 1, 0, "none"))
	for _, c := range []struct {
		query, param string
	}{
		{"scenario_run_id=" + scen, "baseline_run_id"},
		{"baseline_run_id=" + base, "scenario_run_id"},
		{"baseline_run_id=" + base + "&baseline_run_id=" + base + "&scenario_run_id=" + scen, "baseline_run_id"},
		{"baseline_run_id=mv_01M4YDQK00FRYWV7GS7X1QCXZR&scenario_run_id=" + scen, "baseline_run_id"},
		{"baseline_run_id=" + base + "&scenario_run_id=" + base, "scenario_run_id"},
	} {
		refusedWith(t, e.do("GET", "/v1/simulation-comparisons?"+c.query, viewer.Subject, e.a.ID, nil, ""), 400, "INVALID_REQUEST", c.param)
	}
	// Both queued: the baseline first; a missing scenario before a baseline
	// that is not ready.
	refusedWith(t, e.compareRuns(viewer.Subject, e.a.ID, base, scen), 409, "RESULT_NOT_READY", "baseline_run_id")
	refusedWith(t, e.compareRuns(viewer.Subject, e.a.ID, base, "sim_01M4YDQK00FGZZWH0PP15XPNFZ"), 404, "NOT_FOUND", "scenario_run_id")
	cancelled := e.queued(t, editor.Subject, e.a.ID, "cmp-ref-cancel", congested(t, mv, "cancelled", twoServersPatch, 1, 1, 0, "none"))
	if r := e.cancelRun(editor.Subject, e.a.ID, "cmp-ref-cancel-key", cancelled); r.status != 200 {
		t.Fatalf("cancel: %d %s", r.status, r.body)
	}
	drain(t, e.loop(t, runner.Config{}, nil), time.Minute)
	e.comparison(t, viewer.Subject, e.a.ID, base, scen)
	refusedWith(t, e.compareRuns(viewer.Subject, e.a.ID, base, cancelled), 409, "RESULT_NOT_READY", "scenario_run_id")
	refusedWith(t, e.compareRuns(viewer.Subject, e.a.ID, cancelled, scen), 409, "RESULT_NOT_READY", "baseline_run_id")

	// Failed runs (an event budget too small, as TestRunFailures): 409.
	f := setupRuns(t, api.WithRunLimits(simulation.ExecutionLimits{WallTimeoutSeconds: 300, MaxEvents: 150, MaxActiveEntities: 100_000,
		MaxEntityVisits: 10_000, MaxTraceEvents: 100_000}))
	fe := f.a.Members[tenant.Editor]
	_, fmv := f.version(t, f.a, golden(t, "deterministic-queue.json"), nil)
	one := f.queued(t, fe.Subject, f.a.ID, "cmp-fail-1", runRequest(fmv, 2, "none", nil))
	two := f.queued(t, fe.Subject, f.a.ID, "cmp-fail-2", runRequest(fmv, 2, "none", map[string]any{"name": "again"}))
	drain(t, f.loop(t, runner.Config{}, nil), 30*time.Second)
	if run := f.getRun(t, fe.Subject, f.a.ID, one); run.Status != "failed" {
		t.Fatalf("run %+v did not fail", run)
	}
	refusedWith(t, f.compareRuns(fe.Subject, f.a.ID, one, two), 409, "RESULT_NOT_READY", "baseline_run_id")
}
