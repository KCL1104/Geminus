package profile

import (
	"bytes"
	"encoding/json/jsontext"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

// preview keeps the first rows (D-043(b)) as JSON objects of raw values:
// CSV values as strings, JSON values as they are, string values cut to
// PreviewStringChars. It stops at PreviewRows, or at the first row that
// would take the whole past PreviewBytes as the database stores it, so the
// preview is always a prefix of the file.
type preview struct {
	lim  Limits
	rows []jsontext.Value
	size int // the array's bytes as stored so far, brackets included
	full bool
}

func newPreview(lim Limits) *preview { return &preview{lim: lim, size: 2} }

func (p *preview) wants() bool { return !p.full && len(p.rows) < p.lim.PreviewRows }

// push adds row, extra being how much longer the database prints it.
func (p *preview) push(row []byte, extra int) {
	cost := len(row) + extra
	if len(p.rows) > 0 {
		cost++ // the comma
	}
	if p.size+cost > p.lim.PreviewBytes {
		p.full = true
		return
	}
	p.rows = append(p.rows, jsontext.Value(row))
	p.size += cost
}

// cut is s with at most n characters: a longer s keeps n-1 and ends in
// an ellipsis.
func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

func (p *preview) addCSV(names, rec []string) error {
	if !p.wants() {
		return nil
	}
	var b bytes.Buffer
	enc := jsontext.NewEncoder(&b)
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	for i := 0; i < len(rec) && i < len(names); i++ {
		if err := enc.WriteToken(jsontext.String(names[i])); err != nil {
			return err
		}
		if err := enc.WriteToken(jsontext.String(cut(rec[i], p.lim.PreviewStringChars))); err != nil {
			return err
		}
	}
	if err := enc.WriteToken(jsontext.EndObject); err != nil {
		return err
	}
	p.push(bytes.TrimSpace(b.Bytes()), 0)
	return nil
}

// addJSON adds one JSON event (an object): its tokens again, string
// values cut. A number is kept as written unless the database would print
// it longer than storedNumberMax characters (a long fraction or a large
// exponent); then it is the shortest binary64 form of its value, which the
// database always stores.
func (p *preview) addJSON(event jsontext.Value) error {
	if !p.wants() {
		return nil
	}
	v, extra, err := PreviewJSON(event, p.lim.PreviewStringChars)
	if err != nil {
		return err
	}
	p.push(v, extra)
	return nil
}

// PreviewJSON is the JSON value v (one the profile has checked: valid,
// finite numbers) as a preview keeps it: string values (not member names),
// at any depth, cut to chars characters, and a number the database would
// print longer than storedNumberMax characters in its shortest binary64
// form. extra is how many more bytes the database prints it with than the
// result has (its numbers' digits), for bounding a stored preview (D-043;
// T-017's canonical preview, too).
func PreviewJSON(v jsontext.Value, chars int) (out jsontext.Value, extra int, err error) {
	dec := reread(nil, v)
	var b bytes.Buffer
	enc := jsontext.NewEncoder(&b)
	for {
		tok, err := dec.ReadToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, 0, err
		}
		switch tok.Kind() {
		case '"':
			if kind, n := dec.StackIndex(dec.StackDepth()); !(kind == '{' && n%2 == 1) {
				tok = jsontext.String(cut(tok.String(), chars))
			}
		case '0':
			lit := tok.String()
			if n := storedNumberLen(lit); n > storedNumberMax {
				f, err := strconv.ParseFloat(lit, 64)
				if err != nil {
					return nil, 0, err // checked finite before
				}
				lit, tok = strconv.FormatFloat(f, 'g', -1, 64), jsontext.Float(f)
			}
			extra += storedNumberLen(lit) - len(lit)
		}
		if err := enc.WriteToken(tok); err != nil {
			return nil, 0, err
		}
	}
	return jsontext.Value(bytes.TrimSpace(b.Bytes())), extra, nil
}

// storedNumberMax bounds a number's printed length in the preview: every
// binary64 value's shortest form prints within it.
const storedNumberMax = 400

// storedNumberLen bounds how many characters the database prints for the
// number literal lit: PostgreSQL's numeric prints every digit, without an
// exponent (1e300 is 301 characters).
func storedNumberLen(lit string) int {
	mant, exp := lit, 0
	if i := strings.IndexAny(lit, "eE"); i >= 0 {
		mant = lit[:i]
		e, err := strconv.Atoi(strings.TrimPrefix(lit[i+1:], "+"))
		if err != nil || e > 1<<20 || e < -(1<<20) {
			e = 1 << 20 // beyond any printable number
		}
		exp = max(e, -e)
	}
	digits := 0
	for i := 0; i < len(mant); i++ {
		if mant[i] >= '0' && mant[i] <= '9' {
			digits++
		}
	}
	return max(len(lit), digits+exp+3)
}
