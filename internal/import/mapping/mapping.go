// Package mapping is the canonical-event mapper (T-017; APPROVED D-009,
// D-043 (c), D-059; D-070, D-072 PROPOSED): the mapping a person confirms
// from an import's source columns to canonical-event fields
// (schemas/import-mapping.schema.json), checked against the import's
// columns before it is saved (Check), and applied to one row at a time
// (Plan.Apply) through one of the four bounded transforms of D-009, never
// an expression, a script or code.
//
// It is pure: values in, a canonical event and typed issues out; no
// storage, tenant, HTTP or file access (architecture rule
// import-parsing-is-pure). Messages are fixed sentences that name contract
// fields only, never a column name or a value from the file.
package mapping

import (
	"bytes"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kcl1104/geminus/internal/domainpack"
	importprofile "github.com/kcl1104/geminus/internal/import/profile"
	"github.com/kcl1104/geminus/internal/model"
)

// Field is a target field: a canonical-event.schema.json member
// (import-mapping.schema.json#/$defs/TargetField).
type Field string

const (
	CaseID       Field = "case_id"
	EventType    Field = "event_type"
	Timestamp    Field = "timestamp"
	EndTimestamp Field = "end_timestamp"
	Stage        Field = "stage"
	DurationMS   Field = "duration_ms"
	ResourceID   Field = "resource_id"
	Outcome      Field = "outcome"
	Shift        Field = "shift"
	Product      Field = "product"
	DeploymentID Field = "deployment_id"
	IsFailure    Field = "is_failure"
	IsRework     Field = "is_rework"
	Metadata     Field = "metadata"
)

// Fields are the target fields in the schema's order.
var Fields = []Field{CaseID, EventType, Timestamp, EndTimestamp, Stage, DurationMS, ResourceID, Outcome, Shift, Product,
	DeploymentID, IsFailure, IsRework, Metadata}

// Required are the fields every canonical event has.
var Required = []Field{CaseID, EventType, Timestamp}

// Transform is how a source value becomes a field's value
// (import-mapping.schema.json#/$defs/Transform).
type Transform string

const (
	Identity       Transform = "identity"
	RFC3339        Transform = "rfc3339"
	IntegerMS      Transform = "integer_ms"
	BooleanLiteral Transform = "boolean_literal"
)

// Transforms are the four of D-009, in the schema's order.
var Transforms = []Transform{Identity, RFC3339, IntegerMS, BooleanLiteral}

// Entry is import-mapping.schema.json#/$defs/ColumnMapping: a nil
// TargetField leaves the column out.
type Entry struct {
	SourceColumn string    `json:"source_column"`
	TargetField  *Field    `json:"target_field"`
	Transform    Transform `json:"transform"`
}

// Mapping is import-mapping.schema.json.
type Mapping struct {
	Mappings []Entry `json:"mappings"`
}

// SuggestedEntry is an entry of an AI suggestion (ai-mapping.schema.json),
// of which the confirmation rule reads the column, the target, the
// transform, the confidence and the flag (NeedsPerson).
type SuggestedEntry struct {
	SourceColumn         string    `json:"source_column"`
	TargetField          *Field    `json:"target_field"`
	Confidence           float64   `json:"confidence"`
	Evidence             string    `json:"evidence"`
	Transform            Transform `json:"transform"`
	RequiresConfirmation bool      `json:"requires_confirmation"`
}

// Suggestion is ai-mapping.schema.json.
type Suggestion struct {
	Mappings []SuggestedEntry `json:"mappings"`
}

// ConfirmBelow is AI_SPEC §8's confidence threshold: an entry below it needs
// a person's confirmation.
const ConfirmBelow = 0.80

// NeedsPerson reports whether a person must confirm the entry before a
// mapping takes it (AI_SPEC §8): its confidence is below 0.80, it leaves
// the column out (a null target), or it says so. The server decides from
// the confidence and the target; the flag is the AI's own claim and can
// only add to them (T-017 review round 1, M1, M2).
func (e SuggestedEntry) NeedsPerson() bool {
	return e.RequiresConfirmation || e.Confidence < ConfirmBelow || e.TargetField == nil
}

// The codes of a mapping a person may not save (the details of a 422).
const (
	CodeUnknownColumn        = "MAPPING_UNKNOWN_COLUMN"
	CodeDuplicateColumn      = "MAPPING_DUPLICATE_COLUMN"
	CodeUnknownTarget        = "MAPPING_UNKNOWN_TARGET"
	CodeDuplicateTarget      = "MAPPING_DUPLICATE_TARGET"
	CodeRequiredTarget       = "MAPPING_REQUIRED_TARGET_MISSING"
	CodeTransform            = "MAPPING_TRANSFORM_INVALID"
	CodeMetadataLimit        = "MAPPING_METADATA_LIMIT"
	CodeConfirmationRequired = "MAPPING_CONFIRMATION_REQUIRED"
	CodeConfirmationUnknown  = "MAPPING_CONFIRMATION_UNKNOWN"
)

