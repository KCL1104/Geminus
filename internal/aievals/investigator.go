package aievals

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/kcl1104/geminus/internal/ai/aicontext"
	"github.com/kcl1104/geminus/internal/ai/investigator"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/prompts"
)

// The investigator suite (H-061 §14, slice A5): six synthetic cases on the
// electronics line template (testdata/investigator/cases), in a simulated
// tool environment built from recorded results, so that only the planner's
// turns reach a model. Each case runs the planner loop as the worker will
// (H-061 §5, §7, §13): a transcript built from the ledger, one
// provider.Client.PlanTurn a turn, every call decoded strictly and carried
// out by the environment (the executor's stand-in: the investigation's own
// IDs only, the caps, the refusal codes), an invalid turn recorded or not,
// two invalid turns or three refused steps in a row end it with
// invalid_answer, 20 planner turns or the 14 steps limit_reached, finish
// finished_by_ai, a written draft draft_written.
//
// With the fake (make ai-eval, checks/ai-evals/fake-investigator.json) each
// case's scripted turns are the planner and the gates are exact: the end,
// the AI's outcome, the tools carried out in order and the invalid turns.
// Live (cmd/ai-eval-live -suite investigator -planner <model>) the planner
// is the model, and the report adds H-061 §14's pass bar from what was
// measured. The report holds tool names, states, codes and counts only:
// never an idea, a note, a question, a prompt or an answer.

// InvestigatorKind is the live investigator run's kind (report and spend
// record).
const InvestigatorKind = "investigator"

// The caps of the live planner runs (H-061 §14: Muse Spark at most USD 1,
// Muse Wonder at most USD 4); AI_EVAL_LIVE_MAX_USD sets another, within the
// day's and the month's limits.
const (
	DefaultSparkPlannerCap  = 1 * 1_000_000_000
	DefaultWonderPlannerCap = 4 * 1_000_000_000
)

// maxPlannerTurns is the planner turns of one investigation (H-061 §12).
const maxPlannerTurns = 20

// maxTurnRetries is how often a failed turn is asked again (H-061 §12).
const maxTurnRetries = 2

// InvestigatorCase is one case of the suite.
type InvestigatorCase struct {
	ID       string            `json:"id"`
	Title    string            `json:"title"`
	Mode     investigator.Mode `json:"mode"`
	Template string            `json:"template"`
	// Request is the person's request ("" for entry B, which has Seeds).
	Request        string               `json:"request"`
	Seeds          []aicontext.Seed     `json:"seeds,omitzero"`
	BaselineReused bool                 `json:"baseline_reused,omitzero"`
	Objective      *aicontext.Objective `json:"objective,omitzero"`
	RunSettings    jsontext.Value       `json:"run_settings,omitzero"`
	Injection      bool                 `json:"injection,omitzero"`
	Environment    Environment          `json:"environment"`
	Fake           []FakeTurn           `json:"fake"`
	Expect         Expectation          `json:"expect"`
}

// Environment is a case's recorded results, in the order the environment
// hands them out: each a result as the worker writes it, without its budget
// (and a proposal's index, a scenario's proposal_id), which the environment
// fills in.
type Environment struct {
	Proposals    []jsontext.Value `json:"proposals"`
	Scenarios    []jsontext.Value `json:"scenarios,omitzero"`
	Runs         []jsontext.Value `json:"runs,omitzero"`
	Explanations []jsontext.Value `json:"explanations,omitzero"`
	Drafts       []jsontext.Value `json:"drafts,omitzero"`
}

// FakeTurn is a case's scripted planner turn for the fake: its narration and
// calls (the fake makes them as written; the client checks them).
type FakeTurn struct {
	Text  string     `json:"text,omitzero"`
	Calls []FakeCall `json:"calls"`
}

// FakeCall is a scripted call.
type FakeCall struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input jsontext.Value `json:"input"`
}

// Expectation is what a case must end with under the fake.
type Expectation struct {
	End          string   `json:"end"`
	Outcome      string   `json:"outcome,omitzero"`
	InvalidTurns int      `json:"invalid_turns"`
	Tools        []string `json:"tools"`
}

