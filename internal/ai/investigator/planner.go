package investigator

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/prompts"
	"github.com/kcl1104/geminus/schemas"
)

// The registry's side of a planner turn (H-061 §9, slice A5): the tools the
// planner is offered (their descriptions are prompt text, versioned with the
// investigator template; their input schemas the closed projection of the
// schema's argument $defs), which of them a budget leaves, and the decoder
// provider.Client.PlanTurn takes a call through.

// Role is the planner's prompt role.
const Role = provider.RoleInvestigator

// projectionKeeps are the keywords a tool's input schema keeps: what says
// which members an object has and what a value is. The bounds and patterns
// are left out (Meta's documented schema subset has none of them:
// meta.SchemaViolations); the client checks the projection, and Decode the
// full schema, on every call.
var projectionKeeps = map[string]bool{"type": true, "properties": true, "required": true, "additionalProperties": true,
	"enum": true, "const": true, "anyOf": true, "items": true}

// InputSchema is tool t's input schema as the planner is sent it: the
// argument $defs of schemas/investigator.schema.json with every reference
// inlined and only projectionKeeps' keywords, canonical. Its optional
// members (H-061 A9: those that may be null, Optional) are left out of its
// required list, each with its description from prompts/investigator/
// tools.md, prompt text: omit it when unused. No other schema carries a
// description (the tool's own is prompt text too). It is closed (every
// object refuses unknown members) and takes at least what the full schema
// takes, and what Decoder.DecodeCall takes besides (an optional member left
// out).
func InputSchema(t Tool) (jsontext.Value, error) {
	d, ok := tools[t]
	if !ok {
		return nil, &Refusal{UnknownTool, ""}
	}
	docs, err := schemaDocs()
	if err != nil {
		return nil, err
	}
	root, ok := docs["investigator.schema.json"]["$defs"].(map[string]any)[d.def]
	if !ok {
		return nil, fmt.Errorf("investigator: no $defs/%s", d.def)
	}
	out, err := project(docs, "investigator.schema.json", root, 0)
	if err != nil {
		return nil, err
	}
	opt, err := Optional(t)
	if err != nil {
		return nil, err
	}
	if len(opt) > 0 {
		desc, _, err := prompts.MemberDescriptions(string(Role))
		if err != nil {
			return nil, err
		}
		obj := out.(map[string]any)
		props := obj["properties"].(map[string]any)
		var required []any
		for _, r := range obj["required"].([]any) {
			if !slices.Contains(opt, r.(string)) {
				required = append(required, r)
			}
		}
		if obj["required"] = required; len(required) == 0 {
			delete(obj, "required")
		}
		for _, m := range opt {
			text := desc[string(t)][m]
			if text == "" {
				return nil, fmt.Errorf("investigator: prompts/investigator/tools.md does not describe %s of %s", m, t)
			}
			props[m].(map[string]any)["description"] = text
		}
	}
	return json.Marshal(out, json.Deterministic(true))
}

var (
	optionalOnce sync.Once
	optionalMap  map[Tool][]string
	optionalErr  error
)

// Optional is tool t's optional members, in name order: the members its
// full schema requires that may be null (an anyOf with the null schema
// among its alternatives, or a type list naming null), at the arguments'
// top level (below it, every member of every tool is required and none may
// be null: the registry's tests hold it). The planner is shown them as
// optional (InputSchema), and a planner turn's decoder adds each one the
// model left out as null (Decoder.DecodeCall), so a call's canonical
// arguments always hold them. nil for an unknown tool.
func Optional(t Tool) ([]string, error) {
	optionalOnce.Do(func() {
		docs, err := schemaDocs()
		if err != nil {
			optionalErr = err
			return
		}
		defs := docs["investigator.schema.json"]["$defs"].(map[string]any)
		optionalMap = map[Tool][]string{}
		for tool, d := range tools {
			def, ok := defs[d.def].(map[string]any)
			if !ok {
				optionalErr = fmt.Errorf("investigator: no $defs/%s", d.def)
				return
			}
			props, _ := def["properties"].(map[string]any)
			required, _ := def["required"].([]any)
			var out []string
			for _, r := range required {
				name, _ := r.(string)
				member, ok := props[name].(map[string]any)
				if !ok {
					optionalErr = fmt.Errorf("investigator: $defs/%s requires %q, which it does not list", d.def, name)
					return
				}
				if takesNull(member) {
					out = append(out, name)
				}
			}
			slices.Sort(out)
			optionalMap[tool] = out
		}
	})
	if optionalErr != nil {
		return nil, optionalErr
	}
	return slices.Clone(optionalMap[t]), nil
}

