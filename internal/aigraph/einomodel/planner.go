package einomodel

import (
	"bytes"
	"context"
	"errors"
	"slices"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/kcl1104/geminus/internal/ai/provider"
)

// Planner is the investigator's ChatModel (H-061 §5.3, slice A6): a
// model.ToolCallingChatModel over provider.Client.PlanTurn, the investigator
// role's only path to a model (A5: one attempt per reservation, one call,
// the registry's strict decoder, the sanitized note, the opaque replay). It
// is the ADK ChatModelAgent's model in internal/aigraph/investigator; E1's
// Metered stays the model of every other role, and its tool turns go
// through Client.Turn, which refuses the investigator (so a planner turn
// cannot reach a model any other way).
//
// Each call is one planner turn, carried by the user message
// (Extra[PlanKey], a *provider.PlanContext the transcript built from the
// ledger mirror: internal/ai/investigator.Transcript.Next). Its tripwire
// refuses (invalid_request, before the client) any message list that is not
// exactly PlanMessages of that context, any tools other than the turn's
// offered tools (read from model.GetCommonOptions, as Eino's ADK passes
// them, or bound with WithTools) and any option that would set the model,
// its sampling, max_tokens or a tool choice: what is sent is the client's,
// never a node's. Its answer is an assistant message with the one checked
// call (Input: the decoder's canonical arguments) and the client's typed
// result (Extra[PlanResultKey], a *provider.PlanResult: the note and the
// replay live only there, never in the message's text). Its errors are
// *PlanError, which unwraps to the client's *provider.Error (errors.As
// works through Eino's agent and graph errors) and carries an invalid
// turn's kind (provider.InvalidTurn) and the call's metadata.
type Planner struct {
	p     PlanTurner
	tools []*schema.ToolInfo
}

// Keys of a planner turn's messages' Extra.
const (
	// PlanKey: the user message's *provider.PlanContext.
	PlanKey = "geminus.plan"
	// PlanResultKey: the answer message's *provider.PlanResult.
	PlanResultKey = "geminus.plan_result"
)

// PlanTurner is the client's planner turn (provider.Client.PlanTurn).
type PlanTurner interface {
	PlanTurn(context.Context, provider.PlanContext) (provider.PlanResult, error)
}

// PlanError is a failed planner turn: the client's error (a
// *provider.Error for every failure the client or the tripwire
// classifies), an invalid answer's kind (nil otherwise) and the call's
// metadata (zero when the tripwire refused the call before the client).
type PlanError struct {
	Metadata provider.Metadata
	Invalid  *provider.InvalidTurn
	Err      error
}

func (e *PlanError) Error() string { return e.Err.Error() }

// Unwrap exposes the client's error.
func (e *PlanError) Unwrap() error { return e.Err }

var (
	_ model.ToolCallingChatModel = (*Planner)(nil)
	_ components.Typer           = (*Planner)(nil)
	_ components.Checker         = (*Planner)(nil)
)

// NewPlanner returns the investigator's ChatModel over p, the
// investigator's provider.Client.
func NewPlanner(p PlanTurner) (*Planner, error) {
	if p == nil {
		return nil, errors.New("einomodel: no planner client")
	}
	return &Planner{p: p}, nil
}

// WithTools returns the planner with tools bound. Which tools a turn may
// offer is the turn's (PlanContext.Tools); the tripwire checks them.
func (m *Planner) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return &Planner{p: m.p, tools: slices.Clone(tools)}, nil
}

// GetType names the component in Eino's callbacks.
func (m *Planner) GetType() string { return "GeminusPlanner" }

// IsCallbacksEnabled: the planner fires its own callbacks, without content.
func (m *Planner) IsCallbacksEnabled() bool { return true }

// PlanMessages is a planner turn's transcript as the agent sets it before
// the call (BeforeModelRewriteState) and the tripwire takes it: the sealed
// prompt's system text, its user text carrying the context
// (Extra[PlanKey]), then for each step the turn replays (TranscriptReplay)
// an assistant message with the step's one call and no text (the
// narration is in the step's replay, which only the client sends) and a
// tool message with its result (Extra[ToolErrorKey] when it is an error).
// In a digest transcript there are no steps: two messages.
func PlanMessages(pc *provider.PlanContext) []*schema.Message {
	out := []*schema.Message{{Role: schema.System, Content: pc.Prompt.System},
		{Role: schema.User, Content: pc.Prompt.User(), Extra: map[string]any{PlanKey: pc}}}
	for _, s := range pc.Steps {
		if len(s.Calls) != 1 || len(s.Results) != 1 {
			continue // the tripwire refuses the context (PlanTurn would too)
		}
		c, r := s.Calls[0], s.Results[0]
		out = append(out, &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: c.ID, Type: "function",
			Function: schema.FunctionCall{Name: c.Name, Arguments: string(c.Input)}}}})
		tm := &schema.Message{Role: schema.Tool, ToolCallID: r.CallID, ToolName: c.Name, Content: string(r.Content)}
		if r.IsError {
			tm.Extra = map[string]any{ToolErrorKey: true}
		}
		out = append(out, tm)
	}
	return out
}

