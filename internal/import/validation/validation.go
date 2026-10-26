// Package validation reads every row of an uploaded CSV or JSON events
// file through a person's confirmed mapping (T-017; APPROVED D-009, D-043;
// D-071, D-072 APPROVED; TEST-012, TEST-013): Validate counts the rows that
// do not map to a valid canonical event, keeps the first issues for the
// import's validation and the first errors for its error report, the
// canonical events of the preview's rows and the size the canonical dataset
// would have; Write reads the file again, once every row is valid, and
// writes the canonical dataset that a commit freezes (T-018).
//
// The file is read with internal/import/profile's Scan: the same rules and
// limits as the profile it passed, so the rows are the profile's rows.
// Nothing reported quotes the file's values (D-072): an issue has a typed
// code, a JSON Pointer into the file seen as an array of rows
// (/<row>/<source column>, /<row> for the whole row, "" for the dataset)
// and a fixed message; a report entry has the same and the record's line,
// with an empty value. The canonical preview holds the mapped values
// themselves, as the raw preview holds the file's (UNTRUSTED_DATA).
//
// It is pure: it reads and writes bytes (architecture rule
// import-parsing-is-pure); the job that stores what it finds is
// internal/import/validator.
package validation

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/import/mapping"
	importprofile "github.com/kcl1104/geminus/internal/import/profile"
	"github.com/kcl1104/geminus/internal/import/report"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// Limits bound what a validation keeps and what a dataset may be.
type Limits struct {
	// File is the profile's: the same file, read by the same rules.
	File importprofile.Limits
	// MaxIssues are kept for the import's validation (common Validation
	// takes 1000; D-072: 100), MaxRowErrors for its error report.
	MaxIssues, MaxRowErrors int
	// MaxDatasetBytes bounds the canonical dataset (D-071: 256 MiB).
	MaxDatasetBytes int64
	// The canonical preview: the first PreviewRows rows (at most the raw
	// preview's), metadata strings cut to PreviewStringChars characters,
	// PreviewBytes in all as the database stores it (D-043 (b)).
	PreviewRows, PreviewStringChars, PreviewBytes int
}

// DefaultLimits are D-071's and D-072's (implementation parameters to be
// confirmed by the benchmark, T-037; like CB-39(c)).
var DefaultLimits = Limits{
	File:      importprofile.DefaultLimits,
	MaxIssues: 100, MaxRowErrors: 10_000, MaxDatasetBytes: 256 << 20,
	PreviewRows: 100, PreviewStringChars: 256, PreviewBytes: 1 << 20,
}

func (l Limits) valid() bool {
	return l.MaxIssues >= 0 && l.MaxIssues <= 1000 && l.MaxRowErrors >= 0 && l.MaxDatasetBytes >= 2 &&
		l.PreviewRows >= 0 && l.PreviewRows <= 100 && l.PreviewStringChars >= 1 && l.PreviewBytes >= 2
}

// CodeDatasetTooLarge: every row maps, but the canonical dataset would be
// larger than MaxDatasetBytes (mapping fewer columns to metadata makes it
// smaller).
const CodeDatasetTooLarge = "DATASET_TOO_LARGE"

// CodeEventNotCanonical: a row mapped without an issue, yet its event is
// not a canonical-event.schema.json instance. The mapping's rules are meant
// to be the schema's, so this is a defect of the server; the row is
// invalid and never part of a dataset (T-017 review round 1, M3: fail
// closed).
const CodeEventNotCanonical = "EVENT_NOT_CANONICAL"

const notCanonical = "the row's canonical event does not match canonical-event.schema.json"

// canonical reports whether doc, an event as encode wrote it, is a
// canonical-event.schema.json instance: the last check of every event
// before it may be part of a dataset (a variable for the package's tests).
var canonical = func(doc []byte) bool {
	issues, err := validate.Document(doc, "canonical-event.schema.json", CodeEventNotCanonical)
	return err == nil && len(issues) == 0
}

// ErrChanged: the file Write reads is not the file Validate accepted (a
// row no longer maps, its columns differ, or it grew past the dataset's
// limit); nothing it wrote may be used.
var ErrChanged = errors.New("validation: the file is not the one that was validated")

