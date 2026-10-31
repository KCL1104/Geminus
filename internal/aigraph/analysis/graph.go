// Package analysis is the analyst role's Eino graph (H-058, H-059; ADR-031;
// slice E2 of docs/design/H-056_INVESTIGATIONS_ON_EINO.md §6.4): the
// orchestration of an explanation of simulation results, from what the
// analysis job read under its lease to the checked analysis, as a DAG of
// five nodes:
//
//		decode → compare → build_context → generate → ground
//
//	  - decode: the analyzed run's version (model.Decode), the model's domain
//	    pack, the stored results (strictly) and the run's frozen patch;
//	  - compare: with a baseline, the server's comparison of the two runs
//	    (Input.Compare, injected by the job handler: comparisons.Input and
//	    compare.Compare); runs the server does not compare stop here, before
//	    the paid call (T-030 review r1 M4);
//	  - build_context: the sanitized analyst context (aicontext.Analysis) and
//	    the attribution the spend ledger charges the call to (T-053);
//	  - generate: the one call, through einomodel.Metered over the metered
//	    provider client (provider.LLMProvider), so the client's checks run
//	    unchanged: the sealed prompt, the attribution, the prompt budget and
//	    the organization's slot, the spend reservation and its settlement,
//	    the model the answer names, the output bound, the strict decoding of
//	    the answer and no-store;
//	  - ground: the server's check of the answer against what it supplied
//	    (analyst.Check).
//
// Every failure ends the graph with an *Error of its Kind, carrying what
// the job handler (internal/analyses/analyzer) maps to the job's failure and
// its log lines, as it did before the steps moved onto the graph. Nothing
// here stores, logs or reads storage (architecture rule
// aigraph-has-no-storage): the handler reads the job's inputs, injects the
// comparison and observes each call (Config.Called, its log line).
//
// The graph is compiled once (New) and serves concurrent runs; its nodes run
// one after the other in the caller's goroutine. ctx reaches every node and
// the call; Eino also stops between nodes once ctx is done. No callback
// handler is attached, per run or globally (a graph-level handler would see
// the input and the answer: checks/E1_VALIDATION.md finding 4). A panic in
// a node is the caller's panic: Run raises it again as a Panic, the node's
// own value and the stack where the node panicked, which the worker records
// as when the steps were one function (handler.Reraised): INTERNAL, not
// retried, logged with that stack.
package analysis

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"runtime"
	"slices"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/kcl1104/geminus/internal/ai/aicontext"
	"github.com/kcl1104/geminus/internal/ai/analyst"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/aigraph/einomodel"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/results/compare"
	"github.com/kcl1104/geminus/internal/simulation"
)

// The graph's name and its nodes' keys.
const (
	Name         = "analyst"
	NodeDecode   = "decode"
	NodeCompare  = "compare"
	NodeContext  = "build_context"
	NodeGenerate = "generate"
	NodeGround   = "ground"
)

// PackLookup finds a model's domain pack; *domainpack.Registry is one.
type PackLookup interface {
	Lookup(domain string) (domainpack.DomainPack, error)
}

// Config is the graph's setup.
type Config struct {
	// Provider is the checked provider client (provider.Client) of the
	// deployment's mode: the graph reaches it only through einomodel.Metered.
	Provider provider.LLMProvider
	// Budget bounds the context the graph builds; it is the client's.
	Budget provider.Budget
	Packs  PackLookup
	// Called, when set, is told of each provider call as it returns: its
	// metadata and the client's error (nil when the call answered). The job
	// handler logs it.
	Called func(ctx context.Context, md provider.Metadata, err error)
}

// Version is the analyzed run's model version as the job read it.
type Version struct {
	ID, ModelID string
	IR          []byte // the version's canonical IR
}

// Run is a run of the analysis as the job read it.
type Run struct {
	ID, ModelID string
	Result      []byte // the run's stored Result JSON
}

// Input is one analysis: what the job read under the lease, as data.
type Input struct {
	OrganizationID string
	Version        Version
	Scenario       Run
	ScenarioPatch  []byte // the analyzed run's frozen JSON Patch array
	Config         simulation.RunConfig
	// Baseline is the run the scenario is compared with; nil for none.
	Baseline *Run
	// Compare is the server's comparison of the decoded baseline and
	// scenario results; asked only with a Baseline.
	Compare      func(baseline, scenario *simulation.Result) (compare.Comparison, error)
	Question     string // "" for none
	OutputLocale string
	// Attribution is what the spend ledger charges the call to.
	Attribution provider.Attribution
}

