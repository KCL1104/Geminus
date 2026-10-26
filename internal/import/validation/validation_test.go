package validation_test

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/import/mapping"
	importprofile "github.com/kcl1104/geminus/internal/import/profile"
	"github.com/kcl1104/geminus/internal/import/report"
	"github.com/kcl1104/geminus/internal/import/validation"
	"github.com/kcl1104/geminus/internal/model/validate"
)

func field(f mapping.Field) *mapping.Field { return &f }

func entry(column string, f mapping.Field) mapping.Entry {
	return mapping.Entry{SourceColumn: column, TargetField: field(f), Transform: mapping.TransformOf(f)}
}

// A synthetic station log (ELECTRONICS_LINE §6) in UTF-8 with a BOM and
// CRLF: serial, record type, station, start, end, result, operator (not
// imported), duration and lot.
const header = "序號,紀錄類型,站別,開始,結束,結果,作業員,耗時毫秒,批號\r\n"

var stationColumns = []string{"序號", "紀錄類型", "站別", "開始", "結束", "結果", "作業員", "耗時毫秒", "批號"}

func stationMapping() mapping.Mapping {
	return mapping.Mapping{Mappings: []mapping.Entry{
		entry("序號", mapping.CaseID), entry("紀錄類型", mapping.EventType), entry("站別", mapping.Stage),
		entry("開始", mapping.Timestamp), entry("結束", mapping.EndTimestamp), entry("結果", mapping.Outcome),
		{SourceColumn: "作業員", Transform: mapping.Identity}, entry("耗時毫秒", mapping.DurationMS), entry("批號", mapping.Metadata),
	}}
}

