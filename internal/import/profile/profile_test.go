package profile_test

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
	"unicode/utf8"

	"github.com/kcl1104/geminus/internal/contracttest"
	importprofile "github.com/kcl1104/geminus/internal/import/profile"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	return contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata", "contracts", name))
}

// small limits keep the limit tests fast; the defaults are checked apart.
var small = importprofile.Limits{
	MaxRows: 5, MaxColumns: 4, MaxNameChars: 8, MaxFieldChars: 12, MaxDepth: 6, MaxModelBytes: 256,
	PreviewRows: 3, PreviewStringChars: 6, PreviewBytes: 4096, MaxRowErrors: 2,
}

func read(t *testing.T, f importprofile.Format, lim importprofile.Limits, data string) (importprofile.Result, error) {
	t.Helper()
	return importprofile.Read(context.Background(), strings.NewReader(data), f, lim)
}

// failure returns the *Failure of a read that must fail on the file.
func failure(t *testing.T, f importprofile.Format, lim importprofile.Limits, data string) *importprofile.Failure {
	t.Helper()
	_, err := read(t, f, lim, data)
	var fail *importprofile.Failure
	if !errors.As(err, &fail) {
		t.Fatalf("%q: want a *Failure, got %v", data, err)
	}
	return fail
}

// TEST-012: what makes a file unreadable is a typed failure with a code,
// a JSON Pointer into the file seen as an array of rows (the document, for
// a JSON model) and a message without the file's bytes.
func TestUnreadableFiles(t *testing.T) {
	long := strings.Repeat("x", 13)
	cases := []struct {
		name   string
		format importprofile.Format
		data   string
		code   string
		path   string
		says   string // part of the message
	}{
		// Encoding (D-043: UTF-8, with or without BOM; nothing is guessed).
		{"Big5 bytes", importprofile.CSV, "a,b\n1,\xa4\xa4\xa4\xe5\n", importprofile.CodeEncodingInvalid, "", "line 2"},
		{"a lone continuation byte", importprofile.CSV, "a\n\x80\n", importprofile.CodeEncodingInvalid, "", "line 2"},
		{"a truncated sequence at the end", importprofile.CSV, "a\n\xe4\xb8", importprofile.CodeEncodingInvalid, "", "line 2"},
		{"an encoded surrogate", importprofile.CSV, "a\n\xed\xa0\x80\n", importprofile.CodeEncodingInvalid, "", "line 2"},
		{"an overlong encoding", importprofile.CSV, "a\n\xc0\xaf\n", importprofile.CodeEncodingInvalid, "", "line 2"},
		{"UTF-16 LE", importprofile.CSV, "\xff\xfea\x00\n\x00", importprofile.CodeEncodingInvalid, "", "UTF-16"},
		{"UTF-16 BE", importprofile.JSONEvents, "\xfe\xff\x00[", importprofile.CodeEncodingInvalid, "", "UTF-16"},
		{"a NUL byte", importprofile.CSV, "a,b\n1,\x00\n", importprofile.CodeNULCharacter, "", "line 2"},
		{"Latin-1 in JSON", importprofile.JSONEvents, "[{\"a\":\"caf\xe9\"}]", importprofile.CodeEncodingInvalid, "", "line 1"},
		// Empty files.
		{"no bytes", importprofile.CSV, "", importprofile.CodeEmpty, "", "empty"},
		{"a BOM only", importprofile.CSV, "\ufeff", importprofile.CodeEmpty, "", "empty"},
		{"a header only", importprofile.CSV, "a,b\r\n", importprofile.CodeEmpty, "", "no rows"},
		{"blank lines only", importprofile.CSV, "\n\n\n", importprofile.CodeEmpty, "", "empty"},
		{"an empty array", importprofile.JSONEvents, " [ ] ", importprofile.CodeEmpty, "", "no rows"},
		{"whitespace", importprofile.JSONEvents, "  \n", importprofile.CodeEmpty, "", "empty"},
		// CSV syntax (RFC 4180, comma separated, strict quotes).
		{"a bare quote", importprofile.CSV, "a,b\n1,x\"y\n", importprofile.CodeCSVSyntax, "/0", "line 2"},
		{"an unterminated quote", importprofile.CSV, "a,b\n1,2\n3,\"x\n", importprofile.CodeCSVSyntax, "/1", "line 3"},
		{"text after a closing quote", importprofile.CSV, "a\n\"x\"y\n", importprofile.CodeCSVSyntax, "/0", "line 2"},
		// Columns (D-043: 1–256 characters, at most 256, each once).
		{"an empty column name", importprofile.CSV, "a,,c\n1,2,3\n", importprofile.CodeColumnInvalid, "", "column 2"},
		{"a repeated column name", importprofile.CSV, "a,b,a\n1,2,3\n", importprofile.CodeColumnInvalid, "", "column 3"},
		{"a column name too long", importprofile.CSV, "a,abcdefghi\n1,2\n", importprofile.CodeColumnInvalid, "", "column 2"},
		{"too many columns", importprofile.CSV, "a,b,c,d,e\n1,2,3,4,5\n", importprofile.CodeLimitExceeded, "", "5 columns"},
		{"an empty member name", importprofile.JSONEvents, `[{"a":1},{"":2}]`, importprofile.CodeColumnInvalid, "/1/", "member name"},
		{"a member name too long", importprofile.JSONEvents, `[{"abcdefghi":1}]`, importprofile.CodeColumnInvalid, "/0/abcdefghi", "member name"},
		{"too many members", importprofile.JSONEvents, `[{"a":1,"b":2},{"c":3,"d":4,"e":5}]`, importprofile.CodeLimitExceeded, "/1/e", "columns"},
		// Size limits (D-007: rows; field length in characters; depth).
		{"too many rows", importprofile.CSV, "a\n1\n2\n3\n4\n5\n6\n", importprofile.CodeLimitExceeded, "/5", "5 rows"},
		{"too many events", importprofile.JSONEvents, `[{},{},{},{},{},{}]`, importprofile.CodeLimitExceeded, "/5", "5 rows"},
		{"a long CSV value", importprofile.CSV, "a,b\n1," + long + "\n", importprofile.CodeLimitExceeded, "/0/b", "13 characters"},
		{"a long value beyond the header", importprofile.CSV, "a\n1," + long + "\n", importprofile.CodeLimitExceeded, "/0", "13 characters"},
		{"a long JSON string", importprofile.JSONEvents, `[{"a":"` + long + `"}]`, importprofile.CodeLimitExceeded, "/0/a", "13 characters"},
		{"a long JSON value", importprofile.JSONEvents, `[{"a":[1,2,3,4,5,6,7]}]`, importprofile.CodeLimitExceeded, "/0/a", "characters"},
		{"too deep", importprofile.JSONEvents, `[{"a":[[[[[1]]]]]}]`, importprofile.CodeLimitExceeded, "/0/a/0/0/0/0", "depth"},
		{"a model too large", importprofile.JSONModel, `{"a":"` + strings.Repeat("x", 300) + `"}`, importprofile.CodeLimitExceeded, "", "256 bytes"},
		{"a model too deep", importprofile.JSONModel, `{"a":[[[[[[1]]]]]]}`, importprofile.CodeLimitExceeded, "/a/0/0/0/0/0", "depth"},
		// JSON (duplicate members and non-finite numbers fail, D-014).
		{"a syntax error", importprofile.JSONEvents, `[{"a":1},{"b":x}]`, importprofile.CodeJSONInvalid, "/1/b", "not valid JSON"},
		{"a duplicate member", importprofile.JSONEvents, `[{"a":1},{"b":1,"b":2}]`, importprofile.CodeJSONDuplicateKey, "/1/b", "repeats"},
		{"a nested duplicate member", importprofile.JSONEvents, `[{"m":{"k":1,"k":2}}]`, importprofile.CodeJSONDuplicateKey, "/0/m/k", "repeats"},
		{"a duplicate in a model", importprofile.JSONModel, `{"nodes":[],"nodes":[]}`, importprofile.CodeJSONDuplicateKey, "/nodes", "repeats"},
		{"an infinite number", importprofile.JSONEvents, `[{"a":1e400}]`, importprofile.CodeJSONNumberNotFinite, "/0/a", "finite"},
		{"a negative infinite number", importprofile.JSONEvents, `[{"m":{"k":[0,-1E999]}}]`, importprofile.CodeJSONNumberNotFinite, "/0/m/k/1", "finite"},
		{"an infinite number in a model", importprofile.JSONModel, `{"x":{"v":2e308}}`, importprofile.CodeJSONNumberNotFinite, "/x/v", "finite"},
		{"NaN", importprofile.JSONEvents, `[{"a":NaN}]`, importprofile.CodeJSONInvalid, "/0/a", "not valid JSON"},
		{"an escaped NUL", importprofile.JSONEvents, `[{"a":"x\u0000"}]`, importprofile.CodeNULCharacter, "/0/a", "U+0000"},
		{"an escaped NUL in a name", importprofile.JSONEvents, `[{"m":{"\u0000":1}}]`, importprofile.CodeNULCharacter, "/0/m", "U+0000"},
		{"a lone surrogate escape", importprofile.JSONEvents, `[{"a":"\ud800"}]`, importprofile.CodeJSONInvalid, "/0/a", "not valid JSON"},
		{"data after the array", importprofile.JSONEvents, `[{"a":1}] {}`, importprofile.CodeJSONInvalid, "", "after"},
		{"data after the model", importprofile.JSONModel, `{"a":1}]`, importprofile.CodeJSONInvalid, "", "after"},
		{"two models", importprofile.JSONModel, `{} {}`, importprofile.CodeJSONInvalid, "", "after"},
		// Shapes (API_DATABASE §5: an events array of objects; a model object).
		{"events in an object", importprofile.JSONEvents, `{"a":1}`, importprofile.CodeShapeInvalid, "", "array"},
		{"an event that is not an object", importprofile.JSONEvents, `[{"a":1},[1]]`, importprofile.CodeShapeInvalid, "/1", "object"},
		{"a model that is an array", importprofile.JSONModel, `[]`, importprofile.CodeShapeInvalid, "", "object"},
		{"an empty model file", importprofile.JSONModel, ``, importprofile.CodeEmpty, "", "empty"},
		{"a CSV given as JSON", importprofile.JSONEvents, "a,b\n1,2\n", importprofile.CodeJSONInvalid, "", "not valid JSON"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := failure(t, c.format, small, c.data)
			if f.Code != c.code || f.Path != c.path || !strings.Contains(f.Message, c.says) {
				t.Errorf("got %s at %q: %q; want %s at %q, saying %q", f.Code, f.Path, f.Message, c.code, c.path, c.says)
			}
			// The same failure whatever the reads look like.
			_, err := importprofile.Read(context.Background(), iotest.OneByteReader(strings.NewReader(c.data)), c.format, small)
			var again *importprofile.Failure
			if !errors.As(err, &again) || *again != *f {
				t.Errorf("one byte at a time: %v, want %v", err, f)
			}
		})
	}
}