// The codes of a row that does not map to a valid canonical event (D-072).
// CodeRowShape is a caller's mistake (a row without a value for every
// mapped column), never a file's.
const (
	CodeValueMissing         = "MAPPING_REQUIRED_VALUE_MISSING"
	CodeValueInvalid         = "MAPPING_VALUE_INVALID"
	CodeValueTooLong         = "MAPPING_VALUE_TOO_LONG"
	CodeMetadataTooLarge     = "EVENT_METADATA_TOO_LARGE"
	CodeEndBeforeStart       = "EVENT_END_BEFORE_START"
	CodeDurationInconsistent = "EVENT_DURATION_INCONSISTENT"
	CodeRowShape             = "MAPPING_ROW_SHAPE"
)

// MaxMetadataColumns is the schema's maxProperties of an event's metadata.
const MaxMetadataColumns = 64

// The bounds of one event's metadata (D-072; as D-029 bounds an IR's
// metadata): its compact JSON bytes and its depth, the object itself
// counting 1.
const (
	MaxMetadataBytes = 16 << 10
	MaxMetadataDepth = 8
)

// MaxDurationMS is the largest duration_ms (2^53-1, the schema's maximum).
const MaxDurationMS = 1<<53 - 1

// TransformOf is the one transform field takes (D-070; the schema's
// allOf): identity for text fields and metadata, rfc3339 for times,
// integer_ms for duration_ms, boolean_literal for the flags. "" for a
// field that is not one.
func TransformOf(f Field) Transform {
	switch f {
	case Timestamp, EndTimestamp:
		return RFC3339
	case DurationMS:
		return IntegerMS
	case IsFailure, IsRework:
		return BooleanLiteral
	case CaseID, EventType, Stage, ResourceID, Outcome, Shift, Product, DeploymentID, Metadata:
		return Identity
	}
	return ""
}

// MaxChars is the most characters a text field holds
// (canonical-event.schema.json maxLength); 0 for other fields.
func MaxChars(f Field) int {
	switch f {
	case CaseID, ResourceID, DeploymentID:
		return 256
	case EventType, Stage, Outcome, Product:
		return 128
	case Shift:
		return 64
	}
	return 0
}

func required(f Field) bool { return slices.Contains(Required, f) }

type issues []model.ValidationIssue

func (is *issues) add(code, path, message string) {
	*is = append(*is, model.NewIssue(code, path, message, model.SeverityError))
}

// Check reports what keeps m from being saved for an import whose source
// columns are columns (the profile's names), with JSON Pointers into the
// saveImportMapping body: every source column is one of the import's,
// named once; no field but metadata is filled twice; case_id, event_type
// and timestamp are filled; at most 64 columns go to metadata; each
// field's transform is its own. When the import has an AI suggestion s,
// a person names in confirmed every column the mapping takes as an entry of
// s that needs a person said (SuggestedEntry.NeedsPerson): imported with
// the same target and transform, or, for an entry that leaves the column
// out, not imported (by a null entry or by not listing it); confirmed
// names only such columns (D-070 (d): a person confirms each such
// suggestion; the AI never confirms). No issue means m may be saved.
func Check(m Mapping, columns []string, s *Suggestion, confirmed []string) []model.ValidationIssue {
	var out issues
	known := make(map[string]bool, len(columns))
	for _, c := range columns {
		known[c] = true
	}
	named := map[string]bool{}
	filled := map[Field]bool{}
	metadata := 0
	for i, e := range m.Mappings {
		at := "/mapping/mappings/" + strconv.Itoa(i)
		switch {
		case !known[e.SourceColumn]:
			out.add(CodeUnknownColumn, at+"/source_column", "the source column is not a column of this import")
		case named[e.SourceColumn]:
			out.add(CodeDuplicateColumn, at+"/source_column", "an earlier entry maps this source column")
		}
		named[e.SourceColumn] = true
		if e.TargetField == nil {
			if e.Transform != Identity {
				out.add(CodeTransform, at+"/transform", "a column that is not imported takes the identity transform")
			}
			continue
		}
		f := *e.TargetField
		want := TransformOf(f)
		if want == "" {
			out.add(CodeUnknownTarget, at+"/target_field", "the target field is not a canonical event field")
			continue
		}
		if e.Transform != want {
			out.add(CodeTransform, at+"/transform", fmt.Sprintf("%s takes the %s transform", f, want))
		}
		if f == Metadata {
			if metadata++; metadata == MaxMetadataColumns+1 {
				out.add(CodeMetadataLimit, at, fmt.Sprintf("at most %d columns go to metadata", MaxMetadataColumns))
			}
			continue
		}
		if filled[f] {
			out.add(CodeDuplicateTarget, at+"/target_field", fmt.Sprintf("an earlier entry fills %s; only metadata takes more than one column", f))
		}
		filled[f] = true
	}
	for _, f := range Required {
		if !filled[f] {
			out.add(CodeRequiredTarget, "/mapping/mappings", fmt.Sprintf("no column is mapped to %s, which every canonical event has", f))
		}
	}
	out = append(out, confirmations(m, s, confirmed)...)
	return out
}

