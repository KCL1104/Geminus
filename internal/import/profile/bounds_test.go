package profile_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
	"unicode/utf8"

	importprofile "github.com/kcl1104/geminus/internal/import/profile"
)

// allocatedBy is what f allocates in all (runtime.MemStats.TotalAlloc), an
// upper bound of the heap it needs at any one time.
func allocatedBy(f func()) uint64 {
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// T-016 review H1 (SECURITY §6: one worker profiles every tenant's
// uploads): what profiling costs never grows with a file a member can
// upload under the 50 MB limit. Each file below, at about the size of the
// review's measurements (a row of 40M commas once took 6.3 GB, a header
// of 20M columns 3.0 GB, a JSON file of 40 MB about 1 GB), is refused or
// profiled with at most a few MiB allocated in all, with the default
// limits: a CSV record or a JSON row is at most 1 MiB as written, and no
// more than the header's fields are kept of a row.
func TestHostileFilesAreBounded(t *testing.T) {
	const mib = 1 << 20
	repeat := func(head, unit, tail string, n int) func() []byte {
		return func() []byte {
			var b bytes.Buffer
			b.Grow(len(head) + len(unit)*n + len(tail))
			b.WriteString(head)
			for range n {
				b.WriteString(unit)
			}
			b.WriteString(tail)
			return b.Bytes()
		}
	}
	columns := func(n int) func() []byte {
		return func() []byte {
			var b bytes.Buffer
			for i := range n {
				if i > 0 {
					b.WriteByte(',')
				}
				b.WriteString("c" + strconv.Itoa(i))
			}
			b.WriteString("\n1\n")
			return b.Bytes()
		}
	}
	members := func(head string, n int, tail string) func() []byte {
		return func() []byte {
			var b bytes.Buffer
			b.WriteString(head)
			for i := range n {
				if i > 0 {
					b.WriteByte(',')
				}
				b.WriteString(`"m` + strconv.Itoa(i) + `":1`)
			}
			b.WriteString(tail)
			return b.Bytes()
		}
	}
	cases := []struct {
		name    string
		format  importprofile.Format
		data    func() []byte
		code    string // "" when the file is profiled
		path    string
		says    string
		ceiling uint64
		check   func(t *testing.T, res importprofile.Result)
	}{
		// CSV: a record is bounded as it is read, before it is split.
		{"a CSV row of 40M commas", importprofile.CSV, repeat("a\n", ",", "\n", 40_000_000),
			importprofile.CodeLimitExceeded, "/0", "the record starting on line 2 is longer than 1048576 bytes", 8 * mib, nil},
		{"a CSV header of 20M columns", importprofile.CSV, repeat("", ",", "\n1\n", 20_000_000),
			importprofile.CodeLimitExceeded, "", "the header is longer than 1048576 bytes", 8 * mib, nil},
		{"a CSV value of 40 MB", importprofile.CSV, repeat("a\n", "x", "\n", 40_000_000),
			importprofile.CodeLimitExceeded, "/0", "longer than 1048576 bytes", 8 * mib, nil},
		{"a quoted CSV value over 20M lines", importprofile.CSV, repeat("a\n\"", "x\n", "\"\n", 20_000_000),
			importprofile.CodeLimitExceeded, "/0", "the record starting on line 2 is longer than 1048576 bytes", 8 * mib, nil},
		{"a CSV header of 300 columns", importprofile.CSV, columns(300),
			importprofile.CodeLimitExceeded, "", "the header has 300 columns; the limit is 256", 1 * mib, nil},
		// Within the record limit a row may have any number of fields: it
		// is an invalid row, and only the header's fields and one more are
		// kept of it.
		{"a CSV row of 500,000 fields", importprofile.CSV, repeat("a\n", ",", "\n", 499_999), "", "", "", 2 * mib,
			func(t *testing.T, res importprofile.Result) {
				if res.Profile.Rows != 1 || res.InvalidRows != 1 || len(res.RowErrors) != 1 ||
					res.RowErrors[0].Message != "the row has 500000 fields; the header has 1" || res.RowErrors[0].Value != "" {
					t.Errorf("%+v %+v", res.Profile, res.RowErrors)
				}
			}},
		// JSON: the decoder never holds more than one row's bytes.
		{"a JSON row of 40 MB", importprofile.JSONEvents, repeat(`[{"a":"`, "x", `"}]`, 40_000_000),
			importprofile.CodeLimitExceeded, "/0", "row 0, with the white space before it, is longer than 1048576 bytes", 8 * mib, nil},
		// The decoder checks member names for repeats as it reads (D-014),
		// at about 20 bytes a name: a row of 1 MiB of short names costs
		// about 20 MiB, wherever the names are, and no more.
		{"a JSON row of 4M members", importprofile.JSONEvents, members("[{", 4_000_000, "}]"),
			importprofile.CodeLimitExceeded, "/0", "row 0, with the white space before it, is longer than 1048576 bytes", 32 * mib, nil},
		{"a JSON row with an object of 4M members", importprofile.JSONEvents, members(`[{"a":{`, 4_000_000, "}}]"),
			importprofile.CodeLimitExceeded, "/0", "row 0, with the white space before it, is longer than 1048576 bytes", 32 * mib, nil},
		{"a JSON model with an object of 4M members", importprofile.JSONModel, members(`{"a":{`, 4_000_000, "}}"),
			importprofile.CodeLimitExceeded, "", "2097152 bytes", 16 * mib, nil},
		// Within the row limit: the names are checked once, as the row is
		// read (about 26 MiB here; checking them again would double it);
		// the value is then too long.
		{"a JSON row with an object of 80,000 members", importprofile.JSONEvents, members(`[{"a":{`, 80_000, "}}]"),
			importprofile.CodeLimitExceeded, "/0/a", "a value of 868891 characters", 40 * mib, nil},
		{"40 MB of white space in a JSON row", importprofile.JSONEvents, repeat(`[{"a":`, " ", `1}]`, 40_000_000),
			importprofile.CodeLimitExceeded, "/0", "longer than 1048576 bytes", 8 * mib, nil},
		{"40 MB of white space between JSON rows", importprofile.JSONEvents, repeat(`[{"a":1},`, " ", `{"a":2}]`, 40_000_000),
			importprofile.CodeLimitExceeded, "/1", "row 1, with the white space before it, is longer than 1048576 bytes", 8 * mib, nil},
		{"40 MB of white space before the JSON array", importprofile.JSONEvents, repeat("", " ", `[{"a":1}]`, 40_000_000),
			importprofile.CodeLimitExceeded, "", "the array does not start within the file's first 1048576 bytes", 8 * mib, nil},
		{"40 MB of white space after the JSON array", importprofile.JSONEvents, repeat(`[{"a":1}]`, " ", "", 40_000_000),
			importprofile.CodeLimitExceeded, "", "more than 1048576 bytes follow the array", 8 * mib, nil},
		// A model is read whole, at most MaxModelBytes and one byte more
		// (about 5 MiB with the reading buffer's growth; about 10 MiB in a
		// -race build).
		{"a JSON model of 40 MB", importprofile.JSONModel, repeat(`{"a":"`, "x", `"}`, 40_000_000),
			importprofile.CodeLimitExceeded, "", "2097152 bytes", 16 * mib, nil},
		// A model within its 2 MiB of short member names: read, at the
		// decoder's cost per name.
		{"a JSON model of 150,000 members", importprofile.JSONModel, members(`{"a":{`, 150_000, "}}"), "", "", "", 64 * mib,
			func(t *testing.T, res importprofile.Result) {
				if res.Profile.Format != importprofile.JSONModel || res.Profile.Rows != 0 {
					t.Errorf("%+v", res.Profile)
				}
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := c.data()
			var res importprofile.Result
			var err error
			n := allocatedBy(func() {
				res, err = importprofile.Read(context.Background(), bytes.NewReader(data), c.format, importprofile.DefaultLimits)
			})
			t.Logf("a file of %d bytes: %d bytes allocated", len(data), n)
			if n > c.ceiling {
				t.Errorf("a file of %d bytes allocated %d bytes; the ceiling is %d", len(data), n, c.ceiling)
			}
			if c.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				c.check(t, res)
				return
			}
			var f *importprofile.Failure
			if !errors.As(err, &f) || f.Code != c.code || f.Path != c.path || !strings.Contains(f.Message, c.says) {
				t.Errorf("got %v; want %s at %q saying %q", err, c.code, c.path, c.says)
			}
		})
	}
}

