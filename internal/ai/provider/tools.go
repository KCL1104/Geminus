package provider

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// Tool calls (H-060, under H-058, H-059 and H-061; slice E1 of
// docs/design/H-056_INVESTIGATIONS_ON_EINO.md §6.3): a request may offer the
// model tools the server defined, and the model may answer with calls of
// them. Only a request that declares tools (Client.Turn) is sent any, and
// only to a backend that sends tools (ToolTurner) and accepts them for the
// prompt's role under its approval; no other backend ever sees it. Every
// other request (an edit, a scenario, research, a photo, a repair, an
// explanation, a mapping) declares none: it is sent as before, through
// Backend.Complete, and an answer of it that holds a tool call is refused as
// before.
//
// The model runs nothing: a tool call is untrusted data the client checks
// and hands back. It names a tool the request offered, has an ID of a
// token's shape that no other call of the turn has, and its arguments are
// one strict JSON object (no duplicate member, valid UTF-8, a number JSON can
// hold) of at most MaxToolArgumentBytes, valid under the tool's input
// schema, whose every object refuses members it does not list and whose
// every array says what its elements are (closedSchema), so no unknown member
// gets through. What acts on a call, and whether it may, is the caller's and
// the server's (H-061: the API carries out an investigator's calls under the
// person's scope, role and audit). A tool turn
// goes through the same checks as every call (call): the sealed prompt, the
// attribution, the approved model, the prompt budget (the tools and the
// transcript count towards it), the organization's slot, the spend
// reservation before it is sent and its settlement after, the timeout, the
// model the answer names, and no-store: the request is stateless, and the
// answer's reasoning is dropped unread. A turn has no output schema (no
// output_config is sent): its answer is calls, text, or both.
//
// The transcript (Steps) is what came after the user message: each step the
// model's text and calls as an earlier turn answered them, then the server's
// result of each call, in the calls' order. The client checks it as it
// checks an answer, so a caller cannot send a call the model could not have
// made. Its hash, with the tools', is the turn's InputHash (Metadata).

// Bounds of a tool turn (H-060). The investigator's seven tools, one call a
// turn, at most 14 steps and observations of at most 6 KiB (H-061 §7) fit
// well within them.
const (
	// MaxTools bounds the tools one request offers.
	MaxTools = 16
	// MaxToolDescriptionBytes bounds a tool's description (server text).
	MaxToolDescriptionBytes = 4 << 10
	// MaxToolSchemaBytes bounds a tool's input schema, canonical.
	MaxToolSchemaBytes = 16 << 10
	// MaxToolCallsPerAnswer bounds the tool calls one answer may make.
	MaxToolCallsPerAnswer = 4
	// MaxToolArgumentBytes bounds one call's arguments as the model wrote
	// them.
	MaxToolArgumentBytes = 16 << 10
	// MaxToolSteps bounds a turn's transcript.
	MaxToolSteps = 32
	// MaxToolResultBytes bounds one tool result the server sends back.
	MaxToolResultBytes = 16 << 10
	// maxToolSchemaDepth bounds how deeply a tool's input schema nests.
	maxToolSchemaDepth = 16
)

// CodeToolsUnsupported: the request offers tools and the client's backend
// cannot send them (the Claude adapter; H-061: a Claude deployment has no
// investigator): refused before any I/O. A backend that can send tools but
// whose approval does not name them for the role is policy_not_approved.
const CodeToolsUnsupported Code = "tools_unsupported"

// Tool is one tool a request offers the model: server-defined, never data.
type Tool struct {
	// Name is the tool's name: lower case letters, digits and underscores,
	// a letter first, at most 64.
	Name string
	// Description tells the model what the tool does: trusted server text
	// (the prompt template's, H-061 §9), at most MaxToolDescriptionBytes and
	// holding no block.
	Description string
	// InputSchema is the tool's arguments: a JSON Schema 2020-12 schema of a
	// JSON object, whose every object refuses unknown members, whose every
	// array says what its elements are (items), and which refers to nothing
	// outside itself (closedSchema).
	InputSchema jsontext.Value
}

// ToolCall is one call the model made: untrusted until the client checked
// it, and only what the client checked after (Input canonical).
type ToolCall struct {
	ID    string
	Name  string
	Input jsontext.Value
}

