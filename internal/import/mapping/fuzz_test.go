package mapping_test

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/kcl1104/geminus/internal/import/mapping"
)

// fixed are the sentences a row issue says after its field's name.
var fixed = map[string]bool{
	"the value is not text":                                                        true,
	"the value is not an RFC 3339 date-time with an offset":                        true,
	"the value is not true or false":                                               true,
	"the value is empty, but every canonical event has one":                        true,
	"the value is longer than 256 characters":                                      true,
	"the value is longer than 128 characters":                                      true,
	"the value is longer than 64 characters":                                       true,
	"the end is before the start (timestamp)":                                      true,
	"the row's metadata is larger than 16384 bytes or nested deeper than 8 levels": true,
	"the duration differs from end_timestamp minus timestamp by 1 ms or more":      true,
	"the value is not a whole number of milliseconds from 0 to 9007199254740991":   true,
}

// sep separates the cells of one fuzzed row: plan entry i takes cell i, a
// missing cell is an empty value.
const sep = "\x1f"

// cells is a row of the station plan, one cell per entry: serial, record
// type, station, start, end, result, shift, product, duration, rework,
// lot.
func cells(c ...string) string { return strings.Join(c, sep) }

// validRows are seeds that must map without an issue: the fuzzer starts
// from rows that reach FuzzApply's schema check (review round 1, M4: with
// one input shared by the start and the rework flag no row ever did).
var validRows = []struct {
	row    string
	asJSON bool
}{
	{cells("SN-000123", "operation", "fct", "2026-10-13T06:12:00+08:00", "2026-10-13T06:15:10+08:00", "pass", "day", "board_a",
		"190000", "false", "L-7"), false},
	{cells("SN-1", "downtime", "ict", "2026-10-13T06:12:00Z", "", "", "", "", "", "TRUE", ""), false},
	{cells(`"SN-2"`, `"operation"`, `"fct"`, `"2026-10-13T06:12:00-23:59"`, `"2026-10-13T06:12:00.5-23:59"`, `"fail"`, `null`, `"b"`,
		`500`, `true`, `{"a":[1,2]}`), true},
}

// FuzzApply: whatever the cells, Apply never panics; a row without issues
// is a canonical-event.schema.json instance with its required fields; an
// issue names a plan entry (or the row) and its field, never the value;
// and the seeds above are such rows.
func FuzzApply(f *testing.F) {
	for _, v := range validRows {
		f.Add(v.row, v.asJSON)
	}
	for _, s := range []string{
		cells("", "", "", "2026-10-13T06:12:00", "x", "", "", "", "-1", "1", ""),
		cells(`{"a":[1,2]}`, "operation", "fct", `"2026-10-13T06:12:00Z"`, "", "", "", "", "9007199254740992", "", ""),
		cells("12345", "operation", "fct", "null", "", "", "", "", "007", "true", ""),
		cells(strings.Repeat("字", 257), "operation", "fct", "2026-02-30T00:00:00Z", "", "", "", "", "", "", ""),
		// Offsets RFC 3339 does not have, and the unknown offset (round 1, M3, L6).
		cells("SN-3", "operation", "fct", "2026-10-13T06:12:00+24:00", "", "", "", "", "", "", ""),
		cells("SN-4", "operation", "fct", "2026-10-13T06:12:00+05:60", "", "", "", "", "", "", ""),
		cells("SN-5", "operation", "fct", "2026-10-13T06:12:00-00:00", "", "", "", "", "", "", ""),
		cells("SN-6", "operation", "fct", "2026-10-13T06:12:00Z", "2026-10-13T06:12:00+24:00", "", "", "", "", "", ""),
		// An end before the start, and a duration off by 1 ms.
		cells("SN-7", "operation", "fct", "2026-10-13T06:12:00Z", "2026-10-13T06:11:00Z", "", "", "", "", "", ""),
		cells("SN-8", "operation", "fct", "2026-10-13T06:12:00Z", "2026-10-13T06:12:01Z", "", "", "", "999", "", ""),
	} {
		f.Add(s, false)
		f.Add(s, true)
	}
	p, err := mapping.Compile(station(), stationColumns)
	if err != nil {
		f.Fatal(err)
	}
	cell := func(s string, asJSON bool) mapping.Value {
		if asJSON {
			if v := jsontext.Value(s); v.IsValid() {
				return mapping.JSON(v)
			}
		}
		return mapping.Text(s)
	}
	row := func(r string, asJSON bool) []mapping.Value {
		in := strings.Split(r, sep)
		values := make([]mapping.Value, p.Len())
		for i := range values {
			if i < len(in) {
				values[i] = cell(in[i], asJSON)
			} else {
				values[i] = mapping.Text("")
			}
		}
		return values
	}
	// The seeds that must map do, so the schema check below runs.
	for _, v := range validRows {
		if _, issues := p.Apply(row(v.row, v.asJSON)); len(issues) > 0 {
			f.Fatalf("the seed %q does not map: %+v", v.row, issues)
		}
	}
	f.Fuzz(func(t *testing.T, r string, asJSON bool) {
		if !utf8.ValidString(r) || strings.ContainsRune(r, 0) {
			return // the profile refuses such files before any row is mapped
		}
		e, issues := p.Apply(row(r, asJSON))
		for _, is := range issues {
			if is.Entry < -1 || is.Entry >= p.Len() || is.Code == "" {
				t.Fatalf("issue %+v", is)
			}
			// A message is one of the fixed sentences (D-072): it never
			// carries the value.
			name := "metadata"
			if is.Entry >= 0 {
				name = string(p.Field(is.Entry))
			}
			if !strings.HasPrefix(is.Message, name+": ") || !fixed[strings.TrimPrefix(is.Message, name+": ")] {
				t.Fatalf("message %q of entry %d", is.Message, is.Entry)
			}
		}
		if len(issues) > 0 {
			return
		}
		doc, err := json.Marshal(e.CanonicalEvent)
		if err != nil {
			t.Fatal(err)
		}
		valid(t, doc)
		if e.CaseID == "" || e.EventType == "" || e.Timestamp == "" || e.Start.IsZero() {
			t.Fatalf("a valid event without its required fields: %s", doc)
		}
	})
}
