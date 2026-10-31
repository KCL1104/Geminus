// Package proposal is the proposal role graph (H-058, H-059; slice E3 of
// docs/design/H-056_INVESTIGATIONS_ON_EINO.md §6.4, with H-057's one repair
// as its branch): one attempt of a proposal asked for in words (an edit or
// a scenario, with or without web research, of the model the person chose)
// as an Eino graph in DAG mode, from the stored request to the server's
// outcome. The worker's model_proposal handler (internal/proposals/generator)
// runs it with its I/O as hooks, and the live eval (internal/aievals) with
// none, so the eval measures the worker's path. It is compiled once (New)
// and run per attempt (Run):
//
//	decode_base    the stored base's IR, its domain's pack, the stored ID pool
//	build_context  the sealed context (aicontext.Proposal)
//	preload        Hooks.Preload: the base's dataset snapshots, read before the paid call
//	verify         Hooks.Verify: the binding stage on the base itself, before the paid call
//	generate       the paid call, through the metered ChatModel (einomodel.Metered)
//	classify       invalid_output is an invalid outcome; any other failure ends the attempt
//	review         the server's review (review.Review)
//	bind           Hooks.Bind: the binding stage on a ready candidate
//	repair_context H-057: the follow-up of a refused answer (repair.Plan), or the end
//	generate_2     the repair's call, through the same ChatModel
//	review_2       what came of it (repair.Settle: the review and the binding stage again)
//
// Each node runs only when the one before chose it, and the graph has no
// cycle, so there is at most one repair by construction. The reviewed steps
// are the same functions as before, in the same order and with the same
// inputs (the context builder, the metered client's checks behind the
// ChatModel, the review, the binding stage, the repair): only the
// orchestration is Eino's. What is decided is carried as data, not as Eino
// errors, so Run returns the step's own error (the client's, a hook's, or a
// typed error of this package) and the Result as it stood.
//
// Each step runs under the attempt's own context (its values, its
// cancellation and its deadline), and sees the attempt stop itself, as the
// handler's own pipeline did before the graph: Eino, which would end a run
// between two nodes once its context ends, runs on one that never does, so
// a stopped attempt ends with the error of the step that saw it (E3 review
// round 1).
//
// No ChatTemplate, checkpoint store, retry or failover configuration and no
// global callback is used; a caller's callbacks would see the attempt's
// state, so none is attached in production. The graph does no I/O of its
// own (aigraph-has-no-storage): what it reads and writes is the hooks'.
package proposal

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"runtime"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/kcl1104/geminus/internal/ai/aicontext"
	"github.com/kcl1104/geminus/internal/ai/idpool"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/repair"
	"github.com/kcl1104/geminus/internal/ai/review"
	"github.com/kcl1104/geminus/internal/aigraph/einomodel"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/model"
)

// The graph's nodes, in order: each one's key and its name in Eino's
// callbacks. NodeModel is the name the model call's own callbacks run under,
// inside generate and generate_2.
const (
	NodeDecode         = "decode_base"
	NodeContext        = "build_context"
	NodePreload        = "preload"
	NodeVerify         = "verify"
	NodeGenerate       = "generate"
	NodeClassify       = "classify"
	NodeReview         = "review"
	NodeBind           = "bind"
	NodeRepairContext  = "repair_context"
	NodeRepairGenerate = "generate_2"
	NodeRepairReview   = "review_2"
	NodeModel          = "model"
)

// PackLookup finds a model's domain pack; *domainpack.Registry is one.
type PackLookup interface {
	Lookup(domain string) (domainpack.DomainPack, error)
}

// Config is what every run shares.
type Config struct {
	// Model is the metered ChatModel over the text client (einomodel.New of
	// the worker's provider.Client): the graph's only way to a model.
	Model *einomodel.Metered
	Packs PackLookup
	// Budget bounds the context; it is the client's, passed on unchanged.
	Budget provider.Budget
}

// Graph is the compiled graph; it is safe for concurrent runs.
type Graph struct {
	cfg Config
	r   compose.Runnable[*flow, *flow]
}

