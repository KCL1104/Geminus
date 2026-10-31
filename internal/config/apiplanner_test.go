package config

import "testing"

// H-061 §9 (A5): the API's availability check of an investigation's
// planner, from the same key-free decision: a live provider whose approval
// serves the investigator, takes its tool calls and the planner model (the
// recorded H-029: Muse Spark 1.3 and Muse Wonder 1.0), with the switch on;
// the fake in development and tests, for the two planner models; nothing
// for a disabled provider, the switch off, or Claude's H-018 (no tools, no
// investigator: 503 AI_POLICY_NOT_APPROVED). Whether a deployment offers
// Muse Wonder is a separate gate (INVESTIGATION_PLANNER_MODELS, A7).
func TestPlannerAvailability(t *testing.T) {
	off := apiMeta()
	off["GEMINUS_AI_LIVE"] = "off"
	for name, c := range map[string]struct {
		env      map[string]string
		approved bool
		planners map[string]bool
	}{
		"meta, live":       {apiMeta(), true, map[string]bool{"muse-spark-1.3": true, "muse-wonder-1.0": true}},
		"meta, switch off": {off, false, nil},
		"claude, live":     {apiClaude(), false, nil},
		"the fake":         {map[string]string{"AI_PROVIDER": "fake", "APP_ENV": "test"}, true, map[string]bool{"muse-spark-1.3": true, "muse-wonder-1.0": true}},
		"disabled":         {map[string]string{}, false, nil},
	} {
		a, err := LoadAPIAI(env(c.env))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := a.InvestigatorApproved(); got != c.approved {
			t.Errorf("%s: investigator approved %v, want %v", name, got, c.approved)
		}
		for _, m := range []string{"muse-spark-1.3", "muse-wonder-1.0", "muse-spark-1.3-contributor", "muse-spark-1.2", "claude-opus-5-5", ""} {
			if got := a.PlannerApproved(m); got != c.planners[m] {
				t.Errorf("%s: planner %q approved %v, want %v", name, m, got, c.planners[m])
			}
		}
	}
	if (APIAI{}).InvestigatorApproved() || (APIAI{}).PlannerApproved("muse-spark-1.3") {
		t.Error("the zero APIAI approves a planner")
	}
}
