package aievals_test

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/aievals"
	"github.com/kcl1104/geminus/internal/config"
	"github.com/kcl1104/geminus/internal/contracttest"
)

// H-061 §14 (A5): cmd/ai-eval-live -suite investigator -planner <model>.
// No test here calls a model: the backend is the fake behind a billable
// wrapper; the key is a placeholder.

// plannerBilled is a billable planner over the fake: its quotes and
// answers name the model asked for.
type plannerBilled struct {
	*fake.Backend
	turns atomic.Int32
	reqs  []provider.Request
}

func (b *plannerBilled) Quote(_ context.Context, req provider.Request) (provider.Quote, error) {
	return provider.Quote{Model: req.Model, MaxInputTokens: 10_000}, nil
}

func (b *plannerBilled) Turn(ctx context.Context, req provider.Request) (provider.Response, error) {
	b.turns.Add(1)
	b.reqs = append(b.reqs, req)
	resp, err := b.Backend.Turn(ctx, req)
	resp.Model, resp.Usage = req.Model, provider.Usage{InputTokens: 4000, OutputTokens: 120}
	return resp, err
}

func liveH029() map[string]string {
	return map[string]string{"AI_PROVIDER": "meta", "AI_POLICY_APPROVAL": "H-029", "AI_MODEL": "muse-spark-1.3", "GEMINUS_MUSE_API_KEY": museKey,
		"GEMINUS_AI_LIVE": "on"}
}

// Refused before any backend is built or anything written: another suite,
// the planner's flags without the suite, the suite with a smoke, live AI
// off, a planner the approval does not list, an unknown transcript or case.
func TestTheInvestigatorSuiteRefusesBeforeAnyCall(t *testing.T) {
	root := contracttest.Root(t)
	off := liveH029()
	delete(off, "GEMINUS_AI_LIVE")
	for name, c := range map[string]struct {
		env  map[string]string
		args []string
		want string
	}{
		"another suite":          {liveH029(), []string{"-suite", "planner"}, "-suite is investigator or unset"},
		"a planner, no suite":    {liveH029(), []string{"-planner", "muse-wonder-1.0"}, "the investigator suite's"},
		"a transcript, no suite": {liveH029(), []string{"-transcript", "replay"}, "the investigator suite's"},
		"with -smoke":            {liveH029(), []string{"-suite", "investigator", "-smoke"}, "runs alone"},
		"live AI off":            {off, []string{"-suite", "investigator"}, "live AI is off (switch_off)"},
		"a planner not listed":   {liveH029(), []string{"-suite", "investigator", "-planner", "muse-spark-1.2"}, "does not serve the investigator"},
		"a Contributor planner":  {liveH029(), []string{"-suite", "investigator", "-planner", "muse-spark-1.3-contributor"}, "does not serve the investigator"},
		"an unknown transcript":  {liveH029(), []string{"-suite", "investigator", "-transcript", "both"}, "digest or replay"},
		"an unknown case":        {liveH029(), []string{"-suite", "investigator", "-cases", "INV-999"}, `no investigator case "INV-999"`},
		"a cap over the day":     {withEnv(liveH029(), "AI_EVAL_LIVE_MAX_USD", "500"), []string{"-suite", "investigator"}, "above the deployment's daily limit"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			var out, errb bytes.Buffer
			built := 0
			b := aievals.Backends{Text: func(config.AI) (provider.Billable, error) { built++; return nil, nil }}
			args := append([]string{"-root", root, "-dir", dir}, c.args...)
			code := aievals.MainWith(context.Background(), args, func(k string) string { return c.env[k] }, &out, &errb, b)
			if code != aievals.ExitRefused || built != 0 {
				t.Fatalf("exit %d, backends built %d: %s", code, built, errb.String())
			}
			if !strings.Contains(errb.String(), "REFUSED (no provider call was made)") || !strings.Contains(errb.String(), c.want) {
				t.Errorf("stderr %q, want %q", errb.String(), c.want)
			}
			if strings.Contains(errb.String()+out.String(), "SENTINEL") {
				t.Error("a key is printed")
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 0 {
				t.Errorf("%d files written", len(entries))
			}
		})
	}
}