// Base is the proposal's base version as the server read it: its IR as
// stored (the canonical text the review applies a patch to; the graph
// decodes it).
type Base struct {
	OrganizationID, ModelID, VersionID string
	IR                                 []byte
}

// Request is one attempt's stored request, as the server read it in Scope.
type Request struct {
	Scope             aicontext.Scope
	Mode              provider.Mode
	Base              Base
	Intent            string
	SelectedEntityIDs []string
	// ProposalID and Pool are the proposal and its stored ID pool (JSON,
	// D-046), allocated in Scope's organization.
	ProposalID string
	Pool       []byte
	// OutputLocale, Research (H-046 (2)) and Model (H-049; "" for the
	// client's own) are the person's.
	OutputLocale string
	Research     bool
	Model        string
	// Attribution is what the spend ledger charges each call to (T-053).
	Attribution provider.Attribution
	// Repair: an answer the server refuses for what it says gets one repair
	// (H-057), as every proposal job does. The live eval scores first
	// answers only, so it runs without.
	Repair bool
}

// Hooks are the caller's I/O, run as the graph's own nodes (the graph does
// none): each nil one is skipped, as the live eval runs.
type Hooks struct {
	// Preload reads what the binding stage will need (the base version's
	// dataset snapshots), before the paid call. Its error ends the attempt
	// as it is.
	Preload func(ctx context.Context) error
	// Verify runs the binding stage on the base itself, before the paid
	// call, and returns the manifest it verified (what a ready proposal
	// binds). Its error ends the attempt as it is.
	Verify func(ctx context.Context, base *model.ModelIR) ([]model.DatasetBinding, error)
	// Bind is the binding stage on a candidate the review found ready: its
	// issues refuse the outcome or stay with it as warnings
	// (review.Outcome.Refuse), before a repair is considered; its error ends
	// the attempt (a *BindError).
	Bind func(ctx context.Context, candidate *model.ModelIR) ([]model.ValidationIssue, error)
	// Answered is told of each call's end: 1 for the first call, 2 for its
	// repair, with the call's metadata and the client's error (the worker's
	// log line).
	Answered func(call int, m provider.Metadata, err error)
}

// Result is what an attempt came to, as it stood when the graph ended.
type Result struct {
	// Outcome is the server's outcome: the repair's when there was one.
	Outcome review.Outcome
	// Call is the first call's metadata once the model was asked (also when
	// the call failed).
	Call *provider.Metadata
	// Manifest is what Hooks.Verify returned.
	Manifest []model.DatasetBinding
	// Sources are the pages the first call's research opened that its
	// answer relied on (provider.FilterSources, H-046 (2)).
	Sources []provider.WebSource
	// Repair is what the one repair did (H-057); nil when there was none.
	Repair *repair.Record
}

// Input names what the server stored that cannot be read.
type Input string

const (
	InputBase   Input = "base"   // the base version's IR does not decode
	InputDomain Input = "domain" // no pack serves the model's domain
	InputPool   Input = "pool"   // the stored ID pool does not decode
)

// InputError: what the server stored cannot be read (before any call).
type InputError struct {
	Input Input
	Err   error
}

func (e *InputError) Error() string {
	return "proposal: the stored " + string(e.Input) + " cannot be read"
}
func (e *InputError) Unwrap() error { return e.Err }

// ContextError: the context builder refused the request (aicontext's Code),
// or, with Repair, the repair's follow-up could not be built.
type ContextError struct {
	Code   aicontext.Code
	Repair bool
	Err    error
}

func (e *ContextError) Error() string {
	if e.Repair {
		return "proposal: the repair's context: " + string(e.Code)
	}
	return "proposal: the context: " + string(e.Code)
}
func (e *ContextError) Unwrap() error { return e.Err }

// CallError: the first call failed, other than by an answer refused for
// what it says (that is an invalid outcome). Err is the client's error;
// Final when the attempt may not run again whatever its code: a research
// call's (H-046 (2)) or a slow model's (H-049), whose jobs have one attempt.
type CallError struct {
	Err   error
	Final bool
}

func (e *CallError) Error() string { return e.Err.Error() }
func (e *CallError) Unwrap() error { return e.Err }

