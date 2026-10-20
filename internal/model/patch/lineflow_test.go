package patch

import (
	"encoding/json/v2"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// valid-line-flow-ir.json: nodes 0 assemble, 1 burn_in (batch), 3 q_burn_in
// and 4 q_line (finite queues), 5–7 sources with schedules; resources
// 1 line (calendar, shift_capacity, breakdowns, setups) and 2 operator
// (calendar); calendar 0 two_shift.
func lineFlow(t *testing.T) []byte { return fixture(t, "valid-line-flow-ir.json") }

const (
	testQLine    = `{"op":"test","path":"/nodes/4/id","value":"q_line"},`
	testBurnIn   = `{"op":"test","path":"/nodes/1/id","value":"burn_in"},`
	testAssemble = `{"op":"test","path":"/nodes/0/id","value":"assemble"},`
	testReleaseA = `{"op":"test","path":"/nodes/5/id","value":"release_a"},`
	testReleaseB = `{"op":"test","path":"/nodes/6/id","value":"release_b"},`
	testChamber  = `{"op":"test","path":"/resources/0/id","value":"chamber"},`
	testLine     = `{"op":"test","path":"/resources/1/id","value":"line"},`
	testOperator = `{"op":"test","path":"/resources/2/id","value":"operator"},`
	testCalendar = `{"op":"test","path":"/calendars/0/id","value":"two_shift"},`
)

// gated reports whether the candidate is refused only because it uses a
// line-flow primitive this build does not implement (D-063 PROPOSED).
func gated(r Result) bool {
	return !r.Validation.Valid() && len(r.Validation.Issues) > 0 && !slices.ContainsFunc(r.Validation.Issues,
		func(i model.ValidationIssue) bool { return i.Code != validate.PrimitiveNotSupported })
}

// D-057 (A): a scenario may change the new parameters, never add or remove
// a breakdown, setups, calendar, schedule or batch. The paths marked replace
// in D-057 take only replace. Each allowed patch applies; while the
// primitives are gated the candidate is refused with PRIMITIVE_NOT_SUPPORTED
// and nothing else.
func TestScenarioLineFlowAllowlist(t *testing.T) {
	allowed := map[string]string{
		"buffer capacity":         testQLine + `{"op":"replace","path":"/nodes/4/capacity","value":30}`,
		"buffer to unlimited":     testQLine + `{"op":"remove","path":"/nodes/4/capacity"}`,
		"time to failure":         testLine + `{"op":"replace","path":"/resources/1/breakdowns/time_to_failure/distribution","value":{"type":"exponential","mean":900}}`,
		"time to repair mean":     testLine + `{"op":"replace","path":"/resources/1/breakdowns/time_to_repair/distribution/mean","value":20}`,
		"time to repair weibull":  testLine + `{"op":"replace","path":"/resources/1/breakdowns/time_to_repair/distribution","value":{"type":"weibull","shape":1.5,"scale":30}}`,
		"release rate":            testReleaseA + `{"op":"replace","path":"/nodes/5/arrival/schedule/segments/1/rate","value":0.6}`,
		"release quantity":        testReleaseB + `{"op":"replace","path":"/nodes/6/arrival/schedule/releases/0/quantity","value":30}`,
		"batch max size":          testBurnIn + `{"op":"replace","path":"/nodes/1/batch/max_size","value":10}`,
		"batch min size":          testBurnIn + `{"op":"replace","path":"/nodes/1/batch/min_size","value":2}`,
		"batch max wait":          testBurnIn + `{"op":"replace","path":"/nodes/1/batch/max_wait","value":90}`,
		"changeover transition":   testLine + `{"op":"replace","path":"/resources/1/setups/transitions/0/distribution","value":{"type":"constant","value":10}}`,
		"changeover default":      testLine + `{"op":"replace","path":"/resources/1/setups/default","value":{"type":"lognormal","mean":5,"cv":0.5}}`,
		"calendar reference":      testOperator + `{"op":"replace","path":"/resources/2/calendar_id","value":"two_shift"}`,
		"units on shift":          testLine + `{"op":"replace","path":"/resources/1/shift_capacity/evening","value":2}`,
		"shifts":                  testCalendar + `{"op":"replace","path":"/calendars/0/shifts","value":[{"start":360,"end":1320,"label":"day"}]}`,
		"exceptions":              testCalendar + `{"op":"replace","path":"/calendars/0/exceptions","value":[{"start":7200,"end":8640,"open":false}]}`,
		"service time lognormal":  testAssemble + `{"op":"replace","path":"/nodes/0/service_time/distribution","value":{"type":"lognormal","mean":1,"cv":0.3}}`,
		"existing capacity still": testChamber + `{"op":"replace","path":"/resources/0/capacity","value":3}`,
	}
	for name, ops := range allowed {
		t.Run(name, func(t *testing.T) {
			r := mustApply(t, lineFlow(t), "["+ops+"]", Scenario)
			if !r.Applied() || !gated(r) {
				t.Fatalf("patch %v, candidate %v", r.PatchIssues, r.Validation.Issues)
			}
		})
	}
	forbidden := map[string]string{
		"add breakdowns":           testChamber + `{"op":"add","path":"/resources/0/breakdowns","value":{"time_to_failure":{"distribution":{"type":"constant","value":100}},"time_to_repair":{"distribution":{"type":"constant","value":5}}}}`,
		"remove breakdowns":        testLine + `{"op":"remove","path":"/resources/1/breakdowns"}`,
		"add setups":               testChamber + `{"op":"add","path":"/resources/0/setups","value":{"default":{"type":"constant","value":1}}}`,
		"remove setups":            testLine + `{"op":"remove","path":"/resources/1/setups"}`,
		"add a transition":         testLine + `{"op":"add","path":"/resources/1/setups/transitions/-","value":{"from":"board_a","to":"board_b","distribution":{"type":"constant","value":1}}}`,
		"add a calendar":           `{"op":"add","path":"/calendars/-","value":{"id":"night","period":1440,"shifts":[{"start":0,"end":360}]}}`,
		"remove a calendar":        testCalendar + `{"op":"remove","path":"/calendars/0"}`,
		"rename a calendar":        testCalendar + `{"op":"replace","path":"/calendars/0/name","value":"x"}`,
		"give a calendar":          testChamber + `{"op":"add","path":"/resources/0/calendar_id","value":"two_shift"}`,
		"drop a calendar":          testOperator + `{"op":"remove","path":"/resources/2/calendar_id"}`,
		"add a shift label":        testLine + `{"op":"add","path":"/resources/1/shift_capacity/night","value":1}`,
		"add a batch":              testAssemble + `{"op":"add","path":"/nodes/0/batch","value":{"min_size":2,"max_size":2}}`,
		"remove a batch":           testBurnIn + `{"op":"remove","path":"/nodes/1/batch"}`,
		"remove a schedule":        testReleaseA + `{"op":"remove","path":"/nodes/5/arrival/schedule"}`,
		"add a rate segment":       testReleaseA + `{"op":"add","path":"/nodes/5/arrival/schedule/segments/-","value":{"start":1400,"rate":0.1}}`,
		"change a segment start":   testReleaseA + `{"op":"replace","path":"/nodes/5/arrival/schedule/segments/1/start","value":300}`,
		"change the period":        testReleaseA + `{"op":"replace","path":"/nodes/5/arrival/schedule/period","value":720}`,
		"change a release time":    testReleaseB + `{"op":"replace","path":"/nodes/6/arrival/schedule/releases/0/at","value":400}`,
		"change a calendar period": testCalendar + `{"op":"replace","path":"/calendars/0/period","value":10080}`,
		"add a declared metric":    `{"op":"add","path":"/metrics/-","value":{"id":"wip2","type":"system_wip"}}`,
	}
	for name, ops := range forbidden {
		t.Run(name, func(t *testing.T) {
			r := mustApply(t, lineFlow(t), "["+ops+"]", Scenario)
			last := len(mustOps(t, "["+ops+"]")) - 1
			wantIssue(t, r, PathForbidden, validate.Pointer(itoa(last), "path"))
		})
	}
	// The arrival distribution path is a D-021 scenario path; adding one to
	// a source with a schedule applies, and the schema refuses the candidate
	// (a source has one or the other, D-053).
	r := mustApply(t, lineFlow(t), "["+testReleaseA+`{"op":"add","path":"/nodes/5/arrival/distribution","value":{"type":"constant","value":1}}]`, Scenario)
	if !r.Applied() || r.Valid() || r.Validation.Issues[0].Code != validate.IRSchemaInvalid || r.Validation.Issues[0].Path != "/nodes/5/arrival" {
		t.Errorf("distribution and schedule: %v %v", r.PatchIssues, r.Validation.Issues)
	}
}

func mustOps(t *testing.T, doc string) []any {
	t.Helper()
	var ops []any
	if err := json.Unmarshal([]byte(doc), &ops); err != nil {
		t.Fatal(err)
	}
	return ops
}

func itoa(i int) string { return string(rune('0' + i)) }

// Finite buffers can be introduced by a scenario (D-057: add/remove of a
// queue's capacity); a buffer that closes a blocking cycle makes the
// candidate invalid: BLOCKING_CYCLE once T-047 checks it, refused by the gate
// until then. Either way the scenario cannot be created.
func TestScenarioAddsABuffer(t *testing.T) {
	// minimal + an explicit queue q (index 3) in front of work, and a retest
	// loop work -> check -> q.
	var ir map[string]any
	if err := json.Unmarshal(base(t), &ir); err != nil {
		t.Fatal(err)
	}
	ir["nodes"] = append(ir["nodes"].([]any), map[string]any{"id": "q", "type": "queue"}, map[string]any{"id": "check", "type": "decision"})
	fl := ir["flows"].([]any)
	fl[0].(map[string]any)["to"] = "q"
	fl[1].(map[string]any)["to"] = "check"
	ir["flows"] = append(fl, flow("q_work", "q", "work", 1), flow("check_end", "check", "end", 0.9), flow("check_q", "check", "q", 0.1))
	doc, err := json.Marshal(ir)
	if err != nil {
		t.Fatal(err)
	}
	if r := mustApply(t, doc, `[]`, Scenario); !r.Valid() {
		t.Fatalf("base: %v", r.Validation.Issues)
	}
	r := mustApply(t, doc, `[{"op":"test","path":"/nodes/3/id","value":"q"},{"op":"add","path":"/nodes/3/capacity","value":5}]`, Scenario)
	want := validate.PrimitiveNotSupported
	if validate.Checked(validate.FiniteBuffer) {
		want = validate.BlockingCycle
	}
	if !r.Applied() || r.Valid() || !slices.ContainsFunc(r.Validation.Issues, func(i model.ValidationIssue) bool {
		return i.Code == want && i.Path == "/nodes/3/capacity"
	}) {
		t.Fatalf("want %s at /nodes/3/capacity: %v %v", want, r.PatchIssues, r.Validation.Issues)
	}
}

func flow(id, from, to string, p float64) map[string]any {
	return map[string]any{"id": id, "from": from, "to": to, "routing": map[string]any{"type": "probability", "probability": p}}
}

// D-020 covers calendars like every catalog: change an element only after
// testing its id; the whole catalog only while it is absent or empty.
func TestCalendarsNeedTheirIDTested(t *testing.T) {
	r := mustApply(t, lineFlow(t), `[{"op":"replace","path":"/calendars/0/exceptions","value":[]}]`, Scenario)
	wantIssue(t, r, TestRequired, "/0/path")
	r = mustApply(t, lineFlow(t), `[{"op":"replace","path":"/calendars","value":[]}]`, ManualDraft)
	wantIssue(t, r, TestRequired, "/0/path")
}

// D-057: the manual and AI edit allowlists include the root calendars; the
// candidate is then validated in full (here: refused by the gate).
func TestEditsMayAddCalendars(t *testing.T) {
	add := `[{"op":"add","path":"/calendars","value":[{"id":"c","period":1440,"shifts":[{"start":0,"end":480}]}]}]`
	for _, mode := range []Mode{ManualDraft, AIEdit} {
		r := mustApply(t, base(t), add, mode)
		if !r.Applied() || !gated(r) {
			t.Errorf("%s: %v %v", mode, r.PatchIssues, r.Validation.Issues)
		}
	}
	if r := mustApply(t, base(t), add, Scenario); r.Applied() {
		t.Error("a scenario added a calendar")
	}
}

// TWIN_IR_SPEC §6 (D-021) for the distributions resources hold: a scenario
// cannot bind a dataset the model does not bind, wherever the distribution is.
func TestScenarioCannotBindADatasetInAResource(t *testing.T) {
	r := mustApply(t, lineFlow(t), `[`+testLine+`{"op":"replace","path":"/resources/1/breakdowns/time_to_repair/distribution",
		"value":{"type":"empirical","samples_ref":"dataset:repair_times"}}]`, Scenario)
	wantIssue(t, r, PathForbidden, "/1/value")
}
