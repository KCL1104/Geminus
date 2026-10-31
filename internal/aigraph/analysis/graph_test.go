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
	md  provider.Metadata
	err error
}

func (c *calls) called(_ context.Context, md provider.Metadata, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen = append(c.seen, observed{md, err})
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
		Config: in.Config, Question: in.Question, OutputLocale: in.OutputLocale, Budget: provider.DefaultBudget()}
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

// Each failure is an *analysis.Error of its kind, carrying what the
// handler maps (the cause, the reasons, the call's metadata, the issues);
// nothing before the call reaches the provider or the observer.
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
			kind: analysis.Ungrounded, called: true, check: func(t *testing.T, e *analysis.Error) {
				if len(e.Issues) == 0 || e.Issues[0].Code != analyst.NumberUnverified || e.Issues[0].Path != "/scenario/summary" || e.Metadata == nil || e.Metadata.OutputHash == "" {
					t.Errorf("%+v %+v", e.Issues, e.Metadata)
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
			if n := len(f.Requests()); (n == 1) != c.called || n > 1 {
				t.Errorf("%d provider requests", n)
			}
			if n := len(obs.all()); (n == 1) != c.called || n > 1 {
				t.Errorf("observed %d calls", n)
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

// A cancelled context reaches the graph and the call: before the run, no
// node runs and nothing is asked; during the call, the client's cancelled
// call comes back as the call's failure (its metadata kept), observed once.
func TestCancellation(t *testing.T) {
	f := fake.New()
	obs := &calls{}
	g := graph(t, client(t, f), packs(t), provider.DefaultBudget(), obs)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := g.Run(ctx, input(t))
	var e *analysis.Error
	if !errors.Is(err, context.Canceled) || errors.As(err, &e) || len(f.Requests()) != 0 || len(obs.all()) != 0 {
		t.Errorf("cancelled before the run: %v; %d requests, %d observed", err, len(f.Requests()), len(obs.all()))
	}

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
	// and the run ends as a cancellation at the next node, not as a failure
	// of the analysis.
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	f = fake.New()
	var answered []error
	g, err = analysis.New(analysis.Config{Provider: client(t, f), Budget: provider.DefaultBudget(), Packs: packs(t),
		Called: func(_ context.Context, _ provider.Metadata, err error) { answered = append(answered, err); cancel() }})
	if err != nil {
		t.Fatal(err)
	}
	_, err = g.Run(ctx, input(t))
	if !errors.Is(err, context.Canceled) || errors.As(err, &e) || len(answered) != 1 || answered[0] != nil || len(f.Requests()) != 1 {
		t.Errorf("cancelled after the answer: %v; observed %v; %d requests", err, answered, len(f.Requests()))
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
// with, as when the steps were one function: the worker records it as
// INTERNAL and does not retry it (it is never turned into an error the
// worker would retry, whose text would carry the stack).
func TestAPanicIsTheCallersPanic(t *testing.T) {
	type boom struct{ n int }
	for name, set := range map[string]func(*analysis.Input, *analysis.PackLookup){
		"in the comparison": func(in *analysis.Input, _ *analysis.PackLookup) {
			in.Compare = func(*simulation.Result, *simulation.Result) (compare.Comparison, error) { panic(boom{1}) }
		},
		"in the pack lookup": func(_ *analysis.Input, l *analysis.PackLookup) { *l = panicking{boom{1}} },
	} {
		t.Run(name, func(t *testing.T) {
			in, lookup := input(t), analysis.PackLookup(packs(t))
			set(&in, &lookup)
			f := fake.New()
			g := graph(t, client(t, f), lookup, provider.DefaultBudget(), nil)
			defer func() {
				if v := recover(); v != (boom{1}) {
					t.Errorf("recovered %#v, want the node's own value", v)
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

type panicking struct{ v any }

func (p panicking) Lookup(string) (domainpack.DomainPack, error) { panic(p.v) }

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