func plan(t testing.TB, m mapping.Mapping, columns []string) *mapping.Plan {
	t.Helper()
	p, err := mapping.Compile(m, columns)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func row(serial, start, end, result, duration, lot string) string {
	return fmt.Sprintf("%s,operation,fct,%s,%s,%s,op-7,%s,%s\r\n", serial, start, end, result, duration, lot)
}

var validLog = "\ufeff" + header +
	row("SN-000123", "2026-10-13T06:12:00+08:00", "2026-10-13T06:15:10+08:00", "pass", "190000", "L-7") +
	row("SN-000124", "2026-10-13T06:16:00+08:00", "2026-10-13T06:19:02+08:00", "fail", "", "") +
	row("\"=HYPERLINK(\"\"http://x.invalid\"\")\"", "2026-10-13T06:20:00Z", "2026-10-13T06:20:30Z", "pass", "30000", "\"L-7, 第二批\"")

func run(t *testing.T, data string, f importprofile.Format, p *mapping.Plan, lim validation.Limits) validation.Outcome {
	t.Helper()
	out, err := validation.Validate(context.Background(), strings.NewReader(data), f, p, lim)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func write(t *testing.T, data string, f importprofile.Format, p *mapping.Plan, lim validation.Limits) ([]byte, int) {
	t.Helper()
	var b bytes.Buffer
	n, err := validation.Write(context.Background(), strings.NewReader(data), f, p, lim, &b)
	if err != nil {
		t.Fatal(err)
	}
	return b.Bytes(), n
}

// schemaValid fails t unless doc is a canonical-event.schema.json instance.
func schemaValid(t testing.TB, doc []byte) {
	t.Helper()
	issues, err := validate.Document(doc, "canonical-event.schema.json", "SCHEMA")
	if err != nil || len(issues) > 0 {
		t.Fatalf("%s is not a canonical event: %v %v", doc, issues, err)
	}
}

// TEST-013 (happy path): every row of a valid station log maps; the
// canonical dataset is a JSON array of schema-valid events in row order,
// whose size Validate predicted, the same bytes on every read and however
// the file arrives; the formula-like serial is data, kept as it is.
func TestAValidLog(t *testing.T) {
	p := plan(t, stationMapping(), stationColumns)
	out := run(t, validLog, importprofile.CSV, p, validation.DefaultLimits)
	if !out.Valid() || out.Rows != 3 || out.InvalidRows != 0 || len(out.Issues) != 0 || len(out.Errors) != 0 || len(out.Preview) != 3 {
		t.Fatalf("%+v", out)
	}
	data, n := write(t, validLog, importprofile.CSV, p, validation.DefaultLimits)
	if n != 3 || int64(len(data)) != out.DatasetBytes {
		t.Fatalf("%d rows, %d bytes; predicted %d", n, len(data), out.DatasetBytes)
	}
	var events []jsontext.Value
	if err := json.Unmarshal(data, &events); err != nil || len(events) != 3 {
		t.Fatalf("%s: %v", data, err)
	}
	for _, e := range events {
		schemaValid(t, e)
	}
	want := `{"case_id":"SN-000123","event_type":"operation","timestamp":"2026-10-13T06:12:00+08:00","end_timestamp":"2026-10-13T06:15:10+08:00",` +
		`"stage":"fct","duration_ms":190000,"outcome":"pass","metadata":{"批號":"L-7"}}`
	if string(events[0]) != want || string(out.Preview[0]) != want {
		t.Errorf("first event %s, preview %s", events[0], out.Preview[0])
	}
	if !strings.Contains(string(events[2]), `"case_id":"=HYPERLINK(\"http://x.invalid\")"`) || !strings.Contains(string(events[2]), `{"批號":"L-7, 第二批"}`) {
		t.Errorf("third event %s", events[2])
	}
	if strings.Contains(string(data), "op-7") {
		t.Errorf("the operator column was imported: %s", data)
	}
	again, _ := write(t, validLog, importprofile.CSV, p, validation.DefaultLimits)
	var slow bytes.Buffer
	if _, err := validation.Write(context.Background(), iotest.OneByteReader(strings.NewReader(validLog)), importprofile.CSV, p,
		validation.DefaultLimits, &slow); err != nil || !bytes.Equal(again, data) || !bytes.Equal(slow.Bytes(), data) {
		t.Errorf("the dataset is not deterministic: %v", err)
	}
}

// TEST-012, TEST-013 (negative): each row that does not map is invalid,
// with every issue of it at its source cell (/<row>/<column>; a field
// count at the row), the record's line, a fixed message and no value; the
// canonical preview holds null for it; Write refuses the file.
func TestInvalidRows(t *testing.T) {
	data := header +
		row("SN-1", "2026-10-13T06:12:00+08:00", "2026-10-13T06:15:10+08:00", "pass", "190000", "") +
		row("SN-2", "2026/10/13 06:16", "2026-10-13T06:19:02+08:00", "pass", "", "") +
		row("", "2026-10-13T06:16:00+08:00", "2026-10-13T06:19:02+08:00", "pass", "", "") +
		row("SN-4", "2026-10-13T06:16:00+08:00", "2026-10-13T06:15:02+08:00", "pass", "-5", "") +
		"SN-5,operation,fct\r\n" +
		row("SN-6", "2026-10-13T06:12:00+08:00", "2026-10-13T06:15:10+08:00", "pass", "190500", "") +
		"\"SN-7\nx\",operation,fct,2026-10-13T06:12:00+08:00,,pass,op-7,,,=1+1\r\n"
	p := plan(t, stationMapping(), stationColumns)
	out := run(t, data, importprofile.CSV, p, validation.DefaultLimits)
	if out.Valid() || out.Rows != 7 || out.InvalidRows != 6 {
		t.Fatalf("%+v", out)
	}
	type is struct{ code, path string }
	var got []is
	for _, i := range out.Issues {
		got = append(got, is{i.Code, i.Path})
		if strings.Contains(i.Message, "2026") || strings.Contains(i.Message, "SN-") || strings.Contains(i.Message, "=1+1") {
			t.Errorf("the message quotes the file: %q", i.Message)
		}
	}
	want := []is{
		{mapping.CodeValueInvalid, "/1/開始"},
		{mapping.CodeValueMissing, "/2/序號"},
		{mapping.CodeEndBeforeStart, "/3/結束"}, {mapping.CodeValueInvalid, "/3/耗時毫秒"}, // in mapping order
		{importprofile.CodeFieldCount, "/4"},
		{mapping.CodeDurationInconsistent, "/5/耗時毫秒"},
		{importprofile.CodeFieldCount, "/6"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("issues %v\nwant %v", got, want)
	}
	var lines []string
	for _, e := range out.Errors {
		lines = append(lines, fmt.Sprintf("%s %d %s %s", e.Path, e.Line, e.Column, e.Code))
		if e.Value != "" {
			t.Errorf("a report entry quotes a value: %+v", e)
		}
	}
	// A short row names its first missing column; a long one none.
	wantLines := []string{"/1/開始 3 開始 " + mapping.CodeValueInvalid, "/2/序號 4 序號 " + mapping.CodeValueMissing,
		"/3/結束 5 結束 " + mapping.CodeEndBeforeStart, "/3/耗時毫秒 5 耗時毫秒 " + mapping.CodeValueInvalid,
		"/4 6 開始 " + importprofile.CodeFieldCount, "/5/耗時毫秒 7 耗時毫秒 " + mapping.CodeDurationInconsistent,
		"/6 8  " + importprofile.CodeFieldCount}
	if fmt.Sprint(lines) != fmt.Sprint(wantLines) {
		t.Errorf("report %v\nwant %v", lines, wantLines)
	}
	if len(out.Preview) != 7 || string(out.Preview[0]) == "null" || string(out.Preview[1]) != "null" || string(out.Preview[6]) != "null" {
		t.Errorf("preview %s", out.Preview)
	}
	if _, err := validation.Write(context.Background(), strings.NewReader(data), importprofile.CSV, p, validation.DefaultLimits, &bytes.Buffer{}); !errors.Is(err, validation.ErrChanged) {
		t.Errorf("Write of an invalid file: %v", err)
	}
	// The report a person downloads has no cell a spreadsheet would run.
	rendered, err := report.Render(out.Errors, out.MoreErrors, report.MaxBytes)
	if err != nil || bytes.Contains(rendered, []byte("=1+1")) {
		t.Fatalf("%v %s", err, rendered)
	}
}

// JSON events: members are read by name, a missing member is empty, a
// number is a text field's literal; other members are not read.
func TestJSONEvents(t *testing.T) {
	m := mapping.Mapping{Mappings: []mapping.Entry{entry("serial", mapping.CaseID), entry("type", mapping.EventType),
		entry("at", mapping.Timestamp), entry("ms", mapping.DurationMS), entry("extra", mapping.Metadata)}}
	cols := []string{"serial", "type", "at", "ms", "extra", "operator"}
	p := plan(t, m, cols)
	data := `[{"serial":12345,"type":"operation","at":"2026-10-13T06:12:00Z","ms":1500,"extra":{"line":[1,2.50]},"operator":"op-7"},` +
		`{"type":"operation","at":"2026-10-13T06:12:00Z"},` +
		`{"serial":"SN-3","type":"operation","at":"2026-10-13T06:12:00Z","ms":"15.0","operator":{"a":1}}]`
	out := run(t, data, importprofile.JSONEvents, p, validation.DefaultLimits)
	if out.Rows != 3 || out.InvalidRows != 2 || len(out.Issues) != 2 || out.Issues[0].Path != "/1/serial" || out.Issues[1].Path != "/2/ms" {
		t.Fatalf("%+v", out)
	}
	if string(out.Preview[0]) != `{"case_id":"12345","event_type":"operation","timestamp":"2026-10-13T06:12:00Z","duration_ms":1500,"metadata":{"extra":{"line":[1,2.50]}}}` {
		t.Errorf("preview %s", out.Preview[0])
	}
	if out.Errors[0].Line != 0 || out.Errors[0].Column != "serial" {
		t.Errorf("a JSON row has no line: %+v", out.Errors[0])
	}
}

// D-072's bounds: at most MaxIssues issues and MaxRowErrors report
// entries are kept, the rest counted; the canonical preview keeps its rows
// and bytes, metadata strings cut (the dataset keeps them whole); a dataset
// past its limit is the dataset's issue.
func TestBounds(t *testing.T) {
	p := plan(t, stationMapping(), stationColumns)
	var b strings.Builder
	b.WriteString(header)
	for i := range 150 {
		b.WriteString(row(fmt.Sprintf("SN-%d", i), "yesterday", "", "pass", "", ""))
	}
	lim := validation.DefaultLimits
	lim.MaxIssues, lim.MaxRowErrors = 10, 20
	out := run(t, b.String(), importprofile.CSV, p, lim)
	if out.InvalidRows != 150 || len(out.Issues) != 10 || len(out.Errors) != 20 || out.MoreErrors != 130 || len(out.Preview) != 100 {
		t.Errorf("%d invalid, %d issues, %d errors, %d more, %d preview", out.InvalidRows, len(out.Issues), len(out.Errors), out.MoreErrors, len(out.Preview))
	}

	long := strings.Repeat("長", 300)
	data := header + row("SN-1", "2026-10-13T06:12:00Z", "", "pass", "", long) + row("SN-2", "2026-10-13T06:12:00Z", "", "pass", "", "x")
	lim = validation.DefaultLimits
	out = run(t, data, importprofile.CSV, p, lim)
	if !out.Valid() || !strings.Contains(string(out.Preview[0]), strings.Repeat("長", 255)+"…") || strings.Contains(string(out.Preview[0]), strings.Repeat("長", 256)) {
		t.Errorf("preview %s", out.Preview[0])
	}
	if dataset, _ := write(t, data, importprofile.CSV, p, lim); !strings.Contains(string(dataset), long) {
		t.Error("the dataset cut a value")
	}
	lim.PreviewRows = 1
	if out := run(t, data, importprofile.CSV, p, lim); len(out.Preview) != 1 {
		t.Errorf("%d preview rows", len(out.Preview))
	}
	lim = validation.DefaultLimits
	lim.PreviewBytes = len(out.Preview[0]) + 2
	if out := run(t, data, importprofile.CSV, p, lim); len(out.Preview) != 1 {
		t.Errorf("%d preview rows within %d bytes", len(out.Preview), lim.PreviewBytes)
	}

	lim = validation.DefaultLimits
	lim.MaxDatasetBytes = out.DatasetBytes - 1
	small := run(t, data, importprofile.CSV, p, lim)
	if small.Valid() || small.InvalidRows != 0 || len(small.Issues) != 1 || small.Issues[0].Code != validation.CodeDatasetTooLarge || small.Issues[0].Path != "" ||
		len(small.Errors) != 1 || small.Errors[0].Code != validation.CodeDatasetTooLarge {
		t.Errorf("%+v", small)
	}
	if _, err := validation.Write(context.Background(), strings.NewReader(data), importprofile.CSV, p, lim, &bytes.Buffer{}); !errors.Is(err, validation.ErrChanged) {
		t.Errorf("a dataset past its limit was written: %v", err)
	}
	lim.MaxDatasetBytes = out.DatasetBytes
	if exact := run(t, data, importprofile.CSV, p, lim); !exact.Valid() {
		t.Errorf("a dataset at its limit: %+v", exact.Issues)
	}
}

// Review round 1, M3: an offset RFC 3339 does not have (+24:00, +05:60)
// or the unknown offset (-00:00, L6) is an invalid row, never an event of
// a dataset; the offsets RFC 3339 has are kept as written.
func TestOffsets(t *testing.T) {
	p := plan(t, stationMapping(), stationColumns)
	for _, c := range []struct {
		start, end string
		column     string
	}{
		{"2026-10-13T06:12:00+24:00", "2026-10-13T06:15:10+24:00", "開始"},
		{"2026-10-13T06:12:00+05:60", "", "開始"},
		{"2026-10-13T06:12:00-00:00", "", "開始"},
		{"2026-10-13T06:12:00Z", "2026-10-13T06:15:10-24:00", "結束"},
	} {
		data := header + row("SN-1", c.start, c.end, "pass", "", "L-7")
		out := run(t, data, importprofile.CSV, p, validation.DefaultLimits)
		if out.Valid() || out.InvalidRows != 1 || len(out.Issues) < 1 || out.Issues[0].Path != "/0/"+c.column ||
			out.Issues[0].Code != mapping.CodeValueInvalid {
			t.Errorf("%s %s: %+v", c.start, c.end, out)
		}
		if _, err := validation.Write(context.Background(), strings.NewReader(data), importprofile.CSV, p, validation.DefaultLimits, &bytes.Buffer{}); !errors.Is(err, validation.ErrChanged) {
			t.Errorf("%s: written: %v", c.start, err)
		}
	}
	data := header + row("SN-1", "2026-10-13T06:12:00+23:59", "2026-10-13T06:15:10-23:59", "pass", "", "L-7")
	if out := run(t, data, importprofile.CSV, p, validation.DefaultLimits); !out.Valid() {
		t.Fatalf("+23:59: %+v", out.Issues)
	}
	dataset, _ := write(t, data, importprofile.CSV, p, validation.DefaultLimits)
	if !strings.Contains(string(dataset), `"timestamp":"2026-10-13T06:12:00+23:59"`) {
		t.Errorf("%s", dataset)
	}
}

// Review round 1, L4 (R17): a column name's '~' and '/' are escaped in an
// issue's path as JSON Pointer says (~0 and ~1).
func TestPointersEscapeColumnNames(t *testing.T) {
	cols := []string{"case/id", "type~1", "at"}
	m := mapping.Mapping{Mappings: []mapping.Entry{entry("case/id", mapping.CaseID), entry("type~1", mapping.EventType), entry("at", mapping.Timestamp)}}
	p := plan(t, m, cols)
	out := run(t, "case/id,type~1,at\n,,2026-10-13T06:12:00Z\n", importprofile.CSV, p, validation.DefaultLimits)
	var paths []string
	for _, is := range out.Issues {
		paths = append(paths, is.Path)
	}
	if fmt.Sprint(paths) != "[/0/case~1id /0/type~01]" {
		t.Errorf("paths %v", paths)
	}
	if len(out.Errors) != 2 || out.Errors[0].Path != "/0/case~1id" || out.Errors[0].Column != "case/id" || out.Errors[1].Column != "type~1" {
		t.Errorf("report entries %+v", out.Errors)
	}
}

// Review round 1, L4 (R19): the canonical preview takes a row only while
// the whole preview fits PreviewBytes, the brackets and commas counted.
func TestPreviewByteBoundary(t *testing.T) {
	p := plan(t, stationMapping(), stationColumns)
	data := header + row("SN-1", "2026-10-13T06:12:00Z", "", "pass", "", "L-7") + row("SN-2", "2026-10-13T06:13:00Z", "", "pass", "", "")
	all := run(t, data, importprofile.CSV, p, validation.DefaultLimits)
	if len(all.Preview) != 2 {
		t.Fatalf("%d preview rows", len(all.Preview))
	}
	exact := 2 + len(all.Preview[0]) + 1 + len(all.Preview[1]) // [a,b]
	lim := validation.DefaultLimits
	lim.PreviewBytes = exact
	if out := run(t, data, importprofile.CSV, p, lim); len(out.Preview) != 2 {
		t.Errorf("at the limit: %d rows", len(out.Preview))
	}
	lim.PreviewBytes = exact - 1
	if out := run(t, data, importprofile.CSV, p, lim); len(out.Preview) != 1 {
		t.Errorf("one byte short: %d rows", len(out.Preview))
	}
	lim.PreviewBytes = 2 + len(all.Preview[0]) - 1
	if out := run(t, data, importprofile.CSV, p, lim); len(out.Preview) != 0 {
		t.Errorf("no room for the first row: %d rows", len(out.Preview))
	}
}

// Review round 1, L4 (R21): Write maps every row again and refuses a file
// in which a row no longer maps, whatever Validate said before.
func TestWriteRefusesARowThatDoesNotMap(t *testing.T) {
	p := plan(t, stationMapping(), stationColumns)
	data := validLog + row("SN-000125", "yesterday", "", "pass", "", "")
	if _, err := validation.Write(context.Background(), strings.NewReader(data), importprofile.CSV, p, validation.DefaultLimits, &bytes.Buffer{}); !errors.Is(err, validation.ErrChanged) {
		t.Errorf("a row that does not map was written: %v", err)
	}
	data = validLog + row("SN-000125", "2026-10-13T06:12:00Z", "2026-10-13T06:11:00Z", "pass", "", "")
	if _, err := validation.Write(context.Background(), strings.NewReader(data), importprofile.CSV, p, validation.DefaultLimits, &bytes.Buffer{}); !errors.Is(err, validation.ErrChanged) {
		t.Errorf("an end before its start was written: %v", err)
	}
}

// The file must be the one the plan was made for, and the profile's rules
// still hold: other columns are ErrChanged, a broken file the profile's
// failure, an ended context its error.
func TestTheFileIsTheProfiled(t *testing.T) {
	p := plan(t, stationMapping(), stationColumns)
	other := strings.Replace(validLog, "站別", "工站", 1)
	if _, err := validation.Validate(context.Background(), strings.NewReader(other), importprofile.CSV, p, validation.DefaultLimits); !errors.Is(err, validation.ErrChanged) {
		t.Errorf("other columns: %v", err)
	}
	if _, err := validation.Write(context.Background(), strings.NewReader(other), importprofile.CSV, p, validation.DefaultLimits, &bytes.Buffer{}); !errors.Is(err, validation.ErrChanged) {
		t.Errorf("Write with other columns: %v", err)
	}
	var fail *importprofile.Failure
	if _, err := validation.Validate(context.Background(), strings.NewReader(header+"\"x\n"), importprofile.CSV, p, validation.DefaultLimits); !errors.As(err, &fail) {
		t.Errorf("a broken file: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := validation.Validate(ctx, strings.NewReader(validLog), importprofile.CSV, p, validation.DefaultLimits); !errors.Is(err, context.Canceled) {
		t.Errorf("an ended context: %v", err)
	}
	if _, err := validation.Validate(context.Background(), strings.NewReader("{}"), importprofile.JSONModel, p, validation.DefaultLimits); err == nil {
		t.Error("a JSON model was validated as rows")
	}
	if _, err := validation.Validate(context.Background(), strings.NewReader(validLog), importprofile.CSV, p, validation.Limits{}); err == nil {
		t.Error("unset limits")
	}
}

// warnOn is a pack's check (D-059) for the tests: a warning at the stage
// of a row whose stage is xray, one at the outcome of a row whose outcome
// is PASS, and one about a product (a field the mapping leaves out) for
// every row of SN-9; calls counts the events it is given.
func warnOn(calls *int) validation.EventCheck {
	return func(e domainpack.CanonicalEvent) []domainpack.EventIssue {
		*calls++
		var out []domainpack.EventIssue
		if e.Stage == "xray" {
			out = append(out, domainpack.EventIssue{Field: "stage", Code: "EVENT_STAGE_UNKNOWN", Message: "stage: not a station"})
		}
		if e.Outcome == "PASS" {
			out = append(out, domainpack.EventIssue{Field: "outcome", Code: "EVENT_OUTCOME_UNKNOWN", Message: "outcome: not pass or fail"})
		}
		if e.CaseID == "SN-9" {
			out = append(out, domainpack.EventIssue{Field: "product", Code: "EVENT_PRODUCT_MISSING", Message: "product: missing"})
		}
		return out
	}
}

func srow(serial, stage, result string) string {
	return fmt.Sprintf("%s,operation,%s,2026-10-13T06:12:00+08:00,2026-10-13T06:15:10+08:00,%s,op-7,,\r\n", serial, stage, result)
}

// The pack's data-quality rules (D-059) are warnings (D-072 (b)): each at
// its source cell, or at the row for a field the mapping leaves out; the
// rows stay valid, the canonical dataset is the same, the error report
// holds none of them; the import's issues list the rows' errors first and
// then the warnings, within MaxIssues, and Warnings counts them all. An
// invalid row is not checked. A check that answers what no issue may be is
// the server's defect: Validate fails.
func TestPackWarnings(t *testing.T) {
	p := plan(t, stationMapping(), stationColumns)
	data := header + srow("SN-1", "fct", "pass") + srow("SN-2", "xray", "PASS") + srow("SN-9", "ict", "pass") + srow("SN-4", "fct", "fail")
	calls := 0
	out, err := validation.ValidateWith(context.Background(), strings.NewReader(data), importprofile.CSV, p, validation.DefaultLimits, warnOn(&calls))
	if err != nil {
		t.Fatal(err)
	}
	plain := run(t, data, importprofile.CSV, p, validation.DefaultLimits)
	type is struct{ code, path, severity string }
	var got []is
	for _, i := range out.Issues {
		got = append(got, is{i.Code, i.Path, string(i.Severity)})
	}
	want := []is{{"EVENT_STAGE_UNKNOWN", "/1/站別", "warning"}, {"EVENT_OUTCOME_UNKNOWN", "/1/結果", "warning"}, {"EVENT_PRODUCT_MISSING", "/2", "warning"}}
	if !out.Valid() || out.InvalidRows != 0 || out.Warnings != 3 || len(out.Errors) != 0 || calls != 4 || fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("valid %v, %d warnings, %d errors, %d calls, issues %v", out.Valid(), out.Warnings, len(out.Errors), calls, got)
	}
	if out.DatasetBytes != plain.DatasetBytes || fmt.Sprint(out.Preview) != fmt.Sprint(plain.Preview) || len(plain.Issues) != 0 || plain.Warnings != 0 {
		t.Errorf("the warnings changed the dataset or the preview, or appear without a check: %+v", plain)
	}

	// Errors first, then warnings; an invalid row is not checked.
	bad := header + srow("SN-1", "xray", "pass") +
		"SN-2,operation,fct,2026-10-13T06:16:00+08:00,2026-10-13T06:15:02+08:00,pass,op-7,,\r\n" + srow("SN-3", "xray", "PASS")
	calls = 0
	lim := validation.DefaultLimits
	lim.MaxIssues = 3
	out, err = validation.ValidateWith(context.Background(), strings.NewReader(bad), importprofile.CSV, p, lim, warnOn(&calls))
	if err != nil {
		t.Fatal(err)
	}
	got = nil
	for _, i := range out.Issues {
		got = append(got, is{i.Code, i.Path, string(i.Severity)})
	}
	want = []is{{mapping.CodeEndBeforeStart, "/1/結束", "error"}, {"EVENT_STAGE_UNKNOWN", "/0/站別", "warning"}, {"EVENT_STAGE_UNKNOWN", "/2/站別", "warning"}}
	if out.Valid() || out.InvalidRows != 1 || out.Warnings != 3 || calls != 2 || len(out.Errors) != 1 || fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("valid %v, %d invalid, %d warnings, %d calls, %d report entries, issues %v", out.Valid(), out.InvalidRows, out.Warnings, calls,
			len(out.Errors), got)
	}

	for name, issue := range map[string]domainpack.EventIssue{
		"no code":            {Field: "stage", Message: "stage: x"},
		"a lower-case code":  {Field: "stage", Code: "event_stage", Message: "stage: x"},
		"a code of 101":      {Field: "stage", Code: strings.Repeat("E", 101), Message: "stage: x"},
		"no message":         {Field: "stage", Code: "EVENT_X"},
		"a message of 2001":  {Field: "stage", Code: "EVENT_X", Message: strings.Repeat("x", 2001)},
		"not an event field": {Field: "station", Code: "EVENT_X", Message: "station: x"},
		"a message with NUL": {Field: "stage", Code: "EVENT_X", Message: "stage: \x00"},
	} {
		check := func(domainpack.CanonicalEvent) []domainpack.EventIssue { return []domainpack.EventIssue{issue} }
		if _, err := validation.ValidateWith(context.Background(), strings.NewReader(data), importprofile.CSV, p, validation.DefaultLimits, check); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
