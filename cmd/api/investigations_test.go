package main

import (
	"context"
	"encoding/json/v2"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/config"
	"github.com/kcl1104/geminus/internal/storage/dbtest"
)

const servedOrg = "org_01M4YDQK00Y5H2929M9WWZWTVH"

// H-061 A7: what the investigation routes are given. With the fake, the
// investigator role and every offered planner; with a live provider that
// is on, only what its recorded approval lists for the investigator role:
// H-029 lists it since A5, with Muse Spark 1.3 and Muse Wonder 1.0 as its
// planners (H-060, H-063), and H-018 does not, so a live Claude API refuses
// every investigation with 503 AI_POLICY_NOT_APPROVED; with AI off,
// nothing.
func TestInvestigationOptions(t *testing.T) {
	inv := config.Investigations{Enabled: true, Organizations: []string{servedOrg}, PlannerModels: []string{"muse-spark-1.3", "muse-wonder-1.0"}}
	for _, c := range []struct {
		name     string
		env      []string
		approved bool
		planners []string
		model    string
	}{
		{"fake", []string{"AI_PROVIDER", "fake", "APP_ENV", "test"}, true, []string{"muse-spark-1.3", "muse-wonder-1.0"}, "muse-spark-1.3"},
		{"meta live, H-029 with the investigator role (A5)", apiMeta, true, []string{"muse-spark-1.3", "muse-wonder-1.0"}, "muse-spark-1.3"},
		{"claude live", apiClaude, false, nil, "claude-opus-5-5"},
		{"meta, the switch off", []string{"AI_PROVIDER", "meta", "AI_POLICY_APPROVAL", "H-029", "AI_MODEL", "muse-spark-1.3"}, false, nil, ""},
		{"disabled", nil, false, nil, ""},
	} {
		ai, err := config.LoadAPIAI(with(c.env...))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		got := investigationOptions(ai, inv)
		if !got.On || !slices.Equal(got.Organizations, inv.Organizations) || !slices.Equal(got.PlannerModels, inv.PlannerModels) ||
			got.InvestigatorApproved != c.approved || !slices.Equal(got.ApprovedPlanners, c.planners) || got.DefaultProposalModel != c.model {
			t.Errorf("%s: %+v", c.name, got)
		}
	}
	if got := investigationOptions(config.APIAI{}, config.Investigations{}); got.On || got.InvestigatorApproved {
		t.Errorf("off: %+v", got)
	}
}

// GEMINUS_INVESTIGATIONS=1 without the organizations the executor serves
// stops the API before it connects anywhere; an unknown switch value too.
func TestRunRefusesInvestigationsWithoutOrganizations(t *testing.T) {
	for _, env := range [][]string{{"GEMINUS_INVESTIGATIONS", "1"}, {"GEMINUS_INVESTIGATIONS", "yes"},
		{"GEMINUS_INVESTIGATIONS", "1", "INVESTIGATION_ORGANIZATION_IDS", "org_a"},
		{"GEMINUS_INVESTIGATIONS", "1", "INVESTIGATION_ORGANIZATION_IDS", servedOrg, "INVESTIGATION_PLANNER_MODELS", "gpt"}} {
		var stderr syncBuffer
		if code := refused(t, with(env...), &stderr); code != exitConfig {
			t.Errorf("%v: exit %d: %s", env, code, stderr.String())
		}
		if !strings.Contains(stderr.String(), "INVESTIGATION") {
			t.Errorf("%v: the log does not name the variable: %s", env, stderr.String())
		}
	}
}

// With investigations on, the API serves, says so (a count, never which
// organizations), runs its executor beside the server and stops both.
func TestRunWithInvestigations(t *testing.T) {
	dbtest.Admin(t) // skips unless the disposable database is configured; creates the API login
	var stderr syncBuffer
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan string, 1)
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, with("DATABASE_URL", dbtest.LoginURL(t, dbtest.APILogin), "APP_ENV", "test", "AI_PROVIDER", "fake",
			"GEMINUS_INVESTIGATIONS", "1", "INVESTIGATION_ORGANIZATION_IDS", servedOrg), &stderr, ready)
	}()
	select {
	case <-ready:
	case code := <-done:
		t.Fatalf("exit %d before serving: %s", code, stderr.String())
	case <-time.After(30 * time.Second):
		t.Fatal("not serving after 30 s")
	}
	time.Sleep(1200 * time.Millisecond) // a few discovery passes in an organization with nothing pending
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit %d after shutdown: %s", code, stderr.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatal("did not stop after its context ended")
	}
	on := false
	for _, l := range strings.Split(strings.TrimSpace(stderr.String()), "\n") {
		var line map[string]any
		if json.Unmarshal([]byte(l), &line) != nil {
			continue
		}
		if line["code"] == "INVESTIGATIONS_ON" && line["organizations"] == float64(1) {
			on = true
		}
		if line["level"] == "WARN" || line["level"] == "ERROR" {
			t.Errorf("a warning or an error: %s", l)
		}
	}
	if !on || strings.Contains(stderr.String(), servedOrg) {
		t.Errorf("the investigations line: %s", stderr.String())
	}
}
