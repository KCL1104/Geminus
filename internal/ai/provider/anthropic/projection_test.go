package anthropic

import (
	"bytes"
	"encoding/json/v2"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/kcl1104/geminus/internal/contracttest"
)

var outputSchemas = []string{"proposal-output.schema.json", "analysis-output.schema.json", "ai-mapping.schema.json"}

// supported is the subset the projection may emit.
var supported = map[string]bool{"type": true, "properties": true, "required": true, "items": true, "enum": true, "const": true,
	"anyOf": true, "allOf": true, "additionalProperties": true, "description": true, "format": true}

func walk(t *testing.T, file string, v any, path string) {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("%s %s: not a schema object", file, path)
	}
	for k, sub := range m {
		if !supported[k] {
			t.Errorf("%s %s: keyword %s outside the subset", file, path, k)
		}
		switch k {
		case "properties":
			for name, s := range sub.(map[string]any) {
				walk(t, file, s, path+"/properties/"+name)
			}
		case "items":
			walk(t, file, sub, path+"/items")
		case "anyOf", "allOf":
			for i, s := range sub.([]any) {
				walk(t, file, s, path+"/"+k+"/"+string(rune('0'+i)))
			}
		}
	}
	if _, has := m["properties"]; has || m["type"] == "object" {
		if m["additionalProperties"] != false {
			t.Errorf("%s %s: an open object", file, path)
		}
	}
}

func compile(t *testing.T, schema map[string]any) *jsonschema.Schema {
	t.Helper()
	data, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	if err := c.AddResource("https://projection.test/schema.json", doc); err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile("https://projection.test/schema.json")
	if err != nil {
		t.Fatalf("the projection is not a valid schema: %v", err)
	}
	return s
}

// Each output schema projects into the subset: self-contained, closed
// objects, no dropped keyword left. The projection only loosens, but for
// its one encoding (AI_SPEC §2: an open value is asked for as a JSON text
// in a string): every fixture valid under the canonical schema, with its
// open values so encoded, is valid under it and decodes back to itself,
// and a structurally invalid answer (extra member, wrong status, missing
// member) is invalid under both.
func TestProjection(t *testing.T) {
	root := contracttest.Root(t)
	for _, file := range outputSchemas {
		p, err := Project(file)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if _, choice := p.Schema["anyOf"]; p.Schema["type"] != "object" && !choice {
			t.Errorf("%s: root type %v", file, p.Schema["type"])
		}
		walk(t, file, p.Schema, "")
		raw, _ := json.Marshal(p.Schema, json.Deterministic(true))
		if strings.Contains(string(raw), "$ref") || strings.Contains(string(raw), "oneOf") {
			t.Errorf("%s: a $ref or oneOf survived", file)
		}
		again, _ := Project(file)
		raw2, _ := json.Marshal(again.Schema, json.Deterministic(true))
		if !bytes.Equal(raw, raw2) || !slices.Equal(p.Dropped, again.Dropped) {
			t.Errorf("%s: not deterministic", file)
		}
		if len(p.Dropped) == 0 || !slices.IsSorted(p.Dropped) {
			t.Errorf("%s: dropped %v", file, p.Dropped)
		}
		for _, d := range p.Dropped {
			kw := d[strings.LastIndex(d, "/")+1:]
			if !dropped[kw] && kw != "format" {
				t.Errorf("%s: dropped a keyword that is not a constraint: %s", file, d)
			}
		}
		s := compile(t, p.Schema)
		n := 0
		for _, c := range contracttest.Cases(t) {
			if c.Schema != "schemas/"+file {
				continue
			}
			data := contracttest.ReadFile(t, filepath.Join(root, c.Instance))
			var doc any
			if err := json.Unmarshal(data, &doc); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(encodeAt(p, doc))
			if err != nil {
				t.Fatal(err)
			}
			inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(encoded))
			if err != nil {
				t.Fatal(err)
			}
			err = s.Validate(inst)
			if c.Valid && err != nil {
				t.Errorf("%s: valid fixture %s fails the projection: %v", file, c.ID, err)
			}
			if c.Valid {
				decoded, issues := p.Decode(encoded)
				var back any
				if len(issues) > 0 || json.Unmarshal(decoded, &back) != nil || !reflect.DeepEqual(back, doc) {
					t.Errorf("%s: valid fixture %s does not decode back to itself: %q %s", file, c.ID, issues, decoded)
				}
			}
			if !c.Valid && err == nil && !strings.Contains(c.ID, "confirmation") && !strings.Contains(c.ID, "low-confidence") {
				// The projection keeps closed objects, consts and enums, so
				// every negative fixture fails it too, except those that
				// break a dropped conditional (confidence < 0.8 needs
				// confirmation), which the canonical schema still catches.
				t.Errorf("%s: invalid fixture %s passes the projection", file, c.ID)
			}
			n++
		}
		if n == 0 {
			t.Errorf("%s: no fixture checked", file)
		}
	}
}

