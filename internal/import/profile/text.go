package profile

import (
	"bytes"
	"context"
	"io"
	"unicode/utf8"
)

// text is the uploaded file as text: it drops a UTF-8 BOM (and remembers
// it), refuses UTF-16, and passes on only valid UTF-8 without NUL, so
// neither parser ever sees anything else. A sequence split across reads is
// held back until it is complete, so where a file fails never depends on
// how its bytes arrive. Each read first checks the context.
type text struct {
	ctx     context.Context
	r       io.Reader
	buf     []byte
	ready   []byte // validated bytes not yet delivered (within buf)
	pending []byte // an incomplete sequence at the end of the last read
	started bool
	bom     bool
	line    int   // the line of the next byte to validate
	err     error // returned once ready is drained
}

func newText(ctx context.Context, r io.Reader) *text {
	return &text{ctx: ctx, r: r, buf: make([]byte, 64<<10), pending: make([]byte, 0, utf8.UTFMax), line: 1}
}

func (t *text) encoding() Encoding {
	if t.bom {
		return UTF8BOM
	}
	return UTF8
}

func (t *text) Read(p []byte) (int, error) {
	for len(t.ready) == 0 {
		if t.err != nil {
			return 0, t.err
		}
		if err := t.ctx.Err(); err != nil {
			t.err = err
			return 0, err
		}
		t.fill()
	}
	n := copy(p, t.ready)
	t.ready = t.ready[n:]
	return n, nil
}

var (
	bomUTF8    = []byte{0xef, 0xbb, 0xbf}
	bomUTF16LE = []byte{0xff, 0xfe}
	bomUTF16BE = []byte{0xfe, 0xff}
)

// fill reads once and validates what it got (after any held-back bytes).
func (t *text) fill() {
	held := copy(t.buf, t.pending)
	t.pending = t.pending[:0]
	n, err := t.r.Read(t.buf[held:])
	data := t.buf[:held+n]
	atEOF := err == io.EOF
	if err != nil && !atEOF {
		t.err = err // after what was read before it
	}
	if !t.started {
		if len(data) < len(bomUTF8) && err == nil {
			t.pending = append(t.pending, data...) // too few bytes to tell yet
			return
		}
		t.started = true
		switch {
		case bytes.HasPrefix(data, bomUTF8):
			t.bom, data = true, data[len(bomUTF8):]
		case bytes.HasPrefix(data, bomUTF16LE), bytes.HasPrefix(data, bomUTF16BE):
			t.err = fail(CodeEncodingInvalid, "", "the file is UTF-16; only UTF-8 (with or without a BOM) is read: save it as UTF-8")
			return
		}
	}
	valid, rest, f := t.validate(data, atEOF || t.err != nil)
	t.ready = valid
	t.pending = append(t.pending, rest...)
	switch {
	case f != nil:
		t.err = f
	case atEOF && t.err == nil:
		t.err = io.EOF
	}
}

// validate returns the longest prefix of data that is valid UTF-8 without
// NUL, the incomplete sequence at its end (none when the input ends there,
// which is then invalid), and why the rest is refused.
func (t *text) validate(data []byte, final bool) (valid, rest []byte, f *Failure) {
	for i := 0; i < len(data); {
		b := data[i]
		if b < utf8.RuneSelf {
			if b == 0 {
				return data[:i], nil, fail(CodeNULCharacter, "", "line %d: the file contains a NUL byte (U+0000), so it is not a text file", t.line)
			}
			if b == '\n' {
				t.line++
			}
			i++
			continue
		}
		r, size := utf8.DecodeRune(data[i:])
		if r == utf8.RuneError && size <= 1 {
			if !final && !utf8.FullRune(data[i:]) {
				return data[:i], data[i:], nil
			}
			return data[:i], nil, fail(CodeEncodingInvalid, "", "line %d: the file is not valid UTF-8; only UTF-8 (with or without a BOM) is read: save it as UTF-8", t.line)
		}
		i += size
	}
	return data, nil, nil
}