// A failure's message never echoes the file's bytes (it reaches logs and
// the import's error; SECURITY §7): here a value that looks like a secret.
func TestFailureMessagesHoldNoFileContent(t *testing.T) {
	secret := "SECRET-" + strings.Repeat("z", 3)
	for _, data := range []string{
		"a,b\n" + secret + ",\xa4\n",
		"a,b\n" + secret + ",x\"" + secret + "\n",
		"a,b\n1," + secret + secret + "\n",
	} {
		f := failure(t, importprofile.CSV, small, data)
		if strings.Contains(f.Message, "SECRET") {
			t.Errorf("%s: %q", f.Code, f.Message)
		}
	}
	// The JSON parser's own messages quote the input; they are not used.
	for _, data := range []string{
		`[{"a":"x"},{"b":SECRET}]`,
		`[{"a":"\ud800SECRET"}]`,
		`[{"a":"SECRET`,
		`[{"a":1}] SECRET`,
	} {
		f := failure(t, importprofile.JSONEvents, importprofile.DefaultLimits, data)
		if strings.Contains(f.Message, "SECRET") {
			t.Errorf("%s: %q", f.Code, f.Message)
		}
	}
}

func columnsOf(r importprofile.Result) map[string]importprofile.Column {
	out := map[string]importprofile.Column{}
	for _, c := range r.Profile.Columns {
		out[c.Name] = c
	}
	return out
}

