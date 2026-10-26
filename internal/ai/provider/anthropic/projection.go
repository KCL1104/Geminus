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
// The projection removes constraints and rewrites oneOf as anyOf: it never
// adds a member, opens an object or widens a type. Every removal is listed
// in Dropped. The structured output is a first constraint only; the server
// checks every answer against the full 2020-12 schema (provider.Client), so
// a dropped bound or pattern is still enforced, and a provider that
// answers outside it gets invalid_output.
//
// One rule is not a removal (T-053 schema follow-up, 2026-10-26): where the
// canonical schema accepts any JSON value (an empty schema, true, or a
// description only; today the value of a JSON Patch add, replace and
// test), the projection asks for a string holding that value as a JSON
// text (RFC 8259) instead, and lists the place in Encoded. Structured
// outputs have no schema for "any JSON value": the API refuses an empty
// schema ("Empty schema ({}) that accepts any JSON value is not
// supported", the live smoke's count_tokens diagnosis), and an object
// schema must be closed, so an open one cannot be spelled out either. The
// adapter decodes exactly those places (Decode: strict, at most
// MaxEncodedValueBytes and MaxEncodedValueDepth levels, no trailing data)
// before the answer goes to provider.Client and its full-schema check, and
// an answer that does not decode is invalid_output; Decode fails closed, so
// an answer it cannot decode at all is invalid_output too, never passed on
// undecoded (review round 1, M1). The coordinator's free count_tokens
// request with this projection was answered 200 (2026-10-26). The places
// are found structurally: by their path of members and array items, and by
// the const member that tells the alternatives of each oneOf or anyOf on
// the way apart (status, op), never by what a string looks like. An open
// value whose place no const member tells apart, and a schema left with no
// constraint the subset takes, are refused, never sent as an empty schema.
type Projection struct {
	Schema map[string]any
	// Dropped lists the removed keywords as "file#/pointer", sorted.
	Dropped []string
	// Encoded lists the places in an answer where the projection asks for
	// a JSON text in a string, sorted by String.
	Encoded []Location
}

// Location is a place in an answer: a path of members and array items,
// and at each object on the way the const members that pick the
// alternative the place is in.
type Location struct {
	Steps []Step
}

// Step goes from a value into its member Member, or into every item of an
// array (Items). When names the const members the value it leaves must
// have.
type Step struct {
	When   []Cond
	Member string
	Items  bool
}

// Cond: the object's member Member is Const.
type Cond struct {
	Member string
	Const  any
}

// String is the place as a JSON Pointer in which "*" stands for every item
// of an array, and its conditions, e.g.
// `/changes/*/value when /status="proposed", /changes/*/op="add"`.
func (l Location) String() string {
	var path strings.Builder
	var when []string
	for _, st := range l.Steps {
		for _, c := range st.When {
			v, _ := json.Marshal(c.Const, json.Deterministic(true))
			when = append(when, path.String()+"/"+escapeToken(c.Member)+"="+string(v))
		}
		if st.Items {
			path.WriteString("/*")
		} else {
			path.WriteString("/" + escapeToken(st.Member))
		}
	}
	if len(when) == 0 {
		return path.String()
	}
	return path.String() + " when " + strings.Join(when, ", ")
}

func escapeToken(s string) string { return strings.NewReplacer("~", "~0", "/", "~1").Replace(s) }

// EncodedDescription is the description of an encoded string: what the
// model reads about it.
const EncodedDescription = `This value as a JSON text (RFC 8259), which the server decodes before it checks the answer: ` +
	`a string with its quotes, e.g. "\"ICT tester\"" for the string ICT tester; a number as its digits, e.g. "6"; ` +
	`true, false, null, an array or an object as their JSON, e.g. "{\"id\":\"node_1\"}". One JSON value, nothing after it.`

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
	encoded map[string]Location
}

// place is where in an answer the schema being projected applies: the
// steps that lead there, the conditions on the value there (the
// alternatives it is in), and untold when an alternative on the way has no
// const member that tells it apart.
type place struct {
	steps  []Step
	when   []Cond
	untold bool
}

func (pl place) into(member string) place {
	return place{steps: append(slices.Clone(pl.steps), Step{When: pl.when, Member: member}), untold: pl.untold}
}

