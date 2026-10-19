package profile

import (
	"encoding/csv"
	"errors"
	"io"
)

// errRecordTooLong: a record is longer than its limit; the caller says
// which record.
var errRecordTooLong = errors.New("profile: the record is longer than its limit")

// position is a byte's line and 1-based byte column in that line, counted
// as encoding/csv counts them ("\r\n" being one line end).
type position struct{ line, col int }

// records reads CSV records exactly as encoding/csv's Reader does with
// Comma ',', no comment character, LazyQuotes and TrimLeadingSpace off and
// FieldsPerRecord -1 (RFC 4180 with strict quotes: every record is taken,
// empty lines are skipped, "\r\n" is read as "\n" and a '\r' ending the
// input is dropped; the errors are the same *csv.ParseError values), but
// bounds what it holds while it reads (T-016 review H1), so that what a
// record costs never depends on the file:
//   - a record, from its first byte to its line end, is at most maxBytes
//     bytes (errRecordTooLong as soon as it is longer);
//   - only its first keep fields are kept; the others are only counted;
//   - each field's characters are counted as it is read (the input is
//     valid UTF-8, text has checked it), and the first field longer than
//     maxChars is remembered.
type records struct {
	src   io.Reader
	chunk []byte // the read buffer
	data  []byte // what is left of the last read
	err   error  // the source's error, once data is used up

	keep, maxChars, maxBytes int

	next   position // where the next byte is
	nl     bool     // the last byte was a line end: the next one starts a line
	at     position // where the last byte read was
	n      int      // bytes of the record so far
	start  int      // the line the record starts on
	buf    []byte   // the kept fields' bytes
	ends   []int    // where each kept field ends in buf
	fields int      // fields in the record, kept or not
	chars  int      // characters of the field being read
	// long is the first field of the record with more than maxChars
	// characters (-1: none) and longChars its characters.
	long, longChars int
	rec             []string
}

func newRecords(src io.Reader, keep, maxChars, maxBytes int) *records {
	return &records{src: src, chunk: make([]byte, 64<<10), keep: keep, maxChars: maxChars, maxBytes: maxBytes,
		next: position{line: 1, col: 1}, long: -1}
}

// fill makes data non-empty, or returns the source's error (io.EOF at the
// end of the input).
func (p *records) fill() error {
	for empty := 0; len(p.data) == 0; empty++ {
		if p.err != nil {
			return p.err
		}
		if empty == 100 {
			p.err = io.ErrNoProgress
			return p.err
		}
		n, err := p.src.Read(p.chunk)
		p.data = p.chunk[:n]
		if err != nil {
			p.err = err
		}
	}
	return nil
}

// read returns the next byte of the input: "\r\n" as '\n', and io.EOF for
// a '\r' that ends the input, as encoding/csv's readLine does.
func (p *records) read() (byte, error) {
	if err := p.fill(); err != nil {
		return 0, err
	}
	b := p.data[0]
	p.data = p.data[1:]
	p.n++
	if b == '\r' {
		switch err := p.fill(); {
		case err == io.EOF:
			return 0, io.EOF
		case err != nil:
			return 0, err
		case p.data[0] == '\n':
			p.data = p.data[1:]
			p.n++
			b = '\n'
		}
	}
	if p.n > p.maxBytes {
		return 0, errRecordTooLong
	}
	if p.nl {
		p.next = position{line: p.next.line + 1, col: 1}
		p.nl = false
	}
	p.at = p.next
	p.next.col++
	p.nl = b == '\n'
	return b, nil
}

// add appends b to the field being read.
func (p *records) add(b byte) {
	if p.fields < p.keep {
		p.buf = append(p.buf, b)
	}
	if b&0xc0 != 0x80 {
		p.chars++
	}
}