// Output is the checked analysis and its call's metadata.
type Output struct {
	Analysis provider.AnalysisOutput
	Metadata provider.Metadata
}

// Kind is what failed.
type Kind string

const (
	// VersionUndecodable: the analyzed run's version does not decode.
	VersionUndecodable Kind = "version_undecodable"
	// DomainNotServed: no pack serves the model's domain.
	DomainNotServed Kind = "domain_not_served"
	// ResultUndecodable: a stored result does not decode.
	ResultUndecodable Kind = "result_undecodable"
	// PatchUndecodable: the analyzed run's patch does not decode.
	PatchUndecodable Kind = "patch_undecodable"
	// CompareFailed: the comparison failed (Err).
	CompareFailed Kind = "compare_failed"
	// NotComparable: the server does not compare the runs (Reasons).
	NotComparable Kind = "not_comparable"
	// ComparisonUnencodable: the comparison has no JSON (Err).
	ComparisonUnencodable Kind = "comparison_unencodable"
	// ContextRefused: the context cannot be built (Err, an aicontext error).
	ContextRefused Kind = "context_refused"
	// CallFailed: the provider call failed (Err, the client's error, and
	// Metadata).
	CallFailed Kind = "call_failed"
	// Ungrounded: the answer is not grounded in the results (Issues and
	// Metadata).
	Ungrounded Kind = "ungrounded"
)

// Error is a failed analysis. Its text names only the kind: no cause,
// prompt or answer.
type Error struct {
	Kind Kind
	// Err is the cause, when there is one.
	Err error
	// Reasons are why the server does not compare the runs (NotComparable).
	Reasons []string
	// Metadata is the call's (CallFailed, Ungrounded); nil before a call.
	Metadata *provider.Metadata
	// Issues are why the answer is not grounded (Ungrounded).
	Issues []analyst.Issue
}

func (e *Error) Error() string { return "analysis: " + string(e.Kind) }

// Unwrap exposes the cause.
func (e *Error) Unwrap() error { return e.Err }

// Graph is the compiled analyst graph.
type Graph struct {
	r compose.Runnable[Input, Output]
}

// New checks cfg and compiles the graph.
func New(cfg Config) (*Graph, error) {
	if cfg.Provider == nil || cfg.Packs == nil {
		return nil, errors.New("analysis: a provider and packs are required")
	}
	if err := cfg.Budget.Validate(); err != nil {
		return nil, fmt.Errorf("analysis: %w", err)
	}
	m, err := einomodel.New(cfg.Provider)
	if err != nil {
		return nil, err
	}
	n := &nodes{model: m, budget: cfg.Budget, packs: cfg.Packs, called: cfg.Called}
	if n.called == nil {
		n.called = func(context.Context, provider.Metadata, error) {}
	}
	g := compose.NewGraph[Input, Output]()
	for _, s := range []struct {
		key  string
		node *compose.Lambda
	}{
		{NodeDecode, compose.InvokableLambda(guard(n.decode))},
		{NodeCompare, compose.InvokableLambda(guard(n.compare))},
		{NodeContext, compose.InvokableLambda(guard(n.buildContext))},
		{NodeGenerate, compose.InvokableLambda(guard(n.generate))},
		{NodeGround, compose.InvokableLambda(guard(n.ground))},
	} {
		if err := g.AddLambdaNode(s.key, s.node); err != nil {
			return nil, fmt.Errorf("analysis: %w", err)
		}
	}
	for _, e := range [][2]string{{compose.START, NodeDecode}, {NodeDecode, NodeCompare}, {NodeCompare, NodeContext},
		{NodeContext, NodeGenerate}, {NodeGenerate, NodeGround}, {NodeGround, compose.END}} {
		if err := g.AddEdge(e[0], e[1]); err != nil {
			return nil, fmt.Errorf("analysis: %w", err)
		}
	}
	r, err := g.Compile(context.Background(), compose.WithGraphName(Name), compose.WithNodeTriggerMode(compose.AllPredecessor))
	if err != nil {
		return nil, fmt.Errorf("analysis: %w", err)
	}
	return &Graph{r: r}, nil
}

