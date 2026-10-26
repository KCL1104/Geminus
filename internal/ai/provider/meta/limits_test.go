package meta_test

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/meta"
	"github.com/kcl1104/geminus/internal/ai/provider/structured"
)

// The schema the adapter sends is inside what Meta documents for
// structured output (https://dev.meta.ai/docs/structured-output, "Stay
// within schema constraints", retrieved 2026-10-26; the Messages endpoint
// runs on the Responses pipeline, whose text.format these limits name):
// nesting depth 10 levels; 5,000 properties across the whole schema;
// 120,000 characters of property names, definition names, enum values and
// const values combined; 1,000 enum values; a string enum of more than 250
// values at most 15,000 characters; at most 200,000 nodes once $refs are
// inlined; no recursive schema. The docs do not say how the depth is
// counted: the guard counts every nested schema (each properties member,
// items, anyOf, allOf and oneOf alternative and followed $ref), stricter
// than counting objects only. Stricter than the page where it costs
// nothing (an empty schema, which the Anthropic API refused, PROPOSED
// D-121), the guard refuses schemas that say nothing of their value and
// objects that are not closed, which the projection never sends. The
// strict subset (root a plain object, no anyOf at the root) applies only
// to strict: true, which the Messages output_config.format has no field
// for; the adapter sends the default (non-strict) form.

func closed(props map[string]any) map[string]any {
	req := []any{}
	for _, k := range slices.Sorted(func(yield func(string) bool) {
		for k := range props {
			if !yield(k) {
				return
			}
		}
	}) {
		req = append(req, k)
	}
	return map[string]any{"type": "object", "properties": props, "required": req, "additionalProperties": false}
}

func nested(depth int) map[string]any {
	s := map[string]any{"type": "string"}
	for range depth - 1 {
		s = closed(map[string]any{"x": s})
	}
	return s
}

func TestTheGuard(t *testing.T) {
	str := map[string]any{"type": "string"}
	if v := meta.SchemaViolations(nested(10)); len(v) != 0 {
		t.Errorf("10 levels: %q", v)
	}
	manyProps := map[string]any{}
	for i := range 5001 {
		manyProps[fmt.Sprintf("p%04d", i)] = str
	}
	longNames := map[string]any{}
	for i := range 130 {
		longNames[fmt.Sprintf("%s%03d", strings.Repeat("n", 997), i)] = str
	}
	enum := func(n, size int) map[string]any {
		var vals []any
		for i := range n {
			vals = append(vals, fmt.Sprintf("%0*d", size, i))
		}
		return map[string]any{"enum": vals}
	}
	cyclic := closed(map[string]any{"a": str})
	cyclic["properties"].(map[string]any)["self"] = cyclic
	defs := map[string]any{"$defs": map[string]any{"n": closed(map[string]any{"child": map[string]any{"$ref": "#/$defs/n"}})}, "$ref": "#/$defs/n"}
	// $refs inlined: a definition of 2,000 alternatives (2,001 nodes)
	// referenced 120 times is 240,120 nodes, though the schema as written
	// holds some 2,100.
	var alts []any
	for range 2000 {
		alts = append(alts, map[string]any{"type": "string"})
	}
	var refs []any
	for range 120 {
		refs = append(refs, map[string]any{"$ref": "#/$defs/wide"})
	}
	expanded := map[string]any{"$defs": map[string]any{"wide": map[string]any{"anyOf": alts}}, "anyOf": refs}
	for name, c := range map[string]struct {
		schema any
		want   string
	}{
		"11 levels":                     {nested(11), "nested 11 levels"},
		"5001 properties":               {closed(manyProps), "5001 properties"},
		"130,000 characters of names":   {closed(longNames), "characters of names, enum and const values"},
		"1001 enum values":              {closed(map[string]any{"e": enum(1001, 1)}), "1001 enum values"},
		"a long string enum":            {closed(map[string]any{"e": enum(251, 60)}), "a string enum of 251 values"},
		"a recursive object":            {cyclic, "recursive"},
		"a recursive $ref":              {defs, "recursive"},
		"an external $ref":              {closed(map[string]any{"v": map[string]any{"$ref": "https://example.com/s.json"}}), "not a local reference"},
		"an empty schema":               {closed(map[string]any{"v": map[string]any{}}), "says nothing of its value"},
		"the true schema":               {closed(map[string]any{"v": true}), "not a schema object"},
		"an open object":                {map[string]any{"type": "object", "properties": map[string]any{"a": str}}, "not closed"},
		"too many nodes, $refs inlined": {expanded, "nodes once $refs are inlined"},
		// Mutation testing (checks/T-055_VALIDATION.md §5): each bound at
		// its edge, a keyword the guard does not know, recursion named as a
		// $ref's, and the depth of a $ref's target counted at the $ref.
		"a string enum one character over":   {closed(map[string]any{"e": map[string]any{"enum": append(enumOf(250, 60), "x")}}), "15001 characters, more than 15000"},
		"120,001 characters of names":        {closed(withName(names(120, 1000), "x")), "120001 characters of names"},
		"an unknown keyword":                 {closed(map[string]any{"v": map[string]any{"type": "string", "pattern": "^a$"}}), "/v/pattern: a keyword the guard does not know"},
		"a recursive $ref, named":            {defs, "a recursive schema ($ref #/$defs/n)"},
		"11 levels through a $ref":           {throughRef(), "nested 11 levels"},
		"5,001 properties across the schema": {closed(map[string]any{"a": closed(names(2500, 4)), "b": closed(names(2499, 4))}), "5001 properties"},
	} {
		got := meta.SchemaViolations(c.schema)
		if !slices.ContainsFunc(got, func(v string) bool { return strings.Contains(v, c.want) }) {
			t.Errorf("%s: %q, want one with %q", name, got, c.want)
		}
	}
	inside := closed(map[string]any{"s": str, "e": map[string]any{"enum": []any{"a", "b"}}, "c": map[string]any{"const": "x"},
		"a": map[string]any{"type": "array", "items": str}, "u": map[string]any{"anyOf": []any{str, map[string]any{"type": "null"}}}})
	if v := meta.SchemaViolations(map[string]any{"anyOf": []any{inside, closed(map[string]any{"k": map[string]any{"const": "y"}})}}); len(v) != 0 {
		t.Errorf("a schema inside the limits: %q", v)
	}
	// Each bound at its edge is inside.
	for name, s := range map[string]any{
		"5,000 properties":                   closed(map[string]any{"a": closed(names(2500, 4)), "b": closed(names(2498, 4))}),
		"1,000 enum values":                  closed(map[string]any{"e": map[string]any{"enum": enumOf(1000, 1)}}),
		"a string enum of 15,000 characters": closed(map[string]any{"e": map[string]any{"enum": append(enumOf(250, 60), "")}}),
		"120,000 characters of names":        closed(names(120, 1000)),
	} {
		if v := meta.SchemaViolations(s); len(v) != 0 {
			t.Errorf("%s: %q", name, v)
		}
	}
}