// ToolResult is the server's answer to one call, for the next turn: one
// strict JSON value (an observation the server wrote; H-061 §7) and whether
// the call was refused or failed.
type ToolResult struct {
	CallID  string
	Content jsontext.Value
	IsError bool
}

// ToolStep is one step of a turn's transcript: the model's text and calls as
// an earlier turn answered them (untrusted; never logged or stored), then the
// server's result of each call, in the calls' order.
type ToolStep struct {
	Text    string
	Calls   []ToolCall
	Results []ToolResult
}

// TurnContext is a tool turn's request (H-060): a sealed prompt of a role the
// backend accepts tools for (no research, no images, no repair), the tools it
// offers and the transcript so far.
type TurnContext struct {
	OrganizationID string
	Prompt         Prompt
	Attribution    Attribution
	// Model is the model the turn asks for (H-049); "" for the backend's
	// own. As for a proposal, only a ModelChooser that may ask it for the
	// role is handed the turn.
	Model string
	Tools []Tool
	Steps []ToolStep
}

// TurnResult is a tool turn's checked answer: the model's text (a plain
// answer, or what it wrote before its calls; untrusted, never logged or
// stored) and its calls, each checked against the tool it names.
type TurnResult struct {
	Text     string
	Calls    []ToolCall
	Metadata Metadata
}

// ToolTurner is a backend that can offer the model tools. The client hands a
// request with tools to no other backend, nor to one whose AcceptsTools is
// false for the prompt's role, and refuses the call before anything else is
// asked (tools_unsupported or policy_not_approved). Turn sends req (whose
// Tools and Steps are set and checked) and returns the answer's text and its
// tool calls, untrusted; Complete is never handed tools.
type ToolTurner interface {
	Backend
	AcceptsTools(role Role) bool
	Turn(ctx context.Context, req Request) (Response, error)
}

// AcceptsTools reports whether the client hands a tool turn of role to its
// backend, so a caller can refuse up front. The turn itself still checks.
func (c *Client) AcceptsTools(role Role) bool {
	t, ok := c.backend.(ToolTurner)
	return ok && t.AcceptsTools(role)
}

// Turn asks for one tool turn: the model answers with text, tool calls, or
// both. It is checked, metered and bounded as every call is (call), and its
// calls are checked against the tools offered (toolTurn.answer).
func (c *Client) Turn(ctx context.Context, in TurnContext) (TurnResult, error) {
	tt, err := checkTurn(in, c.budget)
	if err != nil {
		return TurnResult{}, err
	}
	r, err := c.call(ctx, in.OrganizationID, in.Prompt.Role, in.Prompt, in.Attribution, nil, in.Model, nil, tt)
	if err != nil {
		return TurnResult{Metadata: r.meta}, err
	}
	return TurnResult{Text: r.turnText, Calls: r.turnCalls, Metadata: r.meta}, nil
}

// ToolBytes is how many bytes req's tools and transcript add to it: the
// tools' names, descriptions and schemas, and each step's text, calls and
// results. 0 for a request without tools.
func (r Request) ToolBytes() int {
	n := 0
	for _, t := range r.Tools {
		n += len(t.Name) + len(t.Description) + len(t.InputSchema)
	}
	for _, s := range r.Steps {
		n += len(s.Text)
		for _, c := range s.Calls {
			n += len(c.ID) + len(c.Name) + len(c.Input)
		}
		for _, res := range s.Results {
			n += len(res.CallID) + len(res.Content)
		}
	}
	return n
}

var (
	toolName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	// toolCallID is a call ID kept and sent back: only of a token's shape.
	toolCallID = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)
)

// toolTurn is a checked tool turn: its tools (canonical) and their compiled
// schemas, its transcript (canonical), what they add to the prompt, and the
// turn's input hash.
type toolTurn struct {
	tools   []Tool
	schemas map[string]*jsonschema.Schema
	steps   []ToolStep
	ids     map[string]bool // every call ID of the transcript
	size    int
	hash    string
}