// ReviewError: the server could not review an answer against the base (a
// fault of the server's own); Repair when it was the repair's answer.
type ReviewError struct {
	Repair bool
	Err    error
}

func (e *ReviewError) Error() string { return "proposal: the review: " + e.Err.Error() }
func (e *ReviewError) Unwrap() error { return e.Err }

// BindError: Hooks.Bind failed after an answer (the caller's own fault, or
// the attempt stopping); Repair when it was the repair's answer.
type BindError struct {
	Repair bool
	Err    error
}

func (e *BindError) Error() string { return "proposal: the binding stage: " + e.Err.Error() }
func (e *BindError) Unwrap() error { return e.Err }

// Cause is err without this package's wrapper: the client's, a hook's or
// the context builder's own error (or err itself).
func Cause(err error) error {
	switch e := err.(type) {
	case *InputError:
		return e.Err
	case *ContextError:
		return e.Err
	case *CallError:
		return e.Err
	case *ReviewError:
		return e.Err
	case *BindError:
		return e.Err
	}
	return err
}

// flow is one attempt's state, handed from node to node. Each node reads
// what the ones before it set; err ends the attempt.
type flow struct {
	// ctx is the attempt's context, which every step runs under (attempt).
	ctx   context.Context
	req   Request
	hooks Hooks

	ir       model.ModelIR
	pack     domainpack.DomainPack
	pool     idpool.Pool
	pc       provider.ProposalContext
	manifest []model.DatasetBinding

	// The first call: its result (the error's result when it failed), the
	// client's error and the metadata.
	res     provider.ProposalResult
	callErr error
	call    *provider.Metadata

	outcome review.Outcome

	// The repair: its follow-up, record, and the second call's result and
	// error.
	revision provider.ProposalContext
	rec      *repair.Record
	res2     provider.ProposalResult
	err2     error

	err      error
	panicked *Panic
}

func (f *flow) result() Result {
	return Result{Outcome: f.outcome, Call: f.call, Manifest: f.manifest, Sources: f.res.Sources, Repair: f.rec}
}

// Panic is a node's panic (a hook's included) as Run raises it again on the
// caller's goroutine, once Eino's task has recovered it and its frames are
// gone: the value the node panicked with and the goroutine's stack where it
// did (E3 review round 1). The worker records it as that first panic
// (internal/worker/handler.Reraised): only a runtime error's message, the
// stack where it was raised, not retried.
type Panic struct {
	value any
	stack []byte
}

// PanicValue is what the node panicked with.
func (p *Panic) PanicValue() any { return p.value }

// PanicStack is the goroutine's stack where the node panicked.
func (p *Panic) PanicStack() []byte { return p.stack }

// String is what the Go runtime prints of it when nothing recovers it (the
// live eval): the value, as it would have printed the node's own panic, and
// the stack where the node panicked; the runtime's own trace, of Run's
// re-raise, follows.
func (p *Panic) String() string {
	return fmt.Sprint(p.value) + " [a node of the proposal graph panicked]\n\n" + string(p.stack)
}

var errPanicked = errors.New("proposal: a node panicked")

// stack is the calling goroutine's stack, as runtime/debug.Stack (which
// this package may not import: it links os) returns it.
func stack() []byte {
	buf := make([]byte, 4096)
	for {
		if n := runtime.Stack(buf, false); n < len(buf) {
			return buf[:n]
		}
		buf = make([]byte, 2*len(buf))
	}
}

// attempt is a step's context: the node's (its values: the run's callbacks,
// and below them the attempt's) with the attempt's cancellation and
// deadline. A value the node's does not hold is the attempt's: the
// cancellation's own, which keeps context.Cause and the contexts the step
// derives tied to the attempt's directly.
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

