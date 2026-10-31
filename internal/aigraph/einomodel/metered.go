// Package einomodel is the app's one Eino ChatModel (H-058, H-059, H-060;
// ADR-031; slice E1 of docs/design/H-056_INVESTIGATIONS_ON_EINO.md §6.3):
// Metered, a model.ToolCallingChatModel over provider.LLMProvider, the
// reviewed metered *provider.Client (one instance over the text client, one
// over the vision client). Eino reaches a model only through it, and it
// reaches one only through the client, so every call keeps the client's
// checks unchanged: the sealed prompt (Verify), the attribution, image,
// research and tool gating, the approved model (H-049), the prompt budget
// and the organization's slot, the spend reservation before the call and its
// settlement after, the model the answer names (Standard mode), the output
// bound, the strict decoding of the answer (its output schema, or for a tool
// turn the tools' input schemas) and no-store. No node can change what is
// sent: the messages must be the sealed prompt's own texts (the tripwire),
// no option may set the model, its sampling, max_tokens or a tool choice,
// and the model is chosen only in the sealed call (H-049); no effort or
// thinking option exists (H-055).
//
// A call is one of three, carried by the user message (Extra[CallKey], a
// *Call):
//
//   - a proposal (Call.Proposal, a provider.ProposalContext from
//     internal/ai/aicontext): Generate asks GenerateModelProposal;
//   - an explanation (Call.Analysis): Generate asks AnalyzeSimulation;
//   - a tool turn (Call.Turn, H-060): the model may call the tools bound with
//     WithTools or passed with model.WithTools (as Eino's ADK passes them);
//     Generate asks the client's Turn (provider.Client.Turn) with those tools
//     and the transcript the messages after the user message hold (each
//     assistant message's text and tool calls, then one tool message per
//     call, in order), and answers an assistant message of the model's text
//     and its checked tool calls.
//
// A proposal or an explanation offers no tool: tools, a tool choice, or a
// message after the user message refuse it (invalid_request), so every
// existing role's request is the client's as before. A tool turn needs
// tools. Errors are *CallError, which unwraps to the client's *provider.Error
// (errors.As works through Eino's graph errors) and keeps the call's
// metadata, so the job handlers' log line (logCall) and audit metadata
// (auditMeta) read the same fields for every call, a tool turn's included.
//
// The adapter fires its own callbacks (IsCallbacksEnabled) with no message
// content: OnStart with no messages, OnEnd with the model, the token counts
// and the latency, OnError with the error (which carries no prompt or answer
// text). Eino's retry and failover configurations are never set (the client
// retries under its own reservation), and no ChatTemplate is used (prompts
// come from aicontext and Seal only).
package einomodel

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"slices"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/kcl1104/geminus/internal/ai/provider"
)

// Keys of a message's Extra the adapter reads and writes.
const (
	// CallKey: the user message's *Call, what the call is.
	CallKey = "geminus.call"
	// ResultKey: the answer message's *Result, the client's typed result.
	ResultKey = "geminus.result"
	// ToolErrorKey: a tool message's bool, true when the server refused or
	// failed the call (sent as the tool result's is_error).
	ToolErrorKey = "geminus.tool_error"
)

// Call is what a call asks: exactly one of a proposal, an explanation or a
// tool turn, each sealed by internal/ai/aicontext and carrying its
// attribution and, for a proposal or a turn, the chosen model (H-049). A
// turn's Tools and Steps are empty: they come from the bound tools and the
// messages only.
type Call struct {
	Proposal *provider.ProposalContext
	Analysis *provider.AnalysisContext
	Turn     *provider.TurnContext
}

// Result is the client's typed result of a call, in the answer message's
// Extra[ResultKey]: the one of Call's kind.
type Result struct {
	Proposal *provider.ProposalResult
	Analysis *provider.AnalysisResult
	Turn     *provider.TurnResult
}

// CallError is a failed call: the client's error (a *provider.Error for
// every failure the client or the adapter classifies) and the call's
// metadata (zero when the adapter refused the call before the client).
type CallError struct {
	Metadata provider.Metadata
	Err      error
}

func (e *CallError) Error() string { return e.Err.Error() }

// Unwrap exposes the client's error.
func (e *CallError) Unwrap() error { return e.Err }

// Turner is the client's tool turn (provider.Client.Turn, H-060).
type Turner interface {
	Turn(context.Context, provider.TurnContext) (provider.TurnResult, error)
}

// Metered is the ChatModel over the metered client. It is immutable:
// WithTools returns a new one.
type Metered struct {
	p     provider.LLMProvider
	tools []*schema.ToolInfo
}

var (
	_ model.ToolCallingChatModel = (*Metered)(nil)
	_ components.Typer           = (*Metered)(nil)
	_ components.Checker         = (*Metered)(nil)
)

// New returns the ChatModel over p, the metered client (a tool turn needs a
// p that is also a Turner, as *provider.Client is).
func New(p provider.LLMProvider) (*Metered, error) {
	if p == nil {
		return nil, errors.New("einomodel: no provider client")
	}
	return &Metered{p: p}, nil
}