func preview(t *testing.T, r importprofile.Result) []map[string]any {
	t.Helper()
	out := []map[string]any{}
	for _, v := range r.Preview {
		var m map[string]any
		if err := json.Unmarshal(v, &m); err != nil {
			t.Fatalf("preview row %s: %v", v, err)
		}
		out = append(out, m)
	}
	return out
}

// D-043 / T-016: UTF-8 with a BOM and Chinese column names and values is
// profiled and previewed as written; lengths count characters, not bytes.
func TestChineseCSVWithBOM(t *testing.T) {
	data := "\ufeff案件編號,階段,開始時間,耗時毫秒,備註\r\n" +
		"CASE-01,檢驗,2026-10-15T08:30:00+08:00,7200000,\r\n" +
		"CASE-02,組裝,2026-10-15T08:31:00+08:00,,\r\n"
	for _, name := range []string{"whole", "one byte at a time"} {
		var r io.Reader = strings.NewReader(data)
		if name != "whole" {
			r = iotest.OneByteReader(r)
		}
		res, err := importprofile.Read(context.Background(), r, importprofile.CSV, importprofile.DefaultLimits)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		want := importprofile.Profile{Format: importprofile.CSV, Encoding: importprofile.UTF8BOM, Rows: 2, Columns: []importprofile.Column{
			{Name: "案件編號", Index: 0, Inferred: importprofile.String, NonEmpty: 2, MaxLength: 7},
			{Name: "階段", Index: 1, Inferred: importprofile.String, NonEmpty: 2, MaxLength: 2},
			{Name: "開始時間", Index: 2, Inferred: importprofile.Timestamp, NonEmpty: 2, MaxLength: 25},
			{Name: "耗時毫秒", Index: 3, Inferred: importprofile.Integer, NonEmpty: 1, MaxLength: 7},
			{Name: "備註", Index: 4, Inferred: importprofile.Empty, NonEmpty: 0, MaxLength: 0},
		}}
		if got, _ := json.Marshal(res.Profile); string(got) != string(must(json.Marshal(want))) {
			t.Errorf("%s: profile %s", name, got)
		}
		p := preview(t, res)
		if len(p) != 2 || p[0]["階段"] != "檢驗" || p[1]["耗時毫秒"] != "" || p[0]["耗時毫秒"] != "7200000" || len(p[0]) != 5 {
			t.Errorf("%s: preview %v", name, p)
		}
		if res.InvalidRows != 0 || len(res.RowErrors) != 0 {
			t.Errorf("%s: %d invalid rows", name, res.InvalidRows)
		}
	}
	// The contract fixture is this file's profile and preview.
	res, err := read(t, importprofile.CSV, importprofile.DefaultLimits, data)
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Profile importprofile.Profile `json:"profile"`
		Preview []jsontext.Value      `json:"preview"`
	}
	if err := json.Unmarshal(readFixture(t, "valid-import.json"), &fixture); err != nil {
		t.Fatal(err)
	}
	if got, want := must(json.Marshal(res.Profile)), must(json.Marshal(fixture.Profile)); string(got) != string(want) {
		t.Errorf("profile %s, fixture %s", got, want)
	}
	for i := range fixture.Preview {
		var a, b any
		_ = json.Unmarshal(res.Preview[i], &a)
		_ = json.Unmarshal(fixture.Preview[i], &b)
		if string(must(json.Marshal(a, json.Deterministic(true)))) != string(must(json.Marshal(b, json.Deterministic(true)))) {
			t.Errorf("preview %d: %s, fixture %s", i, res.Preview[i], fixture.Preview[i])
		}
	}
}