// plain adds, in one go, the bytes at the start of what was read that
// need no care: up to the first quote, line end or '\r' (and comma, outside
// quotes). Read byte by byte they would do the same.
func (p *records) plain(quoted bool) error {
	i := 0
	for i < len(p.data) {
		c := p.data[i]
		if c == '"' || c == '\n' || c == '\r' || (c == ',' && !quoted) {
			break
		}
		i++
	}
	if i == 0 {
		return nil
	}
	if p.n+i > p.maxBytes {
		return errRecordTooLong
	}
	run := p.data[:i]
	p.data = p.data[i:]
	p.n += i
	if p.fields < p.keep {
		p.buf = append(p.buf, run...)
	}
	for _, c := range run {
		if c&0xc0 != 0x80 {
			p.chars++
		}
	}
	if p.nl {
		p.next = position{line: p.next.line + 1, col: 1}
		p.nl = false
	}
	p.next.col += i
	p.at = position{line: p.next.line, col: p.next.col - 1}
	return nil
}

func (p *records) endField() {
	if p.chars > p.maxChars && p.long < 0 {
		p.long, p.longChars = p.fields, p.chars
	}
	if p.fields < p.keep {
		p.ends = append(p.ends, len(p.buf))
	}
	p.fields++
	p.chars = 0
}

// Read reads the next record (io.EOF when there is none). Its kept fields
// are p.rec, valid until the next call; p.fields counts all of them, and
// p.start is the line it starts on. Any error but io.EOF ends the input.
func (p *records) Read() error {
	p.buf, p.ends, p.fields, p.chars, p.long, p.longChars = p.buf[:0], p.ends[:0], 0, 0, -1, 0
	var b byte
	for {
		p.n = 0
		c, err := p.read()
		if err != nil {
			return err
		}
		if c != '\n' { // an empty line is skipped
			b = c
			break
		}
	}
	p.start = p.at.line
	for {
		var more bool
		var err error
		if b == '"' {
			more, err = p.quoted()
		} else {
			more, err = p.unquoted(b)
		}
		if err != nil {
			return err
		}
		p.endField()
		if !more {
			break
		}
		c, err := p.read()
		if err == io.EOF { // a comma ends the input: an empty last field
			p.endField()
			break
		}
		if err != nil {
			return err
		}
		b = c
	}
	s := string(p.buf)
	p.rec = p.rec[:0]
	from := 0
	for _, to := range p.ends {
		p.rec = append(p.rec, s[from:to])
		from = to
	}
	return nil
}

// unquoted reads a field that does not start with a quote, b being its
// first byte (a comma or a line end when it is empty); more says whether
// a comma ends it.
func (p *records) unquoted(b byte) (more bool, err error) {
	for {
		switch b {
		case ',':
			return true, nil
		case '\n':
			return false, nil
		case '"':
			return false, &csv.ParseError{StartLine: p.start, Line: p.at.line, Column: p.at.col, Err: csv.ErrBareQuote}
		}
		p.add(b)
		if err := p.plain(false); err != nil {
			return false, err
		}
		if b, err = p.read(); err == io.EOF {
			return false, nil
		} else if err != nil {
			return false, err
		}
	}
}

// quoted reads a field that starts with a quote (already read); more says
// whether a comma ends it.
func (p *records) quoted() (more bool, err error) {
	for {
		b, err := p.read()
		if err == io.EOF { // the input ends inside the quotes
			return false, &csv.ParseError{StartLine: p.start, Line: p.next.line, Column: p.next.col, Err: csv.ErrQuote}
		}
		if err != nil {
			return false, err
		}
		if b != '"' {
			p.add(b)
			if err := p.plain(true); err != nil {
				return false, err
			}
			continue
		}
		quote := p.at
		switch b, err = p.read(); {
		case err == io.EOF:
			return false, nil
		case err != nil:
			return false, err
		case b == '"':
			p.add('"')
		case b == ',':
			return true, nil
		case b == '\n':
			return false, nil
		default:
			return false, &csv.ParseError{StartLine: p.start, Line: quote.line, Column: quote.col, Err: csv.ErrQuote}
		}
	}
}
