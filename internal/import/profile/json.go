package profile

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// jsonError is the failure of JSON that does not parse, with base (the
// pointer of the decoded part) before its pointer; the reader's errors,
// the context's and the text's own failures pass on as they are.
func jsonError(err error, base string) error {
	var f *Failure
	if errors.As(err, &f) {
		return f
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var se *jsontext.SyntacticError
	if !errors.As(err, &se) {
		return err
	}
	path := base + string(se.JSONPointer)
	if errors.Is(err, jsontext.ErrDuplicateName) {
		return fail(CodeJSONDuplicateKey, path, "a member name repeats within one JSON object")
	}
	// The parser's own message quotes the file, so only where is said.
	if errors.Is(se.Err, io.ErrUnexpectedEOF) {
		return fail(CodeJSONInvalid, path, "the file is not valid JSON: it ends inside a value (byte offset %d)", se.ByteOffset)
	}
	return fail(CodeJSONInvalid, path, "the file is not valid JSON at byte offset %d", se.ByteOffset)
}

// after checks that nothing but white space follows the file's value.
func after(dec *jsontext.Decoder) error {
	_, err := dec.ReadToken()
	if err == io.EOF {
		return nil
	}
	if err != nil {
		var se *jsontext.SyntacticError
		if !errors.As(err, &se) {
			return jsonError(err, "")
		}
	}
	return fail(CodeJSONInvalid, "", "the file has data after its JSON value")
}

// check walks the JSON value v at pointer base, depth containers deep, and
// refuses what the file may not hold: nesting deeper than MaxDepth, a
// number that is not finite as binary64 and U+0000 in a string or a name.
func check(v jsontext.Value, base string, depth int, lim Limits) error {
	dec := jsontext.NewDecoder(bytes.NewReader(v))
	for {
		tok, err := dec.ReadToken()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return jsonError(err, base) // v was read as one valid value already
		}
		at := base + string(dec.StackPointer())
		switch tok.Kind() {
		case '{', '[':
			if d := depth + dec.StackDepth(); d > lim.MaxDepth {
				return fail(CodeLimitExceeded, at, "nesting depth %d; the limit is %d", d, lim.MaxDepth)
			}
		case '"':
			if strings.ContainsRune(tok.String(), 0) {
				if kind, n := dec.StackIndex(dec.StackDepth()); kind == '{' && n%2 == 1 {
					at = base + string(dec.StackPointer().Parent()) // a name: its object
				}
				return fail(CodeNULCharacter, at, "a string contains U+0000")
			}
		case '0':
			if f, _ := strconv.ParseFloat(tok.String(), 64); math.IsInf(f, 0) {
				return fail(CodeJSONNumberNotFinite, at, "a number that is not finite as a 64-bit binary floating-point value")
			}
		}
	}
}

// columns are the JSON events' top-level member names in order of first
// appearance, with their statistics.
type columns struct {
	names []string
	index map[string]int
	stats []colStats
}

func readEvents(ctx context.Context, tr *text, lim Limits) (Result, error) {
	dec := jsontext.NewDecoder(tr)
	tok, err := dec.ReadToken()
	if err == io.EOF {
		return Result{}, fail(CodeEmpty, "", "the file is empty")
	}
	if err != nil {
		return Result{}, jsonError(err, "")
	}
	if tok.Kind() != '[' {
		return Result{}, fail(CodeShapeInvalid, "", "a JSON events file is one array of objects")
	}
	cols := columns{index: map[string]int{}}
	pv := newPreview(lim)
	var res Result
	for row := 0; ; row++ {
		if row%256 == 0 {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
		}
		if dec.PeekKind() == ']' {
			if _, err := dec.ReadToken(); err != nil {
				return Result{}, jsonError(err, "")
			}
			break
		}
		event, err := dec.ReadValue()
		if err != nil {
			return Result{}, jsonError(err, "")
		}
		if row >= lim.MaxRows {
			return Result{}, fail(CodeLimitExceeded, pointer(row), "the file has more than %d rows", lim.MaxRows)
		}
		if event.Kind() != '{' {
			return Result{}, fail(CodeShapeInvalid, pointer(row), "row %d is not a JSON object: a JSON events file is one array of objects", row)
		}
		if err := cols.add(event, row, lim); err != nil {
			return Result{}, err
		}
		if err := pv.addJSON(event); err != nil {
			return Result{}, err
		}
		res.Profile.Rows++
	}
	if err := after(dec); err != nil {
		return Result{}, err
	}
	if res.Profile.Rows == 0 {
		return Result{}, fail(CodeEmpty, "", "the file has no rows")
	}
	res.Profile.Columns = make([]Column, len(cols.names))
	for i, name := range cols.names {
		res.Profile.Columns[i] = cols.stats[i].column(name, i)
	}
	res.Preview = pv.rows
	return res, nil
}

// add checks one event and adds its members to the columns.
func (c *columns) add(event jsontext.Value, row int, lim Limits) error {
	dec := jsontext.NewDecoder(bytes.NewReader(event))
	if _, err := dec.ReadToken(); err != nil { // '{'
		return jsonError(err, pointer(row))
	}
	for dec.PeekKind() != '}' {
		tok, err := dec.ReadToken()
		if err != nil {
			return jsonError(err, pointer(row))
		}
		name := tok.String()
		if strings.ContainsRune(name, 0) {
			return fail(CodeNULCharacter, pointer(row), "a member name contains U+0000")
		}
		at := pointer(row, name)
		if n := utf8.RuneCountInString(name); name == "" || n > lim.MaxNameChars {
			return fail(CodeColumnInvalid, at, "a member name of %d characters; a column's member name is 1 to %d characters", n, lim.MaxNameChars)
		}
		v, err := dec.ReadValue()
		if err != nil {
			return jsonError(err, pointer(row))
		}
		if err := check(v, at, 2, lim); err != nil {
			return err
		}
		t, length, err := classifyJSON(v)
		if err != nil {
			return jsonError(err, at)
		}
		if length > lim.MaxFieldChars {
			return fail(CodeLimitExceeded, at, "a value of %d characters; a value is at most %d", length, lim.MaxFieldChars)
		}
		i, ok := c.index[name]
		if !ok {
			if len(c.names) == lim.MaxColumns {
				return fail(CodeLimitExceeded, at, "the events have more than %d columns", lim.MaxColumns)
			}
			i = len(c.names)
			c.index[name] = i
			c.names = append(c.names, name)
			c.stats = append(c.stats, colStats{})
		}
		c.stats[i].add(t, length)
	}
	return nil
}

func readModel(tr *text, lim Limits) (Result, error) {
	data, err := io.ReadAll(io.LimitReader(tr, int64(lim.MaxModelBytes)+1))
	if err != nil {
		return Result{}, err
	}
	if len(data) > lim.MaxModelBytes {
		return Result{}, fail(CodeLimitExceeded, "", "the model is larger than %d bytes", lim.MaxModelBytes)
	}
	dec := jsontext.NewDecoder(bytes.NewReader(data))
	v, err := dec.ReadValue()
	if err == io.EOF {
		return Result{}, fail(CodeEmpty, "", "the file is empty")
	}
	if err != nil {
		return Result{}, jsonError(err, "")
	}
	if v.Kind() != '{' {
		return Result{}, fail(CodeShapeInvalid, "", "a JSON model file is one JSON object (a Model IR document)")
	}
	if err := check(v, "", 0, lim); err != nil {
		return Result{}, err
	}
	if err := after(dec); err != nil {
		return Result{}, err
	}
	return Result{}, nil
}