// The same Chinese file without a BOM, and as JSON events, is UTF-8 all the
// same: non-ASCII content never changes the encoding (no guessing).
func TestNoEncodingGuess(t *testing.T) {
	res, err := read(t, importprofile.CSV, importprofile.DefaultLimits, "名稱,數量\n電阻,10\n電容,20\n")
	if err != nil || res.Profile.Encoding != importprofile.UTF8 || res.Profile.Columns[0].Name != "名稱" || res.Profile.Columns[0].MaxLength != 2 {
		t.Fatalf("%+v %v", res.Profile, err)
	}
	ev, err := read(t, importprofile.JSONEvents, importprofile.DefaultLimits, "\ufeff[{\"名稱\":\"電阻\",\"數量\":10}]")
	if err != nil || ev.Profile.Encoding != importprofile.UTF8BOM || ev.Profile.Columns[0].Name != "名稱" || ev.Profile.Columns[1].Inferred != importprofile.Integer {
		t.Fatalf("%+v %v", ev.Profile, err)
	}
	// A value of exactly the field limit in four-byte characters is within it.
	limit := strings.Repeat("😀", small.MaxFieldChars)
	if _, err := read(t, importprofile.CSV, small, "a\n"+limit+"\n"); err != nil {
		t.Errorf("%d characters (%d bytes): %v", utf8.RuneCountInString(limit), len(limit), err)
	}
}

