package mapping_test

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/import/mapping"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
)

func field(f mapping.Field) *mapping.Field { return &f }

func entry(column string, f mapping.Field, t mapping.Transform) mapping.Entry {
	return mapping.Entry{SourceColumn: column, TargetField: field(f), Transform: t}
}

func unmapped(column string) mapping.Entry {
	return mapping.Entry{SourceColumn: column, Transform: mapping.Identity}
}

// A synthetic station log (ELECTRONICS_LINE §6): the operator column is
// not imported.
var stationColumns = []string{"序號", "紀錄類型", "站別", "開始", "結束", "結果", "班別", "機種", "耗時毫秒", "重測", "批號", "作業員"}

func station() mapping.Mapping {
	return mapping.Mapping{Mappings: []mapping.Entry{
		entry("序號", mapping.CaseID, mapping.Identity),
		entry("紀錄類型", mapping.EventType, mapping.Identity),
		entry("站別", mapping.Stage, mapping.Identity),
		entry("開始", mapping.Timestamp, mapping.RFC3339),
		entry("結束", mapping.EndTimestamp, mapping.RFC3339),
		entry("結果", mapping.Outcome, mapping.Identity),
		entry("班別", mapping.Shift, mapping.Identity),
		entry("機種", mapping.Product, mapping.Identity),
		entry("耗時毫秒", mapping.DurationMS, mapping.IntegerMS),
		entry("重測", mapping.IsRework, mapping.BooleanLiteral),
		entry("批號", mapping.Metadata, mapping.Identity),
		unmapped("作業員"),
	}}
}

func schemaEnum(t *testing.T, s *contracttest.Schemas, ref string) []string {
	t.Helper()
	var out []string
	for _, v := range s.At(ref).Value["enum"].([]any) {
		out = append(out, v.(string))
	}
	return out
}

// The vocabulary is the schemas': the target fields and transforms of
// import-mapping.schema.json, the required fields and text lengths of
// canonical-event.schema.json, and the one transform each field takes
// (the schema's allOf, checked by the JSON Schema validator itself).
func TestVocabularyIsTheSchemas(t *testing.T) {
	s := contracttest.LoadSchemas(t)
	var fields, transforms []string
	for _, f := range mapping.Fields {
		fields = append(fields, string(f))
	}
	for _, x := range mapping.Transforms {
		transforms = append(transforms, string(x))
	}
	if want := schemaEnum(t, s, "import-mapping.schema.json#/$defs/TargetField"); !slices.Equal(fields, want) {
		t.Errorf("fields %v, schema %v", fields, want)
	}
	if want := schemaEnum(t, s, "import-mapping.schema.json#/$defs/Transform"); !slices.Equal(transforms, want) {
		t.Errorf("transforms %v, schema %v", transforms, want)
	}
	event := s.At("canonical-event.schema.json#")
	var required []string
	for _, r := range event.Value["required"].([]any) {
		required = append(required, r.(string))
	}
	var ours []string
	for _, f := range mapping.Required {
		ours = append(ours, string(f))
	}
	if !slices.Equal(ours, required) {
		t.Errorf("required %v, schema %v", ours, required)
	}
	props := event.Value["properties"].(map[string]any)
	for _, f := range mapping.Fields {
		p := props[string(f)].(map[string]any)
		want := 0
		if p["type"] == "string" && p["format"] == nil {
			want = int(p["maxLength"].(float64))
		}
		if got := mapping.MaxChars(f); got != want {
			t.Errorf("%s: %d characters, schema %d", f, got, want)
		}
	}
	for _, f := range append([]*mapping.Field{nil}, func() []*mapping.Field {
		var out []*mapping.Field
		for _, f := range mapping.Fields {
			out = append(out, field(f))
		}
		return out
	}()...) {
		for _, x := range mapping.Transforms {
			doc, err := json.Marshal(mapping.Entry{SourceColumn: "c", TargetField: f, Transform: x})
			if err != nil {
				t.Fatal(err)
			}
			issues, err := validate.Document(doc, "import-mapping.schema.json#/$defs/ColumnMapping", "SCHEMA")
			if err != nil {
				t.Fatal(err)
			}
			want := x == mapping.Identity
			if f != nil {
				want = x == mapping.TransformOf(*f)
			}
			if (len(issues) == 0) != want {
				t.Errorf("%s with %s: schema issues %v, want accepted %v", doc, x, issues, want)
			}
		}
	}
}

