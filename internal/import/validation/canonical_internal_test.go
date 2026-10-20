package validation

// Review round 1, M3: every event goes through canonical-event.schema.json
// before it may be part of a dataset, so a rule of the mapping that the
// schema does not share fails closed (the row is invalid; Write refuses
// the file) instead of freezing an event the contract refuses.

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/import/mapping"
	importprofile "github.com/kcl1104/geminus/internal/import/profile"
)

func TestTheSchemaIsTheLastWord(t *testing.T) {
	for doc, want := range map[string]bool{
		`{"case_id":"C","event_type":"x","timestamp":"2026-10-13T06:12:00+08:00"}`: true,
		`{"case_id":"C","event_type":"x","timestamp":"2026-10-13T06:12:00+24:00"}`: false,
		`{"case_id":"C","event_type":"x","timestamp":"2026-10-13T06:12:00+05:60"}`: false,
		`{"case_id":"C","event_type":"x"}`:                                         false,
		`{"case_id":"","event_type":"x","timestamp":"2026-10-13T06:12:00Z"}`:       false,
	} {
		if canonical([]byte(doc)) != want {
			t.Errorf("%s: %v", doc, !want)
		}
	}
}

func TestEveryEventGoesThroughTheSchema(t *testing.T) {
	cols := []string{"c", "e", "t"}
	plan, err := mapping.Compile(mapping.Mapping{Mappings: []mapping.Entry{
		{SourceColumn: "c", TargetField: ptr(mapping.CaseID), Transform: mapping.Identity},
		{SourceColumn: "e", TargetField: ptr(mapping.EventType), Transform: mapping.Identity},
		{SourceColumn: "t", TargetField: ptr(mapping.Timestamp), Transform: mapping.RFC3339},
	}}, cols)
	if err != nil {
		t.Fatal(err)
	}
	data := "c,e,t\nSN-1,op,2026-10-13T06:12:00Z\nSN-2,op,2026-10-13T06:13:00Z\nSN-3,op,2026-10-13T06:14:00Z\n"
	// A schema that refuses SN-2's event, which every mapping rule accepts.
	real := canonical
	defer func() { canonical = real }()
	canonical = func(doc []byte) bool { return real(doc) && !bytes.Contains(doc, []byte(`"SN-2"`)) }
	out, err := Validate(context.Background(), strings.NewReader(data), importprofile.CSV, plan, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if out.Valid() || out.InvalidRows != 1 || len(out.Issues) != 1 || out.Issues[0].Code != CodeEventNotCanonical || out.Issues[0].Path != "/1" ||
		strings.Contains(out.Issues[0].Message, "SN-2") || len(out.Errors) != 1 || out.Errors[0].Line != 3 || len(out.Preview) != 3 ||
		string(out.Preview[1]) != "null" {
		t.Errorf("%+v", out)
	}
	if _, err := Write(context.Background(), strings.NewReader(data), importprofile.CSV, plan, DefaultLimits, &bytes.Buffer{}); !errors.Is(err, ErrChanged) {
		t.Errorf("Write wrote an event the schema refuses: %v", err)
	}
}

func ptr(f mapping.Field) *mapping.Field { return &f }