// What a value's text looks like decides its type (exactly as written, no
// trimming); a column's type is the one its non-empty values share.
func TestInference(t *testing.T) {
	cases := []struct {
		values []string
		want   importprofile.Type
	}{
		{[]string{"", ""}, importprofile.Empty},
		{[]string{"true", "FALSE", "True", ""}, importprofile.Boolean},
		{[]string{"0", "-12", "9007199254740993"}, importprofile.Integer},
		{[]string{"1", "2.5", "-3e-2", "4E+10"}, importprofile.Number},
		{[]string{"2026-10-15T08:30:00Z", "2026-10-15t08:30:00.123456789+08:00", "2026-02-28T23:59:59-00:30"}, importprofile.Timestamp},
		{[]string{"007"}, importprofile.String},
		{[]string{"+5"}, importprofile.String},
		{[]string{" 5"}, importprofile.String},
		{[]string{"1e400"}, importprofile.String},
		{[]string{"2026-13-01T00:00:00Z"}, importprofile.String},
		{[]string{"2026-10-15 08:30:00"}, importprofile.String},
		{[]string{"2026-10-15T08:30:60Z"}, importprofile.String},
		{[]string{"yes"}, importprofile.String},
		{[]string{"falſe"}, importprofile.String}, // a Unicode fold of "false" is not a boolean
		{[]string{"1", "true"}, importprofile.Mixed},
		{[]string{"=SUM(A1)", "-1"}, importprofile.Mixed},
	}
	for _, c := range cases {
		// Each value in column v of its own row, next to a string in w.
		res, err := read(t, importprofile.CSV, importprofile.DefaultLimits, "v,w\n"+strings.Join(c.values, ",x\n")+",x\n")
		if err != nil {
			t.Fatalf("%q: %v", c.values, err)
		}
		if got := columnsOf(res)["v"].Inferred; got != c.want {
			t.Errorf("%q: %s, want %s", c.values, got, c.want)
		}
	}
	ev, err := read(t, importprofile.JSONEvents, importprofile.DefaultLimits,
		`[{"b":true,"i":1,"n":1.5,"t":"2026-10-15T08:30:00Z","s":"x","o":{"k":1},"z":null,"e":"","m":1},`+
			`{"b":false,"i":-2,"n":3,"t":"2026-10-16T00:00:00Z","s":"42","o":[1],"z":null,"e":"","m":"1"}]`)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]importprofile.Type{"b": importprofile.Boolean, "i": importprofile.Integer, "n": importprofile.Number,
		"t": importprofile.Timestamp, "s": importprofile.String, "o": importprofile.Mixed, "z": importprofile.Empty,
		"e": importprofile.Empty, "m": importprofile.Mixed}
	cols := columnsOf(ev)
	for name, ty := range want {
		if cols[name].Inferred != ty {
			t.Errorf("JSON %s: %s, want %s", name, cols[name].Inferred, ty)
		}
	}
	if cols["z"].NonEmpty != 0 || cols["e"].NonEmpty != 0 || cols["o"].NonEmpty != 2 || cols["o"].MaxLength != 7 || cols["s"].MaxLength != 2 {
		t.Errorf("counts %+v", cols)
	}
	order := []string{}
	for _, c := range ev.Profile.Columns {
		order = append(order, c.Name)
	}
	if strings.Join(order, ",") != "b,i,n,t,s,o,z,e,m" {
		t.Errorf("columns in order of first appearance: %v", order)
	}
}