// PlanToolInfos are the turn's offered tools as Eino's agent narrows its
// state to them (BeforeModelRewriteState): each tool's name and
// description. Their input schemas are the turn's own (PlanContext.Tools,
// the registry's projections), which the client sends; the agent's tools
// carry no parameters of their own.
func PlanToolInfos(pc *provider.PlanContext) []*schema.ToolInfo {
	out := make([]*schema.ToolInfo, 0, len(pc.Tools))
	for _, t := range pc.Tools {
		out = append(out, &schema.ToolInfo{Name: t.Name, Desc: t.Description})
	}
	return out
}

// Generate asks the client for one planner turn (see Planner).
func (m *Planner) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	ctx = callbacks.EnsureRunInfo(ctx, m.GetType(), components.ComponentOfChatModel)
	ctx = callbacks.OnStart(ctx, &model.CallbackInput{})
	out, md, err := m.generate(ctx, input, opts...)
	if err != nil {
		callbacks.OnError(ctx, err)
		return nil, err
	}
	callbacks.OnEnd(ctx, &model.CallbackOutput{Config: &model.Config{Model: md.Model}, TokenUsage: callbackUsage(md),
		Extra: map[string]any{"latency_ms": md.LatencyMillis}})
	return out, nil
}

// Stream is Generate's answer as one chunk.
func (m *Planner) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	out, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{out}), nil
}

// tripped is the tripwire's refusal: invalid_request, before the client.
func tripped() *PlanError {
	return &PlanError{Err: provider.NewError(provider.CodeInvalidRequest, 0, "")}
}

func (m *Planner) generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, provider.Metadata, error) {
	none := provider.Metadata{}
	o := model.GetCommonOptions(&model.Options{Tools: m.tools}, opts...)
	if o.Temperature != nil || o.Model != nil || o.TopP != nil || o.MaxTokens != nil || len(o.Stop) > 0 || len(o.AllowedToolNames) > 0 ||
		len(o.DeferredTools) > 0 || o.ToolSearchTool != nil || o.AgenticToolChoice != nil ||
		(o.ToolChoice != nil && *o.ToolChoice != schema.ToolChoiceAllowed) {
		// What is sent is the client's (one call: tool_choice any, H-061 §9),
		// never an option's (H-049, H-055).
		return nil, none, tripped()
	}
	if len(input) < 2 || input[1] == nil {
		return nil, none, tripped()
	}
	pc, ok := input[1].Extra[PlanKey].(*provider.PlanContext)
	if !ok || pc == nil {
		return nil, none, tripped()
	}
	// The tripwire: the messages are exactly the transcript of the turn's
	// context, and the tools exactly the turn's offered tools.
	want := PlanMessages(pc)
	if len(want) != len(input) || len(want) != 2+2*len(pc.Steps) {
		return nil, none, tripped()
	}
	for i := range want {
		if !sameMessage(input[i], want[i], i == 1) {
			return nil, none, tripped()
		}
	}
	if len(o.Tools) != len(pc.Tools) || len(pc.Tools) == 0 {
		return nil, none, tripped()
	}
	for i, ti := range o.Tools {
		if ti == nil || ti.Name != pc.Tools[i].Name || ti.Desc != pc.Tools[i].Description || ti.ParamsOneOf != nil || len(ti.Extra) > 0 {
			return nil, none, tripped()
		}
	}
	res, err := m.p.PlanTurn(ctx, *pc)
	if err != nil {
		return nil, res.Metadata, &PlanError{Metadata: res.Metadata, Invalid: res.Invalid, Err: err}
	}
	zero := 0
	call := schema.ToolCall{Index: &zero, ID: res.Call.ID, Type: "function", Function: schema.FunctionCall{Name: res.Call.Name,
		Arguments: string(res.Call.Input)}}
	out := &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{call},
		ResponseMeta: &schema.ResponseMeta{FinishReason: "tool_calls", Usage: messageUsage(res.Metadata)},
		Extra:        map[string]any{PlanResultKey: &res}}
	return out, res.Metadata, nil
}

// sameMessage reports whether got is exactly want: the role, the text, the
// one call or the tool result, and no other content. The user message
// carries the context itself (the same pointer).
func sameMessage(got, want *schema.Message, user bool) bool {
	if got == nil || got.Role != want.Role || got.Content != want.Content || got.ToolCallID != want.ToolCallID ||
		got.ToolName != want.ToolName || len(got.MultiContent) > 0 || len(got.UserInputMultiContent) > 0 ||
		len(got.AssistantGenMultiContent) > 0 || got.ReasoningContent != "" || got.Name != "" || len(got.ToolCalls) != len(want.ToolCalls) {
		return false
	}
	if user {
		if len(got.Extra) != 1 || got.Extra[PlanKey] != want.Extra[PlanKey] {
			return false
		}
	} else {
		isErr, _ := got.Extra[ToolErrorKey].(bool)
		wantErr, _ := want.Extra[ToolErrorKey].(bool)
		if isErr != wantErr || len(got.Extra) != len(want.Extra) {
			return false
		}
	}
	for i, c := range got.ToolCalls {
		w := want.ToolCalls[i]
		if c.ID != w.ID || (c.Type != "" && c.Type != "function") || c.Function.Name != w.Function.Name ||
			!bytes.Equal([]byte(c.Function.Arguments), []byte(w.Function.Arguments)) {
			return false
		}
	}
	return true
}
