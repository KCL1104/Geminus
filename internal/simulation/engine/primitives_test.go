package engine

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
)

func lineFlowModel(t *testing.T) model.ModelIR {
	t.Helper()
	var ir model.ModelIR
	if err := model.Decode(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/contracts/valid-line-flow-ir.json")), &ir); err != nil {
		t.Fatal(err)
	}
	return ir
}

// The validator checks finite buffers (T-047), breakdowns, lognormal and
// Weibull (T-048), calendars and release schedules (T-049) and no other
// line-flow primitive (ADR-026; T-050–T-051 add them). A model using
// another never compiles, so it is never simulated as if the member were
// absent: the validator's primitive gate refuses it first, with paths
// under /model (D-063). valid-line-flow-ir.json has 19 uses: two finite
// buffers, the breakdowns, a lognormal (their repair time), a Weibull (a
// changeover time), the calendars (the root member, two calendar_id and a
// shift_capacity) and three release schedules are checked; the other 7
// (a batch, setups and five declared metrics) are refused.
func TestCompileRefusesLineFlowPrimitives(t *testing.T) {
	_, err := Compile(input(lineFlowModel(t)))
	var ie *InputError
	if !errors.As(err, &ie) || !errors.Is(err, ErrInvalidInput) || len(ie.Issues) != 7 {
		t.Fatalf("want 7 PRIMITIVE_NOT_SUPPORTED issues, got %v", err)
	}
	for _, is := range ie.Issues {
		if is.Code != validate.PrimitiveNotSupported || is.Severity != model.SeverityError || is.Path[:7] != "/model/" {
			t.Errorf("issue %+v", is)
		}
	}
	issueExactly(t, err, "/model/nodes/1/batch", validate.PrimitiveNotSupported)
	issueExactly(t, err, "/model/resources/1/setups", validate.PrimitiveNotSupported)
	for _, is := range ie.Issues {
		switch is.Path {
		case "/model/nodes/3/capacity", "/model/nodes/4/capacity", "/model/resources/1/breakdowns",
			"/model/resources/1/breakdowns/time_to_repair/distribution", "/model/resources/1/setups/transitions/0/distribution",
			"/model/calendars", "/model/resources/1/calendar_id", "/model/resources/1/shift_capacity", "/model/resources/2/calendar_id",
			"/model/nodes/5/arrival/schedule", "/model/nodes/6/arrival/schedule", "/model/nodes/7/arrival/schedule":
			t.Errorf("a checked primitive is refused: %+v", is)
		}
	}
}

// primitiveModels puts each line-flow primitive alone into
// deterministic-queue.json (arrival -> service -> done, resource server),
// with the path of its use.
var primitiveModels = map[validate.Primitive]struct {
	edit func(ir *model.ModelIR)
	path string
}{
	validate.FiniteBuffer: {func(ir *model.ModelIR) {
		ir.Nodes = append(ir.Nodes, model.Node{ID: "buf", Type: model.NodeQueue, Capacity: 1})
		ir.Flows[0].To = "buf"
		ir.Flows = append(ir.Flows, model.Flow{ID: "b_s", From: "buf", To: "service", Routing: model.Routing{Type: model.RoutingProbability, Probability: 1}})
	}, "/nodes/3/capacity"},
	validate.Breakdowns: {func(ir *model.ModelIR) {
		ir.Resources[0].Breakdowns = &model.Breakdowns{TimeToFailure: model.TimedDistribution{Distribution: constant(100)},
			TimeToRepair: model.TimedDistribution{Distribution: constant(5)}}
	}, "/resources/0/breakdowns"},
	validate.Lognormal: {func(ir *model.ModelIR) {
		mean, cv := 4.0, 0.5
		ir.Nodes[1].ServiceTime.Distribution = model.Distribution{Type: model.DistLognormal, Mean: &mean, CV: &cv}
	}, "/nodes/1/service_time/distribution"},
	validate.Weibull: {func(ir *model.ModelIR) {
		shape, scale := 1.5, 4.0
		ir.Nodes[1].ServiceTime.Distribution = model.Distribution{Type: model.DistWeibull, Shape: &shape, Scale: &scale}
	}, "/nodes/1/service_time/distribution"},
	validate.Calendars: {func(ir *model.ModelIR) {
		ir.Calendars = []model.Calendar{{ID: "day", Period: 1440, Shifts: []model.Shift{{Start: 0, End: 480}}}}
	}, "/calendars"},
	validate.ReleaseSchedule: {func(ir *model.ModelIR) {
		ir.Nodes[0].Arrival = &model.Arrival{Schedule: &model.ReleaseSchedule{Process: model.ReleaseTable, Period: 60,
			Releases: []model.Release{{At: 0, Quantity: 3}}}}
	}, "/nodes/0/arrival/schedule"},
	validate.Batch: {func(ir *model.ModelIR) {
		ir.Nodes[1].Batch = &model.Batch{MinSize: 2, MaxSize: 2}
	}, "/nodes/1/batch"},
	validate.Setups: {func(ir *model.ModelIR) {
		d := constant(1)
		ir.Resources[0].Setups = &model.Setups{Default: &d}
	}, "/resources/0/setups"},
	validate.SystemWIP: {func(ir *model.ModelIR) {
		ir.Metrics = append(ir.Metrics, model.Metric{ID: "wip", Type: model.MetricSystemWIP})
	}, "/metrics/2/type"},
	validate.FlowCount: {func(ir *model.ModelIR) {
		ir.Metrics = append(ir.Metrics, model.Metric{ID: "passes", Type: model.MetricFlowCount, FlowID: "s_d"})
	}, "/metrics/2/type"},
	validate.SinkCount: {func(ir *model.ModelIR) {
		ir.Metrics = append(ir.Metrics, model.Metric{ID: "done_count", Type: model.MetricSinkCount, SinkID: "done"})
	}, "/metrics/2/type"},
	validate.PeriodThroughput: {func(ir *model.ModelIR) {
		period := 60.0
		ir.Metrics = append(ir.Metrics, model.Metric{ID: "hourly", Type: model.MetricPeriodThroughput, Period: &period})
	}, "/metrics/2/type"},
}

