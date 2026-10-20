package validate_test

import (
	"encoding/json/v2"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// The line-flow primitives of ADR-026 and D-058, each with the task that
// implements its validator rules and kernel (planning/tasks.json).
var primitiveTasks = map[validate.Primitive]string{
	validate.FiniteBuffer: "T-047", validate.Breakdowns: "T-048", validate.Lognormal: "T-048", validate.Weibull: "T-048",
	validate.Calendars: "T-049", validate.ReleaseSchedule: "T-049", validate.Batch: "T-050", validate.Setups: "T-050",
	validate.SystemWIP: "T-051", validate.FlowCount: "T-051", validate.SinkCount: "T-051", validate.PeriodThroughput: "T-051",
}

func TestPrimitiveCatalog(t *testing.T) {
	if len(validate.Primitives) != len(primitiveTasks) {
		t.Fatalf("primitives %v", validate.Primitives)
	}
	for _, p := range validate.Primitives {
		if primitiveTasks[p] == "" {
			t.Errorf("%s: no implementing task", p)
		}
		// Nothing is checked yet: T-047–T-051 turn each on with its rules
		// and kernel, and update this test and the shared fixtures.
		if validate.Checked(p) {
			t.Errorf("%s is checked before %s implemented it", p, primitiveTasks[p])
		}
	}
}

// D-063 (PROPOSED): a model that uses any line-flow primitive this build does
// not implement is refused with PRIMITIVE_NOT_SUPPORTED at every use, so it
// can neither be published nor run as if the member were absent. The paths
// are hand-derived from valid-line-flow-ir.json.
func TestPrimitiveGate(t *testing.T) {
	r := run(t, contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/contracts/valid-line-flow-ir.json")))
	want := []struct{ path, what string }{
		{"/calendars", "shift calendars"},
		{"/metrics/0/type", "the system_wip metric"},
		{"/metrics/1/type", "the flow_count metric"},
		{"/metrics/2/type", "the flow_count metric"},
		{"/metrics/3/type", "the sink_count metric"},
		{"/metrics/4/type", "the period_throughput metric"},
		{"/nodes/1/batch", "batch processes"},
		{"/nodes/3/capacity", "finite queue capacity"},
		{"/nodes/4/capacity", "finite queue capacity"},
		{"/nodes/5/arrival/schedule", "release schedules"},
		{"/nodes/6/arrival/schedule", "release schedules"},
		{"/nodes/7/arrival/schedule", "release schedules"},
		{"/resources/1/breakdowns", "resource breakdowns"},
		{"/resources/1/breakdowns/time_to_repair/distribution", "the lognormal distribution"},
		{"/resources/1/calendar_id", "shift calendars"},
		{"/resources/1/setups", "changeovers"},
		{"/resources/1/setups/transitions/0/distribution", "the Weibull distribution"},
		{"/resources/1/shift_capacity", "shift calendars"},
		{"/resources/2/calendar_id", "shift calendars"},
	}
	if r.Valid() || len(r.Issues) != len(want) {
		t.Fatalf("got %d issues %v", len(r.Issues), r.Issues)
	}
	for i, w := range want {
		is := r.Issues[i]
		if is.Code != validate.PrimitiveNotSupported || is.Path != w.path || is.Severity != model.SeverityError ||
			!strings.Contains(is.Message, "does not support "+w.what) {
			t.Errorf("issue %d: %+v, want %s at %s", i, is, w.what, w.path)
		}
	}
}

// One primitive at a time on the minimal model: exactly one issue at its
// member, and the later stages do not run (D-023), so an unknown flow target
// in the same model is not reported yet.
func TestPrimitiveGateStopsTheStages(t *testing.T) {
	cases := map[string]struct {
		edit func(ir map[string]any)
		path string
	}{
		"finite buffer": {func(ir map[string]any) {
			ir["nodes"] = append(nodes(ir), map[string]any{"id": "q", "type": "queue", "capacity": 3})
			ir["flows"] = append(flows(ir), flow("q_work", "q", "work", 1))
		}, "/nodes/3/capacity"},
		"breakdowns": {func(ir map[string]any) {
			obj(ir["resources"].([]any)[0])["breakdowns"] = map[string]any{
				"time_to_failure": map[string]any{"distribution": map[string]any{"type": "constant", "value": 100}},
				"time_to_repair":  map[string]any{"distribution": map[string]any{"type": "constant", "value": 5}}}
		}, "/resources/0/breakdowns"},
		"lognormal service": {func(ir map[string]any) {
			obj(obj(nodes(ir)[1])["service_time"])["distribution"] = map[string]any{"type": "lognormal", "mean": 3, "cv": 0.5}
		}, "/nodes/1/service_time/distribution"},
		"weibull interarrival": {func(ir map[string]any) {
			obj(obj(nodes(ir)[0])["arrival"])["distribution"] = map[string]any{"type": "weibull", "shape": 1.5, "scale": 10}
		}, "/nodes/0/arrival/distribution"},
		"calendar": {func(ir map[string]any) {
			ir["calendars"] = []any{map[string]any{"id": "c", "period": 1440, "shifts": []any{map[string]any{"start": 0, "end": 480}}}}
		}, "/calendars"},
		"release table": {func(ir map[string]any) {
			obj(nodes(ir)[0])["arrival"] = map[string]any{"schedule": map[string]any{"process": "table", "period": 60,
				"releases": []any{map[string]any{"at": 0, "quantity": 2}}}}
		}, "/nodes/0/arrival/schedule"},
		"batch": {func(ir map[string]any) {
			obj(nodes(ir)[1])["batch"] = map[string]any{"min_size": 2, "max_size": 2}
		}, "/nodes/1/batch"},
		"setups": {func(ir map[string]any) {
			obj(ir["resources"].([]any)[0])["setups"] = map[string]any{"default": map[string]any{"type": "constant", "value": 1}}
		}, "/resources/0/setups"},
		"system wip": {func(ir map[string]any) {
			ir["metrics"] = []any{map[string]any{"id": "wip", "type": "system_wip"}}
		}, "/metrics/0/type"},
		"period throughput": {func(ir map[string]any) {
			ir["metrics"] = []any{map[string]any{"id": "daily", "type": "period_throughput", "period": 1440}}
		}, "/metrics/0/type"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := run(t, edit(t, func(ir map[string]any) {
				c.edit(ir)
				obj(flows(ir)[0])["to"] = "nowhere"
			}))
			if len(r.Issues) != 1 || r.Issues[0].Code != validate.PrimitiveNotSupported || r.Issues[0].Path != c.path {
				t.Errorf("got %v, want only %s at %s", r.Issues, validate.PrimitiveNotSupported, c.path)
			}
			if r.IR == nil {
				t.Error("a schema-valid model must still decode")
			}
		})
	}
	// Without a primitive the same model fails at the reference stage, as before.
	r := run(t, edit(t, func(ir map[string]any) { obj(flows(ir)[0])["to"] = "nowhere" }))
	if len(r.Issues) != 1 || r.Issues[0].Code != validate.UnknownReference {
		t.Errorf("control: %v", r.Issues)
	}
}

// The gate's messages are fixed: no ID, name or value from the model.
func TestPrimitiveGateMessagesAreFixed(t *testing.T) {
	const sentinel = "Zq9Sentinel"
	doc := contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/contracts/valid-line-flow-ir.json"))
	var ir map[string]any
	if err := json.Unmarshal(doc, &ir); err != nil {
		t.Fatal(err)
	}
	ir["name"] = sentinel
	cal := obj(ir["calendars"].([]any)[0])
	cal["name"] = sentinel
	obj(cal["shifts"].([]any)[0])["label"] = sentinel
	res := obj(ir["resources"].([]any)[1])
	res["shift_capacity"] = map[string]any{sentinel: 1, "evening": 1}
	res["name"] = sentinel
	data, err := json.Marshal(ir)
	if err != nil {
		t.Fatal(err)
	}
	r := run(t, data)
	if len(r.Issues) == 0 {
		t.Fatal("no issues")
	}
	for _, is := range r.Issues {
		if strings.Contains(is.Message, sentinel) || strings.Contains(is.Path, sentinel) {
			t.Errorf("issue quotes the model: %+v", is)
		}
	}
	// Issues come in D-023 order, whatever the order of the map members.
	if !slices.Equal(validate.Sort(r.Issues), r.Issues) {
		t.Errorf("not in D-023 order: %v", r.Issues)
	}
}

// A model without any line-flow member uses no primitive: every older
// fixture and golden model is untouched by the gate (D-047).
func TestNoPrimitiveNoUse(t *testing.T) {
	root := contracttest.Root(t)
	paths, _ := filepath.Glob(filepath.Join(root, "testdata/golden-models/*.json"))
	paths = append(paths, filepath.Join(root, "testdata/contracts/valid-minimal-ir.json"),
		filepath.Join(root, "testdata/contracts/valid-model-ir.json"))
	for _, p := range paths {
		r := run(t, contracttest.ReadFile(t, p))
		if r.IR == nil || len(validate.Uses(r.IR)) != 0 || !r.Valid() {
			t.Errorf("%s: uses %v, issues %v", filepath.Base(p), validate.Uses(r.IR), r.Issues)
		}
	}
}