// Run runs one analysis. Its error is an *Error for every failure the
// graph classifies; otherwise ctx's end (a cancellation wrapped by Eino) or,
// for a bug in the graph's wiring, Eino's own error. A node's panic is
// raised again as a Panic.
func (g *Graph) Run(ctx context.Context, in Input) (Output, error) {
	out, err := g.r.Invoke(ctx, in)
	if err == nil {
		return out, nil
	}
	var p *nodePanic
	if errors.As(err, &p) {
		panic(p.p)
	}
	var e *Error
	if errors.As(err, &e) {
		return Output{}, e
	}
	return Output{}, err
}

// nodes are the graph's node bodies over its setup.
type nodes struct {
	model  *einomodel.Metered
	budget provider.Budget
	packs  PackLookup
	called func(context.Context, provider.Metadata, error)
}

// What each node hands the next.
type (
	decoded struct {
		in     Input
		ir     *model.ModelIR
		domain domainpack.PromptContext
		scen   *simulation.Result
		patch  model.Patch
		base   *simulation.Result // nil without a baseline
	}
	compared struct {
		*decoded
		comparison     *compare.Comparison // nil without a baseline
		comparisonJSON jsontext.Value
	}
	prompted struct {
		*compared
		prompt aicontext.AnalysisPrompt
	}
	answered struct {
		*prompted
		result provider.AnalysisResult
	}
)

// result decodes a stored Result strictly.
func result(data []byte) (*simulation.Result, error) {
	var r simulation.Result
	if err := model.Decode(data, &r); err != nil {
		return nil, &Error{Kind: ResultUndecodable, Err: err}
	}
	return &r, nil
}

func (n *nodes) decode(_ context.Context, in Input) (*decoded, error) {
	var ir model.ModelIR
	if err := model.Decode(in.Version.IR, &ir); err != nil {
		return nil, &Error{Kind: VersionUndecodable, Err: err}
	}
	pack, err := n.packs.Lookup(ir.Domain)
	if err != nil {
		return nil, &Error{Kind: DomainNotServed, Err: err}
	}
	scen, err := result(in.Scenario.Result)
	if err != nil {
		return nil, err
	}
	var patch model.Patch
	if err := json.Unmarshal(in.ScenarioPatch, &patch); err != nil {
		return nil, &Error{Kind: PatchUndecodable, Err: err}
	}
	d := &decoded{in: in, ir: &ir, domain: pack.PromptContext(), scen: scen, patch: patch}
	if in.Baseline != nil {
		if d.base, err = result(in.Baseline.Result); err != nil {
			return nil, err
		}
	}
	return d, nil
}

func (n *nodes) compare(_ context.Context, d *decoded) (*compared, error) {
	c := &compared{decoded: d}
	if d.base == nil {
		return c, nil
	}
	if d.in.Compare == nil {
		return nil, &Error{Kind: CompareFailed, Err: errors.New("analysis: a baseline without a comparison")}
	}
	cm, err := d.in.Compare(d.base, d.scen)
	if err != nil {
		return nil, &Error{Kind: CompareFailed, Err: err}
	}
	if !cm.Comparable {
		// The API refuses such a pair; a job that has one stops before the paid call.
		return nil, &Error{Kind: NotComparable, Reasons: slices.Clone(cm.IncomparableReasons)}
	}
	if c.comparisonJSON, err = cm.JSON(); err != nil {
		return nil, &Error{Kind: ComparisonUnencodable, Err: err}
	}
	c.comparison = &cm
	return c, nil
}

func (n *nodes) buildContext(_ context.Context, c *compared) (*prompted, error) {
	in := c.in
	ai := aicontext.AnalysisInput{
		Scope:         aicontext.Scope{OrganizationID: in.OrganizationID, ModelID: in.Scenario.ModelID},
		Version:       aicontext.Version{OrganizationID: in.OrganizationID, ModelID: in.Version.ModelID, VersionID: in.Version.ID, IR: c.ir},
		Domain:        c.domain,
		Scenario:      aicontext.RunResult{OrganizationID: in.OrganizationID, ModelID: in.Scenario.ModelID, RunID: in.Scenario.ID, Result: c.scen},
		ScenarioPatch: c.patch,
		Config:        in.Config,
		Question:      in.Question,
		OutputLocale:  in.OutputLocale,
		Budget:        n.budget,
	}
	if c.base != nil {
		ai.Comparison = c.comparisonJSON
		ai.Baseline = &aicontext.RunResult{OrganizationID: in.OrganizationID, ModelID: in.Baseline.ModelID, RunID: in.Baseline.ID, Result: c.base}
	}
	ap, err := aicontext.Analysis(ai)
	if err != nil {
		return nil, &Error{Kind: ContextRefused, Err: err}
	}
	ap.Context.Attribution = in.Attribution // what the spend ledger charges the call to (T-053)
	return &prompted{compared: c, prompt: ap}, nil
}

