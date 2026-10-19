package anthropic

import (
	"encoding/json/v2"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/kcl1104/geminus/schemas"
)

// Projection is the provider's view of a canonical output schema (AI_SPEC
// §2: "若 provider 僅支援 schema subset，由 adapter 做明確投影"): one
// self-contained JSON Schema in the subset the provider's structured
// outputs accept — types, properties, required, items, enum, const, anyOf,
// allOf, closed objects and a few string formats.
//
// The projection only removes constraints and rewrites oneOf as anyOf: it
// never adds a member, opens an object or widens a type. Every removal is
// listed in Dropped. The structured output is a first constraint only; the
// server checks every answer against the full 2020-12 schema
// (provider.Client), so a dropped bound or pattern is still enforced, and
// a provider that answers outside it gets invalid_output.
type Projection struct {
	Schema map[string]any
	// Dropped lists the removed keywords as "file#/pointer", sorted.
	Dropped []string
}

// dropped are constraints the subset does not take; the canonical
// validator enforces them.
var dropped = map[string]bool{
	"minLength": true, "maxLength": true, "pattern": true,
	"minimum": true, "maximum": true, "exclusiveMinimum": true, "exclusiveMaximum": true, "multipleOf": true,
	"minItems": true, "maxItems": true, "uniqueItems": true, "minProperties": true, "maxProperties": true,
	"if": true, "then": true, "else": true, "not": true,
}

// annotations carry no constraint and are removed without being listed.
var annotations = map[string]bool{"$schema": true, "$id": true, "$comment": true, "title": true, "examples": true, "$defs": true}

// refused keywords cannot be projected without changing what is allowed.
var refused = map[string]bool{
	"patternProperties": true, "propertyNames": true, "dependentRequired": true, "dependentSchemas": true,
	"unevaluatedProperties": true, "unevaluatedItems": true, "contains": true, "minContains": true, "maxContains": true,
	"prefixItems": true, "$dynamicRef": true, "$dynamicAnchor": true, "$anchor": true, "default": true,
}

var supportedFormats = []string{"date-time", "time", "date", "duration", "email", "hostname", "uri", "ipv4", "ipv6", "uuid"}

// Project projects a canonical output schema (a file in schemas/).
func Project(file string) (Projection, error) {
	docs := map[string]any{}
	entries, err := schemas.FS.ReadDir(".")
	if err != nil {
		return Projection{}, err
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".schema.json") {
			continue
		}
		data, err := schemas.FS.ReadFile(e.Name())
		if err != nil {
			return Projection{}, err
		}
		var doc any
		if err := json.Unmarshal(data, &doc); err != nil {
			return Projection{}, fmt.Errorf("%s: %w", e.Name(), err)
		}
		docs[e.Name()] = doc
	}
	return project(docs, file)
}

type projector struct {
	docs    map[string]any
	dropped map[string]bool
}

func project(docs map[string]any, file string) (Projection, error) {
	p := &projector{docs: docs, dropped: map[string]bool{}}
	root, ok := docs[file]
	if !ok {
		return Projection{}, fmt.Errorf("projection: no schema %s", file)
	}
	out, err := p.node(root, file, "", nil)
	if err != nil {
		return Projection{}, err
	}
	m, ok := out.(map[string]any)
	if !ok {
		return Projection{}, fmt.Errorf("projection: %s is not an object schema", file)
	}
	// The root is a closed object, or a choice of closed objects (the two
	// branches of proposal-output).
	rootOK := m["type"] == "object"
	if alts, ok := m["anyOf"].([]any); ok && m["type"] == nil && len(alts) > 0 {
		rootOK = true
		for _, a := range alts {
			am, _ := a.(map[string]any)
			rootOK = rootOK && am["type"] == "object"
		}
	}
	if !rootOK {
		return Projection{}, fmt.Errorf("projection: the root of %s is not an object", file)
	}
	list := make([]string, 0, len(p.dropped))
	for d := range p.dropped {
		list = append(list, d)
	}
	sort.Strings(list)
	return Projection{Schema: m, Dropped: list}, nil
}