// checkTurn checks a turn's tools and transcript (the prompt is checked by
// call, as for every call). A turn that breaks a rule is a request the
// client refuses (invalid_request); one too large is context_too_large.
func checkTurn(in TurnContext, budget Budget) (*toolTurn, error) {
	if len(in.Steps) > MaxToolSteps {
		return nil, invalidRequest()
	}
	tt, err := checkTools(in.Tools)
	if err != nil {
		return nil, err
	}
	for _, s := range in.Steps {
		if len(s.Calls) == 0 || len(s.Calls) > MaxToolCallsPerAnswer || len(s.Results) != len(s.Calls) ||
			len(s.Text) > budget.MaxOutputBytes || !utf8.ValidString(s.Text) {
			return nil, invalidRequest()
		}
		step := ToolStep{Text: s.Text}
		for i, call := range s.Calls {
			checked, issues := tt.checkCall(call, i, nil)
			if len(issues) > 0 {
				return nil, invalidRequest()
			}
			tt.ids[checked.ID] = true
			step.Calls = append(step.Calls, checked)
			res := s.Results[i]
			if res.CallID != call.ID || len(res.Content) > MaxToolResultBytes {
				return nil, invalidRequest()
			}
			content, err := canonicalContent(res.Content)
			if err != nil {
				return nil, invalidRequest()
			}
			step.Results = append(step.Results, ToolResult{CallID: res.CallID, Content: content, IsError: res.IsError})
		}
		tt.steps = append(tt.steps, step)
	}
	tt.size = Request{Tools: tt.tools, Steps: tt.steps}.ToolBytes()
	if !in.Prompt.sealed() {
		return nil, invalidRequest()
	}
	h, err := turnHash(in.Prompt.InputHash, tt.tools, tt.steps)
	if err != nil {
		return nil, invalidRequest()
	}
	tt.hash = h
	return tt, nil
}

// CheckTools checks a tool list as a turn does (1 to MaxTools tools, each
// with a valid, unique name, a description within its bound holding no
// block, and a closed input schema), so that a caller can refuse it before
// any call (internal/aigraph/einomodel's WithTools). invalid_request when it
// breaks a rule.
func CheckTools(tools []Tool) error {
	_, err := checkTools(tools)
	return err
}

// checkTools is CheckTools, keeping each tool canonical and its schema
// compiled for the turn.
func checkTools(tools []Tool) (*toolTurn, error) {
	if len(tools) == 0 || len(tools) > MaxTools {
		return nil, invalidRequest()
	}
	tt := &toolTurn{schemas: map[string]*jsonschema.Schema{}, ids: map[string]bool{}}
	for _, t := range tools {
		if !toolName.MatchString(t.Name) || tt.schemas[t.Name] != nil || len(t.Description) > MaxToolDescriptionBytes ||
			!utf8.ValidString(t.Description) || holdsBlock(t.Description) {
			return nil, invalidRequest()
		}
		schema, compiled, err := toolSchema(t.Name, t.InputSchema)
		if err != nil {
			return nil, invalidRequest()
		}
		tt.schemas[t.Name] = compiled
		tt.tools = append(tt.tools, Tool{Name: t.Name, Description: t.Description, InputSchema: schema})
	}
	return tt, nil
}

// sealed reports whether p carries an input hash (call verifies it).
func (p Prompt) sealed() bool { return p.InputHash != "" }