// ReadInvestigatorCases reads the suite's cases, strictly, in ID order.
func ReadInvestigatorCases(root string) ([]InvestigatorCase, error) {
	paths, err := filepath.Glob(filepath.Join(root, "testdata", "investigator", "cases", "INV-*.json"))
	if err != nil || len(paths) == 0 {
		return nil, errors.New("aievals: no investigator cases")
	}
	sort.Strings(paths)
	var out []InvestigatorCase
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var c InvestigatorCase
		if err := model.Decode(data, &c); err != nil {
			return nil, fmt.Errorf("aievals: %s: %w", filepath.Base(p), err)
		}
		if c.ID+".json" != filepath.Base(p) {
			return nil, fmt.Errorf("aievals: %s: the case ID is not its file's", filepath.Base(p))
		}
		out = append(out, c)
	}
	return out, nil
}

// InvestigatorConfig is one run of the suite.
type InvestigatorConfig struct {
	// ClientFor is the client a case's planner turns go through: the fake
	// scripted with the case's turns, or the live metered client.
	ClientFor func(InvestigatorCase) (*provider.Client, error)
	// Budget is the prompt budget of the context builder (the client's).
	Budget     provider.Budget
	Packs      *domainpack.Registry
	Cases      []InvestigatorCase
	Only       []string
	Planner    string
	Transcript investigator.TranscriptMode
	Locale     string
	// Live: the planner is a model; the report adds latencies, costs and
	// the pass bar, and the fake's exact expectations are not gates.
	Live bool
	// Spent is what the run's calls cost so far (live).
	Spent func() int64
	// Sleep waits before a turn the organization's slot refused (live:
	// 1 s, H-061 §5.1).
	Sleep func(context.Context, time.Duration) error
}

// InvestigatorReport is the suite's report: tool names, states, codes and
// counts, never AI text.
type InvestigatorReport struct {
	Suite           string                `json:"suite"`
	Kind            string                `json:"kind"`
	Provider        string                `json:"provider,omitzero"`
	Approval        string                `json:"approval,omitzero"`
	Planner         string                `json:"planner_model"`
	Transcript      string                `json:"transcript"`
	TemplateVersion string                `json:"prompt_template_version"`
	RunID           string                `json:"run_id,omitzero"`
	StartedAt       string                `json:"started_at,omitzero"`
	CapUSD          string                `json:"cap_usd,omitzero"`
	CostUSD         string                `json:"cost_usd,omitzero"`
	Run             int                   `json:"cases_run"`
	Passed          int                   `json:"cases_passed"`
	Gates           map[string]int        `json:"gate_failures"`
	Complete        bool                  `json:"complete"`
	Outcomes        []InvestigatorOutcome `json:"cases"`
	PassBar         *PassBar              `json:"pass_bar,omitzero"`
}

// InvestigatorOutcome is one case's run.
type InvestigatorOutcome struct {
	ID           string         `json:"id"`
	Mode         string         `json:"mode"`
	End          string         `json:"end"`
	Outcome      string         `json:"ai_outcome,omitzero"`
	Steps        int            `json:"steps"`
	PlannerTurns int            `json:"planner_turns"`
	ValidCalls   int            `json:"valid_calls"`
	InvalidTurns int            `json:"invalid_turns"`
	InvalidKinds map[string]int `json:"invalid_kinds,omitzero"`
	RefusedSteps int            `json:"refused_steps"`
	Calls        []CallRecord   `json:"calls"`
	// Forbidden counts calls carried out that the mode does not offer or
	// that the registry's decoder does not read as stored: always 0.
	Forbidden int `json:"forbidden_calls"`
	// Garbled counts ideas, questions and notes that are not valid UTF-8,
	// hold U+FFFD, or are not in the output locale's script (H-051).
	Garbled   int      `json:"garbled_texts"`
	Truncated int      `json:"truncated_turns"`
	Passed    bool     `json:"passed"`
	Failures  []string `json:"failures,omitzero"`
	// Live only.
	TurnMillis  []int64 `json:"turn_ms,omitzero"`
	CostNanoUSD int64   `json:"cost_nano_usd,omitzero"`
	ErrorCode   string  `json:"error_code,omitzero"`
}