type issue struct{ code, path string }

func issuesOf(is []model.ValidationIssue) []issue {
	var out []issue
	for _, i := range is {
		if i.Severity != model.SeverityError {
			panic("not an error: " + i.Code)
		}
		out = append(out, issue{i.Code, i.Path})
	}
	return out
}

// Check (D-070): what keeps a person's mapping from being saved, with a
// JSON Pointer into the saveImportMapping body and a message that never
// quotes a column name.
func TestCheck(t *testing.T) {
	timestampAt := 3
	ts := func(m mapping.Mapping) mapping.Mapping { return m }
	cases := []struct {
		name   string
		edit   func(*mapping.Mapping)
		issues []issue
	}{
		{"the station mapping", func(*mapping.Mapping) {}, nil},
		{"a column the import does not have", func(m *mapping.Mapping) { m.Mappings[2].SourceColumn = "=SECRET()" },
			[]issue{{mapping.CodeUnknownColumn, "/mapping/mappings/2/source_column"}}},
		{"a column named twice", func(m *mapping.Mapping) { m.Mappings[11] = entry("序號", mapping.Metadata, mapping.Identity) },
			[]issue{{mapping.CodeDuplicateColumn, "/mapping/mappings/11/source_column"}}},
		{"a field filled twice", func(m *mapping.Mapping) { m.Mappings[11] = entry("作業員", mapping.CaseID, mapping.Identity) },
			[]issue{{mapping.CodeDuplicateTarget, "/mapping/mappings/11/target_field"}}},
		{"metadata takes many columns", func(m *mapping.Mapping) { m.Mappings[11] = entry("作業員", mapping.Metadata, mapping.Identity) }, nil},
		{"no timestamp", func(m *mapping.Mapping) {
			m.Mappings[timestampAt].TargetField = nil
			m.Mappings[timestampAt].Transform = mapping.Identity
		},
			[]issue{{mapping.CodeRequiredTarget, "/mapping/mappings"}}},
		{"no required field at all", func(m *mapping.Mapping) { m.Mappings = m.Mappings[2:3] },
			[]issue{{mapping.CodeRequiredTarget, "/mapping/mappings"}, {mapping.CodeRequiredTarget, "/mapping/mappings"},
				{mapping.CodeRequiredTarget, "/mapping/mappings"}}},
		{"a time without rfc3339", func(m *mapping.Mapping) { m.Mappings[timestampAt].Transform = mapping.Identity },
			[]issue{{mapping.CodeTransform, "/mapping/mappings/3/transform"}}},
		{"an unmapped column with a transform", func(m *mapping.Mapping) { m.Mappings[11].Transform = mapping.IntegerMS },
			[]issue{{mapping.CodeTransform, "/mapping/mappings/11/transform"}}},
		{"an unknown transform", func(m *mapping.Mapping) { m.Mappings[0].Transform = "split(',')" },
			[]issue{{mapping.CodeTransform, "/mapping/mappings/0/transform"}}},
		{"an unknown field", func(m *mapping.Mapping) { m.Mappings[11].TargetField = field("operator_id") },
			[]issue{{mapping.CodeUnknownTarget, "/mapping/mappings/11/target_field"}}},
	}
	for _, c := range cases {
		m := ts(station())
		c.edit(&m)
		got := mapping.Check(m, stationColumns, nil, nil)
		if fmt.Sprint(issuesOf(got)) != fmt.Sprint(c.issues) {
			t.Errorf("%s: %v, want %v", c.name, issuesOf(got), c.issues)
		}
		for _, is := range got {
			for _, col := range append(slices.Clone(stationColumns), "=SECRET()") {
				if strings.Contains(is.Message, col) {
					t.Errorf("%s: the message quotes a column: %q", c.name, is.Message)
				}
			}
		}
	}
	// The required fields are named in a fixed order.
	m := station()
	m.Mappings = m.Mappings[2:3]
	var says []string
	for _, is := range mapping.Check(m, stationColumns, nil, nil) {
		says = append(says, is.Message)
	}
	if len(says) != 3 || !strings.Contains(says[0], "case_id") || !strings.Contains(says[1], "event_type") || !strings.Contains(says[2], "timestamp") {
		t.Errorf("required: %q", says)
	}
	// At most 64 columns go to metadata.
	var cols []string
	m = mapping.Mapping{Mappings: []mapping.Entry{entry("c", mapping.CaseID, mapping.Identity), entry("e", mapping.EventType, mapping.Identity),
		entry("t", mapping.Timestamp, mapping.RFC3339)}}
	cols = append(cols, "c", "e", "t")
	for i := range mapping.MaxMetadataColumns + 1 {
		name := fmt.Sprintf("m%d", i)
		cols = append(cols, name)
		m.Mappings = append(m.Mappings, entry(name, mapping.Metadata, mapping.Identity))
	}
	if got := issuesOf(mapping.Check(m, cols, nil, nil)); fmt.Sprint(got) != fmt.Sprint([]issue{{mapping.CodeMetadataLimit, "/mapping/mappings/67"}}) {
		t.Errorf("65 metadata columns: %v", got)
	}
	m.Mappings = m.Mappings[:len(m.Mappings)-1]
	if got := mapping.Check(m, cols, nil, nil); len(got) != 0 {
		t.Errorf("64 metadata columns: %v", got)
	}
}