// Outcome is what Validate found.
type Outcome struct {
	// Rows are the file's data rows, InvalidRows those that do not map to
	// a valid canonical event.
	Rows, InvalidRows int
	// Issues are the first MaxIssues issues: the rows' errors, then the
	// pack's warnings (with a dataset issue, if any, after them); Errors
	// the first MaxRowErrors for the report, and MoreErrors how many more
	// there are. Warnings counts the pack's warnings (ValidateWith), which
	// are never in the report.
	Issues     []model.ValidationIssue
	Errors     []report.Entry
	MoreErrors int
	Warnings   int
	// Preview is the canonical preview: one JSON value per row of the
	// first rows, the row's canonical event or null; never nil.
	Preview []jsontext.Value
	// DatasetBytes is the size of the canonical dataset Write would write
	// (its valid rows' events).
	DatasetBytes int64
	tooLarge     bool
}

// Valid reports whether every row maps to a valid canonical event and the
// dataset fits: the import is then VALIDATED, and Write may write it.
func (o Outcome) Valid() bool { return o.InvalidRows == 0 && !o.tooLarge }

// columns reads one row's values for the plan.
type columns struct {
	plan  *mapping.Plan
	names map[string][]int // a JSON member's plan entries
	dec   *jsontext.Decoder
}

func newColumns(p *mapping.Plan) *columns {
	c := &columns{plan: p, names: map[string][]int{}, dec: jsontext.NewDecoder(bytes.NewReader(nil))}
	for i := range p.Len() {
		c.names[p.Name(i)] = append(c.names[p.Name(i)], i)
	}
	return c
}

// rowFail is a row that does not map as a whole (a CSV field count): its
// code, message and the column it names ("" for none).
type rowFail struct{ code, message, column string }

// values reads row r's values; fail is set for a row that cannot be mapped
// at all, err when the file is not the one the plan was made for.
func (c *columns) values(r importprofile.Row, out []mapping.Value) (fail *rowFail, err error) {
	if r.Event == nil {
		for i := range out {
			col := c.plan.Column(i)
			if col >= len(r.Header) || r.Header[col] != c.plan.Name(i) {
				return nil, ErrChanged
			}
		}
		if r.FieldCount != len(r.Header) {
			f := &rowFail{code: importprofile.CodeFieldCount,
				message: "the row has " + strconv.Itoa(r.FieldCount) + " fields; the header has " + strconv.Itoa(len(r.Header))}
			if r.FieldCount < len(r.Header) {
				f.column = r.Header[r.FieldCount]
			}
			return f, nil
		}
		for i := range out {
			out[i] = mapping.Text(r.Fields[c.plan.Column(i)])
		}
		return nil, nil
	}
	for i := range out {
		out[i] = mapping.Missing()
	}
	c.dec.Reset(bytes.NewReader(r.Event), jsontext.AllowDuplicateNames(true)) // checked by the profile
	if _, err := c.dec.ReadToken(); err != nil {
		return nil, err
	}
	for c.dec.PeekKind() != '}' {
		name, err := c.dec.ReadToken()
		if err != nil {
			return nil, err
		}
		entries, wanted := c.names[name.String()]
		if !wanted {
			if err := c.dec.SkipValue(); err != nil {
				return nil, err
			}
			continue
		}
		v, err := c.dec.ReadValue()
		if err != nil {
			return nil, err
		}
		for _, i := range entries {
			out[i] = mapping.JSON(bytes.Clone(v))
		}
	}
	return nil, nil
}

// pointer is the JSON Pointer of a row, or of a row's column, in the file
// seen as an array of rows.
func pointer(row int, column string) string {
	p := "/" + strconv.Itoa(row)
	if column != "" {
		var b []byte
		for i := 0; i < len(column); i++ {
			switch column[i] {
			case '~':
				b = append(b, '~', '0')
			case '/':
				b = append(b, '~', '1')
			default:
				b = append(b, column[i])
			}
		}
		p += "/" + string(b)
	}
	return p
}

// encode is the canonical dataset's form of an event.
func encode(e mapping.Event) ([]byte, error) { return json.Marshal(e.CanonicalEvent) }

// preview keeps the canonical preview within its bounds.
type preview struct {
	lim  Limits
	rows []jsontext.Value
	size int
	full bool
}

