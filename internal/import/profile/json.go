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

// reread makes dec (a new decoder when nil) read v, a value the file's own
// decoder has read in full, and so checked (repeated names included,
// D-014): in place, and without tracking names again, which would cost as
// much again (about 20 bytes a name).
func reread(dec *jsontext.Decoder, v []byte) *jsontext.Decoder {
	if dec == nil {
		return jsontext.NewDecoder(bytes.NewBuffer(v), jsontext.AllowDuplicateNames(true))
	}
	dec.Reset(bytes.NewBuffer(v), jsontext.AllowDuplicateNames(true))
	return dec
}

// tooDeep is the failure of a value the decoder could not read because it
// is nested past the decoder's own limit (10,000 levels; T-016 re-review):
// check would have refused it at MaxDepth, and the decoder's pointer then
// loses member names. buf holds the value from its start (after white
// space and, between rows, a comma), at pointer base, depth containers
// deep; it is read again only as far as MaxDepth. A value that is not an
// object is refused as notObject, as when it is read. nil when err is not
// that (it is then reported as it is).
func tooDeep(err error, buf []byte, base string, depth int, lim Limits, notObject *Failure) *Failure {
	var se *jsontext.SyntacticError
	if !errors.As(err, &se) || strings.Count(string(se.JSONPointer), "/") <= lim.MaxDepth {
		return nil
	}
	buf = bytes.TrimLeft(bytes.TrimPrefix(bytes.TrimLeft(buf, " \t\r\n"), []byte(",")), " \t\r\n")
	dec := reread(nil, buf)
	for first := true; ; first = false {
		tok, err := dec.ReadToken()
		if err != nil {
			return nil
		}
		if first && tok.Kind() != '{' {
			return notObject
		}
		if k := tok.Kind(); k == '{' || k == '[' {
			if d := depth + dec.StackDepth(); d > lim.MaxDepth {
				return fail(CodeLimitExceeded, base+string(dec.StackPointer()), "nesting depth %d; the limit is %d", d, lim.MaxDepth)
			}
		}
	}
}

// notAnEvent is the failure of row, a value that is not an object.
func notAnEvent(row int) *Failure {
	return fail(CodeShapeInvalid, pointer(row), "row %d is not a JSON object: a JSON events file is one array of objects", row)
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
// dec, if not nil, is a decoder to reuse.
func check(dec *jsontext.Decoder, v jsontext.Value, base string, depth int, lim Limits) error {
	dec = reread(dec, v)
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

// window is the events file as the decoder reads it: never more than
// MaxRecordBytes past the end of the last row (T-016 review H1). The
// decoder holds a whole value before it returns it, so the window, moved
// past each row before the next one is read, bounds what it holds by one
// row with the white space and comma before it; beyond it the read fails
// with the row's failure.
type window struct {
	r          io.Reader
	off, limit int64 // bytes passed on, and how many may be
	max        int
	row        int // the row being read: beforeArray, afterArray or its index
}

const (
	beforeArray = -1
	afterArray  = -2
)

func (w *window) Read(p []byte) (int, error) {
	room := w.limit - w.off
	if room <= 0 {
		return 0, w.failure()
	}
	if int64(len(p)) > room {
		p = p[:room]
	}
	n, err := w.r.Read(p)
	w.off += int64(n)
	return n, err
}

// at moves the window to max bytes past offset, for row.
func (w *window) at(offset int64, row int) { w.limit, w.row = offset+int64(w.max), row }

func (w *window) failure() *Failure {
	switch w.row {
	case beforeArray:
		return fail(CodeLimitExceeded, "", "the array does not start within the file's first %d bytes", w.max)
	case afterArray:
		return fail(CodeLimitExceeded, "", "more than %d bytes follow the array", w.max)
	}
	return fail(CodeLimitExceeded, pointer(w.row), "row %d, with the white space before it, is longer than %d bytes", w.row, w.max)
}

// columns are the JSON events' top-level member names in order of first
// appearance, with their statistics.
type columns struct {
	names []string
	index map[string]int
	stats []colStats
	// Decoders reused for each row and each value.
	walk, check *jsontext.Decoder
}

func readEvents(ctx context.Context, tr *text, lim Limits) (Result, error) {
	win := &window{r: tr, max: lim.MaxRecordBytes}
	win.at(0, beforeArray)
	dec := jsontext.NewDecoder(win)
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
	cols := columns{index: map[string]int{}, walk: jsontext.NewDecoder(bytes.NewBuffer(nil)), check: jsontext.NewDecoder(bytes.NewBuffer(nil))}
	pv := newPreview(lim)
	var res Result
	for row := 0; ; row++ {
		if row%256 == 0 {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
		}
		win.at(dec.InputOffset(), row)
		if dec.PeekKind() == ']' {
			if _, err := dec.ReadToken(); err != nil {
				return Result{}, jsonError(err, "")
			}
			break
		}
		event, err := dec.ReadValue()
		if err != nil {
			if f := tooDeep(err, dec.UnreadBuffer(), pointer(row), 1, lim, notAnEvent(row)); f != nil {
				return Result{}, f
			}
			return Result{}, jsonError(err, "")
		}
		if row >= lim.MaxRows {
			return Result{}, fail(CodeLimitExceeded, pointer(row), "the file has more than %d rows", lim.MaxRows)
		}
		if event.Kind() != '{' {
			return Result{}, notAnEvent(row)
		}
		if err := cols.add(event, row, lim); err != nil {
			return Result{}, err
		}
		if err := pv.addJSON(event); err != nil {
			return Result{}, err
		}
		res.Profile.Rows++
	}
	win.at(dec.InputOffset(), afterArray)
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
	c.walk = reread(c.walk, event)
	dec := c.walk
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
		if err := check(c.check, v, at, 2, lim); err != nil {
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
	notObject := fail(CodeShapeInvalid, "", "a JSON model file is one JSON object (a Model IR document)")
	if err != nil {
		if f := tooDeep(err, data, "", 0, lim, notObject); f != nil {
			return Result{}, f
		}
		return Result{}, jsonError(err, "")
	}
	if v.Kind() != '{' {
		return Result{}, notObject
	}
	if err := check(nil, v, "", 0, lim); err != nil {
		return Result{}, err
	}
	if err := after(dec); err != nil {
		return Result{}, err
	}
	return Result{}, nil
}
