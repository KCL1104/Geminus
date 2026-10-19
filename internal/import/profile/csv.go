package profile

import (
	"context"
	"encoding/csv"
	"errors"
	"io"
	"strconv"
	"unicode/utf8"
)

// pointer is the JSON Pointer of a row, or of a row's column, in the file
// seen as an array of rows.
func pointer(row int, column ...string) string {
	p := "/" + strconv.Itoa(row)
	for _, c := range column {
		p += "/" + escape(c)
	}
	return p
}

// escape is a JSON Pointer reference token (RFC 6901).
func escape(token string) string {
	out := make([]byte, 0, len(token))
	for i := 0; i < len(token); i++ {
		switch token[i] {
		case '~':
			out = append(out, '~', '0')
		case '/':
			out = append(out, '~', '1')
		default:
			out = append(out, token[i])
		}
	}
	return string(out)
}

// csvError is the failure of a record that does not parse (row -1: the
// header); the reader's own errors pass on as they are.
func csvError(err error, row int) error {
	var pe *csv.ParseError
	if !errors.As(err, &pe) {
		return err
	}
	path := ""
	if row >= 0 {
		path = pointer(row)
	}
	return fail(CodeCSVSyntax, path, "the record starting on line %d is not valid CSV (RFC 4180): %v at line %d, column %d",
		pe.StartLine, pe.Err, pe.Line, pe.Column)
}

func readCSV(ctx context.Context, tr *text, lim Limits) (Result, error) {
	cr := csv.NewReader(tr)
	cr.FieldsPerRecord = -1 // a different count is an invalid row, not a failure
	cr.ReuseRecord = true
	header, err := cr.Read()
	if err == io.EOF {
		return Result{}, fail(CodeEmpty, "", "the file is empty")
	}
	if err != nil {
		return Result{}, csvError(err, -1)
	}
	if len(header) > lim.MaxColumns {
		return Result{}, fail(CodeLimitExceeded, "", "the header has %d columns; the limit is %d", len(header), lim.MaxColumns)
	}
	names := make([]string, len(header))
	seen := make(map[string]int, len(header))
	for i, name := range header {
		n := utf8.RuneCountInString(name)
		if name == "" {
			return Result{}, fail(CodeColumnInvalid, "", "column %d of the header has no name", i+1)
		}
		if n > lim.MaxNameChars {
			return Result{}, fail(CodeColumnInvalid, "", "the name of column %d is %d characters; a column name is at most %d", i+1, n, lim.MaxNameChars)
		}
		if j, dup := seen[name]; dup {
			return Result{}, fail(CodeColumnInvalid, "", "column %d has the same name as column %d", i+1, j+1)
		}
		seen[name], names[i] = i, name
	}
	stats := make([]colStats, len(names))
	pv := newPreview(lim)
	var res Result
	for row := 0; ; row++ {
		if row%256 == 0 {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
		}
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Result{}, csvError(err, row)
		}
		if row >= lim.MaxRows {
			return Result{}, fail(CodeLimitExceeded, pointer(row), "the file has more than %d rows", lim.MaxRows)
		}
		line, _ := cr.FieldPos(0)
		for i, v := range rec {
			if n := utf8.RuneCountInString(v); n > lim.MaxFieldChars {
				path := pointer(row)
				if i < len(names) {
					path = pointer(row, names[i])
				}
				return Result{}, fail(CodeLimitExceeded, path, "line %d: a value of %d characters; a value is at most %d", line, n, lim.MaxFieldChars)
			}
		}
		if len(rec) == len(names) {
			for i, v := range rec {
				stats[i].add(classifyText(v), utf8.RuneCountInString(v))
			}
		} else {
			res.InvalidRows++
			e := RowError{Row: row, Line: line, Code: CodeFieldCount,
				Message: "the row has " + strconv.Itoa(len(rec)) + " fields; the header has " + strconv.Itoa(len(names))}
			if len(rec) < len(names) {
				e.Column = names[len(rec)]
			} else {
				e.Value = cut(rec[len(names)], lim.PreviewStringChars)
			}
			if len(res.RowErrors) < lim.MaxRowErrors {
				res.RowErrors = append(res.RowErrors, e)
			} else {
				res.MoreRowErrors++
			}
		}
		if err := pv.addCSV(names, rec); err != nil {
			return Result{}, err
		}
		res.Profile.Rows++
	}
	if res.Profile.Rows == 0 {
		return Result{}, fail(CodeEmpty, "", "the file has a header but no rows")
	}
	res.Profile.Columns = make([]Column, len(names))
	for i, name := range names {
		res.Profile.Columns[i] = stats[i].column(name, i)
	}
	res.Preview = pv.rows
	return res, nil
}