func (pl place) items() place {
	return place{steps: append(slices.Clone(pl.steps), Step{When: pl.when, Items: true}), untold: pl.untold}
}

func (pl place) alternative(c *Cond) place {
	out := place{steps: pl.steps, when: slices.Clone(pl.when), untold: pl.untold}
	if c == nil {
		out.untold = true
	} else {
		out.when = append(out.when, *c)
	}
	return out
}

func project(docs map[string]any, file string) (Projection, error) {
	p := &projector{docs: docs, dropped: map[string]bool{}, encoded: map[string]Location{}}
	root, ok := docs[file]
	if !ok {
		return Projection{}, fmt.Errorf("projection: no schema %s", file)
	}
	out, err := p.sub(root, file, "", nil, place{})
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
	keys := make([]string, 0, len(p.encoded))
	for k := range p.encoded {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var encoded []Location
	for _, k := range keys {
		encoded = append(encoded, p.encoded[k])
	}
	return Projection{Schema: m, Dropped: list, Encoded: encoded}, nil
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

// open reports whether v, a canonical schema, accepts any JSON value: true,
// or an object with no keyword but annotations and a description.
func open(v any) bool {
	switch s := v.(type) {
	case bool:
		return s
	case map[string]any:
		for k := range s {
			if !annotations[k] && k != "description" {
				return false
			}
		}
		return true
	}
	return false
}

// empty reports whether a projected schema constrains nothing.
func empty(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	for k := range m {
		if k != "description" {
			return false
		}
	}
	return true
}

// typed reports whether a projected schema says what its value is: a type,
// an enum, a const, or alternatives.
func typed(v any) bool {
	m, _ := v.(map[string]any)
	for _, k := range []string{"type", "enum", "const", "anyOf", "allOf"} {
		if _, ok := m[k]; ok {
			return true
		}
	}
	return false
}

// sub projects a schema in a place that takes a schema of its own (the
// root, a member, the items, an alternative): it must say what the value
// is. One left without (an empty schema, or only dropped constraints and
// required) is refused, never sent: the API refuses a schema that accepts
// any JSON value ("Please specify a concrete type").
func (p *projector) sub(v any, file, ptr string, stack []string, at place) (any, error) {
	out, err := p.node(v, file, ptr, stack, at)
	if err != nil {
		return nil, err
	}
	if !typed(out) {
		return nil, fmt.Errorf("projection: %s#%s: nothing the subset takes says what the value is (the API refuses a schema without a concrete type)", file, ptr)
	}
	return out, nil
}

// node projects the schema v found at file#ptr; stack holds the refs being
// expanded, to refuse recursion (the subset has no recursive schemas).
func (p *projector) node(v any, file, ptr string, stack []string, at place) (any, error) {
	if open(v) {
		return p.encode(v, file, ptr, at)
	}
	switch s := v.(type) {
	case bool:
		return nil, fmt.Errorf("projection: %s#%s: the false schema", file, ptr)
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
			return p.node(target, tfile, tptr, append(stack, key), at)
		}
		out := map[string]any{}
		keys := make([]string, 0, len(s))
		for k := range s {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			val := s[k]
			at2 := ptr + "/" + k
			switch {
			case refused[k]:
				return nil, fmt.Errorf("projection: %s#%s: %s cannot be projected", file, at2, k)
			case annotations[k]:
			case dropped[k]:
				p.dropped[file+"#"+at2] = true
			case k == "format":
				if f, _ := val.(string); slices.Contains(supportedFormats, f) {
					out[k] = f
				} else {
					p.dropped[file+"#"+at2] = true
				}
			case k == "type", k == "enum", k == "const", k == "required", k == "description":
				out[k] = val
			case k == "additionalProperties":
				if b, ok := val.(bool); !ok || b {
					return nil, fmt.Errorf("projection: %s#%s: an open object cannot be projected", file, at2)
				}
				out[k] = false
			case k == "items":
				sub, err := p.sub(val, file, at2, stack, at.items())
				if err != nil {
					return nil, err
				}
				out[k] = sub
			case k == "properties":
				props, ok := val.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("projection: %s#%s: properties is not an object", file, at2)
				}
				pout := map[string]any{}
				for name, sub := range props {
					ps, err := p.sub(sub, file, at2+"/"+name, stack, at.into(name))
					if err != nil {
						return nil, err
					}
					pout[name] = ps
				}
				out[k] = pout
			case k == "oneOf", k == "anyOf", k == "allOf":
				list, ok := val.([]any)
				if !ok {
					return nil, fmt.Errorf("projection: %s#%s: %s is not an array", file, at2, k)
				}
				var conds []*Cond
				if k != "allOf" {
					conds = p.discriminator(list, file)
				}
				var alts []any
				for i, sub := range list {
					subAt := fmt.Sprintf("%s/%d", at2, i)
					if k == "allOf" {
						if p.openRef(sub, file) {
							continue // accepts anything: no constraint
						}
						ps, err := p.node(sub, file, subAt, stack, at)
						if err != nil {
							return nil, err
						}
						if empty(ps) {
							continue // a member made only of dropped constraints
						}
						alts = append(alts, ps)
						continue
					}
					ps, err := p.sub(sub, file, subAt, stack, at.alternative(conds[i]))
					if err != nil {
						return nil, err
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
				return nil, fmt.Errorf("projection: %s#%s: unknown keyword %s", file, at2, k)
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

// encode is the projection of the open value v at its place: a string
// holding the value as a JSON text, and the place listed.
func (p *projector) encode(v any, file, ptr string, at place) (any, error) {
	switch {
	case len(at.steps) == 0:
		return nil, fmt.Errorf("projection: %s#%s: the root accepts any JSON value", file, ptr)
	case at.untold || len(at.when) > 0:
		// An alternative of its own, or inside alternatives no const member
		// tells apart: the answer cannot say which place it fills.
		return nil, fmt.Errorf("projection: %s#%s: a value that accepts any JSON value where no const member tells its alternative apart", file, ptr)
	}
	loc := Location{Steps: at.steps}
	p.encoded[loc.String()] = loc
	desc := EncodedDescription
	if s, ok := v.(map[string]any); ok {
		if d, _ := s["description"].(string); d != "" {
			desc = d + " " + EncodedDescription
		}
	}
	return map[string]any{"type": "string", "description": desc}, nil
}

// deref follows $ref from v until a schema that is not a reference.
func (p *projector) deref(v any, file string) (any, string, bool) {
	for range 32 {
		m, ok := v.(map[string]any)
		if !ok {
			return v, file, true
		}
		ref, ok := m["$ref"].(string)
		if !ok {
			return v, file, true
		}
		target, tfile, _, err := p.resolve(ref, file)
		if err != nil {
			return nil, "", false
		}
		v, file = target, tfile
	}
	return nil, "", false
}

// openRef reports whether v, after its references, accepts any JSON value.
func (p *projector) openRef(v any, file string) bool {
	t, _, ok := p.deref(v, file)
	return ok && open(t)
}

// discriminator is, for each alternative of list, the condition that picks
// it: a member required in every alternative, with a const in each, all of
// them different; the first such member by name. Without one every
// condition is nil.
func (p *projector) discriminator(list []any, file string) []*Cond {
	conds := make([]*Cond, len(list))
	type alt struct {
		props    map[string]any
		required []any
	}
	alts := make([]alt, len(list))
	for i, v := range list {
		t, _, ok := p.deref(v, file)
		m, _ := t.(map[string]any)
		if !ok || m == nil {
			return conds
		}
		props, _ := m["properties"].(map[string]any)
		req, _ := m["required"].([]any)
		alts[i] = alt{props, req}
	}
	if len(alts) == 0 || alts[0].props == nil {
		return conds
	}
	names := make([]string, 0, len(alts[0].props))
	for name := range alts[0].props {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		values := make([]any, len(alts))
		seen := map[string]bool{}
		ok := true
		for i, a := range alts {
			ps, _ := a.props[name].(map[string]any)
			c, has := ps["const"]
			if !has || !slices.Contains(a.required, any(name)) {
				ok = false
				break
			}
			key, err := json.Marshal(c, json.Deterministic(true))
			if err != nil || seen[string(key)] {
				ok = false
				break
			}
			seen[string(key)] = true
			values[i] = c
		}
		if ok {
			for i := range conds {
				conds[i] = &Cond{Member: name, Const: values[i]}
			}
			return conds
		}
	}
	return conds
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
