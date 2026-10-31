// Package photo is the photo proposal's role graph (H-059; slice E5 of
// docs/design/H-056_INVESTIGATIONS_ON_EINO.md §6.4): the order of a photo
// proposal's steps as an Eino graph, and its two calls to the vision model
// through the metered Eino ChatModel (internal/aigraph/einomodel), so they are
// the metered client's calls unchanged. internal/ai/photopipeline is the
// photo proposal: its steps (Steps) are its own reviewed code, and Propose
// runs them here; the worker's photo proposals and the live photo smoke call
// Propose as before.
//
//	gate → segment → check_segments → build_context → generate → classify
//	classify → review → repair_context        (a schema-valid answer)
//	classify → repair_context                 (refused by the client for what it says)
//	repair_context → generate_2 → settle      (H-057: exactly one repair, no cycle)
//
// Every node but the two generate nodes is one of the steps, and every step
// may decide the proposal (its outcome or its error), which ends the graph
// there; a generate node hands its call's result to the step after it as
// data, so the steps map a provider error exactly as before (a ChatModel node
// would end the graph on any error).
//
// The graph is compiled once (DAG mode: AllPredecessor) and holds no state;
// each Run has its own. No callback handler, checkpoint store or retry is
// set. The graph package depends only on Eino, the adapter and the provider
// types; the steps come in as an interface. So no AI package the steps use
// (the context builder, the review, the repair) is beneath it, and an AI
// package that links the graph, as an architecture probe of E0 has the
// context builder do, is refused by the rules rather than by an import
// cycle.
//
// The caller's context: Eino ends a run between two nodes once its context
// is done, before the next step could meet the cancellation itself. The
// pipeline's steps meet it where they always did (a client's call answers
// cancelled, and is logged and settled as before; the repair is not asked
// for once the attempt is stopping), so the graph runs on the caller's
// context without its cancellation, and each node hands its step the node's
// context (Eino's values over the caller's) with the caller's deadline and
// cancellation. A step that panics (the vision client included) panics Run
// with the same value: Eino would make it an error whose text holds the
// stack, which the worker would then retry; its handler treats a panic as
// before, a bug never retried.
package photo

import (
	"context"
	"errors"
	"sync"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/aigraph/einomodel"
)

// Steps is one photo proposal's node bodies, in the graph's order. A step
// that returns false has decided the proposal (its outcome or its error,
// which the steps keep): the graph ends there. Each is given its node's
// context with the caller's cancellation.
type Steps interface {
	// Gate: what is refused before anything is spent.
	Gate(ctx context.Context) bool
	// Segment: the segmenter's call.
	Segment(ctx context.Context) bool
	// Segments: the server's checks of the segmentation.
	Segments(ctx context.Context) bool
	// Context: the vision model's first call.
	Context(ctx context.Context) (*provider.ProposalContext, bool)
	// Answered: the first call's result, as the client returned it; where
	// the proposal goes next.
	Answered(ctx context.Context, res provider.ProposalResult, err error) After
	// Review: the server's checks of the first answer.
	Review(ctx context.Context) bool
	// Repair: the repair's call (H-057), when the outcome is one to repair.
	Repair(ctx context.Context) (*provider.ProposalContext, bool)
	// Repaired: the repair's result, as the client returned it, and the
	// server's checks of its answer: the proposal is decided.
	Repaired(ctx context.Context, res provider.ProposalResult, err error)
}

// After is where the first answer sends the proposal.
type After int

const (
	// ToEnd: decided (the call's error).
	ToEnd After = iota
	// ToReview: a schema-valid answer, for the server's checks.
	ToReview
	// ToRepair: an answer the client refused for what it says (its
	// outcome is set): straight to the repair, no review.
	ToRepair
)

