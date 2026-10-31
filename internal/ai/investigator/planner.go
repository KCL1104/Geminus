package investigator

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
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
// inlined and only projectionKeeps' keywords (no description: the tool's is
// prompt text), canonical. It is closed (every object refuses unknown
// members) and takes at least what the full schema takes.
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
	return json.Marshal(out, json.Deterministic(true))
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
// API's executor runs again before it carries the call out.
type Decoder struct{ Mode Mode }

var _ provider.ToolDecoder = Decoder{}

// DecodeCall decodes the call strictly (Decode) and returns its RFC 8785
// arguments, or the refusal's code and path.
func (d Decoder) DecodeCall(name string, input []byte) ([]byte, *provider.ToolRefusal) {
	c, err := Decode(d.Mode, name, input)
	if err != nil {
		var r *Refusal
		if errors.As(err, &r) {
			return nil, &provider.ToolRefusal{Code: string(r.Code), Path: r.Path}
		}
		return nil, &provider.ToolRefusal{Code: string(Internal)}
	}
	return slices.Clone(c.Canonical), nil
}