func (n *nodes) generate(ctx context.Context, p *prompted) (*answered, error) {
	ac := p.prompt.Context
	msgs := []*schema.Message{
		{Role: schema.System, Content: ac.Prompt.System},
		{Role: schema.User, Content: ac.Prompt.User(), Extra: map[string]any{einomodel.CallKey: &einomodel.Call{Analysis: &ac}}},
	}
	msg, err := n.model.Generate(ctx, msgs)
	if err != nil {
		md, cause := provider.Metadata{}, err
		var ce *einomodel.CallError
		if errors.As(err, &ce) {
			md, cause = ce.Metadata, ce.Err
		}
		n.called(ctx, md, cause)
		return nil, &Error{Kind: CallFailed, Err: cause, Metadata: &md}
	}
	r, _ := msg.Extra[einomodel.ResultKey].(*einomodel.Result)
	if r == nil || r.Analysis == nil {
		// Metered answers every explanation with its typed result.
		panic("analysis: the metered chat model answered without its result")
	}
	n.called(ctx, r.Analysis.Metadata, nil)
	return &answered{prompted: p, result: *r.Analysis}, nil
}

func (n *nodes) ground(_ context.Context, a *answered) (Output, error) {
	out, issues := analyst.Check(analyst.Input{Output: a.result.Output, Allowed: a.prompt.Allowed, Supplied: a.prompt.Supplied,
		Names: a.prompt.Names, Scenario: a.scen, Baseline: a.base, Comparison: a.comparison})
	md := a.result.Metadata
	if len(issues) > 0 {
		return Output{}, &Error{Kind: Ungrounded, Issues: issues, Metadata: &md}
	}
	return Output{Analysis: out, Metadata: md}, nil
}

// Panic is a node's panic as Run raises it again in the caller's
// goroutine: the value the node panicked with and the stack where it did.
// Eino recovers a node's panic, unwinding the frames that panicked, so a
// stack taken where the caller recovers Run's panic ends at Run; Stack is
// the panicking goroutine's, taken as the node's panic was recovered. It is
// a handler.Reraised (internal/worker/handler): the worker records the
// panic with this value and this stack, INTERNAL and not retried.
type Panic struct {
	Value any
	Stack string
}

// PanicValue is the node's own value.
func (p Panic) PanicValue() any { return p.Value }

// PanicStack is the stack where the node panicked.
func (p Panic) PanicStack() string { return p.Stack }

// String names the value's type only: the value may hold the data the node
// worked on, and the stack is for the worker's own field.
func (p Panic) String() string { return fmt.Sprintf("analysis: a node panicked with a %T", p.Value) }

// nodePanic is a node's panic, carried out of Eino (which recovers a node's
// panic into an error whose text holds the stack) so that Run raises it
// again in the caller's goroutine.
type nodePanic struct{ p Panic }

func (*nodePanic) Error() string { return "analysis: a node panicked" }

// guard turns a panic of fn into a *nodePanic, with the stack of the
// goroutine that panicked: recover does not unwind it until the deferred
// call returns.
func guard[I, O any](fn func(context.Context, I) (O, error)) func(context.Context, I) (O, error) {
	return func(ctx context.Context, in I) (out O, err error) {
		defer func() {
			if v := recover(); v != nil {
				var zero O
				out, err = zero, &nodePanic{Panic{Value: v, Stack: stack()}}
			}
		}()
		return fn(ctx, in)
	}
}

// stack is the calling goroutine's stack, as runtime/debug.Stack formats
// it (runtime/debug is not among the graph's imports: aigraph-has-no-storage).
func stack() string {
	for n := 4 << 10; ; n *= 2 {
		buf := make([]byte, n)
		if m := runtime.Stack(buf, false); m < n {
			return string(buf[:m])
		}
	}
}