func (p *preview) add(e *mapping.Event) error {
	if p.full || len(p.rows) >= p.lim.PreviewRows {
		return nil
	}
	v, extra := jsontext.Value("null"), 0
	if e != nil {
		ev := e.CanonicalEvent
		if len(ev.Metadata) > 0 {
			m, x, err := importprofile.PreviewJSON(ev.Metadata, p.lim.PreviewStringChars)
			if err != nil {
				return err
			}
			ev.Metadata, extra = m, x
		}
		data, err := json.Marshal(ev)
		if err != nil {
			return err
		}
		v = data
	}
	cost := len(v) + extra
	if len(p.rows) > 0 {
		cost++
	}
	if p.size+cost > p.lim.PreviewBytes {
		p.full = true
		return nil
	}
	p.rows = append(p.rows, v)
	p.size += cost
	return nil
}

// EventCheck is a pack's data-quality check of one valid canonical event
// (domainpack.EventRulesProvider, D-059; T-052).
type EventCheck func(domainpack.CanonicalEvent) []domainpack.EventIssue

// ErrCheck: the pack's check answered something that is not an issue an
// import may record (a code, field or message out of bounds): a defect of
// the server, so the validation fails (fail closed).
var ErrCheck = errors.New("validation: the pack's event check gave an issue an import cannot record")

var issueCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,99}$`)

// warning is the pack's issue e, at /<row>/<column> of the field's column,
// or at the row for a field the plan does not fill.
func warning(plan *mapping.Plan, row int, e domainpack.EventIssue) (model.ValidationIssue, error) {
	n := utf8.RuneCountInString(e.Message)
	if !issueCode.MatchString(e.Code) || n < 1 || n > 2000 || strings.ContainsRune(e.Message, 0) || !utf8.ValidString(e.Message) {
		return model.ValidationIssue{}, ErrCheck
	}
	column := ""
	if e.Field != "" {
		if !slices.Contains(mapping.Fields, mapping.Field(e.Field)) {
			return model.ValidationIssue{}, ErrCheck
		}
		for i := range plan.Len() {
			if plan.Field(i) == mapping.Field(e.Field) {
				column = plan.Name(i)
				break
			}
		}
	}
	return model.NewIssue(e.Code, pointer(row, column), e.Message, model.SeverityWarning), nil
}

// Validate is ValidateWith without a pack's check.
func Validate(ctx context.Context, r io.Reader, f importprofile.Format, plan *mapping.Plan, lim Limits) (Outcome, error) {
	return ValidateWith(ctx, r, f, plan, lim, nil)
}

// ValidateWith reads the file r holds (CSV or JSON events, the import's
// format) and maps every row with plan; check, when set, is the pack's
// data-quality rules (D-059) for every row that maps: their issues are
// warnings (D-072 (b)), which leave the row valid. The error is the
// profile's *importprofile.Failure when the file breaks a rule (it is not
// the file that was profiled), ErrChanged when its columns are not the
// plan's, ErrCheck, ctx's error, or r's own.
func ValidateWith(ctx context.Context, r io.Reader, f importprofile.Format, plan *mapping.Plan, lim Limits, check EventCheck) (Outcome, error) {
	if !lim.valid() {
		return Outcome{}, errors.New("validation: a limit is not set")
	}
	cols := newColumns(plan)
	values := make([]mapping.Value, plan.Len())
	pv := &preview{lim: lim, size: 2}
	var out Outcome
	var warnings []model.ValidationIssue
	valid := 0
	keep := func(is model.ValidationIssue, e report.Entry) {
		if len(out.Issues) < lim.MaxIssues {
			out.Issues = append(out.Issues, is)
		}
		if len(out.Errors) < lim.MaxRowErrors {
			out.Errors = append(out.Errors, e)
		} else {
			out.MoreErrors++
		}
	}
	res, err := importprofile.Scan(ctx, r, f, lim.File, func(row importprofile.Row) error {
		fail, err := cols.values(row, values)
		if err != nil {
			return err
		}
		if fail != nil {
			out.InvalidRows++
			path := pointer(row.Index, "")
			keep(model.NewIssue(fail.code, path, fail.message, model.SeverityError),
				report.Entry{Path: path, Line: row.Line, Column: fail.column, Code: fail.code, Message: fail.message})
			return pv.add(nil)
		}
		e, issues := plan.Apply(values)
		if len(issues) > 0 {
			out.InvalidRows++
			for _, is := range issues {
				column := ""
				if is.Entry >= 0 {
					column = plan.Name(is.Entry)
				}
				path := pointer(row.Index, column)
				keep(model.NewIssue(is.Code, path, is.Message, model.SeverityError),
					report.Entry{Path: path, Line: row.Line, Column: column, Code: is.Code, Message: is.Message})
			}
			return pv.add(nil)
		}
		data, err := encode(e)
		if err != nil {
			return err
		}
		if !canonical(data) {
			out.InvalidRows++
			path := pointer(row.Index, "")
			keep(model.NewIssue(CodeEventNotCanonical, path, notCanonical, model.SeverityError),
				report.Entry{Path: path, Line: row.Line, Code: CodeEventNotCanonical, Message: notCanonical})
			return pv.add(nil)
		}
		out.DatasetBytes += int64(len(data))
		valid++
		if check != nil {
			for _, w := range check(e.CanonicalEvent) {
				is, err := warning(plan, row.Index, w)
				if err != nil {
					return err
				}
				if out.Warnings++; len(warnings) < lim.MaxIssues {
					warnings = append(warnings, is)
				}
			}
		}
		return pv.add(&e)
	})
	if err != nil {
		return Outcome{}, err
	}
	// The rows' errors first (they keep the import from VALIDATED), then
	// the warnings, MaxIssues in all.
	out.Issues = append(out.Issues, warnings[:min(len(warnings), lim.MaxIssues-len(out.Issues))]...)
	out.Rows = res.Profile.Rows
	if valid > 0 {
		out.DatasetBytes += int64(valid + 1) // the brackets and the commas
	} else {
		out.DatasetBytes = 2
	}
	if out.DatasetBytes > lim.MaxDatasetBytes {
		out.tooLarge = true
		msg := fmt.Sprintf("the canonical dataset would be larger than %d bytes; map fewer columns to metadata", lim.MaxDatasetBytes)
		out.Issues = append(out.Issues, model.NewIssue(CodeDatasetTooLarge, "", msg, model.SeverityError))
		if len(out.Errors) < lim.MaxRowErrors {
			out.Errors = append(out.Errors, report.Entry{Code: CodeDatasetTooLarge, Message: msg})
		} else {
			out.MoreErrors++
		}
	}
	out.Preview = pv.rows
	if out.Preview == nil {
		out.Preview = []jsontext.Value{}
	}
	return out, nil
}

// Write reads the file r holds again and writes to w its canonical
// dataset: a JSON array of the rows' canonical events, in row order,
// compact, the bytes a commit freezes and hashes (D-019). The file must be
// the one Validate accepted: a row that does not map, other columns, or a
// dataset past MaxDatasetBytes is ErrChanged. It returns the rows written.
func Write(ctx context.Context, r io.Reader, f importprofile.Format, plan *mapping.Plan, lim Limits, w io.Writer) (int, error) {
	if !lim.valid() {
		return 0, errors.New("validation: a limit is not set")
	}
	cols := newColumns(plan)
	values := make([]mapping.Value, plan.Len())
	var written int64
	rows := 0
	put := func(b []byte) error {
		if written += int64(len(b)); written > lim.MaxDatasetBytes {
			return ErrChanged
		}
		_, err := w.Write(b)
		return err
	}
	_, err := importprofile.Scan(ctx, r, f, lim.File, func(row importprofile.Row) error {
		fail, err := cols.values(row, values)
		if err != nil {
			return err
		}
		if fail != nil {
			return ErrChanged
		}
		e, issues := plan.Apply(values)
		if len(issues) > 0 {
			return ErrChanged
		}
		data, err := encode(e)
		if err != nil {
			return err
		}
		if !canonical(data) {
			return ErrChanged
		}
		sep := []byte(",")
		if rows == 0 {
			sep = []byte("[")
		}
		if err := put(sep); err != nil {
			return err
		}
		rows++
		return put(data)
	})
	if err != nil {
		return 0, err
	}
	if rows == 0 {
		return 0, ErrChanged // a profiled file has rows
	}
	return rows, put([]byte("]"))
}