// The graph's nodes.
const (
	nodeGate     = "gate"
	nodeSegment  = "segment"
	nodeSegments = "check_segments"
	nodeContext  = "build_context"
	nodeGenerate = "generate"
	nodeClassify = "classify"
	nodeReview   = "review"
	nodeRepair   = "repair_context"
	nodeAgain    = "generate_2"
	nodeSettle   = "settle"
)

// ErrGraph: the graph could not run the proposal (it did not compile, or
// Eino refused the run): a fault of the server's own, never the provider's,
// and with no text of Eino's (which can hold a stack).
var ErrGraph = errors.New("photo graph: the photo proposal's graph did not run")

// flow is one run's state, handed from node to node.
type flow struct {
	steps  Steps
	caller context.Context
	vision provider.LLMProvider
	// call is the next generate node's call, res and err its result.
	call *provider.ProposalContext
	res  provider.ProposalResult
	err  error
	// next is the node a branch sends the run to.
	next string
	// panicked is a step's panic, which Run panics with.
	panicked *panicValue
}

type panicValue struct{ v any }

// within is a node's context for its step: the node's values (Eino's, over
// the caller's) with the caller's deadline and cancellation.
type within struct {
	context.Context                 // the caller's: Deadline, Done, Err
	node            context.Context // the node's: Value
}

// Value is the node's value of key, else the caller's. The node's context
// sees every value of the caller's but its cancellation (it is beneath
// context.WithoutCancel), which only the caller's answers: so a context a
// step derives (as a client derives each attempt's) is cancelled with the
// caller's at once, and context.Cause is the caller's, as when the pipeline
// handed the caller's context on.
func (w within) Value(key any) any {
	if v := w.node.Value(key); v != nil {
		return v
	}
	return w.Context.Value(key)
}

// node is a node of the graph: body runs on the node's context for its
// step and names the node the run goes to next (END when the step decided
// the proposal). A panic in body is kept for Run, and the run ends.
func node(body func(ctx context.Context, f *flow) string) *compose.Lambda {
	return compose.InvokableLambda(func(ctx context.Context, f *flow) (out *flow, err error) {
		f.next = compose.END
		if f.panicked != nil {
			return f, nil
		}
		defer func() {
			if v := recover(); v != nil {
				f.panicked, f.next = &panicValue{v: v}, compose.END
				out, err = f, nil
			}
		}()
		f.next = body(within{Context: f.caller, node: ctx}, f)
		return f, nil
	})
}

// then is next when more, else END.
func then(more bool, next string) string {
	if more {
		return next
	}
	return compose.END
}

// generate is a call's node: the call through the metered ChatModel, its
// result kept as data for the step after it.
func generate(next string) *compose.Lambda {
	return node(func(ctx context.Context, f *flow) string {
		f.res, f.err = call(ctx, f.vision, f.call)
		return next
	})
}

// call asks the vision client for pc through the metered ChatModel: the
// sealed prompt's own texts, the call in the user message. Its result is
// the client's (its error the client's own, with the answer it handed back
// and the call's metadata).
func call(ctx context.Context, vision provider.LLMProvider, pc *provider.ProposalContext) (provider.ProposalResult, error) {
	m, err := einomodel.New(vision)
	if err != nil {
		return provider.ProposalResult{}, provider.NewError(provider.CodeNotConfigured, 0, "")
	}
	out, err := m.Generate(ctx, []*schema.Message{
		{Role: schema.System, Content: pc.Prompt.System},
		{Role: schema.User, Content: pc.Prompt.User(), Extra: map[string]any{einomodel.CallKey: &einomodel.Call{Proposal: pc}}},
	})
	if err != nil {
		var ce *einomodel.CallError
		if errors.As(err, &ce) {
			return provider.ProposalResult{Metadata: ce.Metadata, Prior: ce.Prior}, ce.Err
		}
		return provider.ProposalResult{}, err
	}
	r, ok := out.Extra[einomodel.ResultKey].(*einomodel.Result)
	if !ok || r == nil || r.Proposal == nil {
		return provider.ProposalResult{}, ErrGraph
	}
	return *r.Proposal, nil
}

