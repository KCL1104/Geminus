package meta

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"strings"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/model"
)

// Tool turns (H-060; slice E1 of docs/design/H-056_INVESTIGATIONS_ON_EINO.md,
// under H-061): the Messages API's custom tools
// (https://dev.meta.ai/docs/protocols/messages, "Tools", and
// /docs/api-reference/messages/schemas, retrieved 2026-10-31 through a
// summarizer for the H-056 design: custom tools with input_schema, tool_use
// and tool_result blocks, tool_choice auto, any or none; "arguments are not
// guaranteed to validate"). The coordinator's live probe of 2026-10-31 (H-060)
// saw Muse Spark 1.3 and Muse Wonder 1.0 answer one turn with a tool_use for
// a run_simulation tool; nothing else of this shape has been sent live (the
// multi-turn replay below is NOT RUN live: H-061's probe P1).
//
// A turn is a request that offers tools (provider.Client.Turn): POST
// /v1/messages with model, max_tokens, system as one text block, the user
// message as one text block, then for each step of the transcript an
// assistant message (the model's text, if any, as one text block, then its
// tool_use blocks {id, name, input} as the model made them, the input
// canonical) and a user message of its tool_result blocks {tool_use_id,
// content: the server's result as JSON text, is_error when true}, and tools,
// each {name, description, input_schema}. Nothing else: no output_config
// (the answer is calls or text, checked by the client against the tools), no
// tool_choice (the API's default, auto: the model decides; H-061's any is
// its probe P2), no thinking, effort or sampling (H-055), no built-in tool,
// no stream. The model's reasoning blocks are dropped unread and never sent
// back (H-061's probe P1 is whether the API takes a replay without them).
//
// It is sent only for a role the approval's data policy names tool calls for
// (AcceptsTools); this build records no such approval, so a live backend
// refuses every turn before any I/O (policy_not_approved) until H-061's
// investigator role is approved (its slice A5). A request with images, web
// research or a repair is never a turn. Every tool's input schema is checked
// against the same documented limits as an output schema (SchemaViolations)
// and one outside them is never sent. The answer must be a complete message
// from the model asked for: stop_reason tool_use with at least one tool_use
// block, or end_turn with none; text and reasoning blocks besides; any other
// block (a built-in tool's, a result, one this build does not know) fails it
// (invalid_output), its usage settled all the same.

// AcceptsTools: the approval's data policy names tool calls for role, a role
// the backend serves (H-060). No approval this build records names any.
func (b *Backend) AcceptsTools(role provider.Role) bool {
	return b.tools != nil && b.allows != nil && b.allows(string(role)) && b.tools(string(role))
}

// turnRequest is a tool turn's Messages request.
type turnRequest struct {
	Model     string         `json:"model"`
	MaxTokens int64          `json:"max_tokens"`
	System    []textBlock    `json:"system"`
	Messages  []userMessage  `json:"messages"`
	Tools     []functionTool `json:"tools"`
}

// functionTool is a custom tool of the request.
type functionTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitzero"`
	InputSchema jsontext.Value `json:"input_schema"`
}

type toolUseBlock struct {
	Type  string         `json:"type"`
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input jsontext.Value `json:"input"`
}

type toolResultBlock struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error,omitzero"`
}

// errToolSchema: a tool's input schema is outside the documented limits.
var errToolSchema = errors.New("meta: a tool's input schema is outside the documented limits")

