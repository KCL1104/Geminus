package validate_test

import (
	"encoding/json/v2"
	"errors"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/simulation"
	"github.com/kcl1104/geminus/internal/simulation/engine"
)

// distributionPosition is one place of model-ir.schema.json that holds a
// Distribution, put into the position base: start(0) -> work(1) -> wait(3,
// a delay) -> end(2), resource worker(0), entity types change and board.
type distributionPosition struct {
	name string
	put  func(ir map[string]any, d map[string]any)
	path string // the distribution's JSON Pointer in that model
	// container is the gated member that holds it, if any (its own use).
	container string
	holder    validate.Primitive
}

// The positions are hand-written (T-046 review round 1, M1); every
// lognormal or Weibull distribution in any of them is a use of its
// primitive (TWIN_IR_SPEC §4), and TestDistributionPositionsCoverTheSchema
// fails when the schema gets a position this table does not have.
var distributionPositions = []distributionPosition{
	{"arrival", func(ir map[string]any, d map[string]any) {
		obj(obj(nodes(ir)[0])["arrival"])["distribution"] = d
	}, "/nodes/0/arrival/distribution", "", ""},
	{"service time", func(ir map[string]any, d map[string]any) {
		obj(obj(nodes(ir)[1])["service_time"])["distribution"] = d
	}, "/nodes/1/service_time/distribution", "", ""},
	{"delay time", func(ir map[string]any, d map[string]any) {
		obj(obj(nodes(ir)[3])["delay_time"])["distribution"] = d
	}, "/nodes/3/delay_time/distribution", "", ""},
	{"time to failure", func(ir map[string]any, d map[string]any) {
		obj(ir["resources"].([]any)[0])["breakdowns"] = map[string]any{"time_to_failure": map[string]any{"distribution": d},
			"time_to_repair": map[string]any{"distribution": map[string]any{"type": "constant", "value": 5}}}
	}, "/resources/0/breakdowns/time_to_failure/distribution", "/resources/0/breakdowns", validate.Breakdowns},
	{"time to repair", func(ir map[string]any, d map[string]any) {
		obj(ir["resources"].([]any)[0])["breakdowns"] = map[string]any{
			"time_to_failure": map[string]any{"distribution": map[string]any{"type": "constant", "value": 100}},
			"time_to_repair":  map[string]any{"distribution": d}}
	}, "/resources/0/breakdowns/time_to_repair/distribution", "/resources/0/breakdowns", validate.Breakdowns},
	{"changeover default", func(ir map[string]any, d map[string]any) {
		obj(ir["resources"].([]any)[0])["setups"] = map[string]any{"default": d}
	}, "/resources/0/setups/default", "/resources/0/setups", validate.Setups},
	{"changeover transition", func(ir map[string]any, d map[string]any) {
		obj(ir["resources"].([]any)[0])["setups"] = map[string]any{"transitions": []any{
			map[string]any{"from": "change", "to": "board", "distribution": d}}}
	}, "/resources/0/setups/transitions/0/distribution", "/resources/0/setups", validate.Setups},
}

var gatedDistributions = map[validate.Primitive]map[string]any{
	validate.Lognormal: {"type": "lognormal", "mean": 3, "cv": 0.5},
	validate.Weibull:   {"type": "weibull", "shape": 1.5, "scale": 10},
}

// positionBase is valid-minimal-ir.json with a delay after work and a
// second entity type.
func positionBase(t *testing.T) map[string]any {
	t.Helper()
	var ir map[string]any
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t),
		"testdata/contracts/valid-minimal-ir.json")), &ir); err != nil {
		t.Fatal(err)
	}
	ir["entity_types"] = append(ir["entity_types"].([]any), map[string]any{"id": "board", "name": "Board"})
	ir["nodes"] = append(nodes(ir), map[string]any{"id": "wait", "type": "delay",
		"delay_time": map[string]any{"distribution": map[string]any{"type": "constant", "value": 2}}})
	obj(flows(ir)[1])["to"] = "wait"
	ir["flows"] = append(flows(ir), flow("wait_end", "wait", "end", 1))
	return ir
}

