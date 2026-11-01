package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/kcl1104/geminus/internal/model"
)

// Planner turns (H-060, H-061 §9 and §5.4, approved in H-063; slice A5;
// behind GEMINUS_INVESTIGATIONS): an AI investigation's planner picks its
// next step as one native tool call. Client.PlanTurn is the investigator
// role's only path to a model: a policy over the same checked, metered call
// as every tool turn (call, tools.go), with these rules of its own:
//
//   - the prompt's role is investigator, the call is attributed to an
//     investigation (target "investigation", an inv_ ID), it names its
//     planner model (muse-spark-1.3 or, after its evaluation, muse-wonder-1.0)
//     and the backend's approval takes tool calls and that model for the
//     role (AcceptsTools, AcceptsModel): refused before any I/O otherwise;
//   - one attempt per reservation (MaxRetries 0): the agent retries a failed
//     turn at most twice, each its own reservation (H-061 §12);
//   - max_tokens PlannerMaxOutputTokens (the default budget's 8,192: no
//     thinking, effort or output-budget change, H-055) and at most
//     PlannerTurnTimeout per turn, a slow model (Muse Wonder 1.0) included,
//     instead of its proposal bounds;
//   - the request asks for exactly one call (Request.OneCall: Meta's
//     tool_choice any with disable_parallel_tool_use, H-061's probe P2);
//   - the answer is exactly one tool_use whose name is a tool the turn
//     offered and whose input the registry's strict decoder takes
//     (ToolDecoder: internal/ai/investigator, the decoder the API's executor
//     runs again); anything else is an invalid turn (PlanResult.Invalid),
//     which the worker records as a refused invalid_call step or not at all
//     (H-061 §7), never as a call;
//   - the narration before the call is the step's note, sanitized
//     (SanitizeNote: at most MaxNoteRunes); it is never put into a digest;
//   - the model's assistant content as returned (its reasoning blocks, the
//     narration and the tool_use) is kept opaque in memory only
//     (TurnReplay), for the next turn of the same ADK run to replay as
//     returned; never stored, logged, printed or checkpointed.

// Bounds of a planner turn (H-061 §9, §12, §14).
const (
	// PlannerMaxOutputTokens is a planner turn's max_tokens, for every
	// planner model: the default budget's (DefaultBudget().MaxOutputTokens),
	// whatever the deployment's AI_MAX_OUTPUT_TOKENS says. A planner turn is
	// one short tool call (Muse Wonder's live probe: 183 output tokens).
	PlannerMaxOutputTokens int64 = 8192
	// PlannerTurnTimeout bounds one planner turn, its one attempt included:
	// Muse Wonder 1.0's planner turns get it instead of SlowModelTimeout
	// (H-061 §9, the pass bar's "no turn over 120 s").
	PlannerTurnTimeout = 120 * time.Second
	// MaxNoteRunes bounds a planner turn's note (the investigation step's
	// note: investigation.schema.json, 500 characters).
	MaxNoteRunes = 500
	// PlannerTarget is the attribution target of a planner turn: the spend
	// ledger and its audit name the investigation (H-061 §10).
	PlannerTarget = "investigation"
)

