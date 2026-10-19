// Package contracttest checks Go types against the JSON Schemas in schemas/,
// which are the contract source of truth (AGENTS.md §1). Test-only: imported
// from _test.go files, never from production code.
package contracttest

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/model"
)

// Root is the repository root, found from the test's working directory.
func Root(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "schemas", "model-ir.schema.json")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repository root with schemas/ not found")
		}
		dir = parent
	}
}

// Schemas resolves $refs across schemas/*.schema.json.
type Schemas struct {
	t    testing.TB
	docs map[string]any // file name -> document
}

func LoadSchemas(t testing.TB) *Schemas {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(Root(t), "schemas", "*.schema.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no schemas: %v", err)
	}
	s := &Schemas{t: t, docs: map[string]any{}}
	for _, p := range paths {
		var doc any
		if err := json.Unmarshal(ReadFile(t, p), &doc); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		s.docs[filepath.Base(p)] = doc
	}
	return s
}

// Node is a schema location: the file it lives in and the JSON value.
type Node struct {
	File  string
	Value map[string]any
}

// At returns the schema at "file.schema.json#/json/pointer".
func (s *Schemas) At(ref string) Node {
	s.t.Helper()
	file, frag, _ := strings.Cut(ref, "#")
	return s.pointer(file, frag)
}

func (s *Schemas) pointer(file, frag string) Node {
	s.t.Helper()
	cur, ok := s.docs[file]
	if !ok {
		s.t.Fatalf("unknown schema file %q", file)
	}
	for _, tok := range strings.Split(strings.TrimPrefix(frag, "/"), "/") {
		if tok == "" {
			continue
		}
		tok = strings.NewReplacer("~1", "/", "~0", "~").Replace(tok)
		switch c := cur.(type) {
		case map[string]any:
			cur = c[tok]
		case []any:
			var i int
			for _, ch := range tok {
				i = i*10 + int(ch-'0')
			}
			cur = c[i]
		}
		if cur == nil {
			s.t.Fatalf("%s#%s: no such location", file, frag)
		}
	}
	m, ok := cur.(map[string]any)
	if !ok {
		s.t.Fatalf("%s#%s is not a schema object", file, frag)
	}
	return s.deref(Node{file, m})
}

// deref follows $ref chains (local or to a sibling schema file).
func (s *Schemas) deref(n Node) Node {
	for {
		ref, ok := n.Value["$ref"].(string)
		if !ok {
			return n
		}
		file, frag, _ := strings.Cut(ref, "#")
		if file == "" {
			file = n.File
		}
		n = s.pointer(file, frag)
	}
}

// branches expands oneOf/anyOf/allOf into the alternatives that describe values,
// dropping the {"type":"null"} branch of nullable fields.
func (s *Schemas) branches(n Node) []Node {
	n = s.deref(n)
	var out []Node
	for _, key := range []string{"oneOf", "anyOf"} {
		if alts, ok := n.Value[key].([]any); ok {
			for _, a := range alts {
				b := s.deref(Node{n.File, a.(map[string]any)})
				if b.Value["type"] == "null" {
					continue
				}
				out = append(out, s.branches(b)...)
			}
		}
	}
	// Every allOf member constrains the same value, so its shape counts too
	// (proposal-output's changes: allOf [json-patch, {minItems: 1}], T-024).
	if all, ok := n.Value["allOf"].([]any); ok {
		for _, a := range all {
			out = append(out, s.branches(Node{n.File, a.(map[string]any)})...)
		}
	}
	if _, ok := n.Value["properties"]; ok || len(out) == 0 {
		out = append([]Node{n}, out...)
	}
	return out
}

// Parity fails t when the JSON member names of Go type typ differ from the
// schema's properties at ref, recursively through nested objects, arrays and
// maps. Discriminated unions compare against the union of their branches.
func (s *Schemas) Parity(t testing.TB, typ reflect.Type, ref string) {
	t.Helper()
	s.parity(t, typ, s.At(ref), ref)
}

var rawJSON = reflect.TypeFor[jsontext.Value]()

func (s *Schemas) parity(t testing.TB, typ reflect.Type, n Node, where string) {
	t.Helper()
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ == rawJSON {
		return // untrusted container or arbitrary patch value; schema decides
	}
	alts := s.branches(n)
	switch typ.Kind() {
	case reflect.Struct:
		props := map[string][]Node{}
		for _, b := range alts {
			if p, ok := b.Value["properties"].(map[string]any); ok {
				for name, sub := range p {
					props[name] = append(props[name], Node{b.File, sub.(map[string]any)})
				}
			}
		}
		fields := jsonFields(typ)
		if got, want := keys(fields), keys(props); !slices.Equal(got, want) {
			t.Errorf("%s: Go %s has members %v, schema has %v", where, typ, got, want)
			return
		}
		for name, f := range fields {
			for _, sub := range props[name] {
				s.parity(t, f, sub, where+"/"+name)
			}
		}
	case reflect.Slice:
		for _, b := range alts {
			if items, ok := b.Value["items"].(map[string]any); ok {
				s.parity(t, typ.Elem(), Node{b.File, items}, where+"/[]")
				return
			}
		}
		t.Errorf("%s: Go %s is a slice, schema has no items", where, typ)
	case reflect.Map:
		for _, b := range alts {
			if ap, ok := b.Value["additionalProperties"].(map[string]any); ok {
				s.parity(t, typ.Elem(), Node{b.File, ap}, where+"/{}")
				return
			}
		}
		t.Errorf("%s: Go %s is a map, schema has no additionalProperties schema", where, typ)
	default:
		if want := scalarType(alts); want != "" && !scalarMatches(typ.Kind(), want) {
			t.Errorf("%s: Go kind %s does not fit schema type %q", where, typ.Kind(), want)
		}
	}
}

