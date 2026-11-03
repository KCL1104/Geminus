// Package analysis is the analyst role's Eino graph (H-058, H-059; ADR-031;
// slice E2 of docs/design/H-056_INVESTIGATIONS_ON_EINO.md §6.4): the
// orchestration of an explanation of simulation results, from what the
// analysis job read under its lease to the checked analysis, as a DAG of
// five nodes and, since H-086 (R), the one re-ask of an answer the check
// refused as its branch:
//
//		decode → compare → build_context → generate → ground
//		                                               ↳ recheck_context → generate_2 → ground_2
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
//	    (analyst.Check);
//	  - recheck_context (H-086 (R), the owner's choice of 2026-11-03): an
//	    answer the check refused is asked again once, when the client handed
//	    it back (provider.AnalysisResult.Prior), the attempt has not stopped
//	    and Input.MayAskAgain, when set, says it may (an investigation's
//	    analysis: its investigation still runs, H-061 §12, read again by the
//	    job handler just before): the follow-up (aicontext.ReviseAnalysis) is
//	    the first prompt again, the model's own answer as its turn and the
//	    check's issues (code, path and the rule the code stands for), at most
//	    aicontext.MaxRepairIssues;
//	  - generate_2: the second call, through the same metered ChatModel as
//	    the first (its own reservation and settlement, the analyst's output
//	    bound and timeouts, the same attribution, so an investigation's
//	    ceiling counts it);
//	  - ground_2: the same check of the second answer against the same
//	    supplied data, never loosened.
//
// Only the check's refusal is asked again: a failed first call (a provider
// error, a spend cap, a truncated or schema-invalid answer, a cancellation)
// ends the graph as before, and so does an attempt that stopped before the
// re-ask (the check's refusal is then final, as before: no attempt asks, and
// pays, again), and so does an analysis Input.MayAskAgain does not let ask
// again (an investigation that has ended: no paid call after its end; the
// refusal is then final too; its error ends the graph as RecheckUnchecked,
// nothing asked again). The second answer, when it passes, is the analysis
// (Output, with its Recheck); when it does not, the analysis is ungrounded with the
// second answer's issues. A second call that fails, whatever its error (the
// attempt stopping during it included), leaves the first refusal as it was:
// Ungrounded with the first answer's issues, the Recheck saying how it
// failed. Nothing asks a third time: the branch has no cycle. Neither answer
// is kept but the analysis that passed: the first answer goes back to the
// model in memory only (provider.PriorAnswer).
//
// Every failure ends the graph with an *Error of its Kind, carrying what
// the job handler (internal/analyses/analyzer) maps to the job's failure and
// its log lines, as it did before the steps moved onto the graph. Nothing
// here stores, logs or reads storage (architecture rule
// aigraph-has-no-storage): the handler reads the job's inputs, injects the
// comparison and observes each call (Config.Called, its log line).
//
// The graph is compiled once (New) and serves concurrent runs; its nodes run
// one after the other in the caller's goroutine. Each step runs under the
// attempt's own context (its values, its cancellation, its cause and its
// deadline) and meets a stop itself, as the handler's steps did before the
// graph: Eino, which would end a run between two nodes once its context is
// done, runs on one that never is (context.WithoutCancel), so a stopped
// attempt ends with the failure of the step that saw it (the client refuses
// a call once the attempt has stopped, and the check of an answer that came
// before the stop still runs; E6 review round 1, the proposal graph's E3
// review round 1 pattern). No callback handler is attached, per run or
// globally (a graph-level handler would see the input and the answer:
// checks/E1_VALIDATION.md finding 4). A panic in a node is the caller's
// panic: Run raises it again as a Panic, the node's
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
	"strings"
	"time"

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
	// H-086 (R): the one re-ask of an answer the check refused.
	NodeRecheckContext  = "recheck_context"
	NodeRecheckGenerate = "generate_2"
	NodeRecheckGround   = "ground_2"
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
	// Called, when set, is told of each provider call as it returns: which
	// call of the analysis it was (1, or 2 for the re-ask, H-086 (R)), its
	// metadata and the client's error (nil when the call answered). The job
	// handler logs it.
	Called func(ctx context.Context, call int, md provider.Metadata, err error)
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
	// Investigation: an AI investigation asked for the analysis (its row
	// carries the label, H-061): the context gains investigation-rules-1.
	Investigation bool
	// MayAskAgain, when set, is asked once before an answer the check
	// refused is asked again, the second paid call (H-086 (R)): for an
	// investigation's analysis, whether its investigation still runs (H-061
	// §12: no paid call once it has ended), which the job handler reads
	// again under the lease (the graph reads no storage). False asks nothing
	// more: the first refusal stands, as after a stop. Its error ends the
	// graph as RecheckUnchecked, with nothing asked again. Nil asks again
	// with no such question (a person's analysis).
	MayAskAgain func(ctx context.Context) (bool, error)
}