// New compiles the graph.
func New(cfg Config) (*Graph, error) {
	if cfg.Model == nil || cfg.Packs == nil {
		return nil, errors.New("proposal: a model and packs are required")
	}
	g := &Graph{cfg: cfg}
	gr := compose.NewGraph[*flow, *flow]()
	for _, n := range []struct {
		key  string
		step func(context.Context, *flow)
	}{
		{NodeDecode, g.decode}, {NodeContext, g.buildContext}, {NodePreload, preload}, {NodeVerify, verify},
		{NodeGenerate, g.generate}, {NodeClassify, classify}, {NodeReview, g.reviewFirst}, {NodeBind, bindFirst},
		{NodeRepairContext, repairContext}, {NodeRepairGenerate, g.generateAgain}, {NodeRepairReview, g.reviewAgain},
	} {
		if err := gr.AddLambdaNode(n.key, node(n.step), compose.WithNodeName(n.key)); err != nil {
			return nil, err
		}
	}
	edges := [][2]string{{compose.START, NodeDecode}, {NodeGenerate, NodeClassify}, {NodeRepairGenerate, NodeRepairReview}, {NodeRepairReview, compose.END}}
	for _, e := range edges {
		if err := gr.AddEdge(e[0], e[1]); err != nil {
			return nil, err
		}
	}
	branches := []struct {
		from string
		to   []string
		pick func(*flow) string
	}{
		{NodeDecode, []string{NodeContext}, onward(NodeContext)},
		{NodeContext, []string{NodePreload}, onward(NodePreload)},
		{NodePreload, []string{NodeVerify}, onward(NodeVerify)},
		{NodeVerify, []string{NodeGenerate}, onward(NodeGenerate)},
		// An answer is reviewed; one refused for what it says goes to the
		// repair; any other failure ends the attempt.
		{NodeClassify, []string{NodeReview, NodeRepairContext}, func(f *flow) string {
			switch {
			case f.err != nil:
				return compose.END
			case f.callErr != nil:
				return NodeRepairContext
			}
			return NodeReview
		}},
		// A ready outcome goes through the binding stage; any other is the
		// repair's to consider.
		{NodeReview, []string{NodeBind, NodeRepairContext}, func(f *flow) string {
			switch {
			case f.err != nil:
				return compose.END
			case f.outcome.Status == review.Ready:
				return NodeBind
			}
			return NodeRepairContext
		}},
		{NodeBind, []string{NodeRepairContext}, onward(NodeRepairContext)},
		// The repair runs only when Plan found something to repair.
		{NodeRepairContext, []string{NodeRepairGenerate}, func(f *flow) string {
			if f.err != nil || f.rec == nil {
				return compose.END
			}
			return NodeRepairGenerate
		}},
	}
	for _, b := range branches {
		ends := map[string]bool{compose.END: true}
		for _, to := range b.to {
			ends[to] = true
		}
		pick := b.pick
		branch := compose.NewGraphBranch(func(_ context.Context, f *flow) (string, error) { return pick(f), nil }, ends)
		if err := gr.AddBranch(b.from, branch); err != nil {
			return nil, err
		}
	}
	r, err := gr.Compile(context.Background(), compose.WithGraphName("proposal"), compose.WithNodeTriggerMode(compose.AllPredecessor))
	if err != nil {
		return nil, err
	}
	g.r = r
	return g, nil
}

// onward is next, or the end once the attempt ended.
func onward(next string) func(*flow) string {
	return func(f *flow) string {
		if f.err != nil {
			return compose.END
		}
		return next
	}
}

// node is a step as a Lambda node, run under the attempt's context: a
// panic in it (a hook's included) is kept with the stack where it was
// raised, to be raised again by Run, never handed to Eino, whose own panic
// error prints the value and a stack.
func node(step func(context.Context, *flow)) *compose.Lambda {
	return compose.InvokableLambda(func(ctx context.Context, f *flow) (out *flow, err error) {
		defer func() {
			if v := recover(); v != nil {
				f.panicked, out, err = &Panic{value: v, stack: stack()}, nil, errPanicked
			}
		}()
		step(attempt{Context: ctx, of: f.ctx}, f)
		return f, nil
	})
}

// Run runs one attempt. The error is what ended it: a hook's own error
// (Preload, Verify), an *InputError, a *ContextError, a *CallError, a
// *ReviewError or a *BindError, or the repair's (repair.Plan's and
// repair.Settle's: the attempt's when ctx ended); when ctx ends, the step
// that sees it ends the attempt with its own error, as before the graph.
// The Result says what was done either way. A node's panic is raised again
// here, as a *Panic.
func (g *Graph) Run(ctx context.Context, req Request, hooks Hooks) (Result, error) {
	return g.run(ctx, req, hooks)
}

