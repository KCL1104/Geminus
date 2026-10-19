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
// length (in characters), nesting depth, a model's size, the preview's rows,
// string length and bytes, and the invalid rows kept. A file that breaks a
// rule is a *Failure with a typed code, a JSON Pointer into the file seen as
// an array of rows (for a JSON model, into the document) and a message that
// never quotes the file's own bytes for CSV and encoding problems. Reading
// stops when the context ends.
package profile

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"strconv"

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
	// The preview: the first PreviewRows rows, string values cut to
	// PreviewStringChars characters, PreviewBytes in all as stored.
	PreviewRows, PreviewStringChars, PreviewBytes int
	// MaxRowErrors is how many invalid rows are kept for the report; the
	// rest are only counted.
	MaxRowErrors int
}

// DefaultLimits are D-007's and D-043's limits and the implementation's
// own (field length, depth, preview string and bytes, kept errors; to be
// confirmed by the benchmark, T-037).
var DefaultLimits = Limits{
	MaxRows: 100_000, MaxColumns: 256, MaxNameChars: 256, MaxFieldChars: 32_768, MaxDepth: 32,
	MaxModelBytes: validate.MaxIRBytes, PreviewRows: 100, PreviewStringChars: 256, PreviewBytes: 1 << 20, MaxRowErrors: 10_000,
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
// the whole file.
type Failure struct {
	Code, Message, Path string
}

func (f *Failure) Error() string {
	return "profile: " + f.Code + " at " + strconv.Quote(f.Path) + ": " + f.Message
}

func fail(code, path, format string, args ...any) *Failure {
	return &Failure{Code: code, Path: path, Message: fmt.Sprintf(format, args...)}
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
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	tr := newText(ctx, r)
	var res Result
	var err error
	switch f {
	case CSV:
		res, err = readCSV(ctx, tr, lim)
	case JSONEvents:
		res, err = readEvents(ctx, tr, lim)
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