// T-016 review L1: a failure's path is a JSON Pointer of at most 2,048
// characters (common.schema.json, model.MaxIssuePath): a longer one is cut
// at a '/', so it names an ancestor of where the file fails, and member
// names of any length never fill the import's error.
func TestFailurePathsAreBounded(t *testing.T) {
	long := strings.Repeat("名", 3000)
	fits := strings.Repeat("n", 2043) // "/0/m/" and it: 2,048 characters
	deep := strings.Repeat("[", 40) + "1" + strings.Repeat("]", 40)
	cases := []struct {
		name   string
		format importprofile.Format
		data   string
		code   string
		path   string
	}{
		{"a column name of 3,000 characters", importprofile.JSONEvents, `[{"` + long + `":1}]`, importprofile.CodeColumnInvalid, "/0"},
		{"too deep below a long member name", importprofile.JSONEvents, `[{"m":{"` + long + `":` + deep + `}}]`, importprofile.CodeLimitExceeded, "/0/m"},
		{"a long name repeated in a model", importprofile.JSONModel, `{"` + long + `":1,"` + long + `":2}`, importprofile.CodeJSONDuplicateKey, ""},
		{"a syntax error below a long name", importprofile.JSONModel, `{"a":{"` + long + `":[1,x]}}`, importprofile.CodeJSONInvalid, "/a"},
		// The longest whole-token prefix that fits is kept.
		{"too deep below a name that fits", importprofile.JSONEvents, `[{"m":{"` + fits + `":` + deep + `}}]`, importprofile.CodeLimitExceeded, "/0/m/" + fits},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := failure(t, c.format, importprofile.DefaultLimits, c.data)
			if f.Code != c.code || f.Path != c.path || utf8.RuneCountInString(f.Path) > 2048 || strings.Contains(f.Message, "名") {
				t.Errorf("got %s at %d characters %.40q: %q; want %s at %.40q", f.Code, utf8.RuneCountInString(f.Path), f.Path, f.Message, c.code, c.path)
			}
		})
	}
}

