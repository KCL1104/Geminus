package analysis_test

// Tests of the analyst's Eino graph (H-059, slice E2 of
// docs/design/H-056_INVESTIGATIONS_ON_EINO.md §6.4) over the real
// provider.Client and the deterministic fake backend: no live call. The
// handler's mapping of the graph's errors to job failures and log lines is
// internal/analyses/analyzer's (its goldens were written before E2).

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/aicontext"
	"github.com/kcl1104/geminus/internal/ai/analyst"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/aigraph/analysis"
	"github.com/kcl1104/geminus/internal/aigraph/einomodel"
	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/domainpack/softwaredelivery"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/results/compare"
	"github.com/kcl1104/geminus/internal/simulation"
)

const (
	org        = "org_01M4YDQK00TA00000000000000"
	mdl        = "mdl_01M4YDQK00R942EDGA45T538HS" // deterministic-queue.json's model_id
	mv         = "mv_01M4YDQK00FRYWV7GS7X1QCXZR"
	baseID     = "sim_01M4YDQK00FGZZWH0PP15XPNFA"
	scenID     = "sim_01M4YDQK00FGZZWH0PP15XPNFB"
	question   = "Why is the queue shorter?"
	engine     = "des-go-0.1.0-PROPOSED-fixture"
	zeroHash   = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	twoServers = `[{"op":"test","path":"/resources/0/id","value":"server"},{"op":"replace","path":"/resources/0/capacity","value":2}]`
)

var attribution = provider.Attribution{TargetType: "ai_analysis", TargetID: "ana_01M4YDQK00FGZZWH0PP15XPNFC", RequestID: "req_01M4YDQK00FGZZWH0PP15XPNFD"}

var config = simulation.RunConfig{Horizon: simulation.Duration{Value: 1010, Unit: model.Minute}, Warmup: simulation.Duration{Value: 0, Unit: model.Minute},
	Replications: 1, Seed: 7, TraceLevel: simulation.TraceNone}

func read(t *testing.T, rel string) []byte {
	t.Helper()
	return contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), filepath.FromSlash(rel)))
}

// pairCompare is the comparison the analysis job makes (comparisons.Input
// and compare.Compare) of the two fixture runs.
func pairCompare(baseVersion string) func(base, scen *simulation.Result) (compare.Comparison, error) {
	run := func(id, version, patch string, r *simulation.Result) compare.Run {
		return compare.Run{ID: id, ModelVersionID: version, EngineVersion: engine, DatasetManifestHash: zeroHash, ModelHash: zeroHash,
			EffectiveModelHash: zeroHash, ConfigHash: zeroHash, ScenarioPatch: []byte(patch), Config: config, Result: r}
	}
	return func(base, scen *simulation.Result) (compare.Comparison, error) {
		return compare.Compare(run(baseID, baseVersion, "[]", base), run(scenID, mv, twoServers, scen))
	}
}

// input is the analysis of the two-server run beside its baseline
// (internal/results/compare's golden pair) on the deterministic queue.
func input(t *testing.T) analysis.Input {
	t.Helper()
	return analysis.Input{
		OrganizationID: org,
		Version:        analysis.Version{ID: mv, ModelID: mdl, IR: read(t, "testdata/golden-models/deterministic-queue.json")},
		Scenario:       analysis.Run{ID: scenID, ModelID: mdl, Result: read(t, "testdata/contracts/valid-result-scenario.json")},
		ScenarioPatch:  []byte(twoServers),
		Config:         config,
		Baseline:       &analysis.Run{ID: baseID, ModelID: mdl, Result: read(t, "testdata/contracts/valid-result.json")},
		Compare:        pairCompare(mv),
		Question:       question,
		OutputLocale:   "zh-Hant",
		Attribution:    attribution,
	}
}

func alone(t *testing.T) analysis.Input {
	in := input(t)
	in.Baseline, in.Compare, in.Question, in.OutputLocale = nil, nil, "", "en"
	return in
}