// checkCall checks one call against the tools offered: its ID (of a token's
// shape, unique in the turn: seen holds the answer's IDs so far, tt.ids the
// transcript's), the tool it names, and its arguments (strict, within
// MaxToolArgumentBytes, valid under the tool's schema). It returns the call
// with its arguments canonical, or where it is wrong (paths under
// /tool_calls/i, fixed messages; never what the model wrote).
func (tt *toolTurn) checkCall(c ToolCall, i int, seen map[string]bool) (ToolCall, []model.ValidationIssue) {
	at := validate.Pointer("tool_calls", fmt.Sprint(i))
	issue := func(path, msg string) []model.ValidationIssue {
		return []model.ValidationIssue{model.NewIssue(OutputInvalid, at+path, msg, model.SeverityError)}
	}
	if !toolCallID.MatchString(c.ID) || tt.ids[c.ID] || seen[c.ID] {
		return ToolCall{}, issue("/id", "is not a call ID of a token's shape, or repeats one")
	}
	s, ok := tt.schemas[c.Name]
	if !ok {
		return ToolCall{}, issue("/name", "names no tool the request offered")
	}
	if len(c.Input) == 0 || len(c.Input) > MaxToolArgumentBytes {
		return ToolCall{}, issue("/input", "is empty or larger than the tool argument bound")
	}
	input, err := canonicalContent(c.Input)
	if err != nil || input.Kind() != '{' {
		return ToolCall{}, issue("/input", "is not one strict JSON object")
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(input))
	if err != nil {
		return ToolCall{}, issue("/input", "is not one strict JSON object")
	}
	if err := s.Validate(inst); err != nil {
		var ve *jsonschema.ValidationError
		if !errors.As(err, &ve) {
			return ToolCall{}, issue("/input", "could not be checked against the tool's input schema")
		}
		var out []model.ValidationIssue
		leaves(ve, func(loc []string) {
			if len(out) < 20 {
				out = append(out, model.NewIssue(OutputInvalid, at+"/input"+validate.Pointer(loc...), "does not match the tool's input schema", model.SeverityError))
			}
		})
		return ToolCall{}, validate.Sort(out)
	}
	return ToolCall{ID: c.ID, Name: c.Name, Input: input}, nil
}

// leaves calls f with the instance location of every leaf failure of e.
func leaves(e *jsonschema.ValidationError, f func([]string)) {
	if len(e.Causes) == 0 {
		f(e.InstanceLocation)
		return
	}
	for _, c := range e.Causes {
		leaves(c, f)
	}
}

// answer checks a tool turn's answer: at most MaxToolCallsPerAnswer calls,
// each checked (checkCall), and text within the output budget; an answer with
// neither is no answer. It returns the text and the checked calls, or the
// issues that refuse the answer.
func (tt *toolTurn) answer(resp Response, budget Budget) (string, []ToolCall, []model.ValidationIssue) {
	invalid := func(path, msg string) []model.ValidationIssue {
		return []model.ValidationIssue{model.NewIssue(OutputInvalid, path, msg, model.SeverityError)}
	}
	if len(resp.ToolCalls) > MaxToolCallsPerAnswer {
		return "", nil, invalid("/tool_calls", "the answer makes more tool calls than the bound")
	}
	if len(resp.Text) > budget.MaxOutputBytes || !utf8.Valid(resp.Text) {
		return "", nil, invalid("", "the answer's text is larger than the output budget or not UTF-8")
	}
	if len(resp.ToolCalls) == 0 && len(bytes.TrimSpace(resp.Text)) == 0 {
		return "", nil, invalid("", "the answer has no text and no tool call")
	}
	seen := map[string]bool{}
	calls := make([]ToolCall, 0, len(resp.ToolCalls))
	for i, c := range resp.ToolCalls {
		checked, issues := tt.checkCall(c, i, seen)
		if len(issues) > 0 {
			return "", nil, issues
		}
		seen[checked.ID] = true
		calls = append(calls, checked)
	}
	return string(resp.Text), calls, nil
}

// outputHash is the hash of a checked tool turn's answer: its text and calls.
func turnOutputHash(text string, calls []ToolCall) (string, error) {
	type callRef struct {
		ID    string         `json:"id"`
		Name  string         `json:"name"`
		Input jsontext.Value `json:"input"`
	}
	refs := make([]callRef, 0, len(calls))
	for _, c := range calls {
		refs = append(refs, callRef{c.ID, c.Name, c.Input})
	}
	doc, err := json.Marshal(struct {
		Text  string    `json:"text"`
		Calls []callRef `json:"tool_calls"`
	}{text, refs})
	if err != nil {
		return "", err
	}
	return hashing.Of(doc)
}

