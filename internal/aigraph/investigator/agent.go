// Package investigator is the AI investigation's planner agent (H-060,
// H-061 §5.3, approved in H-063; slice A6; behind GEMINUS_INVESTIGATIONS):
// one ADK run of an Eino ChatModelAgent whose model is
// einomodel.Planner, whose tools' bodies are injected, and whose every
// model call sees exactly the transcript its environment builds from the
// investigation's ledger. It is pure: no storage, tenant scope, HTTP, pgx
// or live adapter (architecture rule aigraph-has-no-storage), and it does
// not link the tool registry either (internal/ai/investigator, whose
// Unicode tables that rule refuses): its environment hands it the tools and
// the turns. The worker's job handler (internal/investigator) is the
// environment, and the only code that touches the database (H-061 §5.5).
//
// The assembly (H-061 §5.3):
//   - adk.NewChatModelAgent with Model the planner, a GenModelInput that
//     returns the messages unchanged (Eino's default FString template would
//     break on JSON), MaxIterations the steps left (always set),
//     ToolsConfig with ExecuteSequentially and ReturnDirectly {finish}, and
//     the mode's tools, each an InvokableTool whose body is the
//     environment's Carry;
//   - a handler whose BeforeModelRewriteState sets the state's messages to
//     the turn's transcript (einomodel.PlanMessages of Env.Next's context)
//     and narrows its ToolInfos to the turn's offered tools (the budget's:
//     finish only once 13 steps are used);
//   - adk.NewRunner with streaming off and no checkpoint store; no model
//     retry or failover configuration and no global callbacks (the
//     planner's own callbacks carry no content).
//
// Run maps the run's events to a typed Outcome through errors.As across
// Eino's wrapping; Eino's error text (node paths, stacks) is never logged
// or shown, only the outcome's kind and codes.
package investigator

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/aigraph/einomodel"
)

// FinishTool is the tool that ends an investigation (both modes); the
// agent returns directly after it.
const FinishTool = "finish"

// Observed is what the planner is shown of a carried-out call: the stored
// observation (RFC 8785) and whether it is an error's.
type Observed struct {
	Content string
	IsError bool
}

// Env is the agent's environment: the worker's job handler.
type Env interface {
	// Next is the turn the planner is asked now: its context, built from
	// the ledger mirror (internal/ai/investigator.Transcript.Next), with
	// the tools the budget leaves. An error ends the run (Outcome Ended
	// when it is an *End).
	Next(ctx context.Context) (*provider.PlanContext, error)
	// Carry carries out the call the turn returned (res.Call, checked and
	// decoded by the client) and returns what the planner is shown, once
	// the call is resolved and observed. An error ends the run: an *End
	// when the investigation ends with it (a written draft, a stop, the
	// deadline, three refused steps), anything else Internal.
	Carry(ctx context.Context, res provider.PlanResult) (Observed, error)
}

// End is an environment's end of the run: the investigation ends (or has
// ended) with Reason, an InvestigationReason.
type End struct {
	Reason string
}

func (e *End) Error() string { return "investigator: the investigation ends: " + e.Reason }

// Config is one ADK run.
type Config struct {
	// Planner is the investigator's ChatModel (einomodel.Planner).
	Planner model.ToolCallingChatModel
	// Tools are the investigation mode's tools (the registry's, finish
	// included), registered for execution; each turn offers those its
	// context names.
	Tools []provider.Tool
	// StepsLeft is the steps the investigation may still record: the run's
	// MaxIterations (each iteration one model call), at least 1.
	StepsLeft int
	Env       Env
}

// Kind is how a run ended.
type Kind int

const (
	// Finished: the planner called finish (Outcome.Finish is the turn).
	Finished Kind = iota + 1
	// Ended: the environment ended the run (Outcome.End).
	Ended
	// Invalid: the planner's answer was invalid (Outcome.Invalid, H-061
	// §7); the next run starts from the digest.
	Invalid
	// Failed: the planner turn failed (Outcome.Err, a *provider.Error).
	Failed
	// LimitReached: the run used its iterations (adk.ErrExceedMaxIterations).
	LimitReached
	// Cancelled: ctx ended.
	Cancelled
	// Internal: anything else (Outcome.Err; never shown, never logged as
	// text by the caller beyond its type).
	Internal
)