// Output is the checked analysis and the first call's metadata; Recheck
// is set when the analysis is the re-ask's answer (H-086 (R)).
type Output struct {
	Analysis provider.AnalysisOutput
	Metadata provider.Metadata
	Recheck  *Recheck
}

// What the one re-ask came to (Recheck.Outcome; the stored metadata's
// repair.outcome and the audit's repair, H-086 (R)).
const (
	// RecheckGrounded: the second answer passed the check and is the
	// analysis.
	RecheckGrounded = "grounded"
	// RecheckUngrounded: the check refused the second answer too; the
	// analysis is ungrounded with its issues.
	RecheckUngrounded = "ungrounded"
	// RecheckFailed: the second call failed (Code says how); the first
	// answer's refusal stands.
	RecheckFailed = "failed"
)

// Recheck is what the one re-ask of an answer the check refused did (H-086
// (R)): never an answer or a prompt.
type Recheck struct {
	// Issues is how many of the first answer's issues the follow-up listed,
	// NotListed how many it left out.
	Issues, NotListed int
	// Outcome is RecheckGrounded, RecheckUngrounded or RecheckFailed.
	Outcome string
	// Code is a failed second call's code: "AI_" and the provider client's
	// code in upper case, as a job failure names it (INTERNAL for an error
	// that is not the client's).
	Code string
	// Call is the second call's metadata (also when it failed: what it
	// cost).
	Call *provider.Metadata
}

// Revised reports whether the second answer was checked: the analysis, or
// the issues it fails with, are the second answer's.
func (r *Recheck) Revised() bool {
	return r != nil && (r.Outcome == RecheckGrounded || r.Outcome == RecheckUngrounded)
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
	// Metadata; Recheck when it was asked again, H-086 (R): the issues are
	// then the second answer's, unless the second call failed).
	Ungrounded Kind = "ungrounded"
	// RecheckRefused: the follow-up of an answer the check refused cannot be
	// built (Err, an aicontext error: the server's own fault; Metadata and
	// Issues are the first answer's).
	RecheckRefused Kind = "recheck_refused"
	// RecheckUnchecked: whether an answer the check refused may be asked
	// again could not be learned (Err, Input.MayAskAgain's error; Metadata
	// and Issues are the first answer's): nothing was asked again.
	RecheckUnchecked Kind = "recheck_unchecked"
)

// Error is a failed analysis. Its text names only the kind: no cause,
// prompt or answer.
type Error struct {
	Kind Kind
	// Err is the cause, when there is one.
	Err error
	// Reasons are why the server does not compare the runs (NotComparable).
	Reasons []string
	// Metadata is the call's (CallFailed, Ungrounded, RecheckRefused,
	// RecheckUnchecked: the first call's); nil before a call.
	Metadata *provider.Metadata
	// Issues are why the answer is not grounded (Ungrounded, RecheckRefused,
	// RecheckUnchecked).
	Issues []analyst.Issue
	// Recheck is what the one re-ask did (Ungrounded; nil when there was
	// none).
	Recheck *Recheck
}

func (e *Error) Error() string { return "analysis: " + string(e.Kind) }

// Unwrap exposes the cause.
func (e *Error) Unwrap() error { return e.Err }

