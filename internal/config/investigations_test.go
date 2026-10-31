package config

import (
	"slices"
	"testing"
)

const (
	orgA = "org_01M4YDQK00Y5H2929M9WWZWTVH"
	orgB = "org_01M4YDQK00Y5H2929M9WWZWTVJ"
)

// H-061 (A7): investigations are off unless GEMINUS_INVESTIGATIONS is
// exactly "1"; unset and "0" are off, and nothing else is read as either.
func TestLoadInvestigationsSwitch(t *testing.T) {
	for raw, want := range map[string]bool{"": false, "0": false, "1": true} {
		kv := map[string]string{"GEMINUS_INVESTIGATIONS": raw}
		if want {
			kv["INVESTIGATION_ORGANIZATION_IDS"] = orgA
		}
		got, err := LoadInvestigations(env(kv))
		if err != nil || got.Enabled != want {
			t.Errorf("%q: %+v %v", raw, got, err)
		}
	}
	for _, raw := range []string{"true", "on", "yes", "2", " 1", "1 ", "01", "TRUE", "off", "false"} {
		_, err := LoadInvestigations(env(map[string]string{"GEMINUS_INVESTIGATIONS": raw, "INVESTIGATION_ORGANIZATION_IDS": orgA}))
		if err == nil || !slices.Equal(codes(t, err), []string{"GEMINUS_INVESTIGATIONS:invalid"}) {
			t.Errorf("%q: %v", raw, err)
		}
	}
}

// INVESTIGATION_ORGANIZATION_IDS are the organizations the executor
// polls (the worker's allowlist, by a Railway reference): required once
// investigations are on, organization IDs each listed once, in order.
func TestLoadInvestigationsOrganizations(t *testing.T) {
	got, err := LoadInvestigations(env(map[string]string{"GEMINUS_INVESTIGATIONS": "1", "INVESTIGATION_ORGANIZATION_IDS": orgB + ", " + orgA}))
	if err != nil || !slices.Equal(got.Organizations, []string{orgB, orgA}) {
		t.Errorf("two: %+v %v", got, err)
	}
	if _, err := LoadInvestigations(env(map[string]string{"GEMINUS_INVESTIGATIONS": "1"})); err == nil ||
		!slices.Equal(codes(t, err), []string{"INVESTIGATION_ORGANIZATION_IDS:required"}) {
		t.Errorf("on without organizations: %v", err)
	}
	for _, raw := range []string{"org_a", orgA + "," + orgA, orgA + ",", "usr_01M4YDQK00Y5H2929M9WWZWTVH", " , "} {
		if _, err := LoadInvestigations(env(map[string]string{"GEMINUS_INVESTIGATIONS": "1", "INVESTIGATION_ORGANIZATION_IDS": raw})); err == nil ||
			!slices.Equal(codes(t, err), []string{"INVESTIGATION_ORGANIZATION_IDS:invalid"}) {
			t.Errorf("%q: %v", raw, err)
		}
	}
	// Off, the list is not read at all: a deployment that declares it before
	// the switch is turned on starts as before, whatever it holds, and the
	// executor serves nothing.
	if got, err := LoadInvestigations(env(map[string]string{"INVESTIGATION_ORGANIZATION_IDS": "org_a"})); err != nil || got.Organizations != nil {
		t.Errorf("off, a list that is not one: %+v %v", got, err)
	}
	if got, err := LoadInvestigations(env(map[string]string{"GEMINUS_INVESTIGATIONS": "0", "INVESTIGATION_ORGANIZATION_IDS": orgA})); err != nil ||
		got.Enabled || got.Organizations != nil {
		t.Errorf("off, a valid list: %+v %v", got, err)
	}
	if got, err := LoadInvestigations(env(nil)); err != nil || got.Enabled || got.Organizations != nil {
		t.Errorf("nothing set: %+v %v", got, err)
	}
}

// INVESTIGATION_PLANNER_MODELS are the planners a person may choose
// (H-061 §14 gate 2): Muse Spark 1.3 unless set; Muse Wonder 1.0 only when
// the coordinator lists it after a recorded pass of its evaluation.
func TestLoadInvestigationsPlannerModels(t *testing.T) {
	base := map[string]string{"GEMINUS_INVESTIGATIONS": "1", "INVESTIGATION_ORGANIZATION_IDS": orgA}
	with := func(raw string) map[string]string {
		kv := map[string]string{}
		for k, v := range base {
			kv[k] = v
		}
		kv["INVESTIGATION_PLANNER_MODELS"] = raw
		return kv
	}
	for raw, want := range map[string][]string{
		"":                                {"muse-spark-1.3"},
		"muse-spark-1.3":                  {"muse-spark-1.3"},
		"muse-spark-1.3,muse-wonder-1.0":  {"muse-spark-1.3", "muse-wonder-1.0"},
		"muse-wonder-1.0, muse-spark-1.3": {"muse-wonder-1.0", "muse-spark-1.3"},
	} {
		got, err := LoadInvestigations(env(with(raw)))
		if err != nil || !slices.Equal(got.PlannerModels, want) {
			t.Errorf("%q: %+v %v", raw, got, err)
		}
	}
	for _, raw := range []string{"muse-spark", "claude-sonnet-4-5", "muse-spark-1.3,muse-spark-1.3", "muse-spark-1.3,", ",", "MUSE-SPARK-1.3"} {
		if _, err := LoadInvestigations(env(with(raw))); err == nil || !slices.Equal(codes(t, err), []string{"INVESTIGATION_PLANNER_MODELS:invalid"}) {
			t.Errorf("%q: %v", raw, err)
		}
	}
	// Every problem is reported together.
	if _, err := LoadInvestigations(env(map[string]string{"GEMINUS_INVESTIGATIONS": "1", "INVESTIGATION_ORGANIZATION_IDS": "x",
		"INVESTIGATION_PLANNER_MODELS": "x"})); err == nil || !slices.Equal(codes(t, err),
		[]string{"INVESTIGATION_ORGANIZATION_IDS:invalid", "INVESTIGATION_PLANNER_MODELS:invalid"}) {
		t.Errorf("nothing valid: %v", err)
	}
}