func pointerGet(doc any, frag string) (any, bool) {
	cur := doc
	if frag == "" {
		return cur, true
	}
	for _, tok := range strings.Split(strings.TrimPrefix(frag, "/"), "/") {
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

// node projects the schema v found at file#ptr; stack holds the refs being
// expanded, to refuse recursion (the subset has no recursive schemas).
func (p *projector) node(v any, file, ptr string, stack []string) (any, error) {
	switch s := v.(type) {
	case bool:
		if !s {
			return nil, fmt.Errorf("projection: %s#%s: the false schema", file, ptr)
		}
		return map[string]any{}, nil
	case map[string]any:
		if ref, ok := s["$ref"].(string); ok {
			if len(s) > 1 {
				for k := range s {
					if k != "$ref" && !annotations[k] && k != "description" {
						return nil, fmt.Errorf("projection: %s#%s: $ref beside %s", file, ptr, k)
					}
				}
			}
			target, tfile, tptr, err := p.resolve(ref, file)
			if err != nil {
				return nil, fmt.Errorf("projection: %s#%s: %w", file, ptr, err)
			}
			key := tfile + "#" + tptr
			if slices.Contains(stack, key) {
				return nil, fmt.Errorf("projection: %s#%s: recursive $ref %s", file, ptr, ref)
			}
			return p.node(target, tfile, tptr, append(stack, key))
		}
		out := map[string]any{}
		keys := make([]string, 0, len(s))
		for k := range s {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			val := s[k]
			at := ptr + "/" + k
			switch {
			case refused[k]:
				return nil, fmt.Errorf("projection: %s#%s: %s cannot be projected", file, at, k)
			case annotations[k]:
			case dropped[k]:
				p.dropped[file+"#"+at] = true
			case k == "format":
				if f, _ := val.(string); slices.Contains(supportedFormats, f) {
					out[k] = f
				} else {
					p.dropped[file+"#"+at] = true
				}
			case k == "type", k == "enum", k == "const", k == "required", k == "description":
				out[k] = val
			case k == "additionalProperties":
				if b, ok := val.(bool); !ok || b {
					return nil, fmt.Errorf("projection: %s#%s: an open object cannot be projected", file, at)
				}
				out[k] = false
			case k == "items":
				sub, err := p.node(val, file, at, stack)
				if err != nil {
					return nil, err
				}
				out[k] = sub
			case k == "properties":
				props, ok := val.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("projection: %s#%s: properties is not an object", file, at)
				}
				pout := map[string]any{}
				for name, sub := range props {
					ps, err := p.node(sub, file, at+"/"+name, stack)
					if err != nil {
						return nil, err
					}
					pout[name] = ps
				}
				out[k] = pout
			case k == "oneOf", k == "anyOf", k == "allOf":
				list, ok := val.([]any)
				if !ok {
					return nil, fmt.Errorf("projection: %s#%s: %s is not an array", file, at, k)
				}
				var alts []any
				for i, sub := range list {
					ps, err := p.node(sub, file, fmt.Sprintf("%s/%d", at, i), stack)
					if err != nil {
						return nil, err
					}
					if m, ok := ps.(map[string]any); ok && len(m) == 0 && k == "allOf" {
						continue // a member made only of dropped constraints
					}
					alts = append(alts, ps)
				}
				switch {
				case k == "allOf" && len(alts) == 0:
				case k == "oneOf":
					// The alternatives of every output schema are told apart
					// by a const member, so at most one ever matches: oneOf
					// and anyOf allow the same values.
					out["anyOf"] = alts
				default:
					out[k] = alts
				}
			default:
				return nil, fmt.Errorf("projection: %s#%s: unknown keyword %s", file, at, k)
			}
		}
		_, hasProps := out["properties"]
		if t, _ := out["type"].(string); (t == "object" || hasProps) && out["additionalProperties"] != false {
			return nil, fmt.Errorf("projection: %s#%s: an object without additionalProperties: false cannot be projected", file, ptr)
		}
		return out, nil
	}
	return nil, fmt.Errorf("projection: %s#%s: not a schema", file, ptr)
}

func (p *projector) resolve(ref, file string) (any, string, string, error) {
	target, frag, _ := strings.Cut(ref, "#")
	if target == "" {
		target = file
	}
	if strings.Contains(target, "/") || strings.Contains(target, ":") {
		return nil, "", "", fmt.Errorf("$ref %s is not a sibling schema", ref)
	}
	doc, ok := p.docs[target]
	if !ok {
		return nil, "", "", fmt.Errorf("$ref %s: no such schema", ref)
	}
	v, ok := pointerGet(doc, frag)
	if !ok {
		return nil, "", "", fmt.Errorf("$ref %s: no such location", ref)
	}
	return v, target, frag, nil
}