func packs(t *testing.T) *domainpack.Registry {
	t.Helper()
	reg, err := domainpack.NewRegistry(softwaredelivery.Pack{})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// calls records what the Called observer saw.
type calls struct {
	mu   sync.Mutex
	seen []observed
}

type observed struct {
	call int
	md   provider.Metadata
	err  error
}

func (c *calls) called(_ context.Context, call int, md provider.Metadata, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen = append(c.seen, observed{call, md, err})
}

func (c *calls) all() []observed {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.seen)
}

func graph(t *testing.T, p provider.LLMProvider, lookup analysis.PackLookup, budget provider.Budget, obs *calls) *analysis.Graph {
	t.Helper()
	cfg := analysis.Config{Provider: p, Budget: budget, Packs: lookup}
	if obs != nil {
		cfg.Called = obs.called
	}
	g, err := analysis.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func client(t *testing.T, b provider.Backend) *provider.Client {
	t.Helper()
	c, err := provider.New(b, provider.DefaultBudget())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// direct is the analyst's steps called one after the other, without the
// graph: decode, compare, aicontext.Analysis, the client's call,
// analyst.Check.
func direct(t *testing.T, c *provider.Client, in analysis.Input) (provider.AnalysisOutput, provider.Metadata, []analyst.Issue) {
	t.Helper()
	var ir model.ModelIR
	if err := model.Decode(in.Version.IR, &ir); err != nil {
		t.Fatal(err)
	}
	pack, err := packs(t).Lookup(ir.Domain)
	if err != nil {
		t.Fatal(err)
	}
	decode := func(b []byte) *simulation.Result {
		var r simulation.Result
		if err := model.Decode(b, &r); err != nil {
			t.Fatal(err)
		}
		return &r
	}
	scen := decode(in.Scenario.Result)
	var patch model.Patch
	if err := json.Unmarshal(in.ScenarioPatch, &patch); err != nil {
		t.Fatal(err)
	}
	ai := aicontext.AnalysisInput{Scope: aicontext.Scope{OrganizationID: org, ModelID: in.Scenario.ModelID},
		Version: aicontext.Version{OrganizationID: org, ModelID: in.Version.ModelID, VersionID: in.Version.ID, IR: &ir}, Domain: pack.PromptContext(),
		Scenario: aicontext.RunResult{OrganizationID: org, ModelID: in.Scenario.ModelID, RunID: in.Scenario.ID, Result: scen}, ScenarioPatch: patch,
		Config: in.Config, Question: in.Question, OutputLocale: in.OutputLocale, Budget: provider.DefaultBudget(), Investigation: in.Investigation}
	var base *simulation.Result
	var cmp *compare.Comparison
	if in.Baseline != nil {
		base = decode(in.Baseline.Result)
		cm, err := in.Compare(base, scen)
		if err != nil || !cm.Comparable {
			t.Fatalf("compare: %v %+v", err, cm)
		}
		cmp = &cm
		if ai.Comparison, err = cm.JSON(); err != nil {
			t.Fatal(err)
		}
		ai.Baseline = &aicontext.RunResult{OrganizationID: org, ModelID: in.Baseline.ModelID, RunID: in.Baseline.ID, Result: base}
	}
	ap, err := aicontext.Analysis(ai)
	if err != nil {
		t.Fatal(err)
	}
	ap.Context.Attribution = in.Attribution
	res, err := c.AnalyzeSimulation(context.Background(), ap.Context)
	if err != nil {
		t.Fatal(err)
	}
	out, issues := analyst.Check(analyst.Input{Output: res.Output, Allowed: ap.Allowed, Supplied: ap.Supplied, Names: ap.Names, Scenario: scen,
		Baseline: base, Comparison: cmp})
	return out, res.Metadata, issues
}

// The graph is the steps: for an analysis beside a baseline and one alone,
// the provider is sent the same request (byte for byte), and the same
// checked analysis and metadata come back, as when the steps are called
// directly; the observer sees the one call.
func TestTheGraphIsTheSteps(t *testing.T) {
	for name, in := range map[string]analysis.Input{"beside a baseline": input(t), "alone": alone(t)} {
		t.Run(name, func(t *testing.T) {
			fg, fd := fake.New(), fake.New()
			obs := &calls{}
			out, err := graph(t, client(t, fg), packs(t), provider.DefaultBudget(), obs).Run(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			wantOut, wantMeta, issues := direct(t, client(t, fd), in)
			if len(issues) > 0 {
				t.Fatalf("the fake's answer is not grounded: %+v", issues)
			}
			out.Metadata.LatencyMillis, wantMeta.LatencyMillis = 0, 0
			if !reflect.DeepEqual(out.Analysis, wantOut) || !reflect.DeepEqual(out.Metadata, wantMeta) {
				t.Errorf("graph %+v %+v\ndirect %+v %+v", out.Analysis, out.Metadata, wantOut, wantMeta)
			}
			gr, dr := fg.Requests(), fd.Requests()
			if len(gr) != 1 || len(dr) != 1 || !reflect.DeepEqual(gr, dr) {
				t.Errorf("requests\ngraph  %+v\ndirect %+v", gr, dr)
			}
			if gr[0].Prompt.Role != provider.RoleAnalyst || gr[0].OutputSchema != provider.RoleAnalyst.OutputSchema() {
				t.Errorf("request %+v", gr[0].Prompt)
			}
			seen := obs.all()
			if len(seen) != 1 || seen[0].err != nil || seen[0].md.InputHash != out.Metadata.InputHash || seen[0].md.OutputHash == "" {
				t.Errorf("observed %+v", seen)
			}
		})
	}
}

// H-061 §9 (slice A6, on the graph): an analysis an AI investigation asked
// for (Input.Investigation, its row's label) carries the
// investigation-rules-1 line in its call's system text, and its request is
// the direct steps' with the same labelled context; a person's carries none.
func TestAnInvestigationsAnalysisCarriesItsRule(t *testing.T) {
	const rule = "\n## Investigation rules (investigation-rules-1)\n\n"
	for _, labelled := range []bool{false, true} {
		in := input(t)
		in.Investigation = labelled
		fg, fd := fake.New(), fake.New()
		if _, err := graph(t, client(t, fg), packs(t), provider.DefaultBudget(), nil).Run(context.Background(), in); err != nil {
			t.Fatal(err)
		}
		direct(t, client(t, fd), in)
		gr, dr := fg.Requests(), fd.Requests()
		if len(gr) != 1 || !reflect.DeepEqual(gr, dr) {
			t.Fatalf("labelled %v: the graph's request differs from the direct steps'", labelled)
		}
		if n := strings.Count(gr[0].Prompt.System, rule); n != map[bool]int{false: 0, true: 1}[labelled] {
			t.Errorf("labelled %v: the call carries the rule %d times", labelled, n)
		}
	}
}

// Each failure is an *analysis.Error of its kind, carrying what the
// handler maps (the cause, the reasons, the call's metadata, the issues);
// nothing before the call reaches the provider or the observer. A failed
// call is not asked again; an ungrounded answer is, once (H-086 (R)): the
// fake repeats it, so the second is ungrounded too, with its own issues.
func TestFailuresAreTyped(t *testing.T) {
	ungrounded, err := json.Marshal(provider.AnalysisOutput{
		Baseline: &provider.AnalysisSummary{Summary: "Baseline."}, Scenario: provider.AnalysisSummary{Summary: "Throughput rose by 4242.5."},
		Delta: []provider.AnalysisDelta{}, Bottleneck: []provider.Bottleneck{}, Assumptions: []string{}, Uncertainty: []string{"Only one replication was run."},
		SuggestedNextExperiments: []provider.Experiment{}})
	if err != nil {
		t.Fatal(err)
	}
	nan := math.NaN()
	tiny := provider.DefaultBudget()
	tiny.MaxPromptBytes = 2000
	for _, c := range []struct {
		name   string
		change func(*analysis.Input)
		script func(*fake.Backend)
		lookup analysis.PackLookup
		budget provider.Budget
		kind   analysis.Kind
		called bool
		calls  int // with called: 1 unless set
		check  func(t *testing.T, e *analysis.Error)
	}{
		{name: "the version does not decode", change: func(in *analysis.Input) { in.Version.IR = []byte(`{"model_id":`) }, kind: analysis.VersionUndecodable},
		{name: "the domain is not served", lookup: noPacks{}, kind: analysis.DomainNotServed},
		{name: "the scenario's result does not decode", change: func(in *analysis.Input) { in.Scenario.Result = []byte(`{"unknown":1}`) }, kind: analysis.ResultUndecodable},
		{name: "the patch does not decode", change: func(in *analysis.Input) { in.ScenarioPatch = []byte(`{"op":`) }, kind: analysis.PatchUndecodable},
		{name: "the baseline's result does not decode", change: func(in *analysis.Input) { in.Baseline.Result = []byte(`[]`) }, kind: analysis.ResultUndecodable},
		{name: "the comparison fails", change: func(in *analysis.Input) { in.Baseline.Result = in.Scenario.Result }, kind: analysis.CompareFailed,
			check: func(t *testing.T, e *analysis.Error) {
				if !errors.Is(e.Err, compare.ErrInconsistent) {
					t.Errorf("cause %v", e.Err)
				}
			}},
		{name: "the runs are not comparable", change: func(in *analysis.Input) { in.Compare = pairCompare("mv_01M4YDQK00FRYWV7GS7X1QCXZS") }, kind: analysis.NotComparable,
			check: func(t *testing.T, e *analysis.Error) {
				if !slices.Equal(e.Reasons, []string{compare.ModelVersionMismatch}) {
					t.Errorf("reasons %v", e.Reasons)
				}
			}},
		{name: "the comparison does not encode", change: func(in *analysis.Input) {
			in.Compare = func(*simulation.Result, *simulation.Result) (compare.Comparison, error) {
				return compare.Comparison{BaselineRunID: baseID, ScenarioRunID: scenID, Comparable: true, IncomparableReasons: []string{},
					Metrics: []compare.Metric{{MetricPath: "/metrics/throughput/mean", Baseline: &nan, Status: compare.Available}}, Warnings: []string{}}, nil
			}
		}, kind: analysis.ComparisonUnencodable},
		{name: "the context is too large", budget: tiny, kind: analysis.ContextRefused, check: func(t *testing.T, e *analysis.Error) {
			if aicontext.CodeOf(e.Err) != aicontext.TooLarge {
				t.Errorf("cause %v", e.Err)
			}
		}},
		{name: "the context cannot be built", change: func(in *analysis.Input) { in.OutputLocale = "fr" }, kind: analysis.ContextRefused, check: func(t *testing.T, e *analysis.Error) {
			if aicontext.CodeOf(e.Err) == "" || aicontext.CodeOf(e.Err) == aicontext.TooLarge {
				t.Errorf("cause %v", e.Err)
			}
		}},
		{name: "the provider refuses", script: func(f *fake.Backend) {
			f.Fail(provider.RoleAnalyst, question, provider.NewError(provider.CodeRefused, 0, ""))
		},
			kind: analysis.CallFailed, called: true, check: func(t *testing.T, e *analysis.Error) {
				var pe *provider.Error
				var ce *einomodel.CallError
				if !errors.As(e.Err, &pe) || pe.Code != provider.CodeRefused || errors.As(e.Err, &ce) || e.Metadata == nil || e.Metadata.Role != provider.RoleAnalyst {
					t.Errorf("the client's own error and the call's metadata: %v %+v", e.Err, e.Metadata)
				}
			}},
		{name: "the answer is not an analysis", script: func(f *fake.Backend) { f.Script(provider.RoleAnalyst, question, []byte(`{"scenario":{}}`)) },
			kind: analysis.CallFailed, called: true, check: func(t *testing.T, e *analysis.Error) {
				if provider.CodeOf(e.Err) != provider.CodeInvalidOutput || e.Metadata == nil {
					t.Errorf("%v %+v", e.Err, e.Metadata)
				}
			}},
		{name: "the answer is ungrounded", script: func(f *fake.Backend) { f.Script(provider.RoleAnalyst, question, ungrounded) },
			kind: analysis.Ungrounded, called: true, calls: 2, check: func(t *testing.T, e *analysis.Error) {
				if len(e.Issues) == 0 || e.Issues[0].Code != analyst.NumberUnverified || e.Issues[0].Path != "/scenario/summary" || e.Metadata == nil || e.Metadata.OutputHash == "" {
					t.Errorf("%+v %+v", e.Issues, e.Metadata)
				}
				if r := e.Recheck; r == nil || r.Outcome != analysis.RecheckUngrounded || r.Issues != len(e.Issues) || r.Call == nil ||
					!strings.HasSuffix(r.Call.TemplateVersion, "+reground-0.1.0") || e.Metadata.TemplateVersion != "analyst-0.3.3" {
					t.Errorf("the re-ask %+v", r)
				}
			}},
	} {
		t.Run(c.name, func(t *testing.T) {
			in := input(t)
			if c.change != nil {
				c.change(&in)
			}
			f := fake.New()
			if c.script != nil {
				c.script(f)
			}
			lookup := analysis.PackLookup(packs(t))
			if c.lookup != nil {
				lookup = c.lookup
			}
			budget := c.budget
			if budget == (provider.Budget{}) {
				budget = provider.DefaultBudget()
			}
			obs := &calls{}
			_, err := graph(t, client(t, f), lookup, budget, obs).Run(context.Background(), in)
			var e *analysis.Error
			if !errors.As(err, &e) || e.Kind != c.kind {
				t.Fatalf("%v, want an *analysis.Error of kind %s", err, c.kind)
			}
			if c.check != nil {
				c.check(t, e)
			}
			calls := 0
			if c.called {
				calls = max(1, c.calls)
			}
			if n := len(f.Requests()); n != calls {
				t.Errorf("%d provider requests, want %d", n, calls)
			}
			if n := len(obs.all()); n != calls {
				t.Errorf("observed %d calls, want %d", n, calls)
			}
			if c.kind != analysis.Ungrounded && e.Recheck != nil {
				t.Errorf("a failure that is not the check's was asked again: %+v", e.Recheck)
			}
			if !c.called && e.Metadata != nil {
				t.Errorf("metadata before any call: %+v", e.Metadata)
			}
			if strings.Contains(err.Error(), question) || strings.Contains(e.Error(), "4242") {
				t.Errorf("the error's text carries content: %q", err.Error())
			}
		})
	}
}

// The check is given the model's names as the context showed them: prose
// that quotes a name holding a digit ("Server 4242") is grounded, while the
// same digit outside the name is a number nobody supplied.
func TestTheCheckGetsTheModelsNames(t *testing.T) {
	in := alone(t)
	var ir map[string]any
	if err := json.Unmarshal(in.Version.IR, &ir); err != nil {
		t.Fatal(err)
	}
	ir["resources"].([]any)[0].(map[string]any)["name"] = "Server 4242"
	b, err := json.Marshal(ir)
	if err != nil {
		t.Fatal(err)
	}
	in.Version.IR, in.Question = b, question
	answer := func(summary string) []byte {
		out, err := json.Marshal(provider.AnalysisOutput{Scenario: provider.AnalysisSummary{Summary: summary}, Delta: []provider.AnalysisDelta{},
			Bottleneck: []provider.Bottleneck{}, Assumptions: []string{}, Uncertainty: []string{"Only one replication was run."},
			SuggestedNextExperiments: []provider.Experiment{}})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	for summary, grounded := range map[string]bool{"Server 4242 sets the pace.": true, "4242 items wait.": false} {
		f := fake.New()
		f.Script(provider.RoleAnalyst, question, answer(summary))
		_, err := graph(t, client(t, f), packs(t), provider.DefaultBudget(), nil).Run(context.Background(), in)
		var e *analysis.Error
		if grounded && err != nil || !grounded && (!errors.As(err, &e) || e.Kind != analysis.Ungrounded) {
			t.Errorf("%q: %v", summary, err)
		}
	}
}

type noPacks struct{}

func (noPacks) Lookup(string) (domainpack.DomainPack, error) { return nil, errors.New("no pack") }

// Without a baseline the comparison is never asked for.
func TestNoComparisonWithoutABaseline(t *testing.T) {
	in := alone(t)
	in.Compare = func(*simulation.Result, *simulation.Result) (compare.Comparison, error) {
		t.Error("compared without a baseline")
		return compare.Comparison{}, nil
	}
	if _, err := graph(t, client(t, fake.New()), packs(t), provider.DefaultBudget(), nil).Run(context.Background(), in); err != nil {
		t.Fatal(err)
	}
}

// A cancelled context reaches every step and the call, which meet it
// themselves, as the analyzer's steps did before the graph (E6 review round
// 1, finding 1: Eino no longer stops a run between two nodes): before the
// run, every step runs, and the client refuses the call before the backend
// (its cancelled call comes back as the call's failure, observed once);
// during the call, the client's cancelled call comes back as the call's
// failure (its metadata kept), observed once.
func TestCancellation(t *testing.T) {
	f := fake.New()
	obs := &calls{}
	g := graph(t, client(t, f), packs(t), provider.DefaultBudget(), obs)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := g.Run(ctx, input(t))
	var e *analysis.Error
	if !errors.As(err, &e) || e.Kind != analysis.CallFailed || provider.CodeOf(e.Err) != provider.CodeCancelled || !errors.Is(e.Err, context.Canceled) ||
		len(f.Requests()) != 0 {
		t.Errorf("cancelled before the run: %v %+v; %d requests", err, e, len(f.Requests()))
	}
	if seen := obs.all(); len(seen) != 1 || e == nil || seen[0].err != e.Err {
		t.Errorf("cancelled before the run: observed %+v", seen)
	}
	obs = &calls{}

	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	g = graph(t, client(t, cancelling{Backend: f, cancel: cancel}), packs(t), provider.DefaultBudget(), obs)
	_, err = g.Run(ctx, input(t))
	if !errors.As(err, &e) || e.Kind != analysis.CallFailed || provider.CodeOf(e.Err) != provider.CodeCancelled || !errors.Is(e.Err, context.Canceled) ||
		e.Metadata == nil || e.Metadata.Provider != "fake" {
		t.Errorf("cancelled during the call: %v %+v", err, e)
	}
	if seen := obs.all(); len(seen) != 1 || provider.CodeOf(seen[0].err) != provider.CodeCancelled {
		t.Errorf("observed %+v", seen)
	}

	// Cancelled after the call answered, before the check (the attempt
	// stopped as the answer came in): the call was observed as answered,
	// and the check still runs, as it did before the graph: a grounded
	// answer is the checked analysis, an ungrounded one the run's failure.
	for name, script := range map[string]func(*fake.Backend){
		"grounded":   nil,
		"ungrounded": func(f *fake.Backend) { f.Script(provider.RoleAnalyst, question, invented(t)) },
	} {
		ctx, cancel = context.WithCancel(context.Background())
		defer cancel()
		f = fake.New()
		if script != nil {
			script(f)
		}
		var answered []error
		g, err = analysis.New(analysis.Config{Provider: client(t, f), Budget: provider.DefaultBudget(), Packs: packs(t),
			Called: func(_ context.Context, _ int, _ provider.Metadata, err error) {
				answered = append(answered, err)
				cancel()
			}})
		if err != nil {
			t.Fatal(err)
		}
		out, err := g.Run(ctx, input(t))
		if len(answered) != 1 || answered[0] != nil || len(f.Requests()) != 1 || ctx.Err() == nil {
			t.Errorf("cancelled after the %s answer: observed %v; %d requests", name, answered, len(f.Requests()))
		}
		switch {
		case script == nil && (err != nil || out.Analysis.Baseline == nil || out.Metadata.Provider != "fake"):
			t.Errorf("cancelled after the grounded answer: %v, want the checked analysis", err)
		case script != nil && (!errors.As(err, &e) || e.Kind != analysis.Ungrounded || len(e.Issues) == 0 || e.Metadata == nil):
			t.Errorf("cancelled after the ungrounded answer: %v, want it ungrounded", err)
		}
	}
}

type cancelling struct {
	*fake.Backend
	cancel context.CancelFunc
}

func (c cancelling) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	c.cancel()
	<-ctx.Done()
	return c.Backend.Complete(ctx, req)
}

// A panic in a node is the caller's panic, with the value it was raised
// with and the stack where it was raised, as when the steps were one
// function: Run raises an analysis.Panic, which the worker records as
// INTERNAL, not retried, with that value and that stack (handler.Reraised).
// It is never turned into an error the worker would retry, whose text
// would carry the stack. (Review round 1, finding 1: Eino recovers a
// node's panic, so a stack taken where the caller recovers ends at Run.)
func TestAPanicIsTheCallersPanic(t *testing.T) {
	for name, c := range map[string]struct {
		set    func(*analysis.Input, *analysis.PackLookup)
		origin string // the frame that panicked
	}{
		"in the comparison": {func(in *analysis.Input, _ *analysis.PackLookup) { in.Compare = comparePanics }, "analysis_test.comparePanics("},
		"in the pack lookup": {func(_ *analysis.Input, l *analysis.PackLookup) { *l = panicking{boom{secret}} },
			"analysis_test.panicking.Lookup("},
	} {
		t.Run(name, func(t *testing.T) {
			in, lookup := input(t), analysis.PackLookup(packs(t))
			c.set(&in, &lookup)
			f := fake.New()
			g := graph(t, client(t, f), lookup, provider.DefaultBudget(), nil)
			defer func() {
				v := recover()
				p, ok := v.(analysis.Panic)
				if !ok {
					t.Fatalf("recovered %#v, want an analysis.Panic", v)
				}
				if p.Value != (boom{secret}) || p.PanicValue() != (boom{secret}) {
					t.Errorf("recovered %#v, want the node's own value", p.Value)
				}
				if !strings.Contains(p.Stack, c.origin) || p.PanicStack() != p.Stack || !strings.HasPrefix(p.Stack, "goroutine ") {
					t.Errorf("the stack does not name the frame that panicked (%s):\n%s", c.origin, p.Stack)
				}
				// Formatted, it names neither the value's data nor the stack.
				for _, s := range []string{fmt.Sprint(p), fmt.Sprintf("%v %s", p, p)} {
					if strings.Contains(s, secret) || strings.Contains(s, "goroutine") {
						t.Errorf("formatted: %q", s)
					}
				}
				if len(f.Requests()) != 0 {
					t.Error("the provider was asked after a panic")
				}
			}()
			_, err := g.Run(context.Background(), in)
			t.Errorf("no panic: %v", err)
		})
	}
}

const secret = "PANIC-VALUE-SENTINEL"

type boom struct{ s string }

func comparePanics(*simulation.Result, *simulation.Result) (compare.Comparison, error) {
	panic(boom{secret})
}

type panicking struct{ v any }

func (p panicking) Lookup(string) (domainpack.DomainPack, error) { panic(p.v) }

type callerKey struct{}

// The observer is told of each call with the caller's context: its values
// (the worker's job correlation, observability.With, D-093 (b)) reach the
// call's log line only that way. Review round 1, finding 2.
func TestTheObserverGetsTheCallersContext(t *testing.T) {
	for name, script := range map[string]func(*fake.Backend){
		"an answered call": nil,
		"a failed call": func(f *fake.Backend) {
			f.Fail(provider.RoleAnalyst, question, provider.NewError(provider.CodeRefused, 0, ""))
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := fake.New()
			if script != nil {
				script(f)
			}
			var seen []any
			g, err := analysis.New(analysis.Config{Provider: client(t, f), Budget: provider.DefaultBudget(), Packs: packs(t),
				Called: func(ctx context.Context, _ int, _ provider.Metadata, _ error) {
					seen = append(seen, ctx.Value(callerKey{}))
				}})
			if err != nil {
				t.Fatal(err)
			}
			_, err = g.Run(context.WithValue(context.Background(), callerKey{}, "job_sentinel"), input(t))
			if (err == nil) != (script == nil) {
				t.Fatalf("run: %v", err)
			}
			if len(seen) != 1 || seen[0] != "job_sentinel" {
				t.Errorf("the observer saw %v, want the caller's value once", seen)
			}
		})
	}
}

// The configuration is checked when the graph is built.
func TestNewChecksTheConfiguration(t *testing.T) {
	c := client(t, fake.New())
	bad := provider.DefaultBudget()
	bad.MaxPromptBytes = 0
	for name, cfg := range map[string]analysis.Config{
		"no provider":  {Packs: packs(t), Budget: provider.DefaultBudget()},
		"no packs":     {Provider: c, Budget: provider.DefaultBudget()},
		"no budget":    {Provider: c, Packs: packs(t)},
		"a bad budget": {Provider: c, Packs: packs(t), Budget: bad},
	} {
		if _, err := analysis.New(cfg); err == nil {
			t.Errorf("%s: built", name)
		}
	}
}

// One compiled graph serves concurrent runs (the worker's lanes share it):
// each run gets its own answer, and the observer each call. (The client's
// per-organization slots are raised to the cap here, so that none of the
// 16 is refused for its slot.)
func TestConcurrentRuns(t *testing.T) {
	f := fake.New()
	obs := &calls{}
	b := provider.DefaultBudget()
	b.MaxConcurrentPerOrganization = 16
	c, err := provider.New(f, b)
	if err != nil {
		t.Fatal(err)
	}
	g := graph(t, c, packs(t), provider.DefaultBudget(), obs)
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := range 16 {
		wg.Go(func() {
			in := input(t)
			if i%2 == 1 {
				in = alone(t)
			}
			out, err := g.Run(context.Background(), in)
			switch {
			case err != nil:
				errs <- err
			case (out.Analysis.Baseline != nil) != (i%2 == 0):
				errs <- errors.New("an answer of another run")
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if n := len(obs.all()); n != 16 || len(f.Requests()) != 16 {
		t.Errorf("%d observed, %d requests", n, len(f.Requests()))
	}
}

// An error's text names its kind only, never its cause (which may carry
// a run's ID or a stored value), the context or the answer; the cause is
// reached by unwrapping.
func TestErrorTextsNameTheKind(t *testing.T) {
	for _, k := range []analysis.Kind{analysis.VersionUndecodable, analysis.DomainNotServed, analysis.ResultUndecodable, analysis.PatchUndecodable,
		analysis.CompareFailed, analysis.NotComparable, analysis.ComparisonUnencodable, analysis.ContextRefused, analysis.CallFailed, analysis.Ungrounded} {
		e := &analysis.Error{Kind: k, Err: errors.New("CAUSE-SENTINEL")}
		if !strings.Contains(e.Error(), string(k)) || strings.Contains(e.Error(), "CAUSE-SENTINEL") {
			t.Errorf("%s: %q", k, e.Error())
		}
		if !errors.Is(e, e.Err) {
			t.Errorf("%s does not unwrap to its cause", k)
		}
	}
}