func (g *Graph) run(ctx context.Context, req Request, hooks Hooks, opts ...compose.Option) (Result, error) {
	f := &flow{ctx: ctx, req: req, hooks: hooks}
	// Eino ends a run between two nodes once its context ends; the steps see
	// the attempt's themselves (attempt), so the run's own never ends.
	_, err := g.r.Invoke(context.WithoutCancel(ctx), f, opts...)
	if f.panicked != nil {
		panic(f.panicked)
	}
	if err != nil {
		return f.result(), fmt.Errorf("proposal: the graph failed: %w", err)
	}
	return f.result(), f.err
}

// decode reads what the server stored: the base's IR, its domain's pack,
// the ID pool.
func (g *Graph) decode(_ context.Context, f *flow) {
	if err := model.Decode(f.req.Base.IR, &f.ir); err != nil {
		f.err = &InputError{Input: InputBase, Err: err}
		return
	}
	pack, err := g.cfg.Packs.Lookup(f.ir.Domain)
	if err != nil {
		f.err = &InputError{Input: InputDomain, Err: err}
		return
	}
	f.pack = pack
	if err := json.Unmarshal(f.req.Pool, &f.pool); err != nil {
		f.err = &InputError{Input: InputPool, Err: err}
	}
}

// buildContext builds the sealed context, charged to the request's attribution
// and naming the person's model (or the call is refused, H-049).
func (g *Graph) buildContext(_ context.Context, f *flow) {
	r, b := f.req, f.req.Base
	pc, err := aicontext.Proposal(aicontext.ProposalInput{
		Scope:             r.Scope,
		Mode:              r.Mode,
		Base:              aicontext.Version{OrganizationID: b.OrganizationID, ModelID: b.ModelID, VersionID: b.VersionID, IR: &f.ir},
		Domain:            f.pack.PromptContext(),
		Intent:            r.Intent,
		SelectedEntityIDs: r.SelectedEntityIDs,
		Pool:              aicontext.Pool{OrganizationID: r.Scope.OrganizationID, ProposalID: r.ProposalID, IDs: f.pool},
		OutputLocale:      r.OutputLocale,
		Budget:            g.cfg.Budget,
		Research:          r.Research,
	})
	if err != nil {
		f.err = &ContextError{Code: aicontext.CodeOf(err), Err: err}
		return
	}
	pc.Attribution = r.Attribution
	pc.Model = r.Model
	f.pc = pc
}

func preload(ctx context.Context, f *flow) {
	if h := f.hooks.Preload; h != nil {
		f.err = h(ctx)
	}
}

func verify(ctx context.Context, f *flow) {
	h := f.hooks.Verify
	if h == nil {
		return
	}
	manifest, err := h(ctx, &f.ir)
	if err != nil {
		f.err = err
		return
	}
	f.manifest = manifest
}

// generate is the paid call.
func (g *Graph) generate(ctx context.Context, f *flow) {
	res, err := g.ask(ctx, f.pc)
	meta := res.Metadata
	f.res, f.callErr, f.call = res, err, &meta
	if h := f.hooks.Answered; h != nil {
		h(1, meta, err)
	}
}

// ask is one call through the metered ChatModel: the sealed prompt's own
// texts, the context in the user message, and the client's result (the
// error's result, with the answer it handed back, when it failed). The
// model's callbacks run as the model's, not as the node's.
func (g *Graph) ask(ctx context.Context, pc provider.ProposalContext) (provider.ProposalResult, error) {
	ctx = callbacks.ReuseHandlers(ctx, &callbacks.RunInfo{Name: NodeModel, Type: g.cfg.Model.GetType(), Component: components.ComponentOfChatModel})
	msgs := []*schema.Message{
		schema.SystemMessage(pc.Prompt.System),
		{Role: schema.User, Content: pc.Prompt.User(), Extra: map[string]any{einomodel.CallKey: &einomodel.Call{Proposal: &pc}}},
	}
	out, err := g.cfg.Model.Generate(ctx, msgs)
	if err != nil {
		var ce *einomodel.CallError
		if errors.As(err, &ce) {
			return provider.ProposalResult{Metadata: ce.Metadata, Prior: ce.Prior}, ce.Err
		}
		return provider.ProposalResult{}, err
	}
	r, _ := out.Extra[einomodel.ResultKey].(*einomodel.Result)
	if r == nil || r.Proposal == nil {
		return provider.ProposalResult{}, errors.New("proposal: the model's answer carries no proposal result")
	}
	return *r.Proposal, nil
}