// CallRecord is one ledger step as the report keeps it.
type CallRecord struct {
	Seq    int    `json:"seq"`
	Tool   string `json:"tool"`
	Status string `json:"status"`
	Code   string `json:"code,omitzero"`
}

// PassBar is H-061 §14's bar for a live planner, from what was measured.
type PassBar struct {
	ValidCallRatio   float64 `json:"valid_call_ratio"`
	ValidCallsOK     bool    `json:"valid_calls_at_least_95_percent"`
	NoTruncation     bool    `json:"no_truncated_turn"`
	NoInvalidAnswer  bool    `json:"no_invalid_answer_end"`
	NoGarbled        bool    `json:"no_garbled_text"`
	MedianTurnMillis int64   `json:"median_turn_ms"`
	P95TurnMillis    int64   `json:"p95_turn_ms"`
	MaxTurnMillis    int64   `json:"max_turn_ms"`
	LatencyOK        bool    `json:"latency_within_bounds"`
	CostOK           bool    `json:"cost_at_most_usd_1_per_case"`
	EndsOK           bool    `json:"ends_finished_or_limit"`
	// Rubric is the finish-reason rubric against Muse Spark's: NOT_COMPUTED
	// here (H-061 A9 compares the two planners' reports).
	Rubric string `json:"finish_rubric"`
	Passed bool   `json:"passed"`
}

// Ok reports whether no gate failed.
func (r *InvestigatorReport) Ok() bool {
	for _, n := range r.Gates {
		if n > 0 {
			return false
		}
	}
	return r.Complete && (r.PassBar == nil || r.PassBar.Passed)
}

// Marshal is the report as written.
func (r *InvestigatorReport) Marshal() ([]byte, error) { return marshalReport(r) }

// Investigation constants of the eval's synthetic tenant.
const (
	evalInv = "inv_01M6EVA0000000000000000NV1"
)

// RunInvestigator runs the suite.
func RunInvestigator(ctx context.Context, cfg InvestigatorConfig) (*InvestigatorReport, error) {
	if cfg.Transcript == "" {
		cfg.Transcript = investigator.TranscriptDigest
	}
	if cfg.Sleep == nil {
		cfg.Sleep = func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		}
	}
	if _, err := investigator.ProviderTools(investigator.Tools); err != nil {
		return nil, err
	}
	_, version, err := prompts.Template(string(provider.RoleInvestigator))
	if err != nil {
		return nil, err
	}
	r := &InvestigatorReport{Suite: "investigator", Kind: map[bool]string{true: "live", false: "fake"}[cfg.Live], Planner: cfg.Planner,
		Transcript: string(cfg.Transcript), TemplateVersion: version, Gates: map[string]int{"expectations": 0, "forbidden_calls": 0,
			"garbled_texts": 0, "truncated_turns": 0}}
	r.Complete = true
	for _, c := range cfg.Cases {
		if len(cfg.Only) > 0 && !slices.Contains(cfg.Only, c.ID) {
			continue
		}
		if err := ctx.Err(); err != nil {
			r.Complete = false
			break
		}
		o, err := runInvestigation(ctx, cfg, c)
		if err != nil {
			return r, fmt.Errorf("aievals: %s: %w", c.ID, err)
		}
		r.Run++
		judgeInvestigation(&o, c, cfg.Live)
		if o.Passed {
			r.Passed++
		}
		r.Gates["forbidden_calls"] += o.Forbidden
		r.Gates["garbled_texts"] += o.Garbled
		r.Gates["truncated_turns"] += o.Truncated
		if !o.Passed && !cfg.Live {
			r.Gates["expectations"]++
		}
		r.Outcomes = append(r.Outcomes, o)
	}
	if cfg.Live {
		r.PassBar = passBar(r.Outcomes)
	}
	return r, nil
}