// D-070: an AI suggestion is never applied, and an entry the person saves
// exactly as a suggestion that requires confirmation (confidence below
// 0.80) is confirmed by name; a name that is not such an entry is refused.
func TestLowConfidenceNeedsAPerson(t *testing.T) {
	s := &mapping.Suggestion{Mappings: []mapping.SuggestedEntry{
		{SourceColumn: "序號", TargetField: field(mapping.CaseID), Confidence: 0.96, Transform: mapping.Identity},
		{SourceColumn: "開始", TargetField: field(mapping.Timestamp), Confidence: 0.7, Transform: mapping.RFC3339, RequiresConfirmation: true},
		{SourceColumn: "班別", TargetField: field(mapping.Shift), Confidence: 0.6, Transform: mapping.Identity, RequiresConfirmation: true},
		{SourceColumn: "作業員", TargetField: nil, Confidence: 0.2, Transform: mapping.Identity, RequiresConfirmation: true},
	}}
	cases := []struct {
		name      string
		edit      func(*mapping.Mapping)
		confirmed []string
		issues    []issue
	}{
		{"two unconfirmed", func(*mapping.Mapping) {}, nil,
			[]issue{{mapping.CodeConfirmationRequired, "/mapping/mappings/3"}, {mapping.CodeConfirmationRequired, "/mapping/mappings/6"}}},
		{"one confirmed", func(*mapping.Mapping) {}, []string{"班別"},
			[]issue{{mapping.CodeConfirmationRequired, "/mapping/mappings/3"}}},
		{"both confirmed", func(*mapping.Mapping) {}, []string{"開始", "班別"}, nil},
		{"the person chose otherwise", func(m *mapping.Mapping) {
			m.Mappings[6] = entry("班別", mapping.Metadata, mapping.Identity)
		}, []string{"開始"}, nil},
		{"a confident suggestion needs nothing", func(*mapping.Mapping) {}, []string{"開始", "班別", "序號"},
			[]issue{{mapping.CodeConfirmationUnknown, "/confirmed_low_confidence/2"}}},
		{"a column the person did not import", func(*mapping.Mapping) {}, []string{"開始", "班別", "作業員"},
			[]issue{{mapping.CodeConfirmationUnknown, "/confirmed_low_confidence/2"}}},
		{"a column of no suggestion", func(*mapping.Mapping) {}, []string{"開始", "班別", "批號"},
			[]issue{{mapping.CodeConfirmationUnknown, "/confirmed_low_confidence/2"}}},
	}
	for _, c := range cases {
		m := station()
		c.edit(&m)
		if got := issuesOf(mapping.Check(m, stationColumns, s, c.confirmed)); fmt.Sprint(got) != fmt.Sprint(c.issues) {
			t.Errorf("%s: %v, want %v", c.name, got, c.issues)
		}
	}
	// Without a suggestion, nothing is confirmed by name.
	if got := issuesOf(mapping.Check(station(), stationColumns, nil, []string{"開始"})); fmt.Sprint(got) !=
		fmt.Sprint([]issue{{mapping.CodeConfirmationUnknown, "/confirmed_low_confidence/0"}}) {
		t.Errorf("no suggestion: %v", got)
	}
}

