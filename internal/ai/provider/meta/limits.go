package meta

import (
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
)

// Meta's documented limits of a structured-output schema
// (https://dev.meta.ai/docs/structured-output, "Stay within schema
// constraints", retrieved 2026-10-26: "Nesting depth | 10 levels", "Total
// properties | 5,000, across the whole schema", "Total string length |
// 120,000 characters (property names, definition names, enum values, and
// const values combined)", "Enum values | 1,000 by default", "Large string
// enums | A single string enum with more than 250 values is additionally
// capped at 15,000 combined characters", "Expanded size | A schema that
// expands beyond 200,000 nodes once $refs are inlined is rejected";
// "Recursive schemas ... returns HTTP 400"). The Messages endpoint runs on
// the Responses pipeline (/docs/protocols/messages), whose text.format the
// limits name.
const (
	maxSchemaDepth      = 10
	maxSchemaProperties = 5000
	maxSchemaChars      = 120_000
	maxEnumValues       = 1000
	largeEnumValues     = 250
	largeEnumChars      = 15_000
	maxSchemaNodes      = 200_000
)

// keywords are the schema keywords the guard knows: those the projection
// sends (internal/ai/provider/structured), and oneOf and the reference
// keywords. Another is a violation: stricter than the page, where that
// costs nothing.
var keywords = map[string]bool{"type": true, "properties": true, "required": true, "items": true, "enum": true, "const": true,
	"anyOf": true, "allOf": true, "oneOf": true, "additionalProperties": true, "description": true, "format": true,
	"$ref": true, "$defs": true, "definitions": true}

// saying are the keywords that say what a value is; a schema with none of
// them accepts any JSON value (the empty schema the Anthropic API refused;
// Meta's page does not say: refused here, PROPOSED D-118).
var saying = []string{"type", "enum", "const", "anyOf", "allOf", "oneOf", "$ref"}

type limitsGuard struct {
	root     map[string]any
	out      map[string]bool
	depth    int
	props    int
	chars    int
	enums    int
	nodes    int
	path     []uintptr // the schema objects on the way: recursion
	overflow bool
}

func (g *limitsGuard) add(format string, a ...any) { g.out[fmt.Sprintf(format, a...)] = true }

// SchemaViolations lists what in schema (a projected output schema, as it
// is sent) Meta's documented limits refuse, each as "pointer: reason",
// sorted; none for a schema inside them. The depth counts every nested
// schema (a properties member, items, an anyOf, allOf or oneOf alternative,
// a followed $ref target counted at its reference's level): the page does
// not say how it counts, and this is stricter than counting objects only.
func SchemaViolations(schema any) []string {
	g := walkLimits(schema)
	out := make([]string, 0, len(g.out))
	for v := range g.out {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// schemaDepth is how deeply schema nests, as SchemaViolations counts it.
func schemaDepth(schema any) int { return walkLimits(schema).depth }

func walkLimits(schema any) *limitsGuard {
	g := &limitsGuard{out: map[string]bool{}}
	g.root, _ = schema.(map[string]any)
	if g.root != nil {
		for _, k := range []string{"$defs", "definitions"} {
			defs, _ := g.root[k].(map[string]any)
			for name := range defs {
				g.chars += len(name)
			}
		}
	}
	g.walk(schema, "", 1, nil)
	switch {
	case g.depth > maxSchemaDepth:
		g.add(": nested %d levels, more than %d", g.depth, maxSchemaDepth)
	}
	if g.props > maxSchemaProperties {
		g.add(": %d properties, more than %d", g.props, maxSchemaProperties)
	}
	if g.chars > maxSchemaChars {
		g.add(": %d characters of names, enum and const values, more than %d", g.chars, maxSchemaChars)
	}
	if g.enums > maxEnumValues {
		g.add(": %d enum values, more than %d", g.enums, maxEnumValues)
	}
	if g.overflow {
		g.add(": more than %d nodes once $refs are inlined", maxSchemaNodes)
	}
	return g
}

func (g *limitsGuard) walk(v any, ptr string, depth int, refs []string) {
	if g.overflow {
		return
	}
	if g.nodes++; g.nodes > maxSchemaNodes {
		g.overflow = true
		return
	}
	g.depth = max(g.depth, depth)
	m, ok := v.(map[string]any)
	if !ok {
		g.add("%s: not a schema object", ptr)
		return
	}
	id := reflect.ValueOf(m).Pointer()
	if slices.Contains(g.path, id) {
		g.add("%s: a recursive schema (an object that holds itself)", ptr)
		return
	}
	g.path = append(g.path, id)
	defer func() { g.path = g.path[:len(g.path)-1] }()

	if !slices.ContainsFunc(saying, func(k string) bool { _, has := m[k]; return has }) {
		g.add("%s: the schema says nothing of its value", ptr)
	}
	_, hasProps := m["properties"]
	if (m["type"] == "object" || hasProps) && m["additionalProperties"] != false {
		g.add("%s: an object that is not closed", ptr)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		sub := m[k]
		at := ptr + "/" + k
		if !keywords[k] {
			g.add("%s: a keyword the guard does not know", at)
			continue
		}
		switch k {
		case "properties":
			props, ok := sub.(map[string]any)
			if !ok {
				g.add("%s: properties is not an object", at)
				continue
			}
			names := make([]string, 0, len(props))
			for name := range props {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				g.props++
				g.chars += len(name)
				g.walk(props[name], at+"/"+name, depth+1, refs)
			}
		case "items":
			g.walk(sub, at, depth+1, refs)
		case "anyOf", "allOf", "oneOf":
			list, ok := sub.([]any)
			if !ok || len(list) == 0 {
				g.add("%s: not a non-empty array", at)
				continue
			}
			for i, s := range list {
				g.walk(s, fmt.Sprintf("%s/%d", at, i), depth+1, refs)
			}
		case "enum":
			list, ok := sub.([]any)
			if !ok || len(list) == 0 {
				g.add("%s: not a non-empty array", at)
				continue
			}
			g.enums += len(list)
			chars, strs := 0, 0
			for _, e := range list {
				if s, ok := e.(string); ok {
					chars += len(s)
					strs++
				}
			}
			g.chars += chars
			if strs == len(list) && len(list) > largeEnumValues && chars > largeEnumChars {
				g.add("%s: a string enum of %d values and %d characters, more than %d", at, len(list), chars, largeEnumChars)
			}
		case "const":
			if s, ok := sub.(string); ok {
				g.chars += len(s)
			}
		case "$ref":
			ref, _ := sub.(string)
			frag, local := strings.CutPrefix(ref, "#")
			target, found := any(nil), false
			if local && (strings.HasPrefix(frag, "/$defs/") || strings.HasPrefix(frag, "/definitions/")) {
				target, found = lookup(g.root, frag)
			}
			switch {
			case !found:
				g.add("%s: $ref is not a local reference to a definition", at)
			case slices.Contains(refs, ref):
				g.add("%s: a recursive schema ($ref %s)", at, ref)
			default:
				g.walk(target, at, depth, append(slices.Clone(refs), ref))
			}
		}
	}
}

// lookup resolves a JSON Pointer fragment in doc.
func lookup(doc map[string]any, frag string) (any, bool) {
	var cur any = doc
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