// runInvestigation runs one case's planner loop.
func runInvestigation(ctx context.Context, cfg InvestigatorConfig, c InvestigatorCase) (o InvestigatorOutcome, err error) {
	o = InvestigatorOutcome{ID: c.ID, Mode: string(c.Mode), InvalidKinds: map[string]int{}, Calls: []CallRecord{}}
	client, err := cfg.ClientFor(c)
	if err != nil {
		return o, err
	}
	env := newEnv(c, cfg.Locale)
	defer func() { o.Steps = env.steps }()
	in, err := briefInput(cfg, c)
	if err != nil {
		return o, err
	}
	brief := func(ledger []investigator.LedgerStep) (provider.Prompt, error) {
		b := in
		b.Digest, b.Left = BriefSteps(ledger), aicontext.InvestigationBudget(env.budget())
		return aicontext.Investigation(b)
	}
	tr, err := investigator.NewTranscript(cfg.Transcript, c.Mode, brief, nil)
	if err != nil {
		return o, err
	}
	ictx, cancel := context.WithTimeout(ctx, time.Duration(investigator.Minutes)*time.Minute-5*time.Second)
	defer cancel()
	attr := provider.Attribution{TargetType: provider.PlannerTarget, TargetID: evalInv, RequestID: "req_" + strings.TrimPrefix(evalInv, "inv_")}
	var spentBefore int64
	if cfg.Spent != nil {
		spentBefore = cfg.Spent()
	}
	defer func() {
		if cfg.Spent != nil {
			o.CostNanoUSD = cfg.Spent() - spentBefore
		}
	}()
	invalidRow, refusedRow, retries := 0, 0, 0
	loc := cfg.Locale
	for {
		b := env.budget()
		switch {
		case ictx.Err() != nil && ctx.Err() == nil:
			o.End = "time_limit"
			return o, nil
		case ctx.Err() != nil:
			o.End, o.ErrorCode = "stopped", "CANCELLED"
			return o, nil
		case b.StepsLeft <= 0 || o.PlannerTurns >= maxPlannerTurns:
			o.End = "limit_reached"
			return o, nil
		}
		pc, err := tr.Next(evalOrg, attr, cfg.Planner, b)
		if err != nil {
			return o, err
		}
		start := time.Now()
		res, err := client.PlanTurn(ictx, pc)
		o.PlannerTurns++
		if cfg.Live {
			o.TurnMillis = append(o.TurnMillis, time.Since(start).Milliseconds())
		}
		if err != nil {
			if res.Invalid != nil {
				o.InvalidTurns++
				o.InvalidKinds[string(res.Invalid.Kind)]++
				if res.Invalid.Kind == provider.InvalidTruncated {
					o.Truncated++
				}
				step := investigator.InvalidStep(env.steps+1, *res.Invalid)
				if err := tr.Invalid(step); err != nil {
					return o, err
				}
				invalidRow++
				if step != nil {
					env.record(*step)
					o.Calls = append(o.Calls, CallRecord{Seq: step.Seq, Tool: step.Tool, Status: string(step.Status), Code: string(step.Error.Code)})
					o.RefusedSteps++
					refusedRow++
				}
				if invalidRow >= 2 || refusedRow >= 3 {
					o.End = "invalid_answer"
					return o, nil
				}
				continue
			}
			switch code := provider.CodeOf(err); {
			case code == provider.CodeQuotaExceeded:
				o.End = "spend_cap"
				return o, nil
			case code == provider.CodeConcurrencyLimited:
				// Nothing was asked or reserved: wait 1 s and ask again.
				o.PlannerTurns--
				_ = cfg.Sleep(ictx, time.Second)
				continue
			case (code == provider.CodeTimeout || code == provider.CodeUnavailable || code == provider.CodeRateLimited ||
				code == provider.CodeSpendUnavailable) && retries < maxTurnRetries:
				retries++
				continue
			case code == provider.CodeCancelled || code == provider.CodeTimeout && ictx.Err() != nil:
				continue // the loop's checks end it
			default:
				o.End, o.ErrorCode = "ai_failed", "AI_"+strings.ToUpper(string(code))
				return o, nil
			}
		}
		retries, invalidRow = 0, 0
		o.ValidCalls++
		o.Garbled += garbled(res.Note, loc)
		step, end := env.apply(res.Call, &o)
		if err := tr.Record(res, step); err != nil {
			return o, err
		}
		rec := CallRecord{Seq: step.Seq, Tool: step.Tool, Status: string(step.Status)}
		if step.Error != nil {
			rec.Code = string(step.Error.Code)
			o.RefusedSteps++
			refusedRow++
		} else {
			refusedRow = 0
		}
		o.Calls = append(o.Calls, rec)
		if end != "" {
			o.End = end
			if end == "finished_by_ai" {
				var a investigator.FinishArguments
				if json.Unmarshal(step.Arguments, &a) == nil {
					o.Outcome = string(a.Outcome)
				}
			}
			return o, nil
		}
		if refusedRow >= 3 {
			o.End = "invalid_answer"
			return o, nil
		}
	}
}