// takesNull reports whether a member's schema names null as a value it
// takes: the null schema as an anyOf alternative, or "null" in a type
// list. (The argument schemas spell null this way only; a $ref to a
// nullable definition would not be seen, and the registry's tests would
// fail on its member: TestShownSchemaLeavesNullableMembersOut.)
func takesNull(s map[string]any) bool {
	if alts, ok := s["anyOf"].([]any); ok {
		for _, a := range alts {
			if m, ok := a.(map[string]any); ok && len(m) == 1 && m["type"] == "null" {
				return true
			}
		}
	}
	if ts, ok := s["type"].([]any); ok {
		return slices.Contains(ts, any("null"))
	}
	return false
}

// withNulls is raw, a planner's arguments for tool t as the model wrote
// them, with each of t's optional members that raw leaves out added as
// null, before the closing brace; only when raw is exactly one JSON object
// as jsontext reads one by default (valid UTF-8, no duplicate member,
// nothing but white space after it). Any other raw is returned as it is,
// for decode to refuse as Decode would. The bytes of the members raw holds
// are untouched, so the result is refused exactly as Decode refuses the same
// call with the left-out members written as null: the only issue the fill
// removes is a left-out member's own (at "", the call as a whole), which
// could hide a deeper one that Decode finds only once the schema passes
// (text not in NFC, an integer written 1.0).
func withNulls(t Tool, raw []byte) []byte {
	opt, err := Optional(t)
	if err != nil || len(opt) == 0 {
		return raw
	}
	dec := jsontext.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.ReadToken(); err != nil || tok.Kind() != '{' {
		return raw
	}
	present := map[string]bool{}
	for dec.PeekKind() == '"' {
		name, err := dec.ReadToken()
		if err != nil {
			return raw
		}
		present[name.String()] = true
		if err := dec.SkipValue(); err != nil {
			return raw
		}
	}
	if tok, err := dec.ReadToken(); err != nil || tok.Kind() != '}' {
		return raw
	}
	if _, err := dec.ReadToken(); !errors.Is(err, io.EOF) {
		return raw
	}
	end := bytes.LastIndexByte(raw, '}')
	out := slices.Clip(slices.Clone(raw[:end]))
	n := len(present)
	for _, m := range opt {
		if present[m] {
			continue
		}
		if n > 0 {
			out = append(out, ',')
		}
		out, _ = jsontext.AppendQuote(out, m)
		out = append(out, ":null"...)
		n++
	}
	return append(out, raw[end:]...)
}

var (
	docsOnce sync.Once
	docsMap  map[string]map[string]any
	docsErr  error
)

func schemaDocs() (map[string]map[string]any, error) {
	docsOnce.Do(func() {
		docsMap = map[string]map[string]any{}
		for _, name := range []string{"investigator.schema.json", "common.schema.json"} {
			data, err := schemas.FS.ReadFile(name)
			if err != nil {
				docsErr = err
				return
			}
			var doc map[string]any
			if err := json.Unmarshal(data, &doc); err != nil {
				docsErr = err
				return
			}
			docsMap[name] = doc
		}
	})
	return docsMap, docsErr
}

// project inlines v's references (local to file, or to common.schema.json)
// and keeps only projectionKeeps' keywords.
func project(docs map[string]map[string]any, file string, v any, depth int) (any, error) {
	if depth > 16 {
		return nil, errors.New("investigator: an argument schema nests too deeply")
	}
	s, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("investigator: an argument schema is not an object")
	}
	if ref, ok := s["$ref"].(string); ok {
		target, at, ok := strings.Cut(ref, "#/$defs/")
		if !ok {
			return nil, fmt.Errorf("investigator: a reference %q this projection does not follow", ref)
		}
		if target == "" {
			target = file
		}
		doc, ok := docs[target]
		if !ok {
			return nil, fmt.Errorf("investigator: a reference into %s", target)
		}
		def, ok := doc["$defs"].(map[string]any)[at]
		if !ok {
			return nil, fmt.Errorf("investigator: no $defs/%s in %s", at, target)
		}
		return project(docs, target, def, depth+1)
	}
	out := map[string]any{}
	for k, val := range s {
		if !projectionKeeps[k] {
			continue
		}
		switch k {
		case "properties":
			props := map[string]any{}
			for name, p := range val.(map[string]any) {
				pp, err := project(docs, file, p, depth+1)
				if err != nil {
					return nil, err
				}
				props[name] = pp
			}
			out[k] = props
		case "anyOf":
			var list []any
			for _, alt := range val.([]any) {
				pa, err := project(docs, file, alt, depth+1)
				if err != nil {
					return nil, err
				}
				list = append(list, pa)
			}
			out[k] = list
		case "items":
			pi, err := project(docs, file, val, depth+1)
			if err != nil {
				return nil, err
			}
			out[k] = pi
		default:
			out[k] = val
		}
	}
	return out, nil
}