// M1: a lognormal or Weibull distribution in each of the seven positions is
// refused at exactly that position, by the validator and by the engine's
// Compile, until T-048 implements them; nothing else is reported.
func TestEveryDistributionPositionIsGated(t *testing.T) {
	data, err := json.Marshal(positionBase(t))
	if err != nil {
		t.Fatal(err)
	}
	if r := run(t, data); !r.Valid() {
		t.Fatalf("position base: %v", r.Issues)
	}
	for kind, dist := range gatedDistributions {
		for _, p := range distributionPositions {
			t.Run(string(kind)+" as "+p.name, func(t *testing.T) {
				ir := positionBase(t)
				p.put(ir, dist)
				data, err := json.Marshal(ir)
				if err != nil {
					t.Fatal(err)
				}
				var want []string
				if p.container != "" && !validate.Checked(p.holder) {
					want = append(want, p.container)
				}
				if !validate.Checked(kind) {
					want = append(want, p.path)
				}
				r := run(t, data)
				if got := gatePaths(t, r.Issues, ""); !slices.Equal(got, want) {
					t.Errorf("validator: gate at %v, want %v (issues %v)", got, want, r.Issues)
				}
				if len(want) > 0 && r.Valid() {
					t.Error("validator: a gated model is valid")
				}
				if r.IR == nil {
					t.Fatal("not schema-valid")
				}
				_, cerr := engine.Compile(simulation.RunInput{Model: *r.IR, Config: simulation.RunConfig{
					Horizon: simulation.Duration{Value: 100, Unit: model.Minute}, Warmup: simulation.Duration{Value: 0, Unit: model.Minute},
					Replications: 1, Seed: 1, TraceLevel: simulation.TraceNone},
					Limits: simulation.ExecutionLimits{WallTimeoutSeconds: 10, MaxEvents: 1000, MaxActiveEntities: 100, MaxEntityVisits: 100}})
				var ie *engine.InputError
				if len(want) > 0 && !errors.As(cerr, &ie) {
					t.Fatalf("Compile accepted a gated model: %v", cerr)
				}
				if ie != nil {
					if got := gatePaths(t, ie.Issues, "/model"); !slices.Equal(got, want) {
						t.Errorf("Compile: gate at %v, want %v (issues %v)", got, want, ie.Issues)
					}
				}
			})
		}
	}
}

// gatePaths returns the paths of the gate's issues, without root, and
// fails t on any other issue.
func gatePaths(t *testing.T, issues []model.ValidationIssue, root string) []string {
	t.Helper()
	var out []string
	for _, is := range issues {
		if is.Code != validate.PrimitiveNotSupported || is.Severity != model.SeverityError || !strings.HasPrefix(is.Path, root) {
			t.Errorf("issue %+v is not the gate's", is)
			continue
		}
		out = append(out, strings.TrimPrefix(is.Path, root))
	}
	return out
}

// Every place model-ir.schema.json allows a Distribution, reached from the
// root through properties, items and the local $defs, is a position of
// distributionPositions: a new one cannot be added without a gate test.
func TestDistributionPositionsCoverTheSchema(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "schemas/model-ir.schema.json")), &schema); err != nil {
		t.Fatal(err)
	}
	defs := schema["$defs"].(map[string]any)
	var found []string
	var walk func(node any, at []string, via []string)
	walk = func(node any, at []string, via []string) {
		m, ok := node.(map[string]any)
		if !ok {
			return
		}
		if ref, ok := m["$ref"].(string); ok {
			name, local := strings.CutPrefix(ref, "#/$defs/")
			switch {
			case ref == "#/$defs/Distribution":
				found = append(found, "/"+strings.Join(at, "/"))
				return
			case local && !slices.Contains(via, name):
				walk(defs[name], at, append(slices.Clone(via), name))
			}
		}
		if props, ok := m["properties"].(map[string]any); ok {
			for _, k := range slices.Sorted(mapKeys(props)) {
				walk(props[k], append(slices.Clone(at), k), via)
			}
		}
		walk(m["items"], append(slices.Clone(at), "*"), via)
		walk(m["additionalProperties"], append(slices.Clone(at), "+"), via)
		for _, key := range []string{"oneOf", "anyOf", "allOf"} {
			alts, _ := m[key].([]any)
			for _, a := range alts {
				walk(a, at, via)
			}
		}
	}
	walk(schema, nil, nil)
	slices.Sort(found)
	found = slices.Compact(found)
	var table []string
	for _, p := range distributionPositions {
		tokens := strings.Split(p.path, "/")
		for i, tok := range tokens {
			if _, err := strconv.Atoi(tok); err == nil {
				tokens[i] = "*"
			}
		}
		table = append(table, strings.Join(tokens, "/"))
	}
	slices.Sort(table)
	if len(found) != 7 || !slices.Equal(found, table) {
		t.Errorf("schema positions %v, table %v", found, table)
	}
}

func mapKeys[V any](m map[string]V) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}
