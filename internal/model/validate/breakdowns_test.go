package validate_test

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// T-048: the rules of resource breakdowns (D-051, D-050 R3) and of the
// lognormal and Weibull distributions (D-056, D-063 (c), D-075), on the
// minimal model (start -> work(worker) -> end).

func breakdowns(ttf, ttr map[string]any) map[string]any {
	return map[string]any{"time_to_failure": map[string]any{"distribution": ttf}, "time_to_repair": map[string]any{"distribution": ttr}}
}

func constantDist(v float64) map[string]any { return map[string]any{"type": "constant", "value": v} }

func resources(ir map[string]any) []any { return ir["resources"].([]any) }

// withMachine adds resource "press" (capacity c, with breakdowns) to the
// minimal model and work's bundle needs units of it.
func withMachine(c, units int, b map[string]any) func(ir map[string]any) {
	return func(ir map[string]any) {
		r := map[string]any{"id": "press", "type": "machine", "name": "Press", "capacity": c, "queue_discipline": "fifo"}
		if b != nil {
			r["breakdowns"] = b
		}
		ir["resources"] = append(resources(ir), r)
		w := obj(nodes(ir)[1])
		w["requires"] = append(w["requires"].([]any), map[string]any{"resource_id": "press", "units": units})
	}
}

func issuesOf(r validate.Result) [][3]string {
	var out [][3]string
	for _, is := range r.Issues {
		out = append(out, [3]string{is.Code, is.Path, string(is.Severity)})
	}
	return out
}