// T-016 re-review: JSON nested past the decoder's own limit (10,000
// levels) fails as JSON nested past MaxDepth does: IMPORT_LIMIT_EXCEEDED at
// the first container too deep, its member names kept, never JSON_INVALID
// with the decoder's pointer (which loses them); the same whatever the
// bytes arrive like.
func TestDeepJSONIsALimit(t *testing.T) {
	nest := func(n int) string { return strings.Repeat("[", n) + "1" + strings.Repeat("]", n) }
	zeros := func(n int) string { return strings.Repeat("/0", n) }
	for _, deep := range []int{33, 10_001, 20_000} {
		cases := []struct {
			name   string
			format importprofile.Format
			data   string
			code   string
			path   string
		}{
			{"under a member", importprofile.JSONEvents, `[{"m":` + nest(deep) + `}]`, importprofile.CodeLimitExceeded, "/0/m" + zeros(30)},
			{"in a later row", importprofile.JSONEvents, `[{"a":1} ,` + "\n" + `{"m":` + nest(deep) + `}]`, importprofile.CodeLimitExceeded, "/1/m" + zeros(30)},
			{"under a nested name", importprofile.JSONEvents, `[{"m":{"k/~":` + nest(deep) + `}}]`, importprofile.CodeLimitExceeded, "/0/m/k~1~0" + zeros(29)},
			{"a row that is an array", importprofile.JSONEvents, `[` + nest(deep) + `]`, importprofile.CodeShapeInvalid, "/0"},
			{"in a model", importprofile.JSONModel, `{"m":` + nest(deep) + `}`, importprofile.CodeLimitExceeded, "/m" + zeros(31)},
		}
		for _, c := range cases {
			t.Run(strconv.Itoa(deep)+" levels "+c.name, func(t *testing.T) {
				for _, r := range []io.Reader{strings.NewReader(c.data), iotest.OneByteReader(strings.NewReader(c.data))} {
					_, err := importprofile.Read(context.Background(), r, c.format, importprofile.DefaultLimits)
					var f *importprofile.Failure
					if !errors.As(err, &f) || f.Code != c.code || f.Path != c.path || (c.code == importprofile.CodeLimitExceeded && !strings.Contains(f.Message, "the limit is 32")) {
						t.Errorf("got %v; want %s at %.60q", err, c.code, c.path)
					}
				}
			})
		}
	}
	// A syntax error the decoder finds first is reported as it is, even
	// after a part nested too deep.
	f := failure(t, importprofile.JSONEvents, importprofile.DefaultLimits, `[{"a":`+nest(40)+`,"b":x}]`)
	if f.Code != importprofile.CodeJSONInvalid || f.Path != "/0/b" {
		t.Errorf("got %s at %q", f.Code, f.Path)
	}
}

// A bound left unset is the caller's error, never a file read without it.
func TestLimitsMustBeSet(t *testing.T) {
	noRecord := importprofile.DefaultLimits
	noRecord.MaxRecordBytes = 0
	negative := importprofile.DefaultLimits
	negative.PreviewRows = -1
	for _, lim := range []importprofile.Limits{{}, noRecord, negative} {
		for _, f := range []importprofile.Format{importprofile.CSV, importprofile.JSONEvents, importprofile.JSONModel} {
			_, err := importprofile.Read(context.Background(), strings.NewReader("a\n1\n"), f, lim)
			var fail *importprofile.Failure
			if err == nil || errors.As(err, &fail) {
				t.Errorf("%s with %+v: %v", f, lim, err)
			}
		}
	}
}