// names is n string properties whose names are size characters each.
func names(n, size int) map[string]any {
	out := map[string]any{}
	for i := range n {
		out[fmt.Sprintf("%0*d", size, i)] = map[string]any{"type": "string"}
	}
	return out
}

func withName(props map[string]any, name string) map[string]any {
	props[name] = map[string]any{"type": "string"}
	return props
}

// enumOf is n distinct values of size characters each.
func enumOf(n, size int) []any {
	var vals []any
	for i := range n {
		vals = append(vals, fmt.Sprintf("%0*d", size, i))
	}
	return vals
}

// throughRef is five levels whose innermost member is a $ref to a
// definition six levels deep: eleven levels once the $ref is followed.
func throughRef() map[string]any {
	var s any = map[string]any{"$ref": "#/$defs/d"}
	for range 5 {
		s = closed(map[string]any{"x": s})
	}
	root := s.(map[string]any)
	root["$defs"] = map[string]any{"d": nested(6)}
	return root
}

// Every schema the adapter can send is inside the limits: the projection
// of every prompt role's output schema (the approval serves modeler and
// scenario, PROPOSED D-112). How deep and large they are is recorded.
func TestEverySchemaTheAdapterSendsIsInsideTheLimits(t *testing.T) {
	for _, r := range provider.Roles {
		p, err := structured.Project(r.OutputSchema())
		if err != nil {
			t.Fatalf("%s: %v", r, err)
		}
		if v := meta.SchemaViolations(p.Schema); len(v) != 0 {
			t.Errorf("%s (%s): %q", r.OutputSchema(), r, v)
		}
		raw, _ := json.Marshal(p.Schema)
		if strings.Contains(string(raw), "{}") {
			t.Errorf("%s: an empty schema", r.OutputSchema())
		}
		t.Logf("%s: %d bytes, nesting %d", r.OutputSchema(), len(raw), meta.SchemaDepth(p.Schema))
	}
}
