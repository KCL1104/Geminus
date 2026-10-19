// Package report writes an import's error report (T-016, REQ-007,
// TEST-029, SECURITY §6): the rows of an uploaded file that are not
// valid, as CSV a person opens in a spreadsheet. It is a derived artifact:
// the uploaded file itself is never changed.
//
// The report is RFC 4180 CSV with CRLF line ends and every cell quoted, in
// UTF-8 with a BOM (so spreadsheets read non-ASCII column names and
// values), with the header path,line,column,code,message,value. Column
// names and values come from the uploaded file, and messages may quote
// them, so every cell goes through Safe: a cell a spreadsheet would run as
// a formula starts with an apostrophe instead, and so does every part of
// it after a ',', ';', tab or line end, should a spreadsheet split it there
// (T-016 review M2: ';' is a list separator in many locales). A report is
// bounded (MaxBytes): what does not fit is counted in a last
// ERRORS_TRUNCATED row.
//
// The package is pure: it neither reads nor writes storage.
package report

import (
	"errors"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxBytes bounds a report (migration 0010: error_report_size_bytes).
const MaxBytes = 16 << 20

// Truncated is the code of the last row when entries were left out.
const Truncated = "ERRORS_TRUNCATED"

// Header is the report's first row.
var Header = []string{"path", "line", "column", "code", "message", "value"}

// Entry is one row of the report.
type Entry struct {
	// Path is the JSON Pointer of the row (or row and column) in the file
	// seen as an array of rows.
	Path string
	// Line is the 1-based line of the uploaded file where the record
	// starts; 0 when unknown (an empty cell).
	Line int
	// Column is the source column's name; "" for the whole row.
	Column string
	// Code and Message say what is wrong.
	Code, Message string
	// Value is (an excerpt of) the offending value; "" when there is none.
	Value string
}

// formula is what makes a spreadsheet run a cell (OWASP CSV injection):
// =, +, -, @ and their full-width forms, after any leading spaces.
func formula(r rune) bool {
	switch r {
	case '=', '+', '-', '@', '＝', '＋', '－', '＠':
		return true
	}
	return false
}

// separator is where a spreadsheet may split a cell: ',', ';' (the list
// separator of many locales), a tab or a line end.
func separator(r rune) bool {
	return r == ',' || r == ';' || r == '\t' || r == '\r' || r == '\n'
}

// startsFormula reports whether s, after white space and quotes, starts
// with a formula character.
func startsFormula(s string) bool {
	r, _ := utf8.DecodeRuneInString(strings.TrimLeftFunc(s, func(r rune) bool { return r == '"' || unicode.IsSpace(r) }))
	return formula(r)
}

// Safe returns cell as a spreadsheet shows it as text (OWASP CSV
// injection): a cell that starts with a tab, a carriage return or a line
// feed, or whose first character after white space and quotes is =, +, -
// or @ (or a full-width form), gets a leading apostrophe; so does every
// part of it after a separator (',', ';', a tab or a line end, "\r\n"
// being one) that would start that way, the apostrophe following the
// separator. Any other cell is returned unchanged; Safe(Safe(c)) is
// Safe(c).
func Safe(cell string) string {
	var b strings.Builder
	changed := false
	at := true // at the start of the cell or of a part
	for i := 0; i < len(cell); {
		r, w := utf8.DecodeRuneInString(cell[i:])
		if at && (startsFormula(cell[i:]) || (i == 0 && (r == '\t' || r == '\r' || r == '\n'))) {
			if !changed {
				b.Grow(len(cell) + 8)
				b.WriteString(cell[:i])
				changed = true
			}
			b.WriteByte('\'')
		}
		if changed {
			b.WriteString(cell[i : i+w])
		}
		at = separator(r) && !(r == '\r' && strings.HasPrefix(cell[i+w:], "\n")) // "\r\n" is one line end
		i += w
	}
	if !changed {
		return cell
	}
	return b.String()
}

// line is one row of the report: every cell UTF-8, safe and quoted, and
// CRLF at the end.
func line(cells []string) ([]byte, error) {
	var b []byte
	for i, c := range cells {
		c = strings.ToValidUTF8(c, "\uFFFD")
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, '"')
		b = append(b, strings.ReplaceAll(Safe(c), `"`, `""`)...)
		b = append(b, '"')
	}
	return append(b, '\r', '\n'), nil
}

func truncation(n int) ([]byte, error) {
	return line([]string{"", "", "", Truncated, strconv.Itoa(n) + " more errors are not listed", ""})
}

// Render writes entries as a report of at most max bytes. omitted counts
// errors the caller did not keep; with the entries that do not fit, they
// are counted in a last ERRORS_TRUNCATED row.
func Render(entries []Entry, omitted, max int) ([]byte, error) {
	out := []byte("\xef\xbb\xbf")
	head, err := line(Header)
	if err != nil {
		return nil, err
	}
	out = append(out, head...)
	// Room for the last row, whatever its count.
	reserve, err := truncation(omitted + len(entries))
	if err != nil {
		return nil, err
	}
	if len(out)+len(reserve) > max {
		return nil, errors.New("report: the limit is too small for the header and a truncation row")
	}
	listed := 0
	for _, e := range entries {
		ln := ""
		if e.Line > 0 {
			ln = strconv.Itoa(e.Line)
		}
		row, err := line([]string{e.Path, ln, e.Column, e.Code, e.Message, e.Value})
		if err != nil {
			return nil, err
		}
		if len(out)+len(row)+len(reserve) > max {
			break
		}
		out = append(out, row...)
		listed++
	}
	if left := omitted + len(entries) - listed; left > 0 {
		last, err := truncation(left)
		if err != nil {
			return nil, err
		}
		out = append(out, last...)
	}
	return out, nil
}
