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

// FuzzApply: whatever the cells, Apply never panics; a row without issues
// is a canonical-event.schema.json instance with its required fields; an
// issue names a plan entry (or the row) and its field, never the value.
func FuzzApply(f *testing.F) {
	for _, s := range [][3]string{
		{"SN-000123", "2026-10-13T06:12:00+08:00", "190000"},
		{"", "2026-10-13T06:12:00", "-1"},
		{`{"a":[1,2]}`, `"2026-10-13T06:12:00Z"`, "9007199254740992"},
		{"12345", "null", "true"},
		{strings.Repeat("字", 257), "2026-02-30T00:00:00Z", "007"},
	} {
		f.Add(s[0], s[1], s[2], false)
		f.Add(s[0], s[1], s[2], true)
	}
	p, err := mapping.Compile(station(), stationColumns)
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, a, b, c string, asJSON bool) {
		if !utf8.ValidString(a) || !utf8.ValidString(b) || !utf8.ValidString(c) || strings.ContainsRune(a+b+c, 0) {
			return // the profile refuses such files before any row is mapped
		}
		cell := func(s string) mapping.Value {
			if asJSON {
				if v := jsontext.Value(s); v.IsValid() {
					return mapping.JSON(v)
				}
			}
			return mapping.Text(s)
		}
		// Every column takes one of the three inputs, so every transform
		// and every field meets each of them.
		inputs := []string{a, b, c}
		values := make([]mapping.Value, p.Len())
		for i := range values {
			values[i] = cell(inputs[i%3])
		}
		e, issues := p.Apply(values)
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
