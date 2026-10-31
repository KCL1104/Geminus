package aievals

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/aicontext"
	"github.com/kcl1104/geminus/internal/ai/investigator"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/packs"
)

// H-061 §14 (A5): the investigator suite under the deterministic fake, in
// make ai-eval (checks/ai-evals/fake-investigator.json): every case ends as
// it must, carries out exactly its tools, counts its invalid turns, and no
// call the mode does not offer, or with arguments the registry does not
// read, is ever carried out; no AI text reaches the report.

func fakeConfig(t *testing.T) InvestigatorConfig {
	t.Helper()
	reg, err := packs.Registry()
	if err != nil {
		t.Fatal(err)
	}
	cases, err := ReadInvestigatorCases(contracttest.Root(t))
	if err != nil {
		t.Fatal(err)
	}
	cfg := InvestigatorConfig{Budget: plannerBudget(), Packs: reg, Cases: cases, Planner: "muse-spark-1.3", Locale: "en"}
	cfg.ClientFor = func(c InvestigatorCase) (*provider.Client, error) { return fakeClient(t, cfg, c) }
	return cfg
}

func plannerBudget() provider.Budget {
	b := provider.DefaultBudget()
	b.MaxPromptBytes = 256 << 10 // the brief and a 14-step digest (H-061 §9)
	return b
}

// fakeClient is the fake scripted with a case's turns, under the person's
// request as the brief sends it (sanitized).
func fakeClient(t *testing.T, cfg InvestigatorConfig, c InvestigatorCase) (*provider.Client, error) {
	t.Helper()
	in, err := briefInput(cfg, c)
	if err != nil {
		return nil, err
	}
	in.Left = aicontext.InvestigationBudget(capsOf(c))
	p, err := aicontext.Investigation(in)
	if err != nil {
		return nil, err
	}
	blocks, err := provider.ParseUser(p.User())
	if err != nil {
		return nil, err
	}
	key := ""
	for _, b := range blocks {
		if b.Name == fake.RequestBlock {
			if err := json.Unmarshal(b.Content, &key); err != nil {
				return nil, err
			}
		}
	}
	f := fake.New()
	var answers []fake.ToolAnswer
	for _, turn := range c.Fake {
		a := fake.ToolAnswer{Text: turn.Text}
		for _, call := range turn.Calls {
			a.Calls = append(a.Calls, provider.ToolCall{ID: call.ID, Name: call.Name, Input: call.Input})
		}
		answers = append(answers, a)
	}
	f.ScriptTools(provider.RoleInvestigator, key, answers...)
	return provider.New(f, cfg.Budget)
}

