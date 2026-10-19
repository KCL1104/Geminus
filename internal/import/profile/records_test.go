package profile

import (
	"bytes"
	"encoding/csv"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"testing/iotest"
)

// unbounded never reaches a bound.
const unbounded = 1 << 30

// sameAsEncodingCSV checks that records reads data as encoding/csv's
// Reader does (FieldsPerRecord -1): the same records, start lines and
// field counts, then the same end (io.EOF or the same *csv.ParseError).
func sameAsEncodingCSV(t *testing.T, data []byte, src io.Reader) {
	t.Helper()
	want := csv.NewReader(bytes.NewReader(data))
	want.FieldsPerRecord = -1
	got := newRecords(src, unbounded, unbounded, unbounded)
	for i := 0; ; i++ {
		rec, werr := want.Read()
		gerr := got.Read()
		if werr == io.EOF || gerr == io.EOF {
			if werr != gerr {
				t.Fatalf("%q record %d: encoding/csv %v, records %v", data, i, werr, gerr)
			}
			return
		}
		if werr != nil || gerr != nil {
			var wp, gp *csv.ParseError
			if !errors.As(werr, &wp) || !errors.As(gerr, &gp) || *wp != *gp {
				t.Fatalf("%q record %d: encoding/csv %v, records %v", data, i, werr, gerr)
			}
			return
		}
		line, _ := want.FieldPos(0)
		if !slices.Equal(rec, got.rec) || got.fields != len(rec) || got.start != line || got.long >= 0 {
			t.Fatalf("%q record %d: encoding/csv %q on line %d, records %q (%d fields) on line %d", data, i, rec, line, got.rec, got.fields, got.start)
		}
	}
}

var csvSeeds = []string{
	"a,b\n1,2\n", "a,b\r\n1,2\r\n", "a,b\n1,2", "a,b\n1,", ",\n,", ",", "", "\n", "\r", "\r\n", "\n\n\r\n\n", "x\r",
	"x\r\r\n", "a\rb\n", "\ra\n", "\r\ra", "\"a\"\"b\",c\r\n", "\"\"", "\"\"\"\"", "\"a\"\r", "\"a\"\rb", "\"a\"\r\n",
	"\"x\n", "\"x", "\"", "\"x\ny\"\n", "\"x\r\ny\"\r\n", "\"x\n\r", "\"x\n\ry", "\"x\"y", "a\"b", "a,\"b\"c", "a,b\"",
	"\n\n\"q\nq\"\n", "a,\"b\nc\"\"\",d\n", "\"a\",\"b\"\n\"c\"", "名稱,數量\r\n電阻,10\r\n", "\xef\xbb\xbfa\n", "a\n\n\nb\n",
	"\"a\nb\nc", "a,\"\n", " \"a\"", "\"a\" ", "a\x00b\n", "\xff,\xfe\n", "\"a\"\"\n", "x,\"y\"\"\"\"z\"",
}

// records reads every byte sequence as encoding/csv does, however the
// bytes arrive (T-016 review H1: the bounds are its only difference).
func FuzzRecordsAsEncodingCSV(f *testing.F) {
	for _, s := range csvSeeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		sameAsEncodingCSV(t, data, bytes.NewReader(data))
		sameAsEncodingCSV(t, data, iotest.OneByteReader(bytes.NewReader(data)))
		sameAsEncodingCSV(t, data, iotest.HalfReader(bytes.NewReader(data)))
	})
}

// The bounds hold while a record is read: a record, its line end
// included, is at most maxBytes bytes; only keep fields are kept (all are
// counted); the first field over maxChars characters is named with its
// characters.
func TestRecordBounds(t *testing.T) {
	read := func(data string, keep, maxChars, maxBytes int) (*records, error) {
		p := newRecords(strings.NewReader(data), keep, maxChars, maxBytes)
		return p, p.Read()
	}
	// The limit counts every byte from the record's first to its line end
	// ("\r\n" being two); skipped empty lines before it do not count.
	for _, c := range []struct {
		data string
		max  int
		ok   bool
	}{
		{"abc\n", 4, true}, {"abc\n", 3, false}, {"abc", 3, true}, {"abc\r\n", 5, true}, {"abc\r\n", 4, false},
		{"\n\r\n\nabc\n", 4, true}, {"\"a\nb\"\n", 6, true}, {"\"a\nb\"\n", 5, false}, {"a,b,c\n", 6, true}, {"a,b,c\n", 5, false},
	} {
		p, err := read(c.data, unbounded, unbounded, c.max)
		if c.ok != (err == nil) || (!c.ok && !errors.Is(err, errRecordTooLong)) {
			t.Errorf("%q within %d bytes: %v", c.data, c.max, err)
		}
		if c.ok && p.n > c.max {
			t.Errorf("%q: %d bytes counted", c.data, p.n)
		}
	}
	// Two fields kept of five, all counted.
	p, err := read("a,\"b,c\",d,e,f\n", 2, unbounded, unbounded)
	if err != nil || !slices.Equal(p.rec, []string{"a", "b,c"}) || p.fields != 5 {
		t.Errorf("%q %d: %v", p.rec, p.fields, err)
	}
	// The first long field, counted in characters, kept or not.
	p, err = read("ab,名名名,cdef,ghijk\n", 1, 3, unbounded)
	if err != nil || p.long != 2 || p.longChars != 4 || !slices.Equal(p.rec, []string{"ab"}) {
		t.Errorf("long field %d of %d characters: %v", p.long, p.longChars, err)
	}
	p, err = read("ab,名名名\n", 2, 3, unbounded)
	if err != nil || p.long != -1 {
		t.Errorf("long field %d: %v", p.long, err)
	}
	// The next record starts afresh.
	p = newRecords(strings.NewReader("aaaa,b\nc\n"), 1, 3, unbounded)
	if err := p.Read(); err != nil || p.long != 0 {
		t.Fatalf("first: %d %v", p.long, err)
	}
	if err := p.Read(); err != nil || p.long != -1 || p.fields != 1 || p.start != 2 || !slices.Equal(p.rec, []string{"c"}) {
		t.Errorf("second: %+v %v", p, err)
	}
	// A source that never makes progress is an error, not a loop.
	p = newRecords(iotest.ErrReader(nil), 1, 1, 1)
	if err := p.Read(); !errors.Is(err, io.ErrNoProgress) {
		t.Errorf("no progress: %v", err)
	}
}