// briefInput is a case's brief: the electronics template's model in the
// eval's synthetic tenant, the case's request, settings and seeds.
func briefInput(cfg InvestigatorConfig, c InvestigatorCase) (aicontext.InvestigationInput, error) {
	p, err := cfg.Packs.Lookup("electronics_line")
	if err != nil {
		return aicontext.InvestigationInput{}, err
	}
	tp, ok := p.(domainpack.TemplateProvider)
	if !ok {
		return aicontext.InvestigationInput{}, errors.New("aievals: the electronics pack builds no template")
	}
	ir, err := tp.Template(c.Template, evalMdl, "Synthetic electronics line", domainpack.Locale(cfg.Locale))
	if err != nil {
		return aicontext.InvestigationInput{}, err
	}
	return aicontext.InvestigationInput{
		Scope:   aicontext.Scope{OrganizationID: evalOrg, ModelID: evalMdl},
		Version: aicontext.Version{OrganizationID: evalOrg, ModelID: evalMdl, VersionID: evalMv, IR: ir},
		Domain:  p.PromptContext(), InvestigationID: evalInv, Mode: string(c.Mode), Request: c.Request, Objective: c.Objective,
		RunSettings: c.RunSettings, Seeds: c.Seeds, Caps: aicontext.InvestigationBudget(capsOf(c)), OutputLocale: cfg.Locale, Budget: cfg.Budget,
	}, nil
}

// BriefSteps is the ledger mirror as the brief's digest takes it
// (aicontext does not link the registry).
func BriefSteps(ledger []investigator.LedgerStep) []aicontext.InvestigationStep {
	out := make([]aicontext.InvestigationStep, 0, len(ledger))
	for _, s := range ledger {
		d := aicontext.InvestigationStep{Seq: s.Seq, Tool: s.Tool, Arguments: s.Arguments, Status: string(s.Status), Observation: s.Observation}
		if s.Error != nil {
			d.ErrorCode, d.ErrorPath = string(s.Error.Code), s.Error.Path
		}
		out = append(out, d)
	}
	return out
}

func capsOf(c InvestigatorCase) investigator.Budget {
	if c.Mode == investigator.Edit {
		return investigator.Budget{WhatIfsLeft: 1, StepsLeft: investigator.MaxSteps}
	}
	runs := investigator.MaxRuns
	if c.BaselineReused {
		runs--
	}
	return investigator.Budget{WhatIfsLeft: investigator.MaxWhatIfs, RunsLeft: runs, ExplanationsLeft: investigator.MaxExplanations,
		StepsLeft: investigator.MaxSteps}
}