// confirmations is D-070 (d)'s rule for the entries of a suggestion that
// need a person: each column the mapping takes as such an entry said is
// named in confirmed, and confirmed names nothing else. A column such an
// entry leaves out is taken as it said when the mapping does not import it
// either: by a null entry (whose issue points at it) or by not listing it
// (whose issue points at /mapping/mappings). Issues follow the mapping's
// entries, then the suggestion's.
func confirmations(m Mapping, s *Suggestion, confirmed []string) issues {
	var out issues
	needs := map[string]bool{}
	required := func(column, at string) {
		if needs[column] {
			return // one issue per column
		}
		needs[column] = true
		if !slices.Contains(confirmed, column) {
			out.add(CodeConfirmationRequired, at,
				"this column is taken as the AI suggested, which needs a person's confirmation; name it in confirmed_low_confidence")
		}
	}
	if s != nil {
		listed := map[string]bool{}
		for i, e := range m.Mappings {
			listed[e.SourceColumn] = true
			for _, se := range s.Mappings {
				if !se.NeedsPerson() || se.SourceColumn != e.SourceColumn {
					continue
				}
				left := se.TargetField == nil && e.TargetField == nil
				same := se.TargetField != nil && e.TargetField != nil && *se.TargetField == *e.TargetField && se.Transform == e.Transform
				if left || same {
					required(e.SourceColumn, "/mapping/mappings/"+strconv.Itoa(i))
					break
				}
			}
		}
		for _, se := range s.Mappings {
			if se.NeedsPerson() && se.TargetField == nil && !listed[se.SourceColumn] {
				required(se.SourceColumn, "/mapping/mappings")
			}
		}
	}
	for j, c := range confirmed {
		if !needs[c] {
			out.add(CodeConfirmationUnknown, "/confirmed_low_confidence/"+strconv.Itoa(j),
				"this is not a column the mapping takes as a suggestion that needs a person said")
		}
	}
	return out
}

// Plan is a checked mapping compiled for an import's columns.
type Plan struct {
	entries []planned
	// The plan entry of the times and the duration; -1 when unmapped.
	start, end, duration int
}

type planned struct {
	column int
	name   string
	field  Field
}

// Compile makes the plan of m, a mapping Check accepts for columns.
func Compile(m Mapping, columns []string) (*Plan, error) {
	if is := Check(m, columns, nil, nil); len(is) > 0 {
		return nil, fmt.Errorf("mapping: %s at %s", is[0].Code, is[0].Path) // pointers and codes only
	}
	index := make(map[string]int, len(columns))
	for i, c := range columns {
		index[c] = i
	}
	p := &Plan{start: -1, end: -1, duration: -1}
	for _, e := range m.Mappings {
		if e.TargetField == nil {
			continue
		}
		switch *e.TargetField {
		case Timestamp:
			p.start = len(p.entries)
		case EndTimestamp:
			p.end = len(p.entries)
		case DurationMS:
			p.duration = len(p.entries)
		}
		p.entries = append(p.entries, planned{column: index[e.SourceColumn], name: e.SourceColumn, field: *e.TargetField})
	}
	return p, nil
}

// Len is how many columns the plan imports.
func (p *Plan) Len() int { return len(p.entries) }

// Column is the index (in the profile's columns) of the source column of
// plan entry i.
func (p *Plan) Column(i int) int { return p.entries[i].column }

// Name is the source column name of plan entry i.
func (p *Plan) Name(i int) string { return p.entries[i].name }

// Field is the target field of plan entry i.
func (p *Plan) Field(i int) Field { return p.entries[i].field }

type kind uint8

const (
	missing kind = iota
	text
	raw
)