// Outcome is a run's end.
type Outcome struct {
	Kind Kind
	// Finish is the turn that called finish (Finished).
	Finish *provider.PlanResult
	// End is the environment's end (Ended).
	End *End
	// Invalid is the invalid answer (Invalid).
	Invalid *provider.InvalidTurn
	// Err is the client's error (Failed: a *provider.Error; Invalid: the
	// invalid_output, output_truncated or refused error) or the internal
	// error (Internal).
	Err error
	// Metadata is the failed or invalid turn's call metadata.
	Metadata provider.Metadata
	// Turns is the planner turns the run asked the client for.
	Turns int
}

// run is one ADK run's own state, shared by the planner's wrapper, the
// handler and the tool bodies (which Eino runs in its goroutines, one after
// another).
type run struct {
	cfg Config
	mu  sync.Mutex
	// last is the latest turn's result, which the tool the turn called
	// carries out; finish is the turn that called finish.
	last   *provider.PlanResult
	finish *provider.PlanResult
	turns  int
}

// Run runs one ADK run of the agent until the planner finishes, the
// environment ends it, a turn fails or is invalid, the iterations are used,
// or ctx ends. It never panics on the planner's or the environment's
// behalf: a panic in either ends the run as Internal.
func Run(ctx context.Context, cfg Config) (o Outcome) {
	defer func() {
		if v := recover(); v != nil {
			o = Outcome{Kind: Internal, Err: fmt.Errorf("investigator: the agent panicked with a %T", v)}
		}
	}()
	if cfg.Planner == nil || cfg.Env == nil || cfg.StepsLeft < 1 || len(cfg.Tools) == 0 ||
		!slices.ContainsFunc(cfg.Tools, func(t provider.Tool) bool { return t.Name == FinishTool }) {
		return Outcome{Kind: Internal, Err: errors.New("investigator: a planner, an environment, the mode's tools with finish and a step left are required")}
	}
	r := &run{cfg: cfg}
	tools := make([]tool.BaseTool, 0, len(cfg.Tools))
	for _, t := range cfg.Tools {
		tools = append(tools, &carried{r: r, info: &schema.ToolInfo{Name: t.Name, Desc: t.Description}})
	}
	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:          "investigator",
		Description:   "The AI investigation's planner.",
		Model:         &recorded{inner: cfg.Planner, r: r},
		GenModelInput: unchanged,
		MaxIterations: cfg.StepsLeft,
		ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: tools, ExecuteSequentially: true},
			ReturnDirectly: map[string]bool{FinishTool: true}},
		Handlers: []adk.ChatModelAgentMiddleware{&transcript{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}, r: r}},
	})
	if err != nil {
		return Outcome{Kind: Internal, Err: err}
	}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent, EnableStreaming: false})
	// The run's input is replaced before the first model call (the
	// handler); the planner's tripwire refuses anything that is not the
	// turn's transcript.
	iter := runner.Run(ctx, []*schema.Message{schema.UserMessage("investigation")})
	var runErr error
	for {
		ev, ok := iter.Next()
		if !ok {
			break
		}
		if ev != nil && ev.Err != nil && runErr == nil {
			runErr = ev.Err
		}
	}
	r.mu.Lock()
	finish, turns := r.finish, r.turns
	r.mu.Unlock()
	o = outcomeOf(ctx, runErr, finish)
	o.Turns = turns
	return o
}

// outcomeOf maps a run's error (through Eino's wrapping) to its outcome.
func outcomeOf(ctx context.Context, err error, finish *provider.PlanResult) Outcome {
	var end *End
	var pe *einomodel.PlanError
	var e *provider.Error
	switch {
	case err == nil && finish != nil:
		return Outcome{Kind: Finished, Finish: finish}
	case err == nil:
		// The planner answered without a call: the client refuses that, so
		// it is not the planner's.
		return Outcome{Kind: Internal, Err: errors.New("investigator: the run ended without finish")}
	case errors.As(err, &end):
		return Outcome{Kind: Ended, End: end}
	case errors.As(err, &pe) && pe.Invalid != nil:
		return Outcome{Kind: Invalid, Invalid: pe.Invalid, Err: pe.Err, Metadata: pe.Metadata}
	case errors.As(err, &pe) && errors.As(err, &e):
		if e.Code == provider.CodeCancelled && ctx.Err() != nil {
			return Outcome{Kind: Cancelled, Err: e, Metadata: pe.Metadata}
		}
		return Outcome{Kind: Failed, Err: e, Metadata: pe.Metadata}
	case errors.Is(err, adk.ErrExceedMaxIterations):
		return Outcome{Kind: LimitReached}
	case ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		return Outcome{Kind: Cancelled, Err: provider.NewError(provider.CodeCancelled, 0, "")}
	}
	return Outcome{Kind: Internal, Err: fmt.Errorf("investigator: the run failed with a %T", err)}
}