// What the subset cannot say is refused, never silently widened.
func TestProjectionRefuses(t *testing.T) {
	docs := map[string]any{
		"open.json":      map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string"}}},
		"open2.json":     map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": true},
		"schemaAP.json":  map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": map[string]any{"type": "string"}},
		"pattern.json":   map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false, "patternProperties": map[string]any{"^x": map[string]any{}}},
		"recursive.json": map[string]any{"type": "object", "properties": map[string]any{"child": map[string]any{"$ref": "#"}}, "additionalProperties": false},
		"remote.json":    map[string]any{"$ref": "https://example.com/x.json"},
		"false.json":     map[string]any{"type": "object", "properties": map[string]any{"a": false}, "additionalProperties": false},
		"unknown.json":   map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false, "x-custom": 1},
		"array.json":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		// An open value is encoded only where its place can be told apart:
		// not inside alternatives without a const member that does it, not
		// as an alternative of its own, not at the root.
		"untold.json": closedObject(map[string]any{"v": map[string]any{"anyOf": []any{
			closedObject(map[string]any{"a": map[string]any{}}), closedObject(map[string]any{"b": map[string]any{"type": "string"}})}}}),
		"open-alternative.json": closedObject(map[string]any{"v": map[string]any{"anyOf": []any{map[string]any{"type": "string"}, map[string]any{}}}}),
		"open-root.json":        map[string]any{},
		// Review round 1, L1 (R2): a const member tells the alternatives apart
		// only when every alternative requires it (an answer that leaves it out
		// matches the alternative that does not) and each has its own const.
		"optional-const.json": closedObject(map[string]any{"v": map[string]any{"anyOf": []any{
			closedObject(map[string]any{"kind": map[string]any{"const": "a"}, "x": map[string]any{}}),
			map[string]any{"type": "object", "additionalProperties": false, "required": []any{"y"},
				"properties": map[string]any{"kind": map[string]any{"const": "b"}, "y": map[string]any{"type": "string"}}}}}}),
		"same-const.json": closedObject(map[string]any{"v": map[string]any{"anyOf": []any{
			closedObject(map[string]any{"kind": map[string]any{"const": "a"}, "x": map[string]any{}}),
			closedObject(map[string]any{"kind": map[string]any{"const": "a"}, "y": map[string]any{"type": "string"}})}}}),
		// A schema left with no constraint the subset takes (only dropped
		// ones) is not an open value of the canonical schema: refused, never
		// sent as an empty schema nor encoded.
		"emptied.json": closedObject(map[string]any{"v": map[string]any{"maxLength": 3.0}}),
		// A description does not say what the value is either (review round
		// 1, L1, R8).
		"emptied-described.json": closedObject(map[string]any{"v": map[string]any{"maxLength": 3.0, "description": "A short text."}}),
	}
	for name := range docs {
		if _, err := project(docs, name); err == nil {
			t.Errorf("%s: projected", name)
		}
	}
	if _, err := Project("no-such.schema.json"); err == nil {
		t.Error("an unknown schema projected")
	}
}
