package validation_test

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"strings"
	"testing"

	importprofile "github.com/kcl1104/geminus/internal/import/profile"
	"github.com/kcl1104/geminus/internal/import/validation"
)

// FuzzValidate: whatever rows follow the station header, Validate either
// fails the file as the profile does or accounts for every row: no more
// invalid rows than rows, the kept issues and preview within their
// bounds, every issue at a row of the file; and a valid outcome is a
// dataset Write writes, of the size Validate predicted, whose every
// element is a canonical event.
func FuzzValidate(f *testing.F) {
	for _, s := range []string{
		row("SN-1", "2026-10-13T06:12:00+08:00", "2026-10-13T06:15:10+08:00", "pass", "190000", "L-7"),
		row("", "2026/10/13", "x", "", "-1", "=1+1"),
		"SN-5,operation\r\n\"a\nb\",x,y,2026-10-13T06:12:00Z,,,,,\r\n",
		row("SN-1", "2026-10-13T06:12:00Z", "2026-10-13T06:11:00Z", "pass", "60000", ""),
	} {
		f.Add(s)
	}
	p := plan(f, stationMapping(), stationColumns)
	lim := validation.DefaultLimits
	lim.MaxIssues, lim.MaxRowErrors, lim.PreviewRows = 5, 7, 3
	f.Fuzz(func(t *testing.T, rows string) {
		data := header + rows
		out, err := validation.Validate(context.Background(), strings.NewReader(data), importprofile.CSV, p, lim)
		var fail *importprofile.Failure
		if errors.As(err, &fail) {
			if _, perr := importprofile.Read(context.Background(), strings.NewReader(data), importprofile.CSV, lim.File); perr == nil {
				t.Fatalf("validation failed a file the profile reads: %v", err)
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if out.InvalidRows > out.Rows || len(out.Issues) > lim.MaxIssues+1 || len(out.Errors) > lim.MaxRowErrors ||
			len(out.Preview) > min(lim.PreviewRows, out.Rows) {
			t.Fatalf("%+v", out)
		}
		for _, is := range out.Issues {
			if is.Code == validation.CodeDatasetTooLarge {
				continue
			}
			if !strings.HasPrefix(is.Path, "/") {
				t.Fatalf("issue %+v", is)
			}
		}
		if !out.Valid() {
			if out.InvalidRows == 0 {
				t.Fatalf("not valid without invalid rows: %+v", out)
			}
			return
		}
		var b bytes.Buffer
		n, err := validation.Write(context.Background(), strings.NewReader(data), importprofile.CSV, p, lim, &b)
		if err != nil || n != out.Rows || int64(b.Len()) != out.DatasetBytes {
			t.Fatalf("Write: %d rows, %d bytes (%d predicted): %v", n, b.Len(), out.DatasetBytes, err)
		}
		var events []jsontext.Value
		if err := json.Unmarshal(b.Bytes(), &events); err != nil || len(events) != n {
			t.Fatalf("%s: %v", b.Bytes(), err)
		}
		for _, e := range events {
			schemaValid(t, e)
		}
	})
}