// Value is one source cell of a row.
type Value struct {
	kind kind
	text string
	raw  jsontext.Value
}

// Text is a CSV field.
func Text(s string) Value { return Value{kind: text, text: s} }

// JSON is a JSON events member's value, as the profile checked it.
func JSON(v jsontext.Value) Value { return Value{kind: raw, raw: v} }

// Missing is a JSON events member the row does not have.
func Missing() Value { return Value{} }

// RowIssue is why a row does not map to a valid canonical event: Entry is
// the plan entry whose column it is about, -1 for the row as a whole.
type RowIssue struct {
	Entry         int
	Code, Message string
}

// Event is a row mapped: the canonical event, and its start and end as
// times (End zero without an end_timestamp).
type Event struct {
	domainpack.CanonicalEvent
	Start, End time.Time
}

// textOf is v as text: a CSV field, a JSON string, or (when numbers is
// set) a JSON number's literal as written. empty: nothing to map (an empty
// field or string, null, a missing member); ok false: another JSON value.
func textOf(v Value, numbers bool) (s string, empty, ok bool) {
	switch v.kind {
	case text:
		return v.text, v.text == "", true
	case raw:
		switch v.raw.Kind() {
		case 'n':
			return "", true, true
		case '"':
			u, err := jsontext.AppendUnquote(nil, v.raw)
			if err != nil {
				return "", false, false
			}
			return string(u), len(u) == 0, true
		case '0':
			if numbers {
				return string(bytes.TrimSpace(v.raw)), false, true
			}
		}
		return "", false, false
	}
	return "", true, true
}

// integerText is JSON's integer grammar without a sign.
var integerText = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)

// invalid is why a value its transform cannot take is invalid.
var invalid = map[Transform]string{
	Identity:       "the value is not text",
	RFC3339:        "the value is not an RFC 3339 date-time with an offset",
	IntegerMS:      fmt.Sprintf("the value is not a whole number of milliseconds from 0 to %d", MaxDurationMS),
	BooleanLiteral: "the value is not true or false",
}

// metaMember is one column of an event's metadata.
type metaMember struct {
	name string
	v    Value
}

// Apply maps one row, values[i] being the value of plan entry i's column.
// The event is valid (canonical-event.schema.json, and D-072's row rules:
// an end not before the start, a duration_ms consistent with them) when no
// issue is returned; issues are in plan order, the row's own last.
func (p *Plan) Apply(values []Value) (Event, []RowIssue) {
	if len(values) != len(p.entries) {
		return Event{}, []RowIssue{{Entry: -1, Code: CodeRowShape, Message: "the row does not have a value for every mapped column"}}
	}
	var e Event
	var out []RowIssue
	bad := func(i int, code, message string) {
		out = append(out, RowIssue{Entry: i, Code: code, Message: string(p.entries[i].field) + ": " + message})
	}
	var meta []metaMember
	var duration *int64
	for i, pe := range p.entries {
		v := values[i]
		f := pe.field
		if f == Metadata {
			if _, empty, _ := textOf(v, true); !empty {
				meta = append(meta, metaMember{pe.name, v})
			}
			continue
		}
		t := TransformOf(f)
		// A JSON number is text for identity and integer_ms, a JSON
		// boolean for boolean_literal; any other non-text value is invalid.
		s, empty, ok := textOf(v, t == Identity || t == IntegerMS)
		if !ok && t == BooleanLiteral && (v.raw.Kind() == 't' || v.raw.Kind() == 'f') {
			s, empty, ok = string(bytes.TrimSpace(v.raw)), false, true
		}
		if !ok {
			bad(i, CodeValueInvalid, invalid[t])
			continue
		}
		if empty {
			if required(f) {
				bad(i, CodeValueMissing, "the value is empty, but every canonical event has one")
			}
			continue
		}
		switch t {
		case Identity:
			if n := utf8.RuneCountInString(s); n > MaxChars(f) {
				bad(i, CodeValueTooLong, fmt.Sprintf("the value is longer than %d characters", MaxChars(f)))
				continue
			}
			setText(&e.CanonicalEvent, f, s)
		case RFC3339:
			if !importprofile.IsTimestamp(s) {
				bad(i, CodeValueInvalid, invalid[RFC3339])
				continue
			}
			at, err := time.Parse(time.RFC3339Nano, strings.ToUpper(s)) // the pattern is ASCII
			if err != nil {
				bad(i, CodeValueInvalid, invalid[RFC3339])
				continue
			}
			if f == Timestamp {
				e.Timestamp, e.Start = s, at
			} else {
				e.EndTimestamp, e.End = s, at
			}
		case IntegerMS:
			n, err := strconv.ParseInt(s, 10, 64)
			if !integerText.MatchString(s) || err != nil || n > MaxDurationMS {
				bad(i, CodeValueInvalid, invalid[IntegerMS])
				continue
			}
			duration = &n
		case BooleanLiteral:
			if !importprofile.IsBoolean(s) {
				bad(i, CodeValueInvalid, invalid[BooleanLiteral])
				continue
			}
			b := strings.ToLower(s) == "true"
			if f == IsFailure {
				e.IsFailure = &b
			} else {
				e.IsRework = &b
			}
		}
	}
	e.DurationMS = duration
	if len(meta) > 0 {
		doc, err := metadata(meta)
		if err != nil {
			out = append(out, RowIssue{Entry: -1, Code: CodeMetadataTooLarge,
				Message: fmt.Sprintf("metadata: the row's metadata is larger than %d bytes or nested deeper than %d levels", MaxMetadataBytes, MaxMetadataDepth)})
		} else {
			e.Metadata = doc
		}
	}
	// D-072's row rules, on the times that mapped.
	if p.start >= 0 && p.end >= 0 && e.Timestamp != "" && e.EndTimestamp != "" {
		switch {
		case e.End.Before(e.Start):
			bad(p.end, CodeEndBeforeStart, "the end is before the start (timestamp)")
		case p.duration >= 0 && duration != nil && !consistent(*duration, e.Start, e.End):
			bad(p.duration, CodeDurationInconsistent, "the duration differs from end_timestamp minus timestamp by 1 ms or more")
		}
	}
	slices.SortStableFunc(out, func(a, b RowIssue) int {
		switch {
		case a.Entry == b.Entry:
			return 0
		case a.Entry < 0:
			return 1
		case b.Entry < 0:
			return -1
		}
		return a.Entry - b.Entry
	})
	if len(out) > 0 {
		return Event{}, out
	}
	return e, nil
}