// A CSV row whose field count differs from the header is an invalid row:
// counted, listed (row, line, column, value) and left out of the column
// statistics; the preview shows the fields it has, under their columns.
func TestInvalidRows(t *testing.T) {
	data := "a,b,c\n" +
		"1,2,3\n" +
		"\"multi\nline\",5\n" + // two fields, starting on line 3
		"7,8,9,=cmd|' /C calc'!A0\n" + // four fields, line 5
		"10,11,12\n" +
		"x\n"
	res, err := read(t, importprofile.CSV, importprofile.Limits{MaxRows: 10, MaxColumns: 4, MaxNameChars: 8, MaxFieldChars: 64, MaxDepth: 6,
		MaxModelBytes: 256, PreviewRows: 10, PreviewStringChars: 64, PreviewBytes: 4096, MaxRowErrors: 2}, data)
	if err != nil {
		t.Fatal(err)
	}
	if res.Profile.Rows != 5 || res.InvalidRows != 3 {
		t.Fatalf("rows %d, invalid %d", res.Profile.Rows, res.InvalidRows)
	}
	want := []importprofile.RowError{
		{Row: 1, Line: 3, Column: "c", Code: importprofile.CodeFieldCount, Message: "the row has 2 fields; the header has 3"},
		{Row: 2, Line: 5, Column: "", Code: importprofile.CodeFieldCount, Message: "the row has 4 fields; the header has 3", Value: "=cmd|' /C calc'!A0"},
	}
	if len(res.RowErrors) != 2 || res.RowErrors[0] != want[0] || res.RowErrors[1] != want[1] || res.MoreRowErrors != 1 {
		t.Errorf("row errors %+v (+%d)", res.RowErrors, res.MoreRowErrors)
	}
	if res.RowErrors[0].Path() != "/1" {
		t.Errorf("path %s", res.RowErrors[0].Path())
	}
	cols := columnsOf(res)
	if cols["a"].NonEmpty != 2 || cols["a"].MaxLength != 2 || cols["c"].Inferred != importprofile.Integer {
		t.Errorf("statistics include invalid rows: %+v", cols)
	}
	p := preview(t, res)
	if len(p) != 5 || p[1]["a"] != "multi\nline" || p[1]["b"] != "5" || len(p[1]) != 2 || len(p[2]) != 3 || p[2]["c"] != "9" || p[4]["a"] != "x" {
		t.Errorf("preview %v", p)
	}
}

// The preview: the first rows only, raw values, strings cut to the limit
// with a final ellipsis, the whole within its byte budget.
func TestPreviewBounds(t *testing.T) {
	wide := small
	wide.MaxFieldChars = 64
	res, err := read(t, importprofile.JSONEvents, wide, `[{"a":"abcdefgh","n":1.50,"m":{"k":"0123456789"}},{"a":"=1"},{"a":"x"},{"a":"y"},{"a":"z"}]`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Preview) != 3 {
		t.Fatalf("%d preview rows", len(res.Preview))
	}
	if string(res.Preview[0]) != `{"a":"abcde…","n":1.50,"m":{"k":"01234…"}}` || string(res.Preview[1]) != `{"a":"=1"}` {
		t.Errorf("preview %s %s", res.Preview[0], res.Preview[1])
	}
	// The budget stops the preview at the last row that fits.
	tight := small
	tight.PreviewRows, tight.PreviewBytes = 100, 40
	res, err = read(t, importprofile.CSV, tight, "a\n0123456789\n0123456789\n0123456789\n0123456789\n")
	if err != nil {
		t.Fatal(err)
	}
	size := 2
	for _, v := range res.Preview {
		size += len(v) + 1
	}
	if len(res.Preview) != 2 || size > tight.PreviewBytes {
		t.Errorf("%d rows, %d bytes", len(res.Preview), size)
	}
	// A number the database would print in full counts at that size.
	tight.PreviewBytes = 200
	res, err = read(t, importprofile.JSONEvents, tight, `[{"a":1e300},{"a":1}]`)
	if err != nil || len(res.Preview) != 0 {
		t.Errorf("a number of 301 digits as stored took %d rows: %v", len(res.Preview), err)
	}
}