func plan(t *testing.T, m mapping.Mapping, columns []string) *mapping.Plan {
	t.Helper()
	p, err := mapping.Compile(m, columns)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// Compile keeps the imported entries in mapping order with their columns,
// and refuses a mapping Check does not accept.
func TestCompile(t *testing.T) {
	p := plan(t, station(), stationColumns)
	if p.Len() != 11 || p.Column(0) != 0 || p.Name(4) != "結束" || p.Field(4) != mapping.EndTimestamp || p.Column(10) != 10 || p.Field(10) != mapping.Metadata {
		t.Errorf("plan of %d entries", p.Len())
	}
	m := station()
	m.Mappings[3].Transform = mapping.Identity
	if _, err := mapping.Compile(m, stationColumns); err == nil {
		t.Error("a mapping Check refuses was compiled")
	}
	if _, err := mapping.Compile(station(), stationColumns[:5]); err == nil {
		t.Error("a mapping of other columns was compiled")
	}
}

// one maps a single value through a one-column plan for field f (with the
// required fields filled as needed).
func one(t *testing.T, f mapping.Field, v mapping.Value) (mapping.Event, []mapping.RowIssue) {
	t.Helper()
	m := mapping.Mapping{Mappings: []mapping.Entry{entry("x", f, mapping.TransformOf(f))}}
	cols := []string{"x"}
	values := []mapping.Value{v}
	for _, r := range mapping.Required {
		if r != f {
			m.Mappings = append(m.Mappings, entry(string(r), r, mapping.TransformOf(r)))
			cols = append(cols, string(r))
			value := "v"
			if r == mapping.Timestamp {
				value = "2026-10-13T06:12:00+08:00"
			}
			values = append(values, mapping.Text(value))
		}
	}
	return plan(t, m, cols).Apply(values)
}

func codes(is []mapping.RowIssue) string {
	var out []string
	for _, i := range is {
		out = append(out, fmt.Sprintf("%d:%s", i.Entry, i.Code))
	}
	return strings.Join(out, ",")
}

func js(s string) mapping.Value { return mapping.JSON(jsontext.Value(s)) }

// The four transforms take exactly the shapes D-070 names; an empty value
// leaves an optional field out and is missing for a required one.
func TestTransforms(t *testing.T) {
	type out struct {
		field mapping.Field
		in    mapping.Value
		want  string // the event's JSON member, or the issue code
	}
	long := strings.Repeat("字", 256)
	cases := []out{
		// identity: text as it is; a JSON number's literal as written.
		{mapping.CaseID, mapping.Text("SN-000123"), `"case_id":"SN-000123"`},
		{mapping.CaseID, mapping.Text(" SN 1 "), `"case_id":" SN 1 "`},
		{mapping.CaseID, js(`"SN-1"`), `"case_id":"SN-1"`},
		{mapping.CaseID, js(`12345`), `"case_id":"12345"`},
		{mapping.CaseID, js(`1.50e2`), `"case_id":"1.50e2"`},
		{mapping.CaseID, mapping.Text(long), `"case_id":"` + long + `"`},
		{mapping.CaseID, mapping.Text(long + "x"), mapping.CodeValueTooLong},
		{mapping.Shift, mapping.Text(strings.Repeat("a", 65)), mapping.CodeValueTooLong},
		{mapping.CaseID, js(`true`), mapping.CodeValueInvalid},
		{mapping.CaseID, js(`{"a":1}`), mapping.CodeValueInvalid},
		{mapping.CaseID, js(`["a"]`), mapping.CodeValueInvalid},
		{mapping.CaseID, mapping.Text(""), mapping.CodeValueMissing},
		{mapping.CaseID, js(`null`), mapping.CodeValueMissing},
		{mapping.CaseID, js(`""`), mapping.CodeValueMissing},
		{mapping.CaseID, mapping.Missing(), mapping.CodeValueMissing},
		{mapping.Stage, mapping.Text(""), ``},
		{mapping.Stage, mapping.Missing(), ``},
		// rfc3339: a date-time with an offset, kept as written.
		{mapping.Timestamp, mapping.Text("2026-10-13T06:12:00+08:00"), `"timestamp":"2026-10-13T06:12:00+08:00"`},
		{mapping.Timestamp, mapping.Text("2026-10-13t06:12:00.5z"), `"timestamp":"2026-10-13t06:12:00.5z"`},
		{mapping.Timestamp, js(`"2026-10-13T06:12:00Z"`), `"timestamp":"2026-10-13T06:12:00Z"`},
		{mapping.Timestamp, mapping.Text("2026-10-13T06:12:00"), mapping.CodeValueInvalid}, // no time zone is assumed
		{mapping.Timestamp, mapping.Text("2026/10/13 06:12"), mapping.CodeValueInvalid},
		{mapping.Timestamp, mapping.Text("2026-02-30T06:12:00Z"), mapping.CodeValueInvalid},
		{mapping.Timestamp, js(`1760000000`), mapping.CodeValueInvalid},
		{mapping.Timestamp, mapping.Text(""), mapping.CodeValueMissing},
		{mapping.EndTimestamp, mapping.Text(""), ``},
		// integer_ms: JSON's integer grammar, no sign, at most 2^53-1.
		{mapping.DurationMS, mapping.Text("0"), `"duration_ms":0`},
		{mapping.DurationMS, mapping.Text("190000"), `"duration_ms":190000`},
		{mapping.DurationMS, mapping.Text("9007199254740991"), `"duration_ms":9007199254740991`},
		{mapping.DurationMS, js(`1500`), `"duration_ms":1500`},
		{mapping.DurationMS, js(`"1500"`), `"duration_ms":1500`},
		{mapping.DurationMS, mapping.Text("9007199254740992"), mapping.CodeValueInvalid},
		{mapping.DurationMS, mapping.Text("99999999999999999999999"), mapping.CodeValueInvalid},
		{mapping.DurationMS, mapping.Text("-1"), mapping.CodeValueInvalid},
		{mapping.DurationMS, mapping.Text("+1"), mapping.CodeValueInvalid},
		{mapping.DurationMS, mapping.Text("007"), mapping.CodeValueInvalid},
		{mapping.DurationMS, mapping.Text("1.0"), mapping.CodeValueInvalid},
		{mapping.DurationMS, mapping.Text("1e3"), mapping.CodeValueInvalid},
		{mapping.DurationMS, mapping.Text(" 1"), mapping.CodeValueInvalid},
		{mapping.DurationMS, js(`1.5e3`), mapping.CodeValueInvalid},
		{mapping.DurationMS, js(`true`), mapping.CodeValueInvalid},
		{mapping.DurationMS, mapping.Text(""), ``},
		// boolean_literal: true or false in ASCII letters of any case.
		{mapping.IsFailure, mapping.Text("true"), `"is_failure":true`},
		{mapping.IsFailure, mapping.Text("FALSE"), `"is_failure":false`},
		{mapping.IsFailure, js(`true`), `"is_failure":true`},
		{mapping.IsFailure, js(`"False"`), `"is_failure":false`},
		{mapping.IsRework, mapping.Text("yes"), mapping.CodeValueInvalid},
		{mapping.IsRework, mapping.Text("1"), mapping.CodeValueInvalid},
		{mapping.IsRework, mapping.Text("falſe"), mapping.CodeValueInvalid},
		{mapping.IsRework, js(`0`), mapping.CodeValueInvalid},
		{mapping.IsRework, js(`null`), ``},
		// metadata: under the source column's name, any JSON value as it is.
		{mapping.Metadata, mapping.Text("L-7"), `"metadata":{"x":"L-7"}`},
		{mapping.Metadata, js(`{"line": [1, 2.50, "a"]}`), `"metadata":{"x":{"line":[1,2.50,"a"]}}`},
		{mapping.Metadata, js(`false`), `"metadata":{"x":false}`},
		{mapping.Metadata, js(`null`), ``},
		{mapping.Metadata, mapping.Text(""), ``},
	}
	for _, c := range cases {
		e, issues := one(t, c.field, c.in)
		switch {
		case strings.HasPrefix(c.want, "MAPPING_") || strings.HasPrefix(c.want, "EVENT_"):
			if codes(issues) != "0:"+c.want {
				t.Errorf("%s %+v: issues %s, want %s", c.field, c.in, codes(issues), c.want)
			}
		default:
			if len(issues) > 0 {
				t.Errorf("%s %+v: issues %s", c.field, c.in, codes(issues))
				continue
			}
			doc, err := json.Marshal(e.CanonicalEvent)
			if err != nil {
				t.Fatal(err)
			}
			if c.want == "" {
				if strings.Contains(string(doc), `"`+string(c.field)+`"`) {
					t.Errorf("%s %+v: %s has the field", c.field, c.in, doc)
				}
			} else if !strings.Contains(string(doc), c.want) {
				t.Errorf("%s %+v: %s, want %s", c.field, c.in, doc, c.want)
			}
			valid(t, doc)
		}
		for _, is := range issues {
			if !strings.HasPrefix(is.Message, string(c.field)+": ") || strings.Contains(is.Message, "SN-") || strings.Contains(is.Message, "2026") {
				t.Errorf("%s: message %q", c.field, is.Message)
			}
		}
	}
}

// valid fails t unless doc is a canonical-event.schema.json instance.
func valid(t *testing.T, doc []byte) {
	t.Helper()
	issues, err := validate.Document(doc, "canonical-event.schema.json", "SCHEMA")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) > 0 {
		t.Errorf("%s is not a canonical event: %v", doc, issues)
	}
}