func setText(e *domainpack.CanonicalEvent, f Field, s string) {
	switch f {
	case CaseID:
		e.CaseID = s
	case EventType:
		e.EventType = s
	case Stage:
		e.Stage = s
	case ResourceID:
		e.ResourceID = s
	case Outcome:
		e.Outcome = s
	case Shift:
		e.Shift = s
	case Product:
		e.Product = s
	case DeploymentID:
		e.DeploymentID = s
	}
}

// consistent reports whether a duration of ms milliseconds differs from
// end minus start (end not before start) by less than 1 ms, exactly: the
// gap may be far beyond time.Duration's range. With the gap written as
// a whole milliseconds and r nanoseconds (0 <= r < 1e6), |ms·1e6 − gap| <
// 1e6 holds exactly when ms is a, or a+1 with r > 0.
func consistent(ms int64, start, end time.Time) bool {
	ns := int64(end.Nanosecond()) - int64(start.Nanosecond())
	q, r := ns/1e6, ns%1e6
	if r < 0 {
		q, r = q-1, r+1e6
	}
	k := ms - ((end.Unix()-start.Unix())*1000 + q)
	return k == 0 || (k == 1 && r > 0)
}

var errMetadata = errors.New("mapping: the metadata is beyond its bounds")

// metadata is the event's metadata object: each column under its own name,
// in plan order, compact; errMetadata beyond MaxMetadataBytes or
// MaxMetadataDepth.
func metadata(members []metaMember) (jsontext.Value, error) {
	var b bytes.Buffer
	enc := jsontext.NewEncoder(&b)
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return nil, err
	}
	for _, m := range members {
		if err := enc.WriteToken(jsontext.String(m.name)); err != nil {
			return nil, err
		}
		var err error
		if m.v.kind == text {
			err = enc.WriteToken(jsontext.String(m.v.text))
		} else {
			err = enc.WriteValue(m.v.raw)
		}
		if err != nil {
			return nil, err
		}
		if b.Len() > MaxMetadataBytes {
			return nil, errMetadata
		}
	}
	if err := enc.WriteToken(jsontext.EndObject); err != nil {
		return nil, err
	}
	doc := bytes.TrimSpace(b.Bytes())
	if len(doc) > MaxMetadataBytes {
		return nil, errMetadata
	}
	dec := jsontext.NewDecoder(bytes.NewReader(doc))
	for {
		tok, err := dec.ReadToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if k := tok.Kind(); (k == '{' || k == '[') && dec.StackDepth() > MaxMetadataDepth {
			return nil, errMetadata
		}
	}
	return jsontext.Value(doc), nil
}
