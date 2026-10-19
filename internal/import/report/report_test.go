package report_test

import (
	"bytes"
	"encoding/csv"
	"strconv"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/import/report"
)

// TEST-029: a cell a spreadsheet would run as a formula (or as DDE) starts
// with an apostrophe, so it shows as text; nothing else changes.
func TestSafe(t *testing.T) {
	cases := map[string]string{
		"=1+2":                                   "'=1+2",
		"+1":                                     "'+1",
		"-1":                                     "'-1",
		"@SUM(A1)":                               "'@SUM(A1)",
		"\t=1":                                   "'\t=1",
		"\r=1":                                   "'\r=1",
		"\n=1":                                   "'\n=1",
		"  =1":                                   "'  =1",
		" \t@x":                                  "' \t@x",
		"＝1":                                     "'＝1",
		"＋1":                                     "'＋1",
		"－1":                                     "'－1",
		"＠x":                                     "'＠x",
		"　=1":                                    "'　=1",
		"=cmd|' /C calc'!A0":                     "'=cmd|' /C calc'!A0",
		"=HYPERLINK(\"http://x.invalid\",\"x\")": "'=HYPERLINK(\"http://x.invalid\",\"x\")",
		"plain":                                  "plain",
		"":                                       "",
		"   ":                                    "   ",
		"a=b":                                    "a=b",
		"'=1":                                    "'=1",
		"案件編號=1":                                 "案件編號=1",
		"1-2":                                    "1-2",
		"/0/a":                                   "/0/a",
	}
	for in, want := range cases {
		if got := report.Safe(in); got != want {
			t.Errorf("Safe(%q) = %q, want %q", in, got, want)
		}
	}
}

func parse(t *testing.T, data []byte) [][]string {
	t.Helper()
	if !bytes.HasPrefix(data, []byte("\xef\xbb\xbf")) {
		t.Fatalf("no UTF-8 BOM: %q", data[:min(len(data), 8)])
	}
	r := csv.NewReader(bytes.NewReader(data[3:]))
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("the report is not RFC 4180 CSV: %v", err)
	}
	return rows
}

// The report is CSV with a BOM and CRLF line ends, a fixed header, one row
// per entry, and every cell neutralised: column names and values come from
// the file, and messages may quote them.
func TestRender(t *testing.T) {
	entries := []report.Entry{
		{Path: "/1", Line: 3, Column: "=c", Code: "CSV_FIELD_COUNT", Message: "the row has 2 fields; the header has 3"},
		{Path: "/2", Line: 5, Column: "", Code: "CSV_FIELD_COUNT", Message: "+x", Value: "=cmd|' /C calc'!A0"},
		{Path: "/3/備註", Line: 6, Column: "備註", Code: "X", Message: "a, \"quoted\"\nmessage", Value: "@SUM(1,2)"},
	}
	data, err := report.Render(entries, 0, report.MaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("\r\n")) || bytes.Count(data, []byte("\r\n")) < 4 {
		t.Errorf("not CRLF: %q", data)
	}
	rows := parse(t, data)
	want := [][]string{
		{"path", "line", "column", "code", "message", "value"},
		{"/1", "3", "'=c", "CSV_FIELD_COUNT", "the row has 2 fields; the header has 3", ""},
		{"/2", "5", "", "CSV_FIELD_COUNT", "'+x", "'=cmd|' /C calc'!A0"},
		{"/3/備註", "6", "備註", "X", "a, \"quoted\"\nmessage", "'@SUM(1,2)"},
	}
	if len(rows) != len(want) {
		t.Fatalf("%d rows: %q", len(rows), rows)
	}
	for i := range want {
		if strings.Join(rows[i], "\x1f") != strings.Join(want[i], "\x1f") {
			t.Errorf("row %d: %q, want %q", i, rows[i], want[i])
		}
	}
	// No cell of the output starts with a formula character.
	for _, row := range rows {
		for _, cell := range row {
			if report.Safe(cell) != cell {
				t.Errorf("cell %q is not safe", cell)
			}
		}
	}
	// An entry without a line has an empty line cell.
	data, _ = report.Render([]report.Entry{{Path: "/0", Code: "X", Message: "m"}}, 0, report.MaxBytes)
	if rows := parse(t, data); rows[1][1] != "" {
		t.Errorf("line %q", rows[1][1])
	}
}

// A report is bounded: entries that do not fit, and those the caller did
// not keep, are counted in a last ERRORS_TRUNCATED row; the whole stays
// within the limit.
func TestRenderIsBounded(t *testing.T) {
	var entries []report.Entry
	for i := range 50 {
		entries = append(entries, report.Entry{Path: "/" + strconv.Itoa(i), Line: i + 2, Code: "CSV_FIELD_COUNT",
			Message: "the row has 4 fields; the header has 3", Value: strings.Repeat("值", 20)})
	}
	const limit = 1500
	data, err := report.Render(entries, 7, limit)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > limit {
		t.Errorf("%d bytes, limit %d", len(data), limit)
	}
	rows := parse(t, data)
	last := rows[len(rows)-1]
	listed := len(rows) - 2
	if last[3] != "ERRORS_TRUNCATED" || last[4] != strconv.Itoa(50-listed+7)+" more errors are not listed" || listed < 1 || listed >= 50 {
		t.Errorf("%d listed, last row %q", listed, last)
	}
	// Nothing left out: no truncation row.
	data, _ = report.Render(entries[:2], 0, report.MaxBytes)
	if rows := parse(t, data); len(rows) != 3 {
		t.Errorf("%d rows", len(rows))
	}
	// Only the omitted count.
	data, _ = report.Render(nil, 3, report.MaxBytes)
	if rows := parse(t, data); len(rows) != 2 || rows[1][4] != "3 more errors are not listed" {
		t.Errorf("%q", rows)
	}
	if _, err := report.Render(entries, 0, 10); err == nil {
		t.Error("a limit below the header and the truncation row was accepted")
	}
}