func jsonFields(typ reflect.Type) map[string]reflect.Type {
	out := map[string]reflect.Type{}
	for i := range typ.NumField() {
		f := typ.Field(i)
		if !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		out[name] = f.Type
	}
	return out
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func scalarType(alts []Node) string {
	for _, b := range alts {
		if ty, ok := b.Value["type"].(string); ok {
			return ty
		}
		if c, ok := b.Value["const"]; ok {
			switch c.(type) {
			case string:
				return "string"
			case float64:
				return "number"
			case bool:
				return "boolean"
			}
		}
	}
	return ""
}

func scalarMatches(k reflect.Kind, schemaType string) bool {
	switch schemaType {
	case "string":
		return k == reflect.String
	case "integer":
		return k >= reflect.Int && k <= reflect.Uint64
	case "number":
		return k == reflect.Float64 || (k >= reflect.Int && k <= reflect.Uint64)
	case "boolean":
		return k == reflect.Bool
	}
	return false
}

// Consts returns the const values of a discriminator across oneOf branches,
// e.g. Consts("model-ir.schema.json#/properties/nodes/items", "type").
func (s *Schemas) Consts(ref, member string) []string {
	var out []string
	for _, b := range s.branches(s.At(ref)) {
		if p, ok := b.Value["properties"].(map[string]any); ok {
			if m, ok := p[member].(map[string]any); ok {
				if c, ok := m["const"].(string); ok {
					out = append(out, c)
				}
			}
		}
	}
	return out
}

// RoundTrip decodes data strictly into a fresh *T, re-encodes it and fails t
// unless the result is the same JSON value (member order and number spelling
// aside). This proves the Go type neither drops nor invents members.
func RoundTrip[T any](t testing.TB, name string, data []byte) {
	t.Helper()
	var v T
	if err := model.Decode(data, &v); err != nil {
		t.Errorf("%s: strict decode into %T: %v", name, v, err)
		return
	}
	out, err := model.Encode(v)
	if err != nil {
		t.Errorf("%s: encode: %v", name, err)
		return
	}
	var want, got any
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("%s: re-encoded output is not JSON: %v", name, err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("%s: round trip through %T changed the document\nwant %s\n got %s", name, v, data, out)
	}
}

// Case is one entry of testdata/contracts/cases.json.
type Case struct {
	ID       string `json:"id"`
	Instance string `json:"instance"`
	Schema   string `json:"schema"`
	Valid    bool   `json:"valid"`
	Layer    string `json:"layer"`
}

// Cases returns the contract fixtures index.
func Cases(t testing.TB) []Case {
	t.Helper()
	var idx struct {
		Cases []Case `json:"cases"`
	}
	if err := json.Unmarshal(ReadFile(t, filepath.Join(Root(t), "testdata", "contracts", "cases.json")), &idx,
		json.RejectUnknownMembers(false)); err != nil {
		t.Fatal(err)
	}
	return idx.Cases
}

func ReadFile(t testing.TB, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// OnlyDeps fails t when the package in the working directory transitively
// imports anything outside the standard library and the allowed packages.
func OnlyDeps(t testing.TB, allowed ...string) {
	t.Helper()
	cmd := exec.Command("go", "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", ".")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v: %s", err, stderr.String())
	}
	self := strings.TrimSpace(string(must(exec.Command("go", "list", ".").Output())))
	for _, dep := range strings.Fields(string(out)) {
		if dep != self && !slices.Contains(allowed, dep) {
			t.Errorf("%s must not depend on %s (AGENTS.md §2: domain types stay free of HTTP/DB/LLM/UI)", self, dep)
		}
	}
	std := must(exec.Command("go", "list", "-deps", "-f", "{{if .Standard}}{{.ImportPath}}{{end}}", ".").Output())
	for _, dep := range strings.Fields(string(std)) {
		if slices.Contains(forbiddenStd, dep) {
			t.Errorf("%s must not depend on %s: domain types do no I/O", self, dep)
		}
	}
}

// Standard-library packages that mean network, database or process I/O.
var forbiddenStd = []string{"net", "net/http", "database/sql", "os/exec"}

func must(b []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return b
}
