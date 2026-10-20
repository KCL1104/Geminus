package review

import (
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/patch"
)

// H-003 review M4: each rule of D-062 (d) on its own. Every case changes a
// valid base (the default pipeline, or that pipeline after a person's
// edit, before) into a valid candidate that only that rule classifies, so
// that dropping or weakening the rule changes the result. (A new cycle
// needs a new or re-targeted flow, which is medium by its own rule, so the
// new-cycle rule has no case of its own.)
func TestEachRiskRule(t *testing.T) {
	doc := base(t)
	p := pool(t, doc)
	// Bases a person could have published: rework that no longer loops
	// back (the failure node is on no cycle), and a retry straight back to
	// CI (a decision on a cycle that routes to a process, not to a failure
	// or rework node); CI needing two runners.
	noLoop := `[{"op":"test","path":"/flows/6/id","value":"f_rework_ci"},{"op":"replace","path":"/flows/6/to","value":"test"}]`
	retry := `[{"op":"test","path":"/flows/4/id","value":"f_retry"},{"op":"replace","path":"/flows/4/to","value":"ci"}]`
	twoRunners := `[{"op":"test","path":"/nodes/2/id","value":"ci"},{"op":"replace","path":"/nodes/2/requires/0/units","value":2}]`
	moreRetry := `[{"op":"test","path":"/flows/4/id","value":"f_retry"},{"op":"replace","path":"/flows/4/routing/probability","value":0.3},
		{"op":"test","path":"/flows/3/id","value":"f_pass"},{"op":"replace","path":"/flows/3/routing/probability","value":0.7}]`
	lessRetry := `[{"op":"test","path":"/flows/4/id","value":"f_retry"},{"op":"replace","path":"/flows/4/routing/probability","value":0.1},
		{"op":"test","path":"/flows/3/id","value":"f_pass"},{"op":"replace","path":"/flows/3/routing/probability","value":0.9}]`
	// A shortcut from the CI result straight to deploy, and a sink no
	// flow enters (UNREACHABLE_NODE, a warning: the base reaches it not).
	shortcut := `[{"op":"test","path":"/flows/3/id","value":"f_pass"},{"op":"replace","path":"/flows/3/routing/probability","value":0.7},
		{"op":"add","path":"/flows/-","value":{"id":"f_skip","from":"ci_result","to":"deploy","routing":{"type":"probability","probability":0.1}}}]`
	orphan := `[{"op":"add","path":"/nodes/-","value":{"id":"orphan","type":"sink","outcome":"completed"}},
		{"op":"add","path":"/views/2d/positions/orphan","value":{"x":0,"y":0}}]`
	for _, c := range []struct {
		name, before, changes string
		want                  Risk
	}{
		{"a node changes its type", "", `[{"op":"test","path":"/nodes/5/id","value":"ci_rework"},
			{"op":"replace","path":"/nodes/5","value":{"id":"ci_rework","type":"delay","delay_time":{"distribution":{"type":"constant","value":1}}}}]`, High},
		// Rework goes on to the test step: every node is still reached.
		{"a flow is re-targeted", "", `[{"op":"test","path":"/flows/6/id","value":"f_rework_ci"},{"op":"replace","path":"/flows/6/to","value":"test"}]`, Medium},
		// D-062 (i) B (H-019): the test step goes straight to the sink, so
		// no source reaches deploy any more; that counts as its deletion.
		{"a re-target cuts a node off", "", `[{"op":"test","path":"/flows/7/id","value":"f_test_deploy"},{"op":"replace","path":"/flows/7/to","value":"deployed"}]`, High},
		{"a constraint is removed", "", `[{"op":"test","path":"/constraints/0/node_id","value":"ci_result"},{"op":"remove","path":"/constraints/0"}]`, Medium},
		{"an entity type is added", "", `[{"op":"add","path":"/entity_types/-","value":{"id":"hotfix","name":"Hotfix"}}]`, Medium},
		{"a metric is added", "", `[{"op":"add","path":"/metrics/-","value":{"id":"rejections","type":"failure_count"}}]`, Medium},
		{"a node is added", "", `[{"op":"add","path":"/nodes/-","value":{"id":"` + p.Node[0] + `","type":"sink","outcome":"completed"}}]`, Medium},
		{"a flow is added", "", `[{"op":"test","path":"/flows/3/id","value":"f_pass"},{"op":"replace","path":"/flows/3/routing/probability","value":0.7},
			{"op":"add","path":"/flows/-","value":{"id":"` + p.Flow[0] + `","from":"ci_result","to":"deploy","routing":{"type":"probability","probability":0.1}}}]`, Medium},
		{"a flow is removed", shortcut, `[{"op":"test","path":"/flows/9/id","value":"f_skip"},{"op":"remove","path":"/flows/9"},
			{"op":"test","path":"/flows/3/id","value":"f_pass"},{"op":"replace","path":"/flows/3/routing/probability","value":0.8}]`, Medium},
		// Without the retry flow no source reaches the failure and rework
		// nodes: cut off like a deletion.
		{"a removed flow cuts nodes off", "", `[{"op":"test","path":"/flows/4/id","value":"f_retry"},{"op":"remove","path":"/flows/4"},
			{"op":"test","path":"/flows/3/id","value":"f_pass"},{"op":"replace","path":"/flows/3/routing/probability","value":1}]`, High},
		// A node the base does not reach is cut off by nothing.
		{"a node no source reached stays so", orphan, `[{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"replace","path":"/resources/1/capacity","value":6}]`, Low},
		{"another resource", "", `[{"op":"test","path":"/nodes/2/id","value":"ci"},{"op":"replace","path":"/nodes/2/requires/0/resource_id","value":"test_environment"}]`, Medium},
		// L1: D-062 (d) says the quantity changing, not only rising.
		{"fewer units", twoRunners, `[{"op":"test","path":"/nodes/2/id","value":"ci"},{"op":"replace","path":"/nodes/2/requires/0/units","value":1}]`, Medium},
		{"more units", "", `[{"op":"test","path":"/nodes/2/id","value":"ci"},{"op":"replace","path":"/nodes/2/requires/0/units","value":2}]`, Medium},
		{"the same units", twoRunners, `[{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"replace","path":"/resources/1/capacity","value":6}]`, Low},
		{"more routing into a failure node on no cycle", noLoop, moreRetry, Medium},
		{"less routing into a failure node on no cycle", noLoop, lessRetry, Low},
		{"more routing on a cycle, into a process", retry, moreRetry, Medium},
		{"less routing on a cycle, into a process", retry, lessRetry, Low},
	} {
		t.Run(c.name, func(t *testing.T) {
			before := doc
			if c.before != "" {
				res, err := patch.Apply(doc, []byte(c.before), patch.ManualDraft)
				if err != nil || !res.Valid() {
					t.Fatalf("the base edit does not apply: %v %v %v", err, res.PatchIssues, res.Validation.Issues)
				}
				before = res.Candidate
			}
			res, err := patch.Apply(before, []byte(c.changes), patch.AIEdit)
			if err != nil || !res.Valid() {
				t.Fatalf("test patch does not apply: %v %v %v", err, res.PatchIssues, res.Validation.Issues)
			}
			if got := ServerRisk(decoded(t, before), res.Validation.IR); got != c.want {
				t.Fatalf("risk %q, want %q", got, c.want)
			}
		})
	}
}

// M4: the domain pack's rules are a stage of the review: an error makes the
// proposal invalid with the pack's code, a warning stays on a ready one.
func TestThePacksRules(t *testing.T) {
	raise := `[{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"replace","path":"/resources/1/capacity","value":6}]`
	in := input(t, provider.ModeEdit, proposed(ops(t, raise), "low"))
	in.Pack = stubPack{issues: []model.ValidationIssue{model.NewIssue("PACK_RULE", "/resources/1/capacity", "the pack refuses it", model.SeverityError)}}
	wantInvalid(t, run(t, in), "PACK_RULE", "/resources/1/capacity")
	in.Pack = stubPack{issues: []model.ValidationIssue{model.NewIssue("PACK_RULE", "/resources/1/capacity", "the pack warns", model.SeverityWarning)}}
	o := run(t, in)
	if o.Status != Ready || !slices.Contains(codes(o)["/resources/1/capacity"], "PACK_RULE") {
		t.Fatalf("a pack warning: %q %v", o.Status, codes(o))
	}
}