// unchanged is the agent's GenModelInput: the messages as given, never
// templated (the handler replaces them before every model call).
func unchanged(_ context.Context, _ string, input *adk.AgentInput) ([]adk.Message, error) {
	return slices.Clone(input.Messages), nil
}

// transcript is the run's handler: before every model call, the turn's
// transcript and its offered tools.
type transcript struct {
	*adk.BaseChatModelAgentMiddleware
	r *run
}

func (h *transcript) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, _ *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	pc, err := h.r.cfg.Env.Next(ctx)
	if err != nil {
		return ctx, state, err
	}
	if pc == nil {
		return ctx, state, errors.New("investigator: no turn")
	}
	state.Messages = einomodel.PlanMessages(pc)
	state.ToolInfos = einomodel.PlanToolInfos(pc)
	state.DeferredToolInfos = nil
	return ctx, state, nil
}

// recorded is the planner as the agent calls it: it keeps each turn's
// result for the tool the turn called, and counts the turns.
type recorded struct {
	inner model.ToolCallingChatModel
	r     *run
}

func (m *recorded) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	inner, err := m.inner.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &recorded{inner: inner, r: m.r}, nil
}

func (m *recorded) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	m.r.mu.Lock()
	m.r.last = nil
	m.r.mu.Unlock()
	out, err := m.inner.Generate(ctx, input, opts...)
	asked := err == nil || reachedTheProvider(err)
	m.r.mu.Lock()
	defer m.r.mu.Unlock()
	if asked {
		m.r.turns++
	}
	if err != nil {
		return nil, err
	}
	res, ok := out.Extra[einomodel.PlanResultKey].(*provider.PlanResult)
	if !ok || res == nil || len(out.ToolCalls) != 1 {
		return nil, errors.New("investigator: the planner answered without its turn")
	}
	m.r.last = res
	return out, nil
}

func (m *recorded) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	out, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{out}), nil
}

// reachedTheProvider reports whether a failed turn was sent: an invalid
// answer, or a failure only the provider's answer (or its absence) gives. A
// turn refused before any I/O (the tripwire, the client's checks, the
// organization's slot, the spend ledger) is not a planner turn.
func reachedTheProvider(err error) bool {
	var pe *einomodel.PlanError
	if errors.As(err, &pe) && pe.Invalid != nil {
		return true
	}
	switch provider.CodeOf(err) {
	case provider.CodeTimeout, provider.CodeUnavailable, provider.CodeRateLimited, provider.CodeRejected, provider.CodeRefused,
		provider.CodeTruncated, provider.CodeInvalidOutput, provider.CodeCredentialsExhausted:
		return true
	}
	return false
}

// carried is one of the mode's tools: its body is the environment's Carry
// of the turn that called it.
type carried struct {
	r    *run
	info *schema.ToolInfo
}

func (t *carried) Info(context.Context) (*schema.ToolInfo, error) { return t.info, nil }

func (t *carried) InvokableRun(ctx context.Context, arguments string, _ ...tool.Option) (string, error) {
	t.r.mu.Lock()
	res := t.r.last
	t.r.last = nil
	t.r.mu.Unlock()
	// The call Eino runs must be the turn's own, as the client checked and
	// decoded it.
	if res == nil || res.Call.Name != t.info.Name || string(res.Call.Input) != arguments {
		return "", errors.New("investigator: a tool call that is not the turn's")
	}
	out, err := t.r.cfg.Env.Carry(ctx, *res)
	if err != nil {
		return "", err
	}
	if t.info.Name == FinishTool {
		t.r.mu.Lock()
		t.r.finish = res
		t.r.mu.Unlock()
	}
	return out.Content, nil
}