// A JSON model is profiled as one document: no rows, no columns, no
// preview; its content is validated later (T-017).
func TestJSONModel(t *testing.T) {
	res, err := read(t, importprofile.JSONModel, importprofile.DefaultLimits, "\ufeff"+string(readFixture(t, "valid-minimal-ir.json")))
	if err != nil {
		t.Fatal(err)
	}
	if res.Profile.Format != importprofile.JSONModel || res.Profile.Encoding != importprofile.UTF8BOM || res.Profile.Rows != 0 ||
		res.Profile.Columns == nil || len(res.Profile.Columns) != 0 || res.Preview == nil || len(res.Preview) != 0 {
		t.Errorf("%+v %v", res.Profile, res.Preview)
	}
}

// Reading stops with the context, and a read error of the store is passed
// on as it is, never as a failure of the file.
func TestCancellationAndReadErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, f := range []importprofile.Format{importprofile.CSV, importprofile.JSONEvents, importprofile.JSONModel} {
		if _, err := importprofile.Read(ctx, strings.NewReader("a\n1\n"), f, importprofile.DefaultLimits); !errors.Is(err, context.Canceled) {
			t.Errorf("%s: %v", f, err)
		}
	}
	// A context that ends in the middle of a large file.
	ctx, cancel = context.WithCancel(context.Background())
	r := &cancelAfter{r: strings.NewReader("a\n" + strings.Repeat("1\n", 50000)), n: 1000, cancel: cancel}
	if _, err := importprofile.Read(ctx, r, importprofile.CSV, importprofile.DefaultLimits); !errors.Is(err, context.Canceled) {
		t.Errorf("midway: %v", err)
	}
	boom := errors.New("the store failed")
	for f, start := range map[importprofile.Format]string{importprofile.CSV: "a,b\n1,2\n3,", importprofile.JSONEvents: `[{"a":1},`, importprofile.JSONModel: `{"a":`} {
		_, err := importprofile.Read(context.Background(), io.MultiReader(strings.NewReader(start), iotest.ErrReader(boom)), f, importprofile.DefaultLimits)
		var fail *importprofile.Failure
		if !errors.Is(err, boom) || errors.As(err, &fail) {
			t.Errorf("%s: %v", f, err)
		}
	}
	if _, err := importprofile.Read(context.Background(), strings.NewReader("a\n1\n"), "xlsx", importprofile.DefaultLimits); err == nil {
		t.Error("an unknown format was read")
	}
}

type cancelAfter struct {
	r      io.Reader
	n      int
	cancel func()
}

func (c *cancelAfter) Read(p []byte) (int, error) {
	if c.n--; c.n == 0 {
		c.cancel()
	}
	if len(p) > 16 {
		p = p[:16]
	}
	return c.r.Read(p)
}

// The limits D-007 and D-043 set, and the implementation parameters
// (benchmark pending, T-037).
func TestDefaultLimits(t *testing.T) {
	d := importprofile.DefaultLimits
	if d.MaxRows != 100_000 || d.MaxColumns != 256 || d.MaxNameChars != 256 || d.MaxFieldChars != 32_768 || d.MaxDepth != 32 ||
		d.MaxModelBytes != 2<<20 || d.PreviewRows != 100 || d.PreviewStringChars != 256 || d.PreviewBytes != 1<<20 || d.MaxRowErrors != 10_000 {
		t.Errorf("%+v", d)
	}
}

// The same file always gives the same result.
func TestDeterministic(t *testing.T) {
	data := `[{"z":1,"a":{"y":2,"b":[3,"4"]}},{"a":null,"q":"r"}]`
	a, err := read(t, importprofile.JSONEvents, importprofile.DefaultLimits, data)
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		b, _ := read(t, importprofile.JSONEvents, importprofile.DefaultLimits, data)
		if !bytes.Equal(must(json.Marshal(a)), must(json.Marshal(b))) {
			t.Fatal("results differ")
		}
	}
}

func must(b []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return b
}