// Metadata keeps every imported column under its own name, in mapping
// order, and is bounded (D-072): 16 KiB and 8 levels, the object counting 1.
func TestMetadata(t *testing.T) {
	m := mapping.Mapping{Mappings: []mapping.Entry{entry("批號", mapping.Metadata, mapping.Identity), entry("c", mapping.CaseID, mapping.Identity),
		entry("e", mapping.EventType, mapping.Identity), entry("=備註", mapping.Metadata, mapping.Identity), entry("t", mapping.Timestamp, mapping.RFC3339)}}
	p := plan(t, m, []string{"c", "e", "t", "=備註", "批號"})
	row := func(lot, note mapping.Value) (string, string) {
		e, issues := p.Apply([]mapping.Value{lot, mapping.Text("SN-1"), mapping.Text("op"), note, mapping.Text("2026-10-13T06:12:00Z")})
		doc, _ := json.Marshal(e.CanonicalEvent)
		return string(doc), codes(issues)
	}
	if doc, is := row(mapping.Text("L-7"), mapping.Text("=1+1")); is != "" || !strings.HasSuffix(doc, `"metadata":{"批號":"L-7","=備註":"=1+1"}}`) {
		t.Errorf("%s %s", doc, is)
	}
	if doc, is := row(mapping.Text(""), mapping.Text("x")); is != "" || !strings.HasSuffix(doc, `"metadata":{"=備註":"x"}}`) {
		t.Errorf("%s %s", doc, is)
	}
	if doc, is := row(mapping.Text(""), mapping.Text("")); is != "" || strings.Contains(doc, "metadata") {
		t.Errorf("%s %s", doc, is)
	}
	// The bytes: what fits is kept; one more byte is the row's issue.
	one := mapping.MaxMetadataBytes - len(`{"批號":""}`) // one member's room
	if _, is := row(mapping.Text(strings.Repeat("a", one)), mapping.Text("")); is != "" {
		t.Errorf("metadata at the limit: %s", is)
	}
	if _, is := row(mapping.Text(strings.Repeat("a", one+1)), mapping.Text("")); is != "-1:"+mapping.CodeMetadataTooLarge {
		t.Errorf("metadata over the limit: %s", is)
	}
	two := mapping.MaxMetadataBytes - len(`{"批號":"","=備註":"b"}`)
	if _, is := row(mapping.Text(strings.Repeat("a", two)), mapping.Text("b")); is != "" {
		t.Errorf("metadata at the limit with two members: %s", is)
	}
	if _, is := row(mapping.Text(strings.Repeat("a", two+1)), mapping.Text("b")); is != "-1:"+mapping.CodeMetadataTooLarge {
		t.Errorf("metadata over the limit with two members: %s", is)
	}
	// The depth: the object is 1, so a value may nest 7 deep.
	nest := func(n int) mapping.Value { return js(strings.Repeat("[", n) + strings.Repeat("]", n)) }
	if _, is := row(nest(7), mapping.Text("")); is != "" {
		t.Errorf("depth 8: %s", is)
	}
	if _, is := row(nest(8), mapping.Text("")); is != "-1:"+mapping.CodeMetadataTooLarge {
		t.Errorf("depth 9: %s", is)
	}
}