// turnBody is the body of the tool turn req, asking modelID.
func turnBody(modelID string, req provider.Request) ([]byte, error) {
	m := turnRequest{Model: modelID, MaxTokens: req.MaxOutputTokens, System: []textBlock{{Type: "text", Text: req.Prompt.System}},
		Messages: []userMessage{{Role: "user", Content: []any{textBlock{Type: "text", Text: req.Prompt.User()}}}}}
	for _, t := range req.Tools {
		var schema any
		if err := json.Unmarshal(t.InputSchema, &schema); err != nil {
			return nil, err
		}
		if len(SchemaViolations(schema)) > 0 {
			return nil, errToolSchema
		}
		m.Tools = append(m.Tools, functionTool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
	}
	for _, s := range req.Steps {
		var calls []any
		if s.Text != "" {
			calls = append(calls, textBlock{Type: "text", Text: s.Text})
		}
		for _, c := range s.Calls {
			calls = append(calls, toolUseBlock{Type: "tool_use", ID: c.ID, Name: c.Name, Input: c.Input})
		}
		var results []any
		for _, r := range s.Results {
			results = append(results, toolResultBlock{Type: "tool_result", ToolUseID: r.CallID, Content: string(r.Content), IsError: r.IsError})
		}
		m.Messages = append(m.Messages, userMessage{Role: "assistant", Content: calls}, userMessage{Role: "user", Content: results})
	}
	return json.Marshal(m, json.Deterministic(true))
}

// quoteTurn is a tool turn's quote: its body's bytes (every token covers at
// least one byte) and quoteOverheadTokens, at most the context window.
// Nothing is asked of the provider.
func (b *Backend) quoteTurn(asked string, req provider.Request) (provider.Quote, error) {
	body, err := turnBody(asked, req)
	if err != nil {
		return provider.Quote{}, provider.NewError(provider.CodeInvalidRequest, 0, "")
	}
	return provider.Quote{Model: asked, MaxInputTokens: min(int64(len(body))+quoteOverheadTokens, contextWindowTokens)}, nil
}

// Turn sends a tool turn as one Messages call (H-060). It refuses, before any
// I/O, a request without a spend reservation or without tools, one with
// images, research or a repair, a model the approval does not list for the
// role, and a role whose tool calls the approval does not name.
func (b *Backend) Turn(ctx context.Context, req provider.Request) (provider.Response, error) {
	if req.Reservation == "" || len(req.Tools) == 0 || len(req.Prompt.Images) > 0 || req.Prompt.Research || req.Prompt.Revision != nil {
		return provider.Response{}, provider.NewError(provider.CodeInvalidRequest, 0, "")
	}
	asked, ok := b.modelOf(req)
	if !ok {
		return provider.Response{}, modelNotApproved()
	}
	if !b.AcceptsTools(req.Prompt.Role) {
		return provider.Response{}, provider.NewError(provider.CodePolicyNotApproved, 0, "")
	}
	body, err := turnBody(asked, req)
	if err != nil {
		return provider.Response{}, provider.NewError(provider.CodeInvalidRequest, 0, "")
	}
	resp := provider.Response{Model: asked, Credential: b.c.label}
	t := &tally{}
	ex, failed, err := b.c.post(ctx, t, "v1/messages", body, req.AttemptTimeout, req.MaxRetries)
	resp.FailedOver = failed
	if err != nil {
		resp.UnknownAttempts, resp.Undecoded = t.unknown, t.undecoded
		return resp, err
	}
	msg, ok := decodeMessage(ex.body)
	if !ok {
		// An answer came back but cannot be read: it may have been billed,
		// for a usage nobody knows (T-053 review round 1, I1).
		t.unknown++
		t.undecoded = true
		resp.UnknownAttempts, resp.Undecoded = t.unknown, t.undecoded
		return resp, provider.NewError(provider.CodeUnavailable, 0, b.c.label)
	}
	out, err := b.turnAnswer(msg, resp)
	out.UnknownAttempts, out.Undecoded = t.unknown, t.undecoded
	return out, err
}

// turnAnswer takes a tool turn's answer: its usage (billed whatever becomes
// of it), its text and its tool calls as the model made them (the client
// checks them); reasoning blocks are dropped unread.
func (b *Backend) turnAnswer(m message, resp provider.Response) (provider.Response, error) {
	resp.Usage = provider.Usage{InputTokens: m.Usage.InputTokens, OutputTokens: m.Usage.OutputTokens,
		CacheReadInputTokens: m.Usage.CacheReadInputTokens, CacheCreationInputTokens: m.Usage.CacheCreationInputTokens,
		ThinkingTokens: m.Usage.OutputTokensDetails.ThinkingTokens}
	resp.ProviderRequestID = safeID(m.ID)
	invalid := func(msg string) (provider.Response, error) {
		e := provider.NewError(provider.CodeInvalidOutput, 0, b.c.label)
		if msg != "" {
			e.Issues = []model.ValidationIssue{model.NewIssue(provider.OutputInvalid, "", msg, model.SeverityError)}
		}
		return resp, e
	}
	if m.Model != resp.Model {
		// Standard mode is the model ID, as for every call.
		return invalid("the answer is not from the Standard-mode model the request named")
	}
	var text strings.Builder
	var calls []provider.ToolCall
	for _, blk := range m.Content {
		switch blk.Type {
		case "text":
			text.WriteString(blk.Text)
		case "thinking", "redacted_thinking":
		case "tool_use":
			var id string
			if json.Unmarshal(blk.ID, &id) != nil {
				return invalid("")
			}
			calls = append(calls, provider.ToolCall{ID: id, Name: blk.Name, Input: blk.Input})
		default: // a built-in tool (none was offered), a tool result, or a block this build does not know
			return invalid("")
		}
	}
	switch m.StopReason {
	case "tool_use":
		if len(calls) == 0 {
			return invalid("")
		}
	case "end_turn":
		if len(calls) > 0 {
			return invalid("")
		}
	case "max_tokens":
		return resp, provider.NewError(provider.CodeTruncated, 0, b.c.label)
	case "refusal":
		return resp, provider.NewError(provider.CodeRefused, 0, b.c.label)
	default: // pause_turn, stop_sequence (none was sent) or a reason this build does not know
		return invalid("")
	}
	resp.Text, resp.ToolCalls = []byte(text.String()), calls
	return resp, nil
}