// judgeInvestigation sets whether a case passed: under the fake its exact
// expectations; always the structural gates.
func judgeInvestigation(o *InvestigatorOutcome, c InvestigatorCase, live bool) {
	fail := func(code string) { o.Failures = append(o.Failures, code) }
	if o.Forbidden > 0 {
		fail("forbidden_call")
	}
	if o.Garbled > 0 {
		fail("garbled_text")
	}
	if o.Truncated > 0 {
		fail("truncated_turn")
	}
	if !live {
		var done []string
		for _, call := range o.Calls {
			if call.Tool != investigator.InvalidCall && call.Status == string(investigator.StepDone) {
				done = append(done, call.Tool)
			}
		}
		if o.End != c.Expect.End {
			fail("end")
		}
		if o.Outcome != c.Expect.Outcome {
			fail("ai_outcome")
		}
		if o.InvalidTurns != c.Expect.InvalidTurns {
			fail("invalid_turns")
		}
		if !slices.Equal(done, c.Expect.Tools) {
			fail("tools")
		}
	} else if o.End == "invalid_answer" || o.End == "time_limit" || o.End == "spend_cap" || o.End == "ai_failed" {
		fail("end")
	}
	o.Passed = len(o.Failures) == 0
}

// garbled counts a text the planner wrote that is not valid UTF-8, holds
// U+FFFD, or holds no letter of the locale's script (H-051's failure).
func garbled(text, loc string) int {
	if text == "" {
		return 0
	}
	if !utf8.ValidString(text) || strings.ContainsRune(text, utf8.RuneError) {
		return 1
	}
	script := unicode.Latin
	if loc == "zh-Hant" {
		script = unicode.Han
	}
	for _, r := range text {
		if unicode.Is(script, r) {
			return 0
		}
	}
	return 1
}

// passBar is H-061 §14's bar over the live cases.
func passBar(out []InvestigatorOutcome) *PassBar {
	var turns []int64
	valid, all := 0, 0
	p := &PassBar{NoTruncation: true, NoInvalidAnswer: true, NoGarbled: true, CostOK: true, EndsOK: true, Rubric: "NOT_COMPUTED"}
	for _, o := range out {
		turns = append(turns, o.TurnMillis...)
		valid += o.ValidCalls
		all += o.ValidCalls + o.InvalidTurns
		p.NoTruncation = p.NoTruncation && o.Truncated == 0
		p.NoInvalidAnswer = p.NoInvalidAnswer && o.End != "invalid_answer"
		p.NoGarbled = p.NoGarbled && o.Garbled == 0
		p.CostOK = p.CostOK && o.CostNanoUSD <= 1_000_000_000
		p.EndsOK = p.EndsOK && (o.End == "finished_by_ai" || o.End == "limit_reached" || o.End == "draft_written")
	}
	if all > 0 {
		p.ValidCallRatio = float64(valid) / float64(all)
	}
	p.ValidCallsOK = all > 0 && p.ValidCallRatio >= 0.95
	slices.Sort(turns)
	if n := len(turns); n > 0 {
		p.MedianTurnMillis, p.P95TurnMillis, p.MaxTurnMillis = turns[n/2], turns[min(n-1, (n*95+99)/100-1)], turns[n-1]
	}
	p.LatencyOK = len(turns) > 0 && p.MedianTurnMillis <= 30_000 && p.P95TurnMillis <= 60_000 && p.MaxTurnMillis <= 120_000
	p.Passed = p.ValidCallsOK && p.NoTruncation && p.NoInvalidAnswer && p.NoGarbled && p.LatencyOK && p.CostOK && p.EndsOK
	return p
}

// env is the simulated tool environment: the executor's stand-in over the
// case's recorded results.
type env struct {
	c         InvestigatorCase
	loc       string
	caps      investigator.Budget
	steps     int
	whatIfs   int
	runs      int
	explains  int
	baseline  bool
	next      map[string]int
	proposals map[string]string // ID -> status
	saved     map[string]bool   // proposal IDs saved
	scenarios map[string]bool   // ID -> run
	known     map[string]bool   // scenario IDs
	completed map[string]bool   // run IDs completed
	analyses  map[string]int    // ID -> suggestions
}