var investigationID = regexp.MustCompile(`^inv_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)

// ToolDecoder is the strict decoder of a planner turn's call: the A2
// registry (internal/ai/investigator.Decoder), which reads the arguments
// against the tool's full schema (every member, nothing else, NFC text) and
// returns them canonical (RFC 8785), or why it refuses them. A member the
// offered input schema leaves optional (one that may be null, H-061 A9) is
// added as null when the model left it out, so the canonical arguments
// always hold every member.
type ToolDecoder interface {
	DecodeCall(name string, input []byte) (canonical []byte, refused *ToolRefusal)
}

// ToolRefusal is a decoder's refusal: an InvestigationToolCode and a JSON
// Pointer into the arguments that names only the tool's own members.
type ToolRefusal struct {
	Code string
	Path string
}

// The decoder's codes a planner turn tells apart (investigator.schema.json
// InvestigationToolCode).
const (
	refusalUnknownTool  = "UNKNOWN_TOOL"
	refusalModeMismatch = "MODE_MISMATCH"
	refusalInvalidArgs  = "INVALID_ARGUMENTS"
)

// InvalidKind is what was wrong with a planner turn's answer (H-061 §7).
type InvalidKind string

const (
	// InvalidNoCall: text with no tool_use, a refusal, or an answer the
	// backend could not take: an invalid turn the ledger does not record.
	InvalidNoCall InvalidKind = "no_call"
	// InvalidText: the narration is not valid UTF-8 or is larger than the
	// output budget (garbled text, H-051): not recorded either.
	InvalidText InvalidKind = "invalid_text"
	// InvalidTruncated: the answer reached max_tokens; not recorded.
	InvalidTruncated InvalidKind = "truncated"
	// InvalidSeveralCalls: more than one tool_use; recorded as a refused
	// invalid_call step (INVALID_ARGUMENTS at "").
	InvalidSeveralCalls InvalidKind = "several_calls"
	// InvalidUnknownTool: a tool the turn did not offer (or another mode's):
	// recorded as a refused invalid_call step (UNKNOWN_TOOL).
	InvalidUnknownTool InvalidKind = "unknown_tool"
	// InvalidArguments: arguments the strict decoder refuses, or a call ID
	// not of a token's shape: recorded as a refused invalid_call step
	// (INVALID_ARGUMENTS at Path). The arguments are never stored.
	InvalidArguments InvalidKind = "invalid_arguments"
)

// InvalidTurn is a planner turn's invalid answer: its kind, and for a
// recorded one the code and path of its invalid_call step. It holds nothing
// the model wrote.
type InvalidTurn struct {
	Kind InvalidKind
	// Code is UNKNOWN_TOOL or INVALID_ARGUMENTS for a kind the ledger
	// records (Recorded), "" otherwise.
	Code string
	// Path is a JSON Pointer into the arguments (the tool's own members
	// only); "" for the call as a whole.
	Path string
}

// Recorded reports whether the worker records the invalid turn as a
// refused invalid_call step (H-061 §7); an unrecorded one only counts
// towards the two-invalid-turns rule.
func (i InvalidTurn) Recorded() bool { return i.Code != "" }

// replayWithheld is what a TurnReplay prints as.
const replayWithheld = "[assistant turn withheld]"

// TurnReplay is a planner turn's assistant content as its backend took it
// (Response.Replay: its reasoning blocks, narration and tool_use, as
// returned), held in memory for the next turn of the same ADK run (H-061
// §5.4). Only PlanTurn makes one, bound to the call it returned with it; it
// prints, formats and encodes as withheld, whatever the verb, and its bytes
// leave it only through Bytes, for a backend to send back.
type TurnReplay struct {
	content []byte
	call    string // the digest of the call it carries (callDigest)
}

// Present reports whether there is content to replay.
func (r TurnReplay) Present() bool { return len(r.content) > 0 }

// Len is the content's size in bytes.
func (r TurnReplay) Len() int { return len(r.content) }

// Bytes is a copy of the content, for a backend to send as the model's own
// turn. Never log or store it.
func (r TurnReplay) Bytes() []byte { return bytes.Clone(r.content) }

func (r TurnReplay) digest() string {
	if !r.Present() {
		return ""
	}
	sum := sha256.Sum256(r.content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// String, GoString and Format print the content as withheld.
func (TurnReplay) String() string   { return replayWithheld }
func (TurnReplay) GoString() string { return replayWithheld }

func (TurnReplay) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(replayWithheld)) }

// MarshalJSON encodes the content as null.
func (TurnReplay) MarshalJSON() ([]byte, error) { return []byte("null"), nil }

// callDigest binds a replay to its call: the call's ID, name and input,
// canonical.
func callDigest(c ToolCall) (string, error) {
	in, err := canonicalContent(c.Input)
	if err != nil {
		return "", err
	}
	doc, err := json.Marshal(struct {
		ID    string         `json:"id"`
		Name  string         `json:"name"`
		Input jsontext.Value `json:"input"`
	}{c.ID, c.Name, in})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(doc)
	return hex.EncodeToString(sum[:]), nil
}

// PlanContext is one planner turn's request: a sealed investigator prompt
// (internal/ai/aicontext.Investigation: the brief and the digest of the
// ledger), the call's attribution (the investigation), the planner model,
// the tools the turn offers (the registry's, narrowed by the budget), the
// turns of the same ADK run to replay (each one call and the server's
// result; empty in a digest transcript), and the registry's strict decoder.
type PlanContext struct {
	OrganizationID string
	Prompt         Prompt
	Attribution    Attribution
	Model          string
	Tools          []Tool
	Steps          []ToolStep
	Decoder        ToolDecoder
}

// PlanResult is a planner turn's outcome: the one call, checked and decoded
// (Input is the decoder's canonical arguments), its sanitized note, the
// replay of the answer, and the call's metadata; or, with an invalid_output,
// output_truncated or refused error, Invalid says what was wrong.
type PlanResult struct {
	Call     ToolCall
	Note     string
	Replay   TurnReplay
	Invalid  *InvalidTurn
	Metadata Metadata
}

// planPolicy is a tool turn's planner policy (PlanTurn).
type planPolicy struct {
	decoder ToolDecoder
}

// planned is a planner turn's checked answer.
type planned struct {
	call    ToolCall
	note    string
	replay  TurnReplay
	invalid *InvalidTurn
}

// PlanTurn asks the planner for one call (H-061 §9). It is checked, metered
// and bounded as every call is (call), under the planner policy above.
func (c *Client) PlanTurn(ctx context.Context, in PlanContext) (PlanResult, error) {
	if err := ctx.Err(); err != nil {
		return PlanResult{}, NewError(CodeCancelled, 0, "").WithCause(err)
	}
	a := in.Attribution
	if in.Prompt.Role != RoleInvestigator || in.Model == "" || in.Decoder == nil || a.TargetType != PlannerTarget ||
		!investigationID.MatchString(a.TargetID) || a.RequestID == "" {
		return PlanResult{}, invalidRequest()
	}
	for _, s := range in.Steps {
		// A replayed turn is one call and its result; its narration is in
		// its replay, never sent apart.
		if len(s.Calls) != 1 || len(s.Results) != 1 || s.Text != "" {
			return PlanResult{}, invalidRequest()
		}
	}
	tt, err := checkTurn(TurnContext{OrganizationID: in.OrganizationID, Prompt: in.Prompt, Attribution: in.Attribution,
		Model: in.Model, Tools: in.Tools, Steps: in.Steps}, c.budget, true)
	if err != nil {
		return PlanResult{}, err
	}
	tt.plan = &planPolicy{decoder: in.Decoder}
	r, err := c.call(ctx, in.OrganizationID, RoleInvestigator, in.Prompt, in.Attribution, nil, in.Model, nil, tt)
	out := PlanResult{Metadata: r.meta}
	if r.plan != nil {
		out.Invalid = r.plan.invalid
		if err == nil {
			out.Call, out.Note, out.Replay = r.plan.call, r.plan.note, r.plan.replay
		}
	}
	return out, err
}

// planTimeouts are a planner turn's attempt timeout and call timeout: at
// most PlannerTurnTimeout, a slow model's too (H-061 §9).
func planTimeouts(b Budget, chosen string) (attempt, call time.Duration) {
	if SlowModel(chosen) {
		return PlannerTurnTimeout, PlannerTurnTimeout
	}
	call = min(b.Timeout, PlannerTurnTimeout)
	return min(b.AttemptTimeout, call), call
}

// planInvalid is the error and the outcome of an invalid planner turn.
func planInvalid(meta Metadata, credential string, kind InvalidKind, code, path string, issues ...model.ValidationIssue) (called, error) {
	e := NewError(CodeInvalidOutput, 0, credential)
	e.Issues = issues
	return called{meta: meta, plan: &planned{invalid: &InvalidTurn{Kind: kind, Code: code, Path: path}}}, e
}

// planAnswer checks a planner turn's answer (H-061 §7): exactly one call,
// of a tool the turn offered, whose ID is of a token's shape and whose
// arguments the registry's strict decoder takes (and the offered input
// schema too); the narration within the output budget and valid UTF-8. The
// call comes back with the decoder's canonical arguments, the narration
// sanitized as the note, and the content to replay bound to the call.
func (tt *toolTurn) planAnswer(resp Response, budget Budget, meta Metadata) (called, error) {
	issue := func(path, msg string) model.ValidationIssue {
		return model.NewIssue(OutputInvalid, path, msg, model.SeverityError)
	}
	cred := resp.Credential
	switch {
	case len(resp.ToolCalls) == 0:
		return planInvalid(meta, cred, InvalidNoCall, "", "", issue("", "the answer makes no tool call"))
	case len(resp.ToolCalls) > 1:
		return planInvalid(meta, cred, InvalidSeveralCalls, refusalInvalidArgs, "", issue("/tool_calls", "the answer makes more than one tool call"))
	case len(resp.Text) > budget.MaxOutputBytes || !utf8.Valid(resp.Text):
		return planInvalid(meta, cred, InvalidText, "", "", issue("", "the answer's text is larger than the output budget or not UTF-8"))
	}
	c := resp.ToolCalls[0]
	if _, offered := tt.schemas[c.Name]; !offered {
		return planInvalid(meta, cred, InvalidUnknownTool, refusalUnknownTool, "", issue("/tool_calls/0/name", "names no tool the turn offered"))
	}
	if !toolCallID.MatchString(c.ID) || tt.ids[c.ID] {
		return planInvalid(meta, cred, InvalidArguments, refusalInvalidArgs, "", issue("/tool_calls/0/id", "is not a call ID of a token's shape, or repeats one"))
	}
	canonical, refused := tt.plan.decoder.DecodeCall(c.Name, c.Input)
	if refused != nil {
		switch refused.Code {
		case refusalUnknownTool, refusalModeMismatch:
			return planInvalid(meta, cred, InvalidUnknownTool, refusalUnknownTool, "", issue("/tool_calls/0/name", "names no tool the investigation offers"))
		case refusalInvalidArgs:
			return planInvalid(meta, cred, InvalidArguments, refusalInvalidArgs, refused.Path,
				issue("/tool_calls/0/input"+refused.Path, "the strict decoder refuses the arguments"))
		}
		// INTERNAL: the server's decoder failed, not the model's answer.
		return called{meta: meta}, invalidRequest()
	}
	// The offered input schema (the closed projection the model was sent)
	// takes what the full schema takes; checked all the same.
	checked, issues := tt.checkCall(ToolCall{ID: c.ID, Name: c.Name, Input: canonical}, 0, nil)
	if len(issues) > 0 {
		return planInvalid(meta, cred, InvalidArguments, refusalInvalidArgs, "", issues...)
	}
	call := ToolCall{ID: checked.ID, Name: checked.Name, Input: jsontext.Value(bytes.Clone(canonical))}
	p := &planned{call: call, note: SanitizeNote(string(resp.Text))}
	if len(resp.Replay) > 0 {
		d, err := callDigest(call)
		if err != nil {
			return called{meta: meta}, invalidRequest()
		}
		p.replay = TurnReplay{content: bytes.Clone(resp.Replay), call: d}
	}
	if h, err := turnOutputHash(string(resp.Text), []ToolCall{call}); err == nil {
		meta.OutputHash = h
	}
	return called{meta: meta, plan: p}, nil
}

// SanitizeNote is a planner turn's narration as the step's note (H-061
// §5.4): valid UTF-8 (an invalid byte becomes U+FFFD, which the eval counts
// as garbled), every control, format and separator character a space, runs
// of white space one space, trimmed, at most MaxNoteRunes characters; ""
// when nothing is left. It is AI text: shown labelled (through visible()
// and <bdi>), never fed back to the model. (Not normalized to NFC: the
// provider package links no Unicode tables; the note has no NFC rule.)
func SanitizeNote(text string) string {
	text = strings.ToValidUTF8(text, "\uFFFD")
	var b strings.Builder
	space := false
	n := 0
	for _, r := range text {
		if unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
			space = b.Len() > 0
			continue
		}
		if n >= MaxNoteRunes {
			break
		}
		if space {
			if n+1 >= MaxNoteRunes {
				break
			}
			b.WriteByte(' ')
			n++
			space = false
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}
