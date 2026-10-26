package anthropic

import (
	"encoding/json/v2"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/schemas"
)

// The official limits of structured outputs (JSON outputs and strict tool
// use share them), from
// https://platform.claude.com/docs/en/build-with-claude/structured-outputs
// ("JSON Schema limitations", retrieved 2026-10-26), checked on the schema
// the adapter sends, apart from the projector's own code:
//   - supported: the basic types; enum of strings, numbers, booleans or
//     null; const; anyOf and allOf (allOf with $ref not supported); local
//     $ref, $defs and definitions; default; required; additionalProperties
//     (false for every object); the string formats date-time, time, date,
//     duration, email, hostname, uri, ipv4, ipv6, uuid; minItems 0 or 1;
//   - not supported: recursive schemas, complex types within enums,
//     external $ref, numerical constraints, string constraints, other array
//     constraints, additionalProperties other than false; an unsupported
//     feature is a 400 with details;
//   - complexity: at most 24 optional parameters and 16 parameters with
//     union types (anyOf or a type array) across the request's schemas.
//
// The page does not mention the empty schema; the API's answer does: the
// live smoke's count_tokens diagnosis (2026-10-26) returned 400
// invalid_request_error "output_config.format.schema: Empty schema ({})
// that accepts any JSON value is not supported. Please specify a concrete
// type." for proposal-output's projection, whose JSON Patch values were
// {}. The guard is stricter than the page where that costs nothing: it
// also refuses pattern (supported only for simple regexes) and title,
// which the projection never sends.

var guardKeywords = map[string]bool{"type": true, "properties": true, "required": true, "items": true, "enum": true,
	"const": true, "anyOf": true, "allOf": true, "additionalProperties": true, "description": true, "format": true,
	"$ref": true, "$defs": true, "definitions": true, "default": true, "minItems": true}

// constraining keywords: a schema with none of them accepts any JSON value.
var constraining = map[string]bool{"type": true, "enum": true, "const": true, "anyOf": true, "allOf": true, "$ref": true}

var guardTypes = map[string]bool{"object": true, "array": true, "string": true, "integer": true, "number": true, "boolean": true, "null": true}

var guardFormats = map[string]bool{"date-time": true, "time": true, "date": true, "duration": true, "email": true, "hostname": true,
	"uri": true, "ipv4": true, "ipv6": true, "uuid": true}

const (
	maxOptional = 24
	maxUnions   = 16
)

type guard struct {
	root     map[string]any
	out      []string
	optional int
	unions   int
	maps     []uintptr // the schema objects on the current path
}

// SubsetViolations lists what in schema the official limits reject, each
// as "pointer: reason", sorted; none for a schema inside them.
func SubsetViolations(schema any) []string {
	g := &guard{}
	g.root, _ = schema.(map[string]any)
	g.check(schema, "", nil)
	if g.optional > maxOptional {
		g.add("", fmt.Sprintf("%d optional parameters, more than %d", g.optional, maxOptional))
	}
	if g.unions > maxUnions {
		g.add("", fmt.Sprintf("%d parameters with union types, more than %d", g.unions, maxUnions))
	}
	sort.Strings(g.out)
	return g.out
}

func (g *guard) add(path, reason string) { g.out = append(g.out, path+": "+reason) }