// turnHash is a tool turn's input hash: the sealed prompt's, the tools and
// the transcript, canonical.
func turnHash(prompt string, tools []Tool, steps []ToolStep) (string, error) {
	type callRef struct {
		ID    string         `json:"id"`
		Name  string         `json:"name"`
		Input jsontext.Value `json:"input"`
	}
	type resultRef struct {
		CallID  string         `json:"call_id"`
		Content jsontext.Value `json:"content"`
		IsError bool           `json:"is_error,omitzero"`
	}
	type stepRef struct {
		Text    string      `json:"text,omitzero"`
		Calls   []callRef   `json:"calls"`
		Results []resultRef `json:"results"`
	}
	type toolRef struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		InputSchema jsontext.Value `json:"input_schema"`
	}
	doc := struct {
		Prompt string    `json:"prompt_input_hash"`
		Tools  []toolRef `json:"tools"`
		Steps  []stepRef `json:"steps,omitzero"`
	}{Prompt: prompt}
	for _, t := range tools {
		doc.Tools = append(doc.Tools, toolRef{t.Name, t.Description, t.InputSchema})
	}
	for _, s := range steps {
		ref := stepRef{Text: s.Text}
		for _, c := range s.Calls {
			ref.Calls = append(ref.Calls, callRef{c.ID, c.Name, c.Input})
		}
		for _, r := range s.Results {
			ref.Results = append(ref.Results, resultRef{r.CallID, r.Content, r.IsError})
		}
		doc.Steps = append(doc.Steps, ref)
	}
	data, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	return hashing.Of(data)
}

// toolSchemaBase is the URL a tool's schema is compiled under; nothing is
// ever loaded from it (refuseToolLoader).
const toolSchemaBase = "https://geminus.invalid/tool/"

type refuseToolLoader struct{}

func (refuseToolLoader) Load(string) (any, error) {
	return nil, errors.New("ai provider: a tool's input schema refers to nothing outside itself")
}