// Graph is the compiled analyst graph.
type Graph struct {
	r compose.Runnable[Input, *ended]
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
		n.called = func(context.Context, int, provider.Metadata, error) {}
	}
	g := compose.NewGraph[Input, *ended]()
	for _, s := range []struct {
		key  string
		node *compose.Lambda
	}{
		{NodeDecode, compose.InvokableLambda(guard(n.decode))},
		{NodeCompare, compose.InvokableLambda(guard(n.compare))},
		{NodeContext, compose.InvokableLambda(guard(n.buildContext))},
		{NodeGenerate, compose.InvokableLambda(guard(n.generate))},
		{NodeGround, compose.InvokableLambda(guard(n.ground))},
		{NodeRecheckContext, compose.InvokableLambda(guard(n.recheckContext))},
		{NodeRecheckGenerate, compose.InvokableLambda(guard(n.generateAgain))},
		{NodeRecheckGround, compose.InvokableLambda(guard(n.groundAgain))},
	} {
		if err := g.AddLambdaNode(s.key, s.node); err != nil {
			return nil, fmt.Errorf("analysis: %w", err)
		}
	}
	for _, e := range [][2]string{{compose.START, NodeDecode}, {NodeDecode, NodeCompare}, {NodeCompare, NodeContext},
		{NodeContext, NodeGenerate}, {NodeGenerate, NodeGround}, {NodeRecheckGenerate, NodeRecheckGround}, {NodeRecheckGround, compose.END}} {
		if err := g.AddEdge(e[0], e[1]); err != nil {
			return nil, fmt.Errorf("analysis: %w", err)
		}
	}
	// H-086 (R): a refused answer is asked again only when ground chose it,
	// and the second call is made only when its follow-up was built.
	for _, b := range []struct {
		from, to string
		pick     func(*ended) bool
	}{
		{NodeGround, NodeRecheckContext, func(e *ended) bool { return e.reask }},
		{NodeRecheckContext, NodeRecheckGenerate, func(e *ended) bool { return e.recheck != nil }},
	} {
		to, pick := b.to, b.pick
		branch := compose.NewGraphBranch(func(_ context.Context, e *ended) (string, error) {
			if pick(e) {
				return to, nil
			}
			return compose.END, nil
		}, map[string]bool{to: true, compose.END: true})
		if err := g.AddBranch(b.from, branch); err != nil {
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
// graph classifies, a stopped attempt's included (the step that saw the
// stop ends the run with its own failure: a cancelled call is CallFailed
// with the client's error); otherwise, for a bug in the graph's wiring,
// Eino's own error. A node's panic is raised again as a Panic.
func (g *Graph) Run(ctx context.Context, in Input) (Output, error) {
	// Eino ends a run between two nodes once its context ends; the steps see
	// the attempt's themselves (attempt), so the run's own never ends.
	out, err := g.r.Invoke(context.WithValue(context.WithoutCancel(ctx), attemptKey{}, ctx), in)
	if err == nil {
		return out.outcome()
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
	called func(context.Context, int, provider.Metadata, error)
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
	// ended is what the check made of the first answer (ground) and of the
	// re-ask's (ground_2), and what the re-ask did: the graph's end.
	ended struct {
		*answered
		first   checked
		reask   bool                     // ground: the refused answer is asked again
		ask     provider.AnalysisContext // recheck_context: the follow-up
		answer  *provider.AnalysisResult // generate_2: the second answer; nil when the call failed
		second  *checked                 // ground_2: the second answer's check; nil without one
		recheck *Recheck
	}
	// checked is one answer as the check left it: the analysis to store, or
	// the issues.
	checked struct {
		out    provider.AnalysisOutput
		issues []analyst.Issue
	}
)

// outcome is the graph's end as Run returns it: the checked analysis (the
// second answer's when it was asked again and passed), or Ungrounded with
// the issues of the last answer the check saw. The metadata is the first
// call's; the re-ask's is in the Recheck.
func (e *ended) outcome() (Output, error) {
	md := e.result.Metadata
	last := e.first
	if e.second != nil {
		last = *e.second
	}
	if len(last.issues) > 0 {
		return Output{}, &Error{Kind: Ungrounded, Issues: last.issues, Metadata: &md, Recheck: e.recheck}
	}
	return Output{Analysis: last.out, Metadata: md, Recheck: e.recheck}, nil
}

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
		Investigation: in.Investigation,
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
	res, md, err := n.ask(ctx, p.prompt.Context)
	n.called(ctx, 1, md, err)
	if err != nil {
		return nil, &Error{Kind: CallFailed, Err: err, Metadata: &md}
	}
	return &answered{prompted: p, result: res}, nil
}

// ask is one explanation call through the metered ChatModel: the sealed
// prompt's own texts, the context in the user message, and the client's
// result, or its error (the client's own) and the call's metadata.
func (n *nodes) ask(ctx context.Context, ac provider.AnalysisContext) (provider.AnalysisResult, provider.Metadata, error) {
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
		return provider.AnalysisResult{}, md, cause
	}
	r, _ := msg.Extra[einomodel.ResultKey].(*einomodel.Result)
	if r == nil || r.Analysis == nil {
		// Metered answers every explanation with its typed result.
		panic("analysis: the metered chat model answered without its result")
	}
	return *r.Analysis, r.Analysis.Metadata, nil
}

// check is the server's check of an answer against what the first context
// supplied (the re-ask supplies nothing more).
func check(a *answered, answer provider.AnalysisOutput) checked {
	out, issues := analyst.Check(analyst.Input{Output: answer, Allowed: a.prompt.Allowed, Supplied: a.prompt.Supplied,
		Names: a.prompt.Names, Scenario: a.scen, Baseline: a.base, Comparison: a.comparison})
	return checked{out: out, issues: issues}
}

// ground checks the first answer and decides whether a refused one is asked
// again (H-086 (R)): only when the client handed it back and the attempt
// has not stopped (a stopped attempt's refusal is final, as before).
func (n *nodes) ground(ctx context.Context, a *answered) (*ended, error) {
	e := &ended{answered: a, first: check(a, a.result.Output)}
	e.reask = len(e.first.issues) > 0 && a.result.Prior.Present() && ctx.Err() == nil
	return e, nil
}

// recheckContext is the follow-up of the refused answer
// (aicontext.ReviseAnalysis): its issues, at most aicontext.MaxRepairIssues,
// and the record the re-ask starts. An attempt that stopped since the check,
// and an analysis Input.MayAskAgain does not let ask again (an
// investigation that has ended since the attempt started, H-061 §12), ask
// nothing (no record: the first refusal stands); MayAskAgain's error ends
// the graph (RecheckUnchecked), nothing asked again.
func (n *nodes) recheckContext(ctx context.Context, e *ended) (*ended, error) {
	if ctx.Err() != nil {
		return e, nil
	}
	if may := e.in.MayAskAgain; may != nil {
		ok, err := may(ctx)
		if err != nil {
			md := e.result.Metadata
			return nil, &Error{Kind: RecheckUnchecked, Err: err, Metadata: &md, Issues: e.first.issues}
		}
		if !ok {
			return e, nil
		}
	}
	listed := e.first.issues[:min(len(e.first.issues), aicontext.MaxRepairIssues)]
	issues := make([]aicontext.CheckIssue, len(listed))
	for i, is := range listed {
		issues[i] = aicontext.CheckIssue{Code: is.Code, Path: is.Path}
	}
	notListed := len(e.first.issues) - len(issues)
	ask, err := aicontext.ReviseAnalysis(e.prompt.Context, e.result.Prior, issues, notListed)
	if err != nil {
		md := e.result.Metadata
		return nil, &Error{Kind: RecheckRefused, Err: err, Metadata: &md, Issues: e.first.issues}
	}
	e.ask, e.recheck = ask, &Recheck{Issues: len(issues), NotListed: notListed}
	return e, nil
}

// generateAgain is the re-ask's call, metered like the first (its own
// reservation). A call that fails, whatever its error, leaves the first
// refusal standing; the record says how it failed.
func (n *nodes) generateAgain(ctx context.Context, e *ended) (*ended, error) {
	res, md, err := n.ask(ctx, e.ask)
	n.called(ctx, 2, md, err)
	e.recheck.Call = &md
	if err != nil {
		e.recheck.Outcome, e.recheck.Code = RecheckFailed, codeOf(err)
		return e, nil
	}
	e.answer = &res
	return e, nil
}

// groundAgain checks the second answer exactly as the first, against the
// same supplied data.
func (n *nodes) groundAgain(_ context.Context, e *ended) (*ended, error) {
	if e.answer == nil {
		return e, nil
	}
	second := check(e.answered, e.answer.Output)
	e.second, e.recheck.Outcome = &second, RecheckGrounded
	if len(second.issues) > 0 {
		e.recheck.Outcome = RecheckUngrounded
	}
	return e, nil
}

// codeOf is a failed call's code as a job failure names it; INTERNAL for an
// error that is not the provider client's.
func codeOf(err error) string {
	if c := provider.CodeOf(err); c != "" {
		return "AI_" + strings.ToUpper(string(c))
	}
	return "INTERNAL"
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

// attemptKey holds, in the context Run hands Eino, the attempt's own
// context (the caller's).
type attemptKey struct{}

// attempt is a step's context: the node's (its values: Eino's, and below
// them the attempt's) with the attempt's cancellation and deadline. A value
// the node's does not hold is the attempt's: the cancellation's own, which
// keeps context.Cause and the contexts the step derives (the client's call)
// tied to the attempt's directly. It is the proposal graph's attempt.
type attempt struct {
	context.Context // the node's, from Eino: never cancelled
	of              context.Context
}

func (c attempt) Deadline() (time.Time, bool) { return c.of.Deadline() }
func (c attempt) Done() <-chan struct{}       { return c.of.Done() }
func (c attempt) Err() error                  { return c.of.Err() }
func (c attempt) Value(key any) any {
	if v := c.Context.Value(key); v != nil {
		return v
	}
	return c.of.Value(key)
}

// guard runs fn under the attempt's context and turns a panic of fn into a
// *nodePanic, with the stack of the goroutine that panicked: recover does
// not unwind it until the deferred call returns.
func guard[I, O any](fn func(context.Context, I) (O, error)) func(context.Context, I) (O, error) {
	return func(ctx context.Context, in I) (out O, err error) {
		defer func() {
			if v := recover(); v != nil {
				var zero O
				out, err = zero, &nodePanic{Panic{Value: v, Stack: stack()}}
			}
		}()
		if of, ok := ctx.Value(attemptKey{}).(context.Context); ok {
			ctx = attempt{Context: ctx, of: of}
		}
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