func newEnv(c InvestigatorCase, loc string) *env {
	e := &env{c: c, loc: loc, caps: capsOf(c), next: map[string]int{}, proposals: map[string]string{}, saved: map[string]bool{},
		scenarios: map[string]bool{}, known: map[string]bool{}, completed: map[string]bool{}, analyses: map[string]int{}, baseline: c.BaselineReused}
	for _, s := range c.Seeds {
		e.analyses[s.AnalysisID] = max(e.analyses[s.AnalysisID], s.Index+1)
	}
	return e
}

func (e *env) budget() investigator.Budget {
	return investigator.Budget{WhatIfsLeft: e.caps.WhatIfsLeft - e.whatIfs, RunsLeft: e.caps.RunsLeft - e.runs,
		ExplanationsLeft: e.caps.ExplanationsLeft - e.explains, StepsLeft: e.caps.StepsLeft - e.steps}
}

// record counts a step the environment did not carry out (a recorded
// invalid turn).
func (e *env) record(investigator.LedgerStep) { e.steps++ }

// take is the next recorded result of kind, decoded, or false when the
// case recorded no more.
func (e *env) take(kind string, list []jsontext.Value) (map[string]any, bool) {
	n := e.next[kind]
	if n >= len(list) {
		return nil, false
	}
	e.next[kind]++
	var v map[string]any
	if json.Unmarshal(list[n], &v) != nil {
		return nil, false
	}
	return v, true
}