// refused is the adapter's own refusal of a call: invalid_request, before
// the client is asked.
func refused() *CallError {
	return &CallError{Err: provider.NewError(provider.CodeInvalidRequest, 0, "")}
}

// WithTools returns the model with tools bound (H-060), for tool turns. Each
// tool must convert to a provider.Tool the client takes (provider.CheckTools:
// a closed object schema, a valid name); one that does not is refused here,
// before any call.
func (m *Metered) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	if len(tools) == 0 {
		return &Metered{p: m.p}, nil
	}
	pt, err := toolsOf(tools)
	if err != nil {
		return nil, err
	}
	if err := provider.CheckTools(pt); err != nil {
		return nil, &CallError{Err: err}
	}
	return &Metered{p: m.p, tools: slices.Clone(tools)}, nil
}

// GetType names the component in Eino's callbacks.
func (m *Metered) GetType() string { return "GeminusMetered" }

// IsCallbacksEnabled: the adapter fires its own callbacks, without content.
func (m *Metered) IsCallbacksEnabled() bool { return true }

// Generate asks the client for one call (see the package comment).
func (m *Metered) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
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
func (m *Metered) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	out, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{out}), nil
}

func (m *Metered) generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, provider.Metadata, error) {
	none := provider.Metadata{}
	o := model.GetCommonOptions(&model.Options{Tools: m.tools}, opts...)
	if o.Temperature != nil || o.Model != nil || o.TopP != nil || o.MaxTokens != nil || len(o.Stop) > 0 || len(o.AllowedToolNames) > 0 ||
		len(o.DeferredTools) > 0 || o.ToolSearchTool != nil || o.AgenticToolChoice != nil ||
		(o.ToolChoice != nil && *o.ToolChoice != schema.ToolChoiceAllowed) {
		// What is sent is the sealed call's and the budget's, never an
		// option's (H-049, H-055); a tool choice other than the default (auto)
		// is H-061's probe P2, not taken.
		return nil, none, refused()
	}
	if len(input) < 2 || input[0] == nil || input[1] == nil {
		return nil, none, refused()
	}
	call, ok := input[1].Extra[CallKey].(*Call)
	if !ok || call == nil || kinds(call) != 1 || len(input[1].Extra) != 1 {
		return nil, none, refused()
	}
	var p provider.Prompt
	switch {
	case call.Proposal != nil:
		p = call.Proposal.Prompt
	case call.Analysis != nil:
		p = call.Analysis.Prompt
	default:
		p = call.Turn.Prompt
	}
	// The tripwire: the messages are the sealed prompt's own texts.
	if !plain(input[0], schema.System, p.System) || len(input[0].Extra) != 0 || !plain(input[1], schema.User, p.User()) {
		return nil, none, refused()
	}
	if call.Turn == nil && (len(o.Tools) > 0 || o.ToolChoice != nil || len(input) != 2) {
		// A proposal or an explanation offers no tool (H-060).
		return nil, none, refused()
	}
	switch {
	case call.Proposal != nil:
		res, err := m.p.GenerateModelProposal(ctx, *call.Proposal)
		if err != nil {
			return nil, res.Metadata, &CallError{Metadata: res.Metadata, Err: err}
		}
		return answer(string(res.Raw), nil, res.Metadata, &Result{Proposal: &res}), res.Metadata, nil
	case call.Analysis != nil:
		res, err := m.p.AnalyzeSimulation(ctx, *call.Analysis)
		if err != nil {
			return nil, res.Metadata, &CallError{Metadata: res.Metadata, Err: err}
		}
		return answer(string(res.Raw), nil, res.Metadata, &Result{Analysis: &res}), res.Metadata, nil
	}
	if len(o.Tools) == 0 || len(call.Turn.Tools) > 0 || len(call.Turn.Steps) > 0 {
		return nil, none, refused()
	}
	t, ok := m.p.(Turner)
	if !ok {
		return nil, none, &CallError{Err: provider.NewError(provider.CodeToolsUnsupported, 0, "")}
	}
	tools, err := toolsOf(o.Tools)
	if err != nil {
		return nil, none, err
	}
	steps, ok := stepsOf(input[2:])
	if !ok {
		return nil, none, refused()
	}
	tc := *call.Turn
	tc.Tools, tc.Steps = tools, steps
	res, err := t.Turn(ctx, tc)
	if err != nil {
		return nil, res.Metadata, &CallError{Metadata: res.Metadata, Err: err}
	}
	calls := make([]schema.ToolCall, 0, len(res.Calls))
	for i, c := range res.Calls {
		calls = append(calls, schema.ToolCall{Index: &i, ID: c.ID, Type: "function", Function: schema.FunctionCall{Name: c.Name, Arguments: string(c.Input)}})
	}
	return answer(res.Text, calls, res.Metadata, &Result{Turn: &res}), res.Metadata, nil
}

// kinds is how many kinds of call c holds.
func kinds(c *Call) int {
	n := 0
	for _, set := range []bool{c.Proposal != nil, c.Analysis != nil, c.Turn != nil} {
		if set {
			n++
		}
	}
	return n
}