func TestBreakdownRules(t *testing.T) {
	good := breakdowns(map[string]any{"type": "exponential", "mean": 600}, constantDist(30))
	cases := []struct {
		name string
		edit func(ir map[string]any)
		want [][3]string // nil: valid, no issue
	}{
		{"a resource that breaks down, one unit", withMachine(2, 1, good), nil},
		{"breakdowns on a resource no process needs", func(ir map[string]any) {
			obj(resources(ir)[0])["capacity"] = 3
			ir["resources"] = append(resources(ir), map[string]any{"id": "spare", "type": "machine", "name": "Spare", "capacity": 4,
				"queue_discipline": "fifo", "breakdowns": good})
		}, nil},
		// D-050 R3, bundle feasibility stage: units of a resource that breaks down must be 1.
		{"R3: two units of a resource that breaks down", withMachine(2, 2, good),
			[][3]string{{validate.ResourceRequirementInvalid, "/nodes/1/requires/1/units", "error"}}},
		{"R3: two resources that break down in one bundle", func(ir map[string]any) {
			obj(resources(ir)[0])["breakdowns"] = good
			withMachine(1, 1, good)(ir)
		}, [][3]string{{validate.ResourceRequirementInvalid, "/nodes/1/requires", "error"}}},
		{"R3: both at once", func(ir map[string]any) {
			obj(resources(ir)[0])["breakdowns"] = good
			withMachine(3, 2, good)(ir)
		}, [][3]string{{validate.ResourceRequirementInvalid, "/nodes/1/requires", "error"},
			{validate.ResourceRequirementInvalid, "/nodes/1/requires/1/units", "error"}}},
		// D-051: the time to failure must be strictly positive (D-033's rule for interarrival times).
		{"a time to failure of 0", withMachine(1, 1, breakdowns(constantDist(0), constantDist(5))),
			[][3]string{{validate.DistributionInvalid, "/resources/1/breakdowns/time_to_failure/distribution", "error"}}},
		{"a triangular time to failure from 0", withMachine(1, 1, breakdowns(map[string]any{"type": "triangular", "min": 0, "mode": 5, "max": 9}, constantDist(5))),
			[][3]string{{validate.DistributionInvalid, "/resources/1/breakdowns/time_to_failure/distribution", "error"}}},
		{"a time to repair of 0 is allowed", withMachine(1, 1, breakdowns(constantDist(60), constantDist(0))), nil},
		{"lognormal and Weibull failure clocks are never 0", withMachine(1, 1, breakdowns(map[string]any{"type": "lognormal", "mean": 600, "cv": 2},
			map[string]any{"type": "weibull", "shape": 1.5, "scale": 20})), nil},
		{"triangular time to failure out of order", withMachine(1, 1, breakdowns(map[string]any{"type": "triangular", "min": 9, "mode": 5, "max": 7}, constantDist(5))),
			[][3]string{{validate.DistributionInvalid, "/resources/1/breakdowns/time_to_failure/distribution", "error"}}},
		// D-029: every distribution parameter, also in a resource, is at most 365 days.
		{"a time to failure over 365 days", withMachine(1, 1, breakdowns(constantDist(365*24*60+1), constantDist(5))),
			[][3]string{{validate.LimitExceeded, "/resources/1/breakdowns/time_to_failure/distribution/value", "error"}}},
		{"a repair mean over 365 days", withMachine(1, 1, breakdowns(constantDist(60), map[string]any{"type": "exponential", "mean": 365*24*60 + 1})),
			[][3]string{{validate.LimitExceeded, "/resources/1/breakdowns/time_to_repair/distribution/mean", "error"}}},
		{"R3 runs after the distribution stage", func(ir map[string]any) {
			withMachine(2, 2, good)(ir)
			obj(obj(nodes(ir)[1])["service_time"])["distribution"] = map[string]any{"type": "triangular", "min": 3, "mode": 1, "max": 2}
		}, [][3]string{{validate.DistributionInvalid, "/nodes/1/service_time/distribution", "error"}}},
		{"R3 runs before the constraint stage", func(ir map[string]any) {
			withMachine(2, 2, good)(ir)
			ir["constraints"] = []any{map[string]any{"type": "outgoing_probability_sum", "node_id": "end", "equals": 1}}
		}, [][3]string{{validate.ResourceRequirementInvalid, "/nodes/1/requires/1/units", "error"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := run(t, edit(t, c.edit))
			if got := issuesOf(r); !slices.Equal(got, c.want) {
				t.Errorf("issues %v, want %v", got, c.want)
			}
			if r.Valid() != (c.want == nil) {
				t.Errorf("valid %v", r.Valid())
			}
		})
	}
}

// R3's messages name the resources in ID order, whatever the bundle's order.
func TestR3Message(t *testing.T) {
	good := breakdowns(constantDist(60), constantDist(5))
	r := run(t, edit(t, func(ir map[string]any) {
		obj(resources(ir)[0])["breakdowns"] = good
		ir["resources"] = append([]any{map[string]any{"id": "zeta", "type": "machine", "name": "Z", "capacity": 1, "queue_discipline": "fifo", "breakdowns": good}},
			resources(ir)...)
		w := obj(nodes(ir)[1])
		w["requires"] = append([]any{map[string]any{"resource_id": "zeta", "units": 1}}, w["requires"].([]any)...)
	}))
	if len(r.Issues) != 1 || r.Issues[0].Path != "/nodes/1/requires" || !strings.Contains(r.Issues[0].Message, `"worker", "zeta"`) {
		t.Errorf("issues %v", r.Issues)
	}
}

// D-056 with D-063 (c) and D-075: lognormal (mean, cv) and Weibull (shape,
// scale) in any distribution position. The ranges 0 < cv <= 10 and
// 0.1 <= shape <= 20 are DISTRIBUTION_INVALID; a mean or a scale over 365
// days is LIMIT_EXCEEDED in the limits stage, before them.
func TestLognormalWeibullRules(t *testing.T) {
	service := func(d map[string]any) func(ir map[string]any) {
		return func(ir map[string]any) { obj(obj(nodes(ir)[1])["service_time"])["distribution"] = d }
	}
	arrival := func(d map[string]any) func(ir map[string]any) {
		return func(ir map[string]any) { obj(obj(nodes(ir)[0])["arrival"])["distribution"] = d }
	}
	ln := func(mean, cv float64) map[string]any {
		return map[string]any{"type": "lognormal", "mean": mean, "cv": cv}
	}
	wb := func(shape, scale float64) map[string]any {
		return map[string]any{"type": "weibull", "shape": shape, "scale": scale}
	}
	const at = "/nodes/1/service_time/distribution"
	year := float64(365 * 24 * 60)
	cases := []struct {
		name string
		edit func(ir map[string]any)
		want [][3]string
	}{
		{"lognormal", service(ln(4, 0.5)), nil},
		{"lognormal cv 10", service(ln(4, 10)), nil},
		{"lognormal cv above 10", service(ln(4, 10.000001)), [][3]string{{validate.DistributionInvalid, at, "error"}}},
		{"lognormal tiny cv", service(ln(4, 1e-300)), nil},
		{"lognormal mean of 365 days", service(ln(year, 1)), nil},
		{"lognormal mean over 365 days", service(ln(year+1, 1)), [][3]string{{validate.LimitExceeded, at + "/mean", "error"}}},
		{"lognormal mean over 365 days and cv out of range: limits first", service(ln(year+1, 11)), [][3]string{{validate.LimitExceeded, at + "/mean", "error"}}},
		{"lognormal interarrival", arrival(ln(10, 1)), nil},
		{"weibull", service(wb(1.5, 4)), nil},
		{"weibull shape 0.1", service(wb(0.1, 4)), nil},
		{"weibull shape 20", service(wb(20, 4)), nil},
		{"weibull shape below 0.1", service(wb(0.0999999, 4)), [][3]string{{validate.DistributionInvalid, at, "error"}}},
		{"weibull shape above 20", service(wb(20.000001, 4)), [][3]string{{validate.DistributionInvalid, at, "error"}}},
		{"weibull scale over 365 days", service(wb(2, year+1)), [][3]string{{validate.LimitExceeded, at + "/scale", "error"}}},
		{"weibull interarrival", arrival(wb(0.5, 10)), nil},
		{"weibull repair time", withMachine(1, 1, breakdowns(constantDist(100), wb(25, 3))),
			[][3]string{{validate.DistributionInvalid, "/resources/1/breakdowns/time_to_repair/distribution", "error"}}},
		{"lognormal time to failure", withMachine(1, 1, breakdowns(ln(100, 12), constantDist(3))),
			[][3]string{{validate.DistributionInvalid, "/resources/1/breakdowns/time_to_failure/distribution", "error"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := run(t, edit(t, c.edit))
			if got := issuesOf(r); !slices.Equal(got, c.want) {
				t.Errorf("issues %v, want %v", got, c.want)
			}
		})
	}
}

// As for the exponential mean (TestExponentialMeanUnderflow), the schema
// checks > 0 on the exact decimal: a lognormal mean or cv, or a Weibull
// scale or shape, of 1e-400 decodes to binary64 0 and is refused.
func TestLognormalWeibullUnderflow(t *testing.T) {
	underflow := func(doc []byte) []byte {
		out := bytes.ReplaceAll(doc, []byte("7777.25"), []byte("1e-400"))
		if bytes.Equal(out, doc) {
			t.Fatal("marker not found")
		}
		return out
	}
	for name, d := range map[string]map[string]any{
		"lognormal mean": {"type": "lognormal", "mean": 7777.25, "cv": 1},
		"lognormal cv":   {"type": "lognormal", "mean": 3, "cv": 7777.25},
		"weibull scale":  {"type": "weibull", "shape": 2, "scale": 7777.25},
		"weibull shape":  {"type": "weibull", "shape": 7777.25, "scale": 3},
	} {
		t.Run(name, func(t *testing.T) {
			r := run(t, underflow(edit(t, func(ir map[string]any) { obj(obj(nodes(ir)[1])["service_time"])["distribution"] = d })))
			if got := issuesOf(r); !slices.Equal(got, [][3]string{{validate.DistributionInvalid, "/nodes/1/service_time/distribution", "error"}}) {
				t.Errorf("issues %v", got)
			}
		})
	}
}

// The dataset binding stage (D-033) also reads the distributions of
// resources: an empirical time to failure with a 0-tick sample is
// DISTRIBUTION_INVALID; a 0-tick repair sample is allowed.
func TestBindingsOfBreakdowns(t *testing.T) {
	emp := map[string]any{"type": "empirical", "samples_ref": "dataset:times"}
	for _, c := range []struct {
		name string
		b    map[string]any
		want [][3]string
	}{
		{"time to failure", breakdowns(emp, constantDist(5)), [][3]string{{validate.DistributionInvalid, "/resources/1/breakdowns/time_to_failure/distribution", "error"}}},
		{"time to repair", breakdowns(constantDist(60), emp), nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := run(t, edit(t, withMachine(1, 1, c.b)))
			if !r.Valid() {
				t.Fatalf("not valid: %v", r.Issues)
			}
			var got [][3]string
			for _, is := range validate.Bindings(r.IR, map[string]bool{"dataset:times": true}) {
				got = append(got, [3]string{is.Code, is.Path, string(is.Severity)})
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("issues %v, want %v", got, c.want)
			}
			if got := validate.Bindings(r.IR, nil); len(got) != 0 {
				t.Errorf("without a 0 sample: %v", got)
			}
		})
	}
}

// model.Resource.Distributions lists the breakdown distributions of a
// resource, in member order, aliasing the resource.
func TestResourceDistributions(t *testing.T) {
	r := run(t, edit(t, withMachine(1, 1, breakdowns(constantDist(60), constantDist(5)))))
	if !r.Valid() {
		t.Fatal(r.Issues)
	}
	ds := r.IR.Resources[1].Distributions()
	if len(ds) != 2 || ds[0].Member != "breakdowns/time_to_failure" || ds[1].Member != "breakdowns/time_to_repair" ||
		ds[0].Distribution != &r.IR.Resources[1].Breakdowns.TimeToFailure.Distribution {
		t.Errorf("distributions %+v", ds)
	}
	if len(r.IR.Resources[0].Distributions()) != 0 || len((&model.Resource{}).Distributions()) != 0 {
		t.Error("a resource without breakdowns has distributions")
	}
}
