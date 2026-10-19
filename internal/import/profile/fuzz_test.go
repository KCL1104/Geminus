package profile_test

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	importprofile "github.com/kcl1104/geminus/internal/import/profile"
)

// Whatever the bytes, Read ends with a profile that keeps every bound of
// the contract (import.schema.json, D-043) or with a typed failure; it
// never panics and never returns another error for a file read in full.
func FuzzRead(f *testing.F) {
	for _, seed := range []string{
		"a,b\n1,2\n", "\xef\xbb\xbf名稱,數量\r\n電阻,10\r\n", "a\n\"x\ny\",2\n", "a,b\n1\n1,2,3\n",
		`[{"a":1,"b":{"c":[1,"x",null]}},{"d":"2026-10-15T08:30:00Z"}]`, `[{"a":1e300,"b":1.5e-320}]`,
		`{"nodes":[]}`, `[{"a":"x\u0000"}]`, "\xff\xfe\x00", "", "[", `[{"a":1,"a":2}]`,
	} {
		for _, format := range []byte{0, 1, 2} {
			f.Add(format, []byte(seed))
		}
	}
	formats := []importprofile.Format{importprofile.CSV, importprofile.JSONEvents, importprofile.JSONModel}
	f.Fuzz(func(t *testing.T, which byte, data []byte) {
		format := formats[int(which)%len(formats)]
		lim := small
		lim.MaxFieldChars, lim.PreviewStringChars, lim.MaxModelBytes = 64, 8, 4096
		res, err := importprofile.Read(context.Background(), bytes.NewReader(data), format, lim)
		if err != nil {
			var fail *importprofile.Failure
			if !errors.As(err, &fail) {
				t.Fatalf("not a failure: %v", err)
			}
			if fail.Code == "" || fail.Message == "" || (fail.Path != "" && !strings.HasPrefix(fail.Path, "/")) || !utf8.ValidString(fail.Path) {
				t.Fatalf("failure %+v", fail)
			}
			return
		}
		p := res.Profile
		if p.Format != format || (p.Encoding != importprofile.UTF8 && p.Encoding != importprofile.UTF8BOM) || p.Rows > lim.MaxRows ||
			len(p.Columns) > lim.MaxColumns || res.InvalidRows > p.Rows || len(res.RowErrors) > lim.MaxRowErrors {
			t.Fatalf("profile %+v, invalid %d", p, res.InvalidRows)
		}
		if format == importprofile.JSONModel && (p.Rows != 0 || len(p.Columns) != 0 || len(res.Preview) != 0) {
			t.Fatalf("model profile %+v", p)
		}
		names := map[string]bool{}
		for i, c := range p.Columns {
			n := utf8.RuneCountInString(c.Name)
			if n < 1 || n > lim.MaxNameChars || c.Index != i || names[c.Name] || c.NonEmpty > p.Rows || c.MaxLength > lim.MaxFieldChars {
				t.Fatalf("column %+v", c)
			}
			names[c.Name] = true
		}
		size := 2
		for i, row := range res.Preview {
			if row.Kind() != '{' || !jsontext.Value(row).IsValid() || bytes.Contains(row, []byte(`\u0000`)) {
				t.Fatalf("preview row %s", row)
			}
			size += len(row)
			if i > 0 {
				size++
			}
		}
		if len(res.Preview) > lim.PreviewRows || size > lim.PreviewBytes || len(res.Preview) > p.Rows {
			t.Fatalf("%d preview rows of %d bytes", len(res.Preview), size)
		}
	})
}