// plain reports whether msg is a plain text message of role whose text is
// exactly text: no parts, tool calls, reasoning or name.
func plain(msg *schema.Message, role schema.RoleType, text string) bool {
	return msg != nil && msg.Role == role && msg.Content == text && len(msg.MultiContent) == 0 && len(msg.UserInputMultiContent) == 0 &&
		len(msg.AssistantGenMultiContent) == 0 && len(msg.ToolCalls) == 0 && msg.ToolCallID == "" && msg.ToolName == "" &&
		msg.ReasoningContent == "" && msg.Name == "" && msg.ResponseMeta == nil
}

// stepsOf reads a turn's transcript from the messages after the user
// message: each an assistant message with its text and at least one tool
// call, then one tool message per call answering it, in order. Anything
// else is not a transcript (the client checks the calls and results).
func stepsOf(msgs []*schema.Message) ([]provider.ToolStep, bool) {
	var steps []provider.ToolStep
	for i := 0; i < len(msgs); {
		a := msgs[i]
		if a == nil || a.Role != schema.Assistant || len(a.ToolCalls) == 0 || len(a.MultiContent) > 0 || len(a.UserInputMultiContent) > 0 ||
			len(a.AssistantGenMultiContent) > 0 || a.ToolCallID != "" || a.ToolName != "" || a.ReasoningContent != "" || a.Name != "" {
			return nil, false
		}
		step := provider.ToolStep{Text: a.Content}
		for _, c := range a.ToolCalls {
			if c.Type != "" && c.Type != "function" {
				return nil, false
			}
			step.Calls = append(step.Calls, provider.ToolCall{ID: c.ID, Name: c.Function.Name, Input: jsontext.Value(c.Function.Arguments)})
		}
		i++
		for _, c := range a.ToolCalls {
			if i >= len(msgs) {
				return nil, false
			}
			r := msgs[i]
			if r == nil || r.Role != schema.Tool || r.ToolCallID != c.ID || (r.ToolName != "" && r.ToolName != c.Function.Name) ||
				len(r.ToolCalls) > 0 || len(r.MultiContent) > 0 || len(r.UserInputMultiContent) > 0 || len(r.AssistantGenMultiContent) > 0 {
				return nil, false
			}
			isError, _ := r.Extra[ToolErrorKey].(bool)
			step.Results = append(step.Results, provider.ToolResult{CallID: c.ID, Content: jsontext.Value(r.Content), IsError: isError})
			i++
		}
		steps = append(steps, step)
	}
	return steps, true
}

// toolsOf converts Eino tool infos to the client's tools: the name, the
// description and the parameters as a JSON Schema (none: an object with no
// member). The client checks them (closed objects, names, bounds).
func toolsOf(infos []*schema.ToolInfo) ([]provider.Tool, error) {
	out := make([]provider.Tool, 0, len(infos))
	for _, ti := range infos {
		if ti == nil || len(ti.Extra) > 0 {
			return nil, refused()
		}
		raw := jsontext.Value(`{"type":"object","properties":{},"additionalProperties":false}`)
		if ti.ParamsOneOf != nil {
			js, err := ti.ParamsOneOf.ToJSONSchema()
			if err != nil || js == nil {
				return nil, refused()
			}
			if raw, err = json.Marshal(js); err != nil {
				return nil, refused()
			}
		}
		out = append(out, provider.Tool{Name: ti.Name, Description: ti.Desc, InputSchema: raw})
	}
	return out, nil
}

// answer is the assistant message of an answer: its text (a role's
// schema-valid answer, or a turn's text), a turn's calls, the usage and the
// typed result.
func answer(text string, calls []schema.ToolCall, md provider.Metadata, r *Result) *schema.Message {
	finish := "stop"
	if len(calls) > 0 {
		finish = "tool_calls"
	}
	return &schema.Message{Role: schema.Assistant, Content: text, ToolCalls: calls,
		ResponseMeta: &schema.ResponseMeta{FinishReason: finish, Usage: messageUsage(md)}, Extra: map[string]any{ResultKey: r}}
}

func messageUsage(md provider.Metadata) *schema.TokenUsage {
	u := md.Usage
	prompt := int(u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens)
	out := &schema.TokenUsage{PromptTokens: prompt, CompletionTokens: int(u.OutputTokens), TotalTokens: prompt + int(u.OutputTokens),
		PromptTokenDetails: schema.PromptTokenDetails{CachedTokens: int(u.CacheReadInputTokens), CacheWriteTokens: int(u.CacheCreationInputTokens)}}
	if u.ThinkingTokens != nil {
		out.CompletionTokensDetails.ReasoningTokens = int(*u.ThinkingTokens)
	}
	return out
}

func callbackUsage(md provider.Metadata) *model.TokenUsage {
	u := messageUsage(md)
	return &model.TokenUsage{PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens, TotalTokens: u.TotalTokens,
		PromptTokenDetails:      model.PromptTokenDetails{CachedTokens: u.PromptTokenDetails.CachedTokens, CacheWriteTokens: u.PromptTokenDetails.CacheWriteTokens},
		CompletionTokensDetails: model.CompletionTokensDetails{ReasoningTokens: u.CompletionTokensDetails.ReasoningTokens}}
}