// toolSchema checks a tool's input schema and compiles it: one strict JSON
// object within MaxToolSchemaBytes, closed (closedSchema), and a valid 2020-12
// schema that refers to nothing outside itself. It returns the schema
// canonical and compiled.
func toolSchema(name string, raw jsontext.Value) (jsontext.Value, *jsonschema.Schema, error) {
	if len(raw) == 0 || len(raw) > MaxToolSchemaBytes {
		return nil, nil, errors.New("ai provider: a tool's input schema is empty or too large")
	}
	canon, err := canonicalContent(raw)
	if err != nil {
		return nil, nil, err
	}
	var doc any
	if err := json.Unmarshal(canon, &doc); err != nil {
		return nil, nil, err
	}
	root, ok := doc.(map[string]any)
	if !ok || root["type"] != "object" {
		return nil, nil, errors.New("ai provider: a tool's input schema is not an object's")
	}
	if err := closedSchema(root, root, 0); err != nil {
		return nil, nil, err
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(canon))
	if err != nil {
		return nil, nil, err
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()
	c.UseLoader(refuseToolLoader{})
	if err := c.AddResource(toolSchemaBase+name, inst); err != nil {
		return nil, nil, err
	}
	compiled, err := c.Compile(toolSchemaBase + name)
	if err != nil {
		return nil, nil, err
	}
	return canon, compiled, nil
}

// toolSchemaKeywords are the keywords a tool's input schema may use: the
// ones whose effect on unknown members closedSchema can tell. Another is
// refused: stricter than JSON Schema, where that costs nothing.
var toolSchemaKeywords = map[string]bool{
	"type": true, "properties": true, "required": true, "additionalProperties": true, "items": true, "prefixItems": true,
	"enum": true, "const": true, "anyOf": true, "allOf": true, "oneOf": true, "$ref": true, "$defs": true,
	"description": true, "title": true, "format": true, "pattern": true, "minLength": true, "maxLength": true,
	"minimum": true, "maximum": true, "exclusiveMinimum": true, "exclusiveMaximum": true, "multipleOf": true,
	"minItems": true, "maxItems": true, "uniqueItems": true, "$schema": true,
}

// saysWhat are the keywords that say what a value is; a schema with none of
// them accepts any value, an object with any member included.
var saysWhat = []string{"type", "enum", "const", "anyOf", "allOf", "oneOf", "$ref"}

const draft2020 = "https://json-schema.org/draft/2020-12/schema"

// closedSchema checks that s (a schema of root, depth levels down) accepts no
// object with a member it does not list: every schema says what it accepts
// (saysWhat; the schema true is refused), every one that may accept an
// object lists its members and sets additionalProperties to false, and every
// one that may accept an array says what its elements are (items: a closed
// schema, or false after prefixItems). It uses only toolSchemaKeywords,
// refers only within root (#/$defs/...), and is 2020-12 if it names a draft
// at all.
func closedSchema(v any, root map[string]any, depth int) error {
	fail := func(format string, a ...any) error {
		return fmt.Errorf("ai provider: a tool's input schema: "+format, a...)
	}
	if depth > maxToolSchemaDepth {
		return fail("nests too deeply")
	}
	if b, ok := v.(bool); ok {
		if b {
			return fail("accepts any value")
		}
		return nil
	}
	s, ok := v.(map[string]any)
	if !ok {
		return fail("a schema is not an object")
	}
	for k, val := range s {
		if !toolSchemaKeywords[k] {
			return fail("uses a keyword this build does not take")
		}
		if k == "$schema" && (depth > 0 || val != draft2020) {
			return fail("names another draft than 2020-12")
		}
	}
	says := false
	for _, k := range saysWhat {
		if _, ok := s[k]; ok {
			says = true
		}
	}
	if !says {
		return fail("a schema accepts any value")
	}
	if ref, ok := s["$ref"]; ok {
		r, isString := ref.(string)
		target, found := pointerIn(root, strings.TrimPrefix(r, "#"))
		if !isString || !strings.HasPrefix(r, "#/$defs/") || !found {
			return fail("refers outside itself")
		}
		_ = target // walked under $defs
	}
	if objectish(s) {
		if s["additionalProperties"] != false {
			return fail("an object does not refuse unknown members")
		}
	} else if ap, ok := s["additionalProperties"]; ok && ap != false {
		return fail("an object does not refuse unknown members")
	}
	if props, ok := s["properties"]; ok {
		m, isMap := props.(map[string]any)
		if !isMap {
			return fail("properties is not an object")
		}
		for _, p := range m {
			if err := closedSchema(p, root, depth+1); err != nil {
				return err
			}
		}
	}
	if defs, ok := s["$defs"]; ok {
		m, isMap := defs.(map[string]any)
		if !isMap || depth > 0 {
			return fail("$defs is not an object of the root")
		}
		for _, d := range m {
			if err := closedSchema(d, root, depth+1); err != nil {
				return err
			}
		}
	}
	items, hasItems := s["items"]
	if arrayish(s) && !hasItems {
		// Without items, every element (after prefixItems' ones) may be any
		// value, an object with any member included (review round 1, M1).
		return fail("an array does not say what its elements are")
	}
	if hasItems {
		if err := closedSchema(items, root, depth+1); err != nil {
			return err
		}
	}
	for _, k := range []string{"prefixItems", "anyOf", "allOf", "oneOf"} {
		list, ok := s[k]
		if !ok {
			continue
		}
		l, isList := list.([]any)
		if !isList || len(l) == 0 {
			return fail("%s is not a list of schemas", k)
		}
		for _, sub := range l {
			if err := closedSchema(sub, root, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

// objectish reports whether s may accept an object by its own type: its
// type is "object" or a list naming it, or it lists properties.
func objectish(s map[string]any) bool {
	if _, ok := s["properties"]; ok {
		return true
	}
	return typeNames(s, "object")
}

// arrayish reports whether s may accept an array by its own type: its type
// is "array" or a list naming it, or it has prefixItems.
func arrayish(s map[string]any) bool {
	if _, ok := s["prefixItems"]; ok {
		return true
	}
	return typeNames(s, "array")
}

// typeNames reports whether s's type is name or a list naming it.
func typeNames(s map[string]any, name string) bool {
	switch t := s["type"].(type) {
	case string:
		return t == name
	case []any:
		for _, x := range t {
			if x == name {
				return true
			}
		}
	}
	return false
}

// pointerIn resolves the JSON Pointer ptr ("" or "/a/b") in doc.
func pointerIn(doc any, ptr string) (any, bool) {
	if ptr == "" {
		return doc, true
	}
	if !strings.HasPrefix(ptr, "/") {
		return nil, false
	}
	cur := doc
	for _, tok := range strings.Split(ptr[1:], "/") {
		tok = strings.NewReplacer("~1", "/", "~0", "~").Replace(tok)
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[tok]; !ok {
			return nil, false
		}
	}
	return cur, true
}