var (
	toolsOnce sync.Once
	toolDefs  map[Tool]provider.Tool
	toolsErr  error
)

// ProviderTools are tools as a planner turn offers them, in the order
// given: each its name, its description (prompts/investigator/tools.md,
// versioned with the investigator template, so the prompt's
// TemplateVersion names them too) and its InputSchema. A tool that is not
// one of the seven, or one named twice, is refused.
func ProviderTools(ts []Tool) ([]provider.Tool, error) {
	toolsOnce.Do(func() {
		desc, _, err := prompts.ToolDescriptions(string(Role))
		if err != nil {
			toolsErr = err
			return
		}
		members, _, err := prompts.MemberDescriptions(string(Role))
		if err != nil {
			toolsErr = err
			return
		}
		// tools.md describes the optional members only (InputSchema
		// requires each one's description).
		for tool, ms := range members {
			opt, err := Optional(Tool(tool))
			if err != nil {
				toolsErr = err
				return
			}
			for m := range ms {
				if !slices.Contains(opt, m) {
					toolsErr = fmt.Errorf("investigator: prompts/investigator/tools.md describes %s of %s, not an optional member", m, tool)
					return
				}
			}
		}
		toolDefs = map[Tool]provider.Tool{}
		for _, t := range Tools {
			schema, err := InputSchema(t)
			if err != nil {
				toolsErr = err
				return
			}
			d, ok := desc[string(t)]
			if !ok {
				toolsErr = fmt.Errorf("investigator: prompts/investigator/tools.md does not describe %s", t)
				return
			}
			toolDefs[t] = provider.Tool{Name: string(t), Description: d, InputSchema: schema}
		}
		if len(desc) != len(Tools) {
			toolsErr = errors.New("investigator: prompts/investigator/tools.md describes a tool the registry does not have")
		}
	})
	if toolsErr != nil {
		return nil, toolsErr
	}
	out := make([]provider.Tool, 0, len(ts))
	for _, t := range ts {
		d, ok := toolDefs[t]
		if !ok || slices.ContainsFunc(out, func(o provider.Tool) bool { return o.Name == string(t) }) {
			return nil, &Refusal{UnknownTool, ""}
		}
		d.InputSchema = slices.Clone(d.InputSchema)
		out = append(out, d)
	}
	return out, nil
}

// Offered is the tools a turn of an investigation in mode m offers with
// budget b (H-061 §5.3, §12): the mode's tools whose caps are not used up,
// in the registry's order; finish always, and once 13 steps are used
// (b.StepsLeft at most 1) finish only. save_scenario and write_to_draft have
// no cap of their own (the executor refuses one without a ready proposal).
func Offered(m Mode, b Budget) []Tool {
	if _, ok := byMode[m]; !ok {
		return nil
	}
	if b.StepsLeft <= 1 {
		return []Tool{Finish}
	}
	var out []Tool
	for _, t := range byMode[m] {
		switch t {
		case ProposeWhatIf, ProposeModelEdit:
			if b.WhatIfsLeft <= 0 {
				continue
			}
		case RunWhatIf:
			if b.RunsLeft <= 0 {
				continue
			}
		case ExplainResults:
			if b.ExplanationsLeft <= 0 {
				continue
			}
		}
		out = append(out, t)
	}
	return out
}

// Decoder is the registry's strict decoder for a planner turn of an
// investigation in mode Mode (provider.ToolDecoder): Decode, the decoder the
// API's executor runs again before it carries the call out, after the one
// thing the planner's shown schema allows that the full schema does not: an
// optional member left out (H-061 A9).
type Decoder struct{ Mode Mode }

var _ provider.ToolDecoder = Decoder{}

// DecodeCall decodes the call strictly and returns its RFC 8785 arguments,
// or the refusal's code and path. An optional member (Optional) the model
// left out is added as null first (withNulls), so the arguments decode
// against the full, unchanged schema and their canonical form, which the
// ledger stores and the executor decodes again, is exactly the explicit
// null's. Nothing else is filled, coerced or repaired: a member that is
// present and wrong (the string "null" included) is refused as Decode
// refuses the call with the explicit null, at the same code and path. The
// 8 KiB bound is on the input as the model wrote it.
func (d Decoder) DecodeCall(name string, input []byte) ([]byte, *provider.ToolRefusal) {
	raw := input
	if len(input) <= MaxArgumentBytes {
		raw = withNulls(Tool(name), input)
	}
	c, err := decode(d.Mode, name, raw, len(input))
	if err != nil {
		var r *Refusal
		if errors.As(err, &r) {
			return nil, &provider.ToolRefusal{Code: string(r.Code), Path: r.Path}
		}
		return nil, &provider.ToolRefusal{Code: string(Internal)}
	}
	return slices.Clone(c.Canonical), nil
}
