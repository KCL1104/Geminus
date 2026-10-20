// Package profile reads an uploaded import file once, streaming, and
// reports its profile, the first rows as a bounded preview, and the rows
// that are not valid (T-016; D-007, D-043; TEST-012). It is pure: it reads
// an io.Reader and returns values, and knows nothing of storage, tenants
// or HTTP. The job that stores what it finds is internal/import/profiler.
//
// What it accepts (API_DATABASE §5, D-043):
//   - CSV: RFC 4180, comma separated, strict quoting, the first record the
//     header. UTF-8, with or without a BOM (recorded as utf-8-bom); no other
//     encoding is guessed: invalid UTF-8, UTF-16 and NUL bytes fail. A
//     record whose field count differs from the header's is an invalid row
//     (counted, listed for the error report, left out of the column
//     statistics); every other problem fails the file.
//   - JSON events: one array of objects; the columns are the objects'
//     top-level member names in order of first appearance.
//   - JSON model: one object (a Model IR document, validated later, T-017).
//     JSON is UTF-8 (a BOM is ignored and recorded); repeated member names,
//     numbers that are not finite as binary64, U+0000 and data after the
//     value fail.
//
// Everything is bounded (Limits): rows, columns, column name and value
// length (in characters), nesting depth, a model's size, a CSV record's and
// a JSON row's size as written, the preview's rows, string length and
// bytes, and the invalid rows kept. The bounds hold while the file is read,
// so what profiling holds in memory never grows with the file (T-016
// review H1): a CSV record is split only once it is known to be within
// MaxRecordBytes, and of a row only the header's fields and one more are
// kept; the JSON decoder never holds more than one row (MaxRecordBytes,
// with the white space before it). A file that breaks a rule is a *Failure
// with a typed code, a JSON Pointer into the file seen as an array of rows
// (for a JSON model, into the document) of at most model.MaxIssuePath
// characters (a longer one is cut at a '/') and a message that never quotes
// the file's own bytes. Reading stops when the context ends.
package profile

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// Format is what the file holds (D-043 profile.format; the import's kind).
type Format string

const (
	CSV        Format = "csv"
	JSONEvents Format = "json_events"
	JSONModel  Format = "json_model"
)

// Encoding is how the file is encoded (D-043 profile.encoding).
type Encoding string

const (
	UTF8    Encoding = "utf-8"
	UTF8BOM Encoding = "utf-8-bom"
)

// Type is a column's inferred type (D-043).
type Type string

const (
	Empty     Type = "empty"
	Boolean   Type = "boolean"
	Integer   Type = "integer"
	Number    Type = "number"
	Timestamp Type = "timestamp"
	String    Type = "string"
	Mixed     Type = "mixed"
)

// Column is import.schema.json#/$defs/ProfileColumn.
type Column struct {
	Name      string `json:"name"`
	Index     int    `json:"index"`
	Inferred  Type   `json:"inferred"`
	NonEmpty  int    `json:"non_empty"`
	MaxLength int    `json:"max_length"`
}

// Profile is import.schema.json#/$defs/Profile.
type Profile struct {
	Format   Format   `json:"format"`
	Encoding Encoding `json:"encoding"`
	Rows     int      `json:"rows"`
	Columns  []Column `json:"columns"`
}

// Limits bound what a file may hold and what is kept of it.
type Limits struct {
	MaxRows       int // data rows (D-007: 100,000)
	MaxColumns    int // D-043: 256
	MaxNameChars  int // a column name (D-043: 256 characters)
	MaxFieldChars int // a value, in characters (a JSON value's compact text)
	MaxDepth      int // JSON nesting, the outermost array or object counting 1
	MaxModelBytes int // a JSON model file (D-029: the IR's 2 MiB)
	// MaxRecordBytes bounds a CSV record (the header included), from its
	// first byte to its line end, and a JSON events row with the white
	// space and comma before it, as written; also the bytes before a JSON
	// events array and after it.
	MaxRecordBytes int
	// The preview: the first PreviewRows rows, string values cut to
	// PreviewStringChars characters, PreviewBytes in all as stored.
	PreviewRows, PreviewStringChars, PreviewBytes int
	// MaxRowErrors is how many invalid rows are kept for the report; the
	// rest are only counted.
	MaxRowErrors int
}

// DefaultLimits are D-007's and D-043's limits and the implementation's
// own (field length, depth, record size, preview string and bytes, kept
// errors; to be confirmed by the benchmark, T-037; CB-39(c)).
var DefaultLimits = Limits{
	MaxRows: 100_000, MaxColumns: 256, MaxNameChars: 256, MaxFieldChars: 32_768, MaxDepth: 32,
	MaxModelBytes: validate.MaxIRBytes, MaxRecordBytes: 1 << 20,
	PreviewRows: 100, PreviewStringChars: 256, PreviewBytes: 1 << 20, MaxRowErrors: 10_000,
}

// valid reports whether every bound is set: at least 1 for what a file may
// hold, at least 0 for what is kept of it.
func (l Limits) valid() bool {
	return min(l.MaxRows, l.MaxColumns, l.MaxNameChars, l.MaxFieldChars, l.MaxDepth, l.MaxModelBytes, l.MaxRecordBytes) >= 1 &&
		min(l.PreviewRows, l.PreviewStringChars, l.PreviewBytes, l.MaxRowErrors) >= 0
}