func (g *guard) check(v any, path string, refs []string) {
	m, ok := v.(map[string]any)
	if !ok {
		g.add(path, fmt.Sprintf("a %T schema (only schema objects are accepted)", v))
		return
	}
	id := reflect.ValueOf(m).Pointer()
	if slices.Contains(g.maps, id) {
		g.add(path, "a recursive schema (an object that contains itself)")
		return
	}
	g.maps = append(g.maps, id)
	defer func() { g.maps = g.maps[:len(g.maps)-1] }()

	open := true
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		sub := m[k]
		at := path + "/" + k
		if !guardKeywords[k] {
			g.add(path, "unsupported keyword "+k)
			continue
		}
		if constraining[k] {
			open = false
		}
		switch k {
		case "type":
			switch t := sub.(type) {
			case string:
				if !guardTypes[t] {
					g.add(at, "unknown type "+t)
				}
			case []any:
				g.unions++
				for _, e := range t {
					if s, _ := e.(string); !guardTypes[s] {
						g.add(at, fmt.Sprintf("unknown type %v", e))
					}
				}
			default:
				g.add(at, "type is neither a string nor an array")
			}
		case "properties":
			props, ok := sub.(map[string]any)
			if !ok {
				g.add(at, "properties is not an object")
				continue
			}
			for _, name := range sortedKeys(props) {
				g.check(props[name], at+"/"+name, refs)
			}
		case "items":
			g.check(sub, at, refs)
		case "anyOf", "allOf":
			list, ok := sub.([]any)
			if !ok || len(list) == 0 {
				g.add(at, k+" is not a non-empty array")
				continue
			}
			if k == "anyOf" {
				g.unions++
			}
			for i, s := range list {
				if sm, ok := s.(map[string]any); ok && k == "allOf" {
					if _, ref := sm["$ref"]; ref {
						g.add(fmt.Sprintf("%s/%d", at, i), "allOf with $ref")
					}
				}
				g.check(s, fmt.Sprintf("%s/%d", at, i), refs)
			}
		case "additionalProperties":
			if sub != false {
				g.add(at, "additionalProperties other than false")
			}
		case "enum":
			list, ok := sub.([]any)
			if !ok || len(list) == 0 {
				g.add(at, "enum is not a non-empty array")
				continue
			}
			for _, e := range list {
				switch e.(type) {
				case string, float64, bool, nil:
				default:
					g.add(at, "a complex type within an enum")
				}
			}
		case "format":
			if f, _ := sub.(string); !guardFormats[f] {
				g.add(at, fmt.Sprintf("unsupported format %v", sub))
			}
		case "minItems":
			if n, _ := sub.(float64); n != 0 && n != 1 {
				g.add(at, fmt.Sprintf("minItems %v (only 0 and 1)", sub))
			}
		case "required":
			if _, ok := sub.([]any); !ok {
				g.add(at, "required is not an array")
			}
		case "description":
			if _, ok := sub.(string); !ok {
				g.add(at, "description is not a string")
			}
		case "$ref":
			ref, _ := sub.(string)
			frag, local := strings.CutPrefix(ref, "#")
			if !local || !(strings.HasPrefix(frag, "/$defs/") || strings.HasPrefix(frag, "/definitions/")) {
				g.add(at, fmt.Sprintf("$ref %q is not a local $defs or definitions reference", ref))
				continue
			}
			if slices.Contains(refs, ref) {
				g.add(at, "a recursive schema ($ref "+ref+")")
				continue
			}
			target, ok := pointerGet(g.root, frag)
			if !ok {
				g.add(at, "$ref "+ref+" resolves to nothing")
				continue
			}
			g.check(target, at, append(slices.Clone(refs), ref))
		}
	}
	if open {
		g.add(path, "an empty schema accepts any JSON value")
	}
	props, hasProps := m["properties"].(map[string]any)
	if (m["type"] == "object" || hasProps) && m["additionalProperties"] != false {
		g.add(path, "an object without additionalProperties: false")
	}
	required := map[string]bool{}
	list, _ := m["required"].([]any)
	for _, r := range list {
		if s, ok := r.(string); ok {
			required[s] = true
		}
	}
	for name := range props {
		if !required[name] {
			g.optional++
		}
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// closedObject is an object of props, all required.
func closedObject(props map[string]any) map[string]any {
	req := []any{}
	for _, k := range sortedKeys(props) {
		req = append(req, k)
	}
	return map[string]any{"type": "object", "properties": props, "required": req, "additionalProperties": false}
}

// The guard refuses what the limits refuse, each for its reason, and takes
// a schema inside them.
func TestTheSubsetGuard(t *testing.T) {
	str := map[string]any{"type": "string"}
	unions, typeArrays, optional := map[string]any{}, map[string]any{}, map[string]any{}
	for i := range maxUnions + 1 {
		unions[fmt.Sprintf("u%02d", i)] = map[string]any{"anyOf": []any{str, map[string]any{"type": "null"}}}
		// a type array is a union parameter too (review round 1, L1, R10)
		typeArrays[fmt.Sprintf("t%02d", i)] = map[string]any{"type": []any{"string", "null"}}
	}
	for i := range maxOptional + 1 {
		optional[fmt.Sprintf("o%02d", i)] = str
	}
	cyclic := closedObject(map[string]any{"a": str})
	cyclic["properties"].(map[string]any)["self"] = cyclic
	defs := func(def map[string]any) map[string]any {
		return map[string]any{"$defs": map[string]any{"n": def}, "$ref": "#/$defs/n"}
	}
	for name, c := range map[string]struct {
		schema any
		want   string
	}{
		"an empty schema":                  {closedObject(map[string]any{"v": map[string]any{}}), "/properties/v: an empty schema"},
		"a description only":               {closedObject(map[string]any{"v": map[string]any{"description": "any JSON value"}}), "/properties/v: an empty schema"},
		"an empty array item":              {closedObject(map[string]any{"v": map[string]any{"type": "array", "items": map[string]any{}}}), "/properties/v/items: an empty schema"},
		"an empty anyOf member":            {closedObject(map[string]any{"v": map[string]any{"anyOf": []any{str, map[string]any{}}}}), "/properties/v/anyOf/1: an empty schema"},
		"the true schema":                  {closedObject(map[string]any{"v": true}), "/properties/v: a bool schema"},
		"an open object":                   {map[string]any{"type": "object", "properties": map[string]any{"a": str}, "required": []any{"a"}}, ": an object without additionalProperties: false"},
		"an object without members":        {map[string]any{"type": "object"}, ": an object without additionalProperties: false"},
		"additionalProperties true":        {map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": true}, "additionalProperties other than false"},
		"additionalProperties as a schema": {map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": str}, "additionalProperties other than false"},
		"minLength":                        {closedObject(map[string]any{"v": map[string]any{"type": "string", "minLength": 1.0}}), "unsupported keyword minLength"},
		"maxLength":                        {closedObject(map[string]any{"v": map[string]any{"type": "string", "maxLength": 9.0}}), "unsupported keyword maxLength"},
		"maximum":                          {closedObject(map[string]any{"v": map[string]any{"type": "number", "maximum": 1.0}}), "unsupported keyword maximum"},
		"multipleOf":                       {closedObject(map[string]any{"v": map[string]any{"type": "number", "multipleOf": 2.0}}), "unsupported keyword multipleOf"},
		"pattern":                          {closedObject(map[string]any{"v": map[string]any{"type": "string", "pattern": "^/"}}), "unsupported keyword pattern"},
		"maxItems":                         {closedObject(map[string]any{"v": map[string]any{"type": "array", "items": str, "maxItems": 3.0}}), "unsupported keyword maxItems"},
		"minItems 2":                       {closedObject(map[string]any{"v": map[string]any{"type": "array", "items": str, "minItems": 2.0}}), "minItems 2"},
		"oneOf":                            {closedObject(map[string]any{"v": map[string]any{"oneOf": []any{str}}}), "unsupported keyword oneOf"},
		"an unknown keyword":               {closedObject(map[string]any{"v": map[string]any{"type": "string", "x-custom": 1.0}}), "unsupported keyword x-custom"},
		"recursion through $ref":           {defs(closedObject(map[string]any{"child": map[string]any{"$ref": "#/$defs/n"}})), "a recursive schema ($ref #/$defs/n)"},
		"an object that holds itself":      {cyclic, "a recursive schema (an object that contains itself)"},
		"an external $ref":                 {closedObject(map[string]any{"v": map[string]any{"$ref": "https://example.com/x.json"}}), "is not a local $defs or definitions reference"},
		"a $ref to nothing":                {closedObject(map[string]any{"v": map[string]any{"$ref": "#/$defs/missing"}}), "resolves to nothing"},
		"allOf with $ref":                  {defs(closedObject(map[string]any{"v": map[string]any{"allOf": []any{map[string]any{"$ref": "#/$defs/s"}}}})), "allOf with $ref"},
		"a complex enum":                   {closedObject(map[string]any{"v": map[string]any{"enum": []any{map[string]any{"a": 1.0}}}}), "a complex type within an enum"},
		"an unsupported format":            {closedObject(map[string]any{"v": map[string]any{"type": "string", "format": "regex"}}), "unsupported format regex"},
		"an unknown type":                  {closedObject(map[string]any{"v": map[string]any{"type": "text"}}), "unknown type text"},
		"too many unions":                  {closedObject(unions), "17 parameters with union types"},
		"too many type-array unions":       {closedObject(typeArrays), "17 parameters with union types"},
		"too many optional members":        {map[string]any{"type": "object", "properties": optional, "additionalProperties": false}, "25 optional parameters"},
	} {
		got := SubsetViolations(c.schema)
		if !slices.ContainsFunc(got, func(v string) bool { return strings.Contains(v, c.want) }) {
			t.Errorf("%s: violations %q, want one with %q", name, got, c.want)
		}
	}
	inside := closedObject(map[string]any{
		"s": map[string]any{"type": "string", "format": "uuid", "description": "an ID"},
		"n": map[string]any{"anyOf": []any{map[string]any{"type": "number"}, map[string]any{"type": "null"}}},
		"e": map[string]any{"enum": []any{"a", "b", 1.0, true, nil}},
		"c": map[string]any{"const": true},
		"a": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "minItems": 1.0},
		"t": map[string]any{"type": []any{"string", "null"}},
		"o": closedObject(map[string]any{"x": map[string]any{"allOf": []any{str}}}),
	})
	if got := SubsetViolations(inside); len(got) != 0 {
		t.Errorf("a schema inside the limits: %q", got)
	}
}

// Every schema the adapter can send is inside the limits: the projection
// of the output schema of every prompt role (provider.Roles; the approval
// serves modeler and scenario, PROPOSED D-112) and of every other schema
// in schemas/ that projects at all. Before this guard, proposal-output's
// projection held three empty schemas (the JSON Patch values of add,
// replace and test), and the live smoke was refused for them.
func TestEverySchemaTheAdapterSendsIsInsideTheLimits(t *testing.T) {
	named := map[string]bool{}
	for _, r := range provider.Roles {
		file := r.OutputSchema()
		named[file] = true
		p, err := Project(file)
		if err != nil {
			t.Errorf("%s (%s): %v", file, r, err)
			continue
		}
		if v := SubsetViolations(p.Schema); len(v) > 0 {
			t.Errorf("%s (role %s): %d violation(s): %q", file, r, len(v), v)
		}
		raw, _ := json.Marshal(p.Schema, json.Deterministic(true))
		if strings.Contains(string(raw), "{}") {
			t.Errorf("%s (role %s): an empty schema in %s", file, r, raw)
		}
	}
	if len(named) != 3 {
		t.Errorf("the roles name %d output schemas", len(named))
	}
	entries, err := schemas.FS.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".schema.json") || named[e.Name()] {
			continue
		}
		p, err := Project(e.Name())
		if err != nil {
			continue // not an output schema: the projection refuses it
		}
		if v := SubsetViolations(p.Schema); len(v) > 0 {
			t.Errorf("%s projects outside the limits: %q", e.Name(), v)
		}
	}
}