// stationRow is a full row of the station mapping, with its start, end
// and duration.
func stationRow(start, end, duration string) []mapping.Value {
	return []mapping.Value{mapping.Text("SN-000123"), mapping.Text("operation"), mapping.Text("fct"), mapping.Text(start), mapping.Text(end),
		mapping.Text("pass"), mapping.Text("day"), mapping.Text("board_a"), mapping.Text(duration), mapping.Text("false"), mapping.Text("L-7")}
}

// D-072's row rules: an end before the start is the end's issue; with an
// end, duration_ms differs from end minus start by less than 1 ms; the
// times compare as instants, whatever their offsets.
func TestRowRules(t *testing.T) {
	p := plan(t, station(), stationColumns)
	for _, c := range []struct {
		start, end, duration, want string
	}{
		{"2026-10-13T06:12:00+08:00", "2026-10-13T06:15:10+08:00", "190000", ""},
		{"2026-10-13T06:12:00+08:00", "2026-10-13T06:12:00+08:00", "0", ""},
		{"2026-10-13T06:12:00+08:00", "2026-10-13T06:15:10+08:00", "", ""},
		{"2026-10-13T06:12:00+08:00", "", "190000", ""},
		{"2026-10-13T06:12:00+08:00", "2026-10-13T06:11:59+08:00", "", "4:" + mapping.CodeEndBeforeStart},
		{"2026-10-12T23:30:00Z", "2026-10-13T06:00:00+08:00", "", "4:" + mapping.CodeEndBeforeStart}, // 22:00Z
		{"2026-10-12T21:30:00Z", "2026-10-13T06:00:00+08:00", "1800000", ""},
		{"2026-10-13T06:12:00+08:00", "2026-10-13T06:11:00+08:00", "60000", "4:" + mapping.CodeEndBeforeStart},
		{"2026-10-13T06:12:00+08:00", "2026-10-13T06:15:10+08:00", "190001", "8:" + mapping.CodeDurationInconsistent},
		{"2026-10-13T06:12:00+08:00", "2026-10-13T06:15:10+08:00", "189999", "8:" + mapping.CodeDurationInconsistent},
		{"2026-10-13T06:12:00+08:00", "2026-10-13T06:15:10+08:00", "191000", "8:" + mapping.CodeDurationInconsistent},
		{"2026-10-13T06:12:00+08:00", "2026-10-13T06:15:10+08:00", "189000", "8:" + mapping.CodeDurationInconsistent},
		{"2026-10-13T06:12:00.0005Z", "2026-10-13T06:12:01Z", "999", ""},
		{"2026-10-13T06:12:00.0005Z", "2026-10-13T06:12:01Z", "1000", ""},
		{"2026-10-13T06:12:00.0005Z", "2026-10-13T06:12:01Z", "998", "8:" + mapping.CodeDurationInconsistent},
		{"2026-10-13T06:12:00.0005Z", "2026-10-13T06:12:01Z", "1001", "8:" + mapping.CodeDurationInconsistent},
	} {
		e, issues := p.Apply(stationRow(c.start, c.end, c.duration))
		if codes(issues) != c.want {
			t.Errorf("%s..%s (%s): %s, want %s", c.start, c.end, c.duration, codes(issues), c.want)
		}
		if c.want == "" {
			doc, _ := json.Marshal(e.CanonicalEvent)
			valid(t, doc)
			if !e.Start.Equal(mustTime(t, c.start)) || (c.end != "" && !e.End.Equal(mustTime(t, c.end))) || (c.end == "" && !e.End.IsZero()) {
				t.Errorf("times %v %v", e.Start, e.End)
			}
		}
	}
	// Beyond time.Duration's 292 years the comparison is still exact.
	start, end := "0001-01-01T00:00:00Z", "9999-12-31T23:59:59.9995Z"
	gap := new(big.Int).Sub(big.NewInt(mustTime(t, end).UnixMilli()), big.NewInt(mustTime(t, start).UnixMilli()))
	for d, want := range map[string]string{gap.String(): "", new(big.Int).Add(gap, big.NewInt(1)).String(): "",
		new(big.Int).Add(gap, big.NewInt(2)).String(): "8:" + mapping.CodeDurationInconsistent,
		new(big.Int).Sub(gap, big.NewInt(1)).String(): "8:" + mapping.CodeDurationInconsistent} {
		if _, issues := p.Apply(stationRow(start, end, d)); codes(issues) != want {
			t.Errorf("%s ms over 9999 years: %s, want %s", d, codes(issues), want)
		}
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339Nano, strings.ToUpper(s))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// Every problem of a row is reported, in plan order, the row's own last;
// the event of a valid row is the station's, member for member.
func TestApplyAll(t *testing.T) {
	p := plan(t, station(), stationColumns)
	row := stationRow("2026-10-13T06:12:00+08:00", "2026-10-13T06:15:10+08:00", "190000")
	e, issues := p.Apply(row)
	doc, err := json.Marshal(e.CanonicalEvent)
	if err != nil || len(issues) > 0 {
		t.Fatalf("%v %s", err, codes(issues))
	}
	want := `{"case_id":"SN-000123","event_type":"operation","timestamp":"2026-10-13T06:12:00+08:00","end_timestamp":"2026-10-13T06:15:10+08:00",` +
		`"stage":"fct","duration_ms":190000,"outcome":"pass","shift":"day","product":"board_a","is_rework":false,"metadata":{"批號":"L-7"}}`
	if string(doc) != want {
		t.Errorf("%s\nwant %s", doc, want)
	}
	row[0], row[3], row[8], row[9], row[10] = mapping.Text(""), mapping.Text("yesterday"), mapping.Text("-5"), mapping.Text("maybe"),
		mapping.JSON(jsontext.Value(strings.Repeat("[", 9)+strings.Repeat("]", 9)))
	if _, issues := p.Apply(row); codes(issues) != "0:"+mapping.CodeValueMissing+",3:"+mapping.CodeValueInvalid+",8:"+mapping.CodeValueInvalid+
		",9:"+mapping.CodeValueInvalid+",-1:"+mapping.CodeMetadataTooLarge {
		t.Errorf("issues %s", codes(issues))
	}
	if _, issues := p.Apply(row[:3]); len(issues) == 0 {
		t.Error("a row with fewer values than the plan was mapped")
	}
}