func withEnv(m map[string]string, kv ...string) map[string]string {
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

// A run: the spend record's open line before any call, one-call planner
// turns on the planner asked for, the report of its own name with the pass
// bar and no AI text, the line closed with what it cost.
// Through the owner's base URL (GEMINUS_META_BASE_URL, the H-083
// follow-up) the report says so ("base_url": "custom") and holds no part of
// it; direct, it names none.
func TestTheInvestigatorSuiteRuns(t *testing.T) {
	for name, base := range map[string]string{"direct": "", "through a base URL": "https://proxy-test.example.workers.dev/PROXY-SECRET-SENTINEL-a1d4/"} {
		t.Run(name, func(t *testing.T) { testTheInvestigatorSuiteRuns(t, base) })
	}
}

func testTheInvestigatorSuiteRuns(t *testing.T, base string) {
	env := liveH029()
	if base != "" {
		env["GEMINUS_META_BASE_URL"] = base
	}
	root := contracttest.Root(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, aievals.RecordName), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cases, err := aievals.ReadInvestigatorCases(root)
	if err != nil {
		t.Fatal(err)
	}
	f := fake.New()
	for _, c := range cases {
		if c.ID != "INV-001" && c.ID != "INV-004" {
			continue
		}
		var answers []fake.ToolAnswer
		for _, turn := range c.Fake {
			a := fake.ToolAnswer{Text: turn.Text}
			for _, call := range turn.Calls {
				a.Calls = append(a.Calls, provider.ToolCall{ID: call.ID, Name: call.Name, Input: call.Input})
			}
			answers = append(answers, a)
		}
		f.ScriptTools(provider.RoleInvestigator, c.Request, answers...)
	}
	backend := &plannerBilled{Backend: f}
	var out, errb bytes.Buffer
	code := aievals.MainWith(context.Background(), []string{"-root", root, "-dir", dir, "-suite", "investigator", "-planner", "muse-wonder-1.0",
		"-cases", "INV-001,INV-004"}, func(k string) string { return env[k] }, &out, &errb,
		aievals.Backends{Text: func(config.AI) (provider.Billable, error) { return backend, nil }})
	if code != aievals.ExitOK {
		t.Fatalf("exit %d: %s %s", code, out.String(), errb.String())
	}
	if backend.turns.Load() != 10 {
		t.Errorf("%d planner turns", backend.turns.Load())
	}
	for _, req := range backend.reqs {
		if !req.OneCall || req.Model != "muse-wonder-1.0" || req.MaxRetries != 0 || req.MaxOutputTokens != provider.PlannerMaxOutputTokens {
			t.Fatalf("a turn %+v", req)
		}
	}
	entries, _ := os.ReadDir(dir)
	var report string
	for _, e := range entries {
		if regexp.MustCompile(`^live-investigator-[0-9T-]+Z-[0-9A-Z]{26}-muse-wonder-1\.0\.json$`).MatchString(e.Name()) {
			report = e.Name()
		}
	}
	if report == "" {
		t.Fatalf("no report among %v", entries)
	}
	data, err := os.ReadFile(filepath.Join(dir, report))
	if err != nil {
		t.Fatal(err)
	}
	var r aievals.InvestigatorReport
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	if r.Kind != "live" || r.Planner != "muse-wonder-1.0" || r.Run != 2 || r.Passed != 2 || r.PassBar == nil || r.Approval != "H-029" ||
		r.CapUSD != "4.000000000" && !strings.HasPrefix(r.CapUSD, "4") {
		t.Errorf("the report %+v", r)
	}
	if want := map[bool]string{true: config.BaseURLCustom, false: ""}[base != ""]; r.BaseURL != want {
		t.Errorf("base_url %q, want %q", r.BaseURL, want)
	}
	for _, leak := range []string{"PROXY-SECRET-SENTINEL", "proxy-test", "workers.dev"} {
		if bytes.Contains(data, []byte(leak)) || strings.Contains(out.String()+errb.String(), leak) {
			t.Errorf("%q is in the report or the output", leak)
		}
	}
	for _, o := range r.Outcomes {
		if len(o.TurnMillis) != o.PlannerTurns || o.CostNanoUSD <= 0 {
			t.Errorf("%s: %d latencies for %d turns, cost %d", o.ID, len(o.TurnMillis), o.PlannerTurns, o.CostNanoUSD)
		}
	}
	for _, text := range []string{"ICT is the busiest test station.", "Add a third ICT tester so boards wait less", cases[0].Request} {
		if bytes.Contains(data, []byte(text)) {
			t.Errorf("AI or person text in the report: %q", text)
		}
	}
	record, err := os.ReadFile(filepath.Join(dir, aievals.RecordName))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(record)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"kind":"investigator"`) || !strings.Contains(lines[1], `"event":"close"`) {
		t.Errorf("the spend record %q", record)
	}
	if bytes.Contains(record, []byte("PROXY-SECRET-SENTINEL")) || bytes.Contains(record, []byte("proxy-test")) {
		t.Errorf("the spend record holds the base URL: %q", record)
	}
}