// classify: an answer refused for what it says is an invalid outcome, with
// where it is wrong and nothing of what it says (AI_SPEC §4); any other
// failure ends the attempt.
func classify(_ context.Context, f *flow) {
	if f.callErr == nil {
		return
	}
	var pe *provider.Error
	if errors.As(f.callErr, &pe) && pe.Code == provider.CodeInvalidOutput {
		f.outcome = review.InvalidOutput(pe.Issues)
		return
	}
	f.err = &CallError{Err: f.callErr, Final: f.req.Research || provider.SlowModel(f.req.Model)}
}

// reviewed is the server's review of an answer. It never sees the sources
// (out has none): what a page says changes neither the validation nor the
// risk (H-046 (2)).
func (g *Graph) reviewed(f *flow, out provider.ProposalOutput) (review.Outcome, error) {
	return review.Review(review.Input{Mode: f.req.Mode, Base: f.req.Base.IR, Pool: f.pool, Pack: f.pack, Output: out})
}

// bound is a ready outcome after the binding stage (Hooks.Bind; unchanged
// without one).
func bound(ctx context.Context, f *flow, o review.Outcome) (review.Outcome, error) {
	h := f.hooks.Bind
	if h == nil || o.Status != review.Ready {
		return o, nil
	}
	issues, err := h(ctx, o.Candidate)
	if err != nil {
		return review.Outcome{}, err
	}
	return o.Refuse(issues), nil
}

func (g *Graph) reviewFirst(_ context.Context, f *flow) {
	outcome, err := g.reviewed(f, f.res.Output)
	if err != nil {
		f.err = &ReviewError{Err: err}
		return
	}
	f.outcome = outcome
}

func bindFirst(ctx context.Context, f *flow) {
	outcome, err := bound(ctx, f, f.outcome)
	if err != nil {
		f.err = &BindError{Err: err}
		return
	}
	f.outcome = outcome
}

// repairContext is H-057's decision: the follow-up of a refused answer
// (repair.Plan), or nothing to repair.
func repairContext(ctx context.Context, f *flow) {
	if !f.req.Repair {
		return
	}
	rc, rec, err := repair.Plan(ctx, f.pc, f.res.Prior, f.outcome)
	if err != nil {
		var ae *aicontext.Error
		if errors.As(err, &ae) {
			err = &ContextError{Code: ae.Code, Repair: true, Err: err}
		}
		f.err = err
		return
	}
	f.revision, f.rec = rc, rec
}

// generateAgain is the repair's call, metered like the first (its own
// reservation), never researched.
func (g *Graph) generateAgain(ctx context.Context, f *flow) {
	res, err := g.ask(ctx, f.revision)
	meta := res.Metadata
	f.rec.Call = &meta
	if h := f.hooks.Answered; h != nil {
		h(2, meta, err)
	}
	f.res2, f.err2 = res, err
}

// reviewAgain is what came of the repair (repair.Settle): its answer goes
// through exactly the first one's checks.
func (g *Graph) reviewAgain(ctx context.Context, f *flow) {
	check := func(out provider.ProposalOutput) (review.Outcome, error) {
		outcome, err := g.reviewed(f, out)
		if err != nil {
			return review.Outcome{}, &ReviewError{Repair: true, Err: err}
		}
		if outcome, err = bound(ctx, f, outcome); err != nil {
			return review.Outcome{}, &BindError{Repair: true, Err: err}
		}
		return outcome, nil
	}
	outcome, rec, err := repair.Settle(ctx, f.rec, f.outcome, f.res2, f.err2, check)
	f.outcome, f.rec, f.err = outcome, rec, err
}