func TestInvestigatorEval(t *testing.T) {
	cfg := fakeConfig(t)
	if len(cfg.Cases) != 6 {
		t.Fatalf("%d cases", len(cfg.Cases))
	}
	var reports [][]byte
	for _, mode := range []investigator.TranscriptMode{investigator.TranscriptDigest, investigator.TranscriptReplay} {
		cfg.Transcript = mode
		r, err := RunInvestigator(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range r.Outcomes {
			if !o.Passed {
				t.Errorf("%s %s: %v (end %s, outcome %q, calls %+v, invalid %d)", mode, o.ID, o.Failures, o.End, o.Outcome, o.Calls, o.InvalidTurns)
			}
			if o.Forbidden != 0 || o.Garbled != 0 {
				t.Errorf("%s %s: forbidden %d garbled %d", mode, o.ID, o.Forbidden, o.Garbled)
			}
		}
		if !r.Ok() || r.Run != 6 || r.Passed != 6 || r.TemplateVersion != "investigator-0.1.0" {
			t.Errorf("%s: %d of %d passed, gates %v", mode, r.Passed, r.Run, r.Gates)
		}
		data, err := r.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		// No AI text, request, prompt or note in the report.
		for _, c := range cfg.Cases {
			for _, turn := range c.Fake {
				if turn.Text != "" && bytes.Contains(data, []byte(turn.Text)) {
					t.Errorf("%s: a note in the report", c.ID)
				}
				for _, call := range turn.Calls {
					var a map[string]any
					_ = json.Unmarshal(call.Input, &a)
					if idea, ok := a["idea"].(string); ok && bytes.Contains(data, []byte(idea)) {
						t.Errorf("%s: an idea in the report", c.ID)
					}
				}
			}
			if c.Request != "" && bytes.Contains(data, []byte(c.Request[:20])) {
				t.Errorf("%s: the request in the report", c.ID)
			}
		}
		again, err := RunInvestigator(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		if d2, _ := again.Marshal(); !bytes.Equal(data, d2) {
			t.Errorf("%s: the report is not deterministic", mode)
		}
		reports = append(reports, data)
	}
	// INV-006: the injected calls are invalid turns, recorded, never carried
	// out; only allowlisted tools were.
	r, _ := RunInvestigator(context.Background(), cfg)
	for _, o := range r.Outcomes {
		if o.ID != "INV-006" {
			continue
		}
		var invalid []string
		for _, c := range o.Calls {
			if c.Tool == investigator.InvalidCall {
				invalid = append(invalid, c.Code)
			}
		}
		if !slices.Equal(invalid, []string{"UNKNOWN_TOOL", "UNKNOWN_TOOL", "INVALID_ARGUMENTS"}) || o.InvalidKinds["unknown_tool"] != 2 || o.InvalidKinds["invalid_arguments"] != 1 {
			t.Errorf("INV-006: invalid steps %v kinds %v", invalid, o.InvalidKinds)
		}
	}
	if path := os.Getenv("GEMINUS_AI_EVAL_INVESTIGATOR_REPORT"); path != "" {
		if err := os.WriteFile(path, reports[0], 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// The gates bite: a planner that ends early, makes two invalid turns in a
// row, or calls a capped tool fails its case.
func TestInvestigatorGatesFail(t *testing.T) {
	cfg := fakeConfig(t)
	bad := cfg.Cases[0]
	bad.Fake = slices.Clone(bad.Fake[:1])
	bad.Fake = append(bad.Fake, FakeTurn{Calls: []FakeCall{{ID: "fake_x", Name: "finish", Input: jsontext.Value(`{"outcome":"improvement_found","best_scenario_id":null}`)}}})
	twice := cfg.Cases[1]
	twice.Fake = []FakeTurn{{Calls: []FakeCall{{ID: "x1", Name: "publish_version", Input: jsontext.Value(`{}`)}}},
		{Text: "no call"}}
	cfg.Cases = []InvestigatorCase{bad, twice}
	r, err := RunInvestigator(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if r.Ok() || r.Passed != 0 || r.Gates["expectations"] != 2 {
		t.Fatalf("gates %v passed %d", r.Gates, r.Passed)
	}
	if o := r.Outcomes[0]; !slices.Contains(o.Failures, "tools") || o.End != "finished_by_ai" {
		t.Errorf("an early finish: %+v", o)
	}
	if o := r.Outcomes[1]; o.End != "invalid_answer" || o.InvalidTurns != 2 || o.Steps != 1 {
		t.Errorf("two invalid turns: end %s, invalid %d, steps %d", o.End, o.InvalidTurns, o.Steps)
	}
}

// The environment is the executor's stand-in: only the investigation's own
// resources, the caps, and the refusal codes the planner reacts to.
func TestInvestigatorEnvironment(t *testing.T) {
	cfg := fakeConfig(t)
	c := cfg.Cases[0]
	e := newEnv(c, "en")
	var o InvestigatorOutcome
	callOf := func(name, input string) provider.ToolCall {
		d, err := investigator.Decode(c.Mode, name, []byte(input))
		if err != nil {
			t.Fatalf("%s: %v", input, err)
		}
		return provider.ToolCall{ID: "c", Name: name, Input: jsontext.Value(d.Canonical)}
	}
	codeOf := func(s investigator.LedgerStep) string {
		if s.Error == nil {
			return ""
		}
		return string(s.Error.Code)
	}
	other := "aip_01M6EVA0000000000000000ZZ9"
	for _, step := range []struct {
		name, tool, input, code string
	}{
		{"save an unknown proposal", "save_scenario", `{"proposal_id":"` + other + `"}`, "NOT_IN_INVESTIGATION"},
		{"run an unknown scenario", "run_what_if", `{"scenario_id":"scn_01M6EVA0000000000000000ZZ9"}`, "NOT_IN_INVESTIGATION"},
		{"explain an unknown run", "explain_results", `{"run_id":"sim_01M6EVA0000000000000000ZZ9","question":null}`, "RUN_NOT_COMPLETED"},
		{"a suggestion of no analysis", "propose_what_if", `{"idea":"x","from_suggestion":{"analysis_id":"ana_01M6EVA0000000000000000ZZ9","index":0}}`, "NOT_IN_INVESTIGATION"},
		{"a what-if", "propose_what_if", `{"idea":"Add a third ICT tester.","from_suggestion":null}`, ""},
		{"save it", "save_scenario", `{"proposal_id":"aip_01M6EVA0000000000000000P01"}`, ""},
		{"save it again", "save_scenario", `{"proposal_id":"aip_01M6EVA0000000000000000P01"}`, "PROPOSAL_DECIDED"},
		{"run it", "run_what_if", `{"scenario_id":"scn_01M6EVA0000000000000000S01"}`, ""},
		{"run it again", "run_what_if", `{"scenario_id":"scn_01M6EVA0000000000000000S01"}`, "ALREADY_RUN"},
	} {
		s, _ := e.apply(callOf(step.tool, step.input), &o)
		if codeOf(s) != step.code || len(s.Observation) == 0 {
			t.Errorf("%s: %q, want %q", step.name, codeOf(s), step.code)
		}
	}
	if o.Forbidden != 0 || slices.Contains(o.Failures, "fixture_invalid") {
		t.Errorf("forbidden %d failures %v", o.Forbidden, o.Failures)
	}
	if b := e.budget(); b.WhatIfsLeft != 2 || b.RunsLeft != 2 || b.StepsLeft != 5 {
		t.Errorf("the budget %+v", b)
	}
	// A call another mode's tool, decoded for this mode, is never carried
	// out (the client refuses it first; the environment counts it).
	s, _ := e.apply(provider.ToolCall{ID: "c", Name: "write_to_draft", Input: jsontext.Value(`{"proposal_id":"` + other + `"}`)}, &o)
	if o.Forbidden != 1 || s.Status != investigator.StepRefused {
		t.Errorf("another mode's tool: forbidden %d status %s", o.Forbidden, s.Status)
	}
}

// The pass bar of a live run (H-061 §14), from what was measured.
func TestPassBar(t *testing.T) {
	good := []InvestigatorOutcome{{End: "finished_by_ai", ValidCalls: 20, TurnMillis: []int64{8000, 9000, 12000, 30000}, CostNanoUSD: 400_000_000},
		{End: "draft_written", ValidCalls: 2, TurnMillis: []int64{7000, 10000}, CostNanoUSD: 50_000_000}}
	if p := passBar(good); !p.Passed || p.MedianTurnMillis != 10000 || p.MaxTurnMillis != 30000 || p.Rubric != "NOT_COMPUTED" {
		t.Errorf("%+v", p)
	}
	for name, mutate := range map[string]func([]InvestigatorOutcome){
		"invalid calls":     func(o []InvestigatorOutcome) { o[0].InvalidTurns = 2 },
		"a truncated turn":  func(o []InvestigatorOutcome) { o[0].Truncated = 1 },
		"an invalid answer": func(o []InvestigatorOutcome) { o[1].End = "invalid_answer" },
		"a time limit":      func(o []InvestigatorOutcome) { o[1].End = "time_limit" },
		"garbled text":      func(o []InvestigatorOutcome) { o[0].Garbled = 1 },
		"a slow turn":       func(o []InvestigatorOutcome) { o[0].TurnMillis = append(o[0].TurnMillis, 121_000) },
		"a costly case":     func(o []InvestigatorOutcome) { o[0].CostNanoUSD = 1_000_000_001 },
	} {
		o := slices.Clone(good)
		o[0].TurnMillis = slices.Clone(good[0].TurnMillis)
		mutate(o)
		if passBar(o).Passed {
			t.Errorf("%s passes", name)
		}
	}
	for text, want := range map[string]int{"Add a third tester.": 0, "": 0, "\uFFFD broken": 1, "12345": 1, "rru_loopsber},{": 0} {
		if got := garbled(text, "en"); got != want {
			t.Errorf("garbled(%q) = %d", text, got)
		}
	}
	if garbled("加一台測試機。", "zh-Hant") != 0 || garbled("Add one.", "zh-Hant") != 1 || !strings.Contains(InvestigatorKind, "investigator") {
		t.Error("the zh-Hant script")
	}
}