func build() (compose.Runnable[*flow, *flow], error) {
	g := compose.NewGraph[*flow, *flow]()
	// Every node but the last is followed by a branch to the node its body
	// named or to END, so a decided proposal or a panic ends the run there.
	nodes := []struct {
		key  string
		node *compose.Lambda
		// to: the nodes the branch may send the run to besides END; none
		// for the last node, whose one edge is to END.
		to []string
	}{
		{nodeGate, node(func(ctx context.Context, f *flow) string { return then(f.steps.Gate(ctx), nodeSegment) }), []string{nodeSegment}},
		{nodeSegment, node(func(ctx context.Context, f *flow) string { return then(f.steps.Segment(ctx), nodeSegments) }), []string{nodeSegments}},
		{nodeSegments, node(func(ctx context.Context, f *flow) string { return then(f.steps.Segments(ctx), nodeContext) }), []string{nodeContext}},
		{nodeContext, node(func(ctx context.Context, f *flow) string {
			pc, more := f.steps.Context(ctx)
			f.call = pc
			return then(more && pc != nil, nodeGenerate)
		}), []string{nodeGenerate}},
		{nodeGenerate, generate(nodeClassify), []string{nodeClassify}},
		{nodeClassify, node(func(ctx context.Context, f *flow) string {
			switch f.steps.Answered(ctx, f.res, f.err) {
			case ToReview:
				return nodeReview
			case ToRepair:
				return nodeRepair
			}
			return compose.END
		}), []string{nodeReview, nodeRepair}},
		{nodeReview, node(func(ctx context.Context, f *flow) string { return then(f.steps.Review(ctx), nodeRepair) }), []string{nodeRepair}},
		{nodeRepair, node(func(ctx context.Context, f *flow) string {
			pc, more := f.steps.Repair(ctx)
			f.call, f.res, f.err = pc, provider.ProposalResult{}, nil
			return then(more && pc != nil, nodeAgain)
		}), []string{nodeAgain}},
		{nodeAgain, generate(nodeSettle), []string{nodeSettle}},
		{nodeSettle, node(func(ctx context.Context, f *flow) string {
			f.steps.Repaired(ctx, f.res, f.err)
			return compose.END
		}), nil},
	}
	for _, n := range nodes {
		if err := g.AddLambdaNode(n.key, n.node); err != nil {
			return nil, err
		}
	}
	edges := [][2]string{{compose.START, nodeGate}, {nodeSettle, compose.END}}
	for _, e := range edges {
		if err := g.AddEdge(e[0], e[1]); err != nil {
			return nil, err
		}
	}
	for _, n := range nodes {
		if n.to == nil {
			continue
		}
		ends := map[string]bool{compose.END: true}
		for _, e := range n.to {
			ends[e] = true
		}
		branch := compose.NewGraphBranch(func(_ context.Context, f *flow) (string, error) { return f.next, nil }, ends)
		if err := g.AddBranch(n.key, branch); err != nil {
			return nil, err
		}
	}
	return g.Compile(context.Background(), compose.WithNodeTriggerMode(compose.AllPredecessor), compose.WithGraphName("photo"))
}

var compiled = sync.OnceValues(build)

// Run runs a photo proposal's steps on the graph, the vision model's calls
// through vision (the metered client). The steps keep the proposal's
// outcome or error; Run's own error is ErrGraph only. A step's panic is
// Run's (see the package comment).
func Run(ctx context.Context, vision provider.LLMProvider, steps Steps) error {
	if steps == nil {
		return ErrGraph
	}
	g, err := compiled()
	if err != nil {
		return ErrGraph
	}
	f := &flow{steps: steps, caller: ctx, vision: vision}
	_, err = g.Invoke(context.WithoutCancel(ctx), f)
	if f.panicked != nil {
		panic(f.panicked.v)
	}
	if err != nil {
		return ErrGraph
	}
	return nil
}