// apply carries out a checked call: decoded again (as the executor does),
// checked against the investigation's own resources and caps, answered
// with the next recorded result. It returns the step, observed, and how the
// investigation ends after it ("" when it goes on).
func (e *env) apply(call provider.ToolCall, o *InvestigatorOutcome) (investigator.LedgerStep, string) {
	seq := e.steps + 1
	e.steps++
	step := investigator.LedgerStep{Seq: seq, Tool: call.Name, Arguments: call.Input}
	decoded, err := investigator.Decode(e.c.Mode, call.Name, call.Input)
	if err != nil || !slices.Equal(decoded.Canonical, []byte(call.Input)) || !investigator.Tool(call.Name).Offered(e.c.Mode) {
		o.Forbidden++
		return e.refuse(step, investigator.InvalidArguments, ""), ""
	}
	end := ""
	var result map[string]any
	code, path := investigator.Code(""), ""
	switch a := decoded.Arguments.(type) {
	case investigator.ProposeWhatIfArguments, investigator.ProposeModelEditArguments:
		if from, ok := a.(investigator.ProposeWhatIfArguments); ok && from.FromSuggestion != nil {
			if n, known := e.analyses[from.FromSuggestion.AnalysisID]; !known || from.FromSuggestion.Index >= n {
				code, path = investigator.NotInInvestigation, "/from_suggestion"
				break
			}
		}
		if e.whatIfs >= e.caps.WhatIfsLeft {
			code = investigator.LimitReached
			break
		}
		v, ok := e.take("proposal", e.c.Environment.Proposals)
		if !ok {
			code = investigator.ServiceUnavailable
			break
		}
		e.whatIfs++
		v["index"] = e.whatIfs
		e.proposals[v["proposal_id"].(string)] = v["status"].(string)
		result = v
	case investigator.SaveScenarioArguments, investigator.WriteToDraftArguments:
		id := ""
		if s, ok := a.(investigator.SaveScenarioArguments); ok {
			id = s.ProposalID
		} else {
			id = a.(investigator.WriteToDraftArguments).ProposalID
		}
		status, known := e.proposals[id]
		switch {
		case !known:
			code, path = investigator.NotInInvestigation, "/proposal_id"
		case e.saved[id] || status == "decided":
			code, path = investigator.ProposalDecided, "/proposal_id"
		case status != "ready":
			code, path = investigator.ProposalNotReady, "/proposal_id"
		}
		if code != "" {
			break
		}
		if _, ok := a.(investigator.WriteToDraftArguments); ok {
			v, ok := e.take("draft", e.c.Environment.Drafts)
			if !ok {
				code = investigator.ServiceUnavailable
				break
			}
			e.saved[id], end = true, "draft_written"
			result = v
			break
		}
		v, ok := e.take("scenario", e.c.Environment.Scenarios)
		if !ok {
			code = investigator.ServiceUnavailable
			break
		}
		e.saved[id] = true
		v["proposal_id"] = id
		e.known[v["scenario_id"].(string)] = true
		result = v
	case investigator.RunWhatIfArguments:
		need := 1
		if !e.baseline {
			need = 2
		}
		switch {
		case !e.known[a.ScenarioID]:
			code, path = investigator.NotInInvestigation, "/scenario_id"
		case e.scenarios[a.ScenarioID]:
			code, path = investigator.AlreadyRun, "/scenario_id"
		case e.runs+need > e.caps.RunsLeft:
			code = investigator.LimitReached
		}
		if code != "" {
			break
		}
		v, ok := e.take("run", e.c.Environment.Runs)
		if !ok {
			code = investigator.ServiceUnavailable
			break
		}
		e.runs += need
		e.baseline, e.scenarios[a.ScenarioID] = true, true
		if v["status"] == "completed" {
			e.completed[v["run_id"].(string)] = true
		}
		result = v
	case investigator.ExplainResultsArguments:
		switch {
		case !e.completed[a.RunID]:
			code, path = investigator.RunNotCompleted, "/run_id"
		case e.explains >= e.caps.ExplanationsLeft:
			code = investigator.LimitReached
		}
		if code != "" {
			break
		}
		v, ok := e.take("explanation", e.c.Environment.Explanations)
		if !ok {
			code = investigator.ServiceUnavailable
			break
		}
		e.explains++
		if list, ok := v["suggested_next_experiments"].([]any); ok {
			e.analyses[v["analysis_id"].(string)] = len(list)
		}
		result = v
	case investigator.FinishArguments:
		step.Status = investigator.StepDone
		return step, "finished_by_ai"
	}
	o.Garbled += argumentsGarbled(decoded.Arguments, e.loc)
	if code != "" {
		return e.refuse(step, code, path), ""
	}
	result["budget"] = e.budget()
	data, err := json.Marshal(result, json.Deterministic(true))
	if err != nil {
		return e.refuse(step, investigator.Internal, ""), ""
	}
	if issues, err := validate.Document(data, "investigator.schema.json#/$defs/ToolObservation", "EVAL_FIXTURE_INVALID"); err != nil || len(issues) > 0 {
		// A recorded result that is not a valid observation is the case's
		// fault: the step fails, and the case cannot pass.
		o.Failures = append(o.Failures, "fixture_invalid")
		return e.refuse(step, investigator.Internal, ""), ""
	}
	step.Status, step.Observation = investigator.StepDone, data
	return step, end
}

// refuse is a refused (or, for SERVICE_UNAVAILABLE and INTERNAL, failed)
// step and its error observation.
func (e *env) refuse(step investigator.LedgerStep, code investigator.Code, path string) investigator.LedgerStep {
	step.Status = investigator.StepRefused
	if code == investigator.ServiceUnavailable || code == investigator.Internal {
		step.Status = investigator.StepFailed
	}
	step.Error = &investigator.ToolError{Code: code, Path: path}
	data, _ := json.Marshal(map[string]any{"is_error": true, "error": map[string]any{"code": string(code), "path": path}, "budget": e.budget()},
		json.Deterministic(true))
	step.Observation = data
	return step
}

// argumentsGarbled counts the AI text of a call's arguments that is
// garbled (an idea, a question).
func argumentsGarbled(a investigator.Arguments, loc string) int {
	switch x := a.(type) {
	case investigator.ProposeWhatIfArguments:
		return garbled(x.Idea, loc)
	case investigator.ProposeModelEditArguments:
		return garbled(x.Idea, loc)
	case investigator.ExplainResultsArguments:
		if x.Question != nil {
			return garbled(*x.Question, loc)
		}
	}
	return 0
}
