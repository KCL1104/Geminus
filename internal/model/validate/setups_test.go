package validate_test

import (
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/model/validate"
)

// T-050: the rules of changeovers (D-055, SETUP_INVALID and
// UNKNOWN_REFERENCE) on the minimal model (batches_test.go has its
// helpers); the details the approved text leaves open are D-097 (g).

// setups gives resource i of the minimal model changeovers; the model
// gets a second entity type "board" (no source makes one) so that
// transitions have two types to name.
func setups(i int, s map[string]any) func(ir map[string]any) {
	return func(ir map[string]any) {
		ir["entity_types"] = append(ir["entity_types"].([]any), map[string]any{"id": "board", "name": "Board"})
		obj(resources(ir)[i])["setups"] = s
	}
}

func transition(from, to string, d map[string]any) map[string]any {
	return map[string]any{"from": from, "to": to, "distribution": d}
}

func TestSetupRules(t *testing.T) {
	tri := map[string]any{"type": "triangular", "min": 15, "mode": 25, "max": 45}
	checkCases(t, []struct {
		name string
		edit func(ir map[string]any)
		want [][3]string
	}{
		{"a default only", setups(0, map[string]any{"default": constantDist(5)}), nil},
		{"a zero default", setups(0, map[string]any{"default": constantDist(0)}), nil},
		{"transitions both ways", setups(0, map[string]any{"transitions": []any{transition("change", "board", tri), transition("board", "change", constantDist(3))}}), nil},
		{"an initial type", setups(0, map[string]any{"initial": "board", "default": constantDist(5)}), nil},
		{"lognormal and Weibull changeovers", setups(0, map[string]any{"default": map[string]any{"type": "lognormal", "mean": 5, "cv": 0.5},
			"transitions": []any{transition("change", "board", map[string]any{"type": "weibull", "shape": 2, "scale": 20})}}), nil},
		// D-055: entity types that exist (UNKNOWN_REFERENCE, the reference stage).
		{"an unknown initial type", setups(0, map[string]any{"initial": "widget", "default": constantDist(5)}),
			[][3]string{{validate.UnknownReference, "/resources/0/setups/initial", "error"}}},
		{"an unknown from", setups(0, map[string]any{"transitions": []any{transition("widget", "board", constantDist(1))}}),
			[][3]string{{validate.UnknownReference, "/resources/0/setups/transitions/0/from", "error"}}},
		{"an unknown to", setups(0, map[string]any{"transitions": []any{transition("change", "widget", constantDist(1))}}),
			[][3]string{{validate.UnknownReference, "/resources/0/setups/transitions/0/to", "error"}}},
		// D-055: from != to and no pair twice (SETUP_INVALID at the transition).
		{"a self transition", setups(0, map[string]any{"transitions": []any{transition("change", "change", constantDist(5))}}),
			[][3]string{{validate.SetupInvalid, "/resources/0/setups/transitions/0", "error"}}},
		{"a pair twice", setups(0, map[string]any{"transitions": []any{transition("change", "board", constantDist(5)),
			transition("board", "change", constantDist(5)), transition("change", "board", constantDist(6))}}),
			[][3]string{{validate.SetupInvalid, "/resources/0/setups/transitions/2", "error"}}},
		// The changeover times are resource distributions (D-029, D-033, D-056).
		{"a malformed default", setups(0, map[string]any{"default": map[string]any{"type": "triangular", "min": 5, "mode": 3, "max": 9}}),
			[][3]string{{validate.DistributionInvalid, "/resources/0/setups/default", "error"}}},
		{"a changeover over 365 days", setups(0, map[string]any{"transitions": []any{transition("change", "board", constantDist(366*day))}}),
			[][3]string{{validate.LimitExceeded, "/resources/0/setups/transitions/0/distribution/value", "error"}}},
		{"a Weibull shape out of range", setups(0, map[string]any{"default": map[string]any{"type": "weibull", "shape": 30, "scale": 2}}),
			[][3]string{{validate.DistributionInvalid, "/resources/0/setups/default", "error"}}},
		// D-055's P0 limits (SETUP_INVALID at the requirement).
		{"two units of a resource with changeovers", both(setups(0, map[string]any{"default": constantDist(5)}), func(ir map[string]any) {
			obj(resources(ir)[0])["capacity"] = 2
			obj(obj(nodes(ir)[1])["requires"].([]any)[0])["units"] = 2
		}), [][3]string{{validate.SetupInvalid, "/nodes/1/requires/0", "error"}}},
		{"two resources with changeovers in one bundle", both(withMachine(1, 1, nil), setups(0, map[string]any{"default": constantDist(5)}),
			func(ir map[string]any) { obj(resources(ir)[1])["setups"] = map[string]any{"default": constantDist(2)} }),
			[][3]string{{validate.SetupInvalid, "/nodes/1/requires/1", "error"}}},
		{"one resource with changeovers and one without", both(withMachine(1, 1, nil), setups(0, map[string]any{"default": constantDist(5)})), nil},
		{"a batch process needing a resource with changeovers", both(batch(2, 2), setups(0, map[string]any{"default": constantDist(5)})),
			[][3]string{{validate.SetupInvalid, "/nodes/1/requires/0", "error"}}},
		{"a resource with changeovers and breakdowns", both(setups(0, map[string]any{"default": constantDist(5)}), func(ir map[string]any) {
			obj(resources(ir)[0])["breakdowns"] = breakdowns(constantDist(100), constantDist(5))
		}), nil},
	})
}

// The SETUP_INVALID messages say which rule and what to do.
func TestSetupMessages(t *testing.T) {
	for _, c := range []struct {
		edit func(ir map[string]any)
		want []string
	}{
		{setups(0, map[string]any{"transitions": []any{transition("change", "change", constantDist(5))}}), []string{"differ"}},
		{both(batch(2, 2), setups(0, map[string]any{"default": constantDist(5)})), []string{"batch", "D-055"}},
		{both(withMachine(1, 1, nil), setups(0, map[string]any{"default": constantDist(5)}),
			func(ir map[string]any) { obj(resources(ir)[1])["setups"] = map[string]any{"default": constantDist(2)} }), []string{"at most one", `"worker"`}},
	} {
		r := run(t, edit(t, c.edit))
		if len(r.Issues) != 1 {
			t.Fatalf("issues %v", r.Issues)
		}
		for _, w := range c.want {
			if !strings.Contains(r.Issues[0].Message, w) {
				t.Errorf("message %q does not say %q", r.Issues[0].Message, w)
			}
		}
	}
}