// M2 (T-046 review round 1): with a validator that checks every primitive
// (a rule landing before its kernel), Compile itself still refuses each
// primitive the kernel does not execute, at its use, so no such model is
// ever simulated as if the member were absent. The validator Compile calls
// is replaced for this test only.
func TestCompileRefusesWhatTheKernelDoesNotRun(t *testing.T) {
	defer func(v func([]byte) (validate.Result, error)) { validateIR = v }(validateIR)
	validateIR = func(data []byte) (validate.Result, error) {
		var ir model.ModelIR
		if err := model.Decode(data, &ir); err != nil {
			return validate.Result{}, err
		}
		return validate.Result{IR: &ir}, nil // valid: no issue at all
	}
	if len(primitiveModels) != len(validate.Primitives) {
		t.Fatalf("%d models for %d primitives", len(primitiveModels), len(validate.Primitives))
	}
	for _, p := range validate.Primitives {
		t.Run(string(p), func(t *testing.T) {
			c, ok := primitiveModels[p]
			if !ok {
				t.Fatalf("no model for %s", p)
			}
			ir := golden(t, "deterministic-queue.json")
			c.edit(&ir)
			_, err := Compile(input(ir))
			var ie *InputError
			if kernelExecutes[p] {
				// The kernel runs it: Compile must not refuse it as unsupported.
				if errors.As(err, &ie) && slices.ContainsFunc(ie.Issues, func(i model.ValidationIssue) bool { return i.Code == validate.PrimitiveNotSupported }) {
					t.Errorf("%s is executed but refused: %v", p, ie.Issues)
				}
				return
			}
			if !errors.As(err, &ie) || len(ie.Issues) != 1 || ie.Issues[0].Code != validate.PrimitiveNotSupported ||
				ie.Issues[0].Path != "/model"+c.path {
				t.Fatalf("want only PRIMITIVE_NOT_SUPPORTED at /model%s, got %v", c.path, err)
			}
		})
	}
	// The same models, through the real validator, are refused there first;
	// a primitive both checked and executed (T-047: finite_buffer; T-048:
	// breakdowns, lognormal, weibull) compiles.
	validateIR = validate.IR
	for _, p := range validate.Primitives {
		ir := golden(t, "deterministic-queue.json")
		primitiveModels[p].edit(&ir)
		_, err := Compile(input(ir))
		if validate.Checked(p) && kernelExecutes[p] {
			if err != nil {
				t.Errorf("%s is checked and executed, but Compile refused it: %v", p, err)
			}
			continue
		}
		var ie *InputError
		if !errors.As(err, &ie) || len(ie.Issues) != 1 || ie.Issues[0].Path != "/model"+primitiveModels[p].path {
			t.Errorf("%s through the validator: %v", p, err)
		}
	}
}

// The kernel keeps its own list of what it executes, apart from what the
// validator checks: should a validator rule land before its kernel, Compile
// still refuses the model. Checked on a decoded model, past the validator.
func TestKernelRefusesWhatItDoesNotExecute(t *testing.T) {
	// Each kernel task adds its own primitive, with this list: so far
	// T-047's, T-048's and T-049's.
	executed := map[validate.Primitive]bool{validate.FiniteBuffer: true, validate.Breakdowns: true, validate.Lognormal: true, validate.Weibull: true,
		validate.Calendars: true, validate.ReleaseSchedule: true}
	for _, p := range validate.Primitives {
		if kernelExecutes[p] != executed[p] {
			t.Errorf("%s: executed %v, want %v; update this test with the kernel task that implements it", p, kernelExecutes[p], executed[p])
		}
	}
	ir := lineFlowModel(t)
	issues := unsupported(&ir)
	var want []validate.Use
	for _, u := range validate.Uses(&ir) {
		if !executed[u.Primitive] {
			want = append(want, u)
		}
	}
	if len(issues) != len(want) || len(want) != 7 {
		t.Fatalf("%d issues for %d uses", len(issues), len(want))
	}
	for i, u := range want {
		if issues[i] != validate.NotSupported(u) {
			t.Errorf("issue %d: %+v, want %+v", i, issues[i], validate.NotSupported(u))
		}
	}
	// Every golden model, none of which has a line-flow member, compiles as before.
	paths, _ := filepath.Glob(filepath.Join(contracttest.Root(t), "testdata/golden-models/*.json"))
	for _, p := range paths {
		g := golden(t, filepath.Base(p))
		if got := unsupported(&g); len(got) != 0 {
			t.Errorf("%s: %v", filepath.Base(p), got)
		}
	}
	if !slices.Equal(unsupported(&model.ModelIR{}), nil) {
		t.Error("an empty model uses a primitive")
	}
}