// Failure codes: why a file cannot be profiled (the import's error,
// details[0].code), and the row error code.
const (
	CodeEncodingInvalid     = "IMPORT_ENCODING_INVALID"
	CodeNULCharacter        = "IMPORT_NUL_CHARACTER"
	CodeEmpty               = "IMPORT_EMPTY"
	CodeColumnInvalid       = "IMPORT_COLUMN_INVALID"
	CodeLimitExceeded       = "IMPORT_LIMIT_EXCEEDED"
	CodeShapeInvalid        = "IMPORT_SHAPE_INVALID"
	CodeCSVSyntax           = "CSV_SYNTAX_INVALID"
	CodeJSONInvalid         = "JSON_INVALID"
	CodeJSONDuplicateKey    = "JSON_DUPLICATE_KEY"
	CodeJSONNumberNotFinite = "JSON_NUMBER_NOT_FINITE"
	// CodeFieldCount is a row error, not a failure.
	CodeFieldCount = "CSV_FIELD_COUNT"
)

// Failure: the file cannot be profiled. Path is a JSON Pointer into the
// file seen as an array of rows (a JSON model: into the document), "" for
// the whole file, of at most model.MaxIssuePath characters.
type Failure struct {
	Code, Message, Path string
}

func (f *Failure) Error() string {
	return "profile: " + f.Code + " at " + strconv.Quote(f.Path) + ": " + f.Message
}

func fail(code, path, format string, args ...any) *Failure {
	return &Failure{Code: code, Path: boundPath(path), Message: fmt.Sprintf(format, args...)}
}

// boundPath is path cut, if it has more than model.MaxIssuePath
// characters, at the last '/' among its first model.MaxIssuePath+1: the
// pointer of an ancestor (T-016 review L1; as model.NewIssue cuts). Member
// names of any length thus never fill an import's error.
func boundPath(path string) string {
	chars := 0
	for i := range path {
		if chars == model.MaxIssuePath+1 {
			cut := path[:i]
			if j := strings.LastIndexByte(cut, '/'); j >= 0 {
				return cut[:j]
			}
			return ""
		}
		chars++
	}
	return path
}

// RowError is a row the profile counts as invalid: a CSV record whose
// field count differs from the header's.
type RowError struct {
	Row     int    // the data row, from 0
	Line    int    // the 1-based line where the record starts
	Column  string // the first missing column; "" when the row has too many fields
	Code    string
	Message string
	Value   string // the first field beyond the header, cut like a preview string
}

// Path is the JSON Pointer of the row in the file seen as an array of rows.
func (e RowError) Path() string { return "/" + strconv.Itoa(e.Row) }

// Result is what profiling found.
type Result struct {
	Profile Profile
	// Preview holds the first rows, each a JSON object of raw values keyed
	// by source column name (D-043(b)); never nil.
	Preview []jsontext.Value
	// InvalidRows counts the invalid rows; RowErrors are the first of them
	// (Limits.MaxRowErrors) and MoreRowErrors the others.
	InvalidRows   int
	RowErrors     []RowError
	MoreRowErrors int
}

// Read profiles the file r holds as format f. The error is a *Failure when
// the file breaks a rule, ctx's error when ctx ends, or r's own error
// (passed on as it is) when reading fails.
func Read(ctx context.Context, r io.Reader, f Format, lim Limits) (Result, error) {
	return read(ctx, r, f, lim, nil)
}

// Row is one data row of a CSV or JSON events file, as Scan hands it to
// its visitor; nothing in it may be kept after the call returns.
type Row struct {
	// Index is the data row, from 0 (its JSON Pointer is "/<Index>").
	Index int
	// Line is the 1-based line where a CSV record starts; 0 for JSON.
	Line int
	// Header is a CSV file's column names (the same slice for every row);
	// Fields are the record's fields, at most one more than the header,
	// and FieldCount how many it has (a row whose count differs from the
	// header's is an invalid row, CodeFieldCount).
	Header     []string
	Fields     []string
	FieldCount int
	// Event is a JSON events row: an object whose members the profile has
	// checked (no repeated name, finite numbers, no U+0000, the depth and
	// value limits).
	Event jsontext.Value
}

// Scan reads the file r holds, CSV or JSON events, exactly as Read does
// (the same rules, limits and failures; T-017 validates every row of the
// file that was profiled), calling visit with each data row as it is read.
// visit's error stops the scan and is returned as it is.
func Scan(ctx context.Context, r io.Reader, f Format, lim Limits, visit func(Row) error) (Result, error) {
	if f == JSONModel {
		return Result{}, errors.New("profile: a JSON model has no rows to scan")
	}
	if visit == nil {
		return Result{}, errors.New("profile: Scan needs a visitor")
	}
	return read(ctx, r, f, lim, visit)
}

// IsTimestamp reports whether s is an RFC 3339 date-time with an offset,
// as profiling infers a timestamp (and as the rfc3339 transform takes one,
// T-017).
func IsTimestamp(s string) bool { return isTimestamp(s) }

// IsBoolean reports whether s is true or false in ASCII letters of any
// case, as profiling infers a boolean (and as the boolean_literal
// transform takes one, T-017).
func IsBoolean(s string) bool { return isBoolean(s) }

func read(ctx context.Context, r io.Reader, f Format, lim Limits, visit func(Row) error) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if !lim.valid() {
		return Result{}, errors.New("profile: a limit is not set")
	}
	tr := newText(ctx, r)
	var res Result
	var err error
	switch f {
	case CSV:
		res, err = readCSV(ctx, tr, lim, visit)
	case JSONEvents:
		res, err = readEvents(ctx, tr, lim, visit)
	case JSONModel:
		res, err = readModel(tr, lim)
	default:
		return Result{}, errors.New("profile: unknown format " + strconv.Quote(string(f)))
	}
	if err != nil {
		return Result{}, err
	}
	res.Profile.Format, res.Profile.Encoding = f, tr.encoding()
	if res.Preview == nil {
		res.Preview = []jsontext.Value{}
	}
	if res.Profile.Columns == nil {
		res.Profile.Columns = []Column{}
	}
	return res, nil
}
