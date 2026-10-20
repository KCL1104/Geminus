package profile_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/iotest"

	importprofile "github.com/kcl1104/geminus/internal/import/profile"
)

// T-017: Scan reads a file exactly as Read profiles it and hands every
// data row to its visitor, in order: CSV rows with their header, line and
// fields (an invalid row's field count too), JSON events rows as checked
// objects. The result is Read's.
func TestScanVisitsEveryRow(t *testing.T) {
	csvFile := "a,b\n1,2\n\"x\ny\",3\n4\n5,6,7\n"
	type seen struct {
		index, line, count int
		fields             string
	}
	var got []seen
	res, err := importprofile.Scan(context.Background(), strings.NewReader(csvFile), importprofile.CSV, small, func(r importprofile.Row) error {
		if strings.Join(r.Header, ",") != "a,b" || r.Event != nil {
			t.Errorf("row %d: header %v event %s", r.Index, r.Header, r.Event)
		}
		got = append(got, seen{r.Index, r.Line, r.FieldCount, strings.Join(r.Fields, "|")})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []seen{{0, 2, 2, "1|2"}, {1, 3, 2, "x\ny|3"}, {2, 5, 1, "4"}, {3, 6, 3, "5|6|7"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("rows %v, want %v", got, want)
	}
	if r, _ := read(t, importprofile.CSV, small, csvFile); fmt.Sprint(r) != fmt.Sprint(res) {
		t.Errorf("Scan's result %v differs from Read's %v", res, r)
	}

	events := `[{"a":1,"b":"x"},{"b":null},{"c":[1,{"d":0}]}]`
	var rows []string
	_, err = importprofile.Scan(context.Background(), iotest.OneByteReader(strings.NewReader(events)), importprofile.JSONEvents, small,
		func(r importprofile.Row) error {
			if r.Line != 0 || r.Header != nil || r.Fields != nil {
				t.Errorf("row %d: a JSON row has %d %v %v", r.Index, r.Line, r.Header, r.Fields)
			}
			rows = append(rows, fmt.Sprintf("%d:%s", r.Index, r.Event))
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{`0:{"a":1,"b":"x"}`, `1:{"b":null}`, `2:{"c":[1,{"d":0}]}`}; fmt.Sprint(rows) != fmt.Sprint(want) {
		t.Errorf("events %v, want %v", rows, want)
	}
}

// Scan fails a file as Read does, before visiting a row the rules refuse;
// the visitor's error stops it as it is; a JSON model has no rows; the
// visitor is required.
func TestScanRefusals(t *testing.T) {
	visited := 0
	count := func(importprofile.Row) error { visited++; return nil }
	_, err := importprofile.Scan(context.Background(), strings.NewReader("a\n1\n\"bad\n"), importprofile.CSV, small, count)
	var fail *importprofile.Failure
	if !errors.As(err, &fail) || fail.Code != importprofile.CodeCSVSyntax || fail.Path != "/1" || visited != 1 {
		t.Errorf("syntax: %v after %d rows", err, visited)
	}
	visited = 0
	_, err = importprofile.Scan(context.Background(), strings.NewReader(`[{"a":1},{"a":1,"a":2}]`), importprofile.JSONEvents, small, count)
	if !errors.As(err, &fail) || fail.Code != importprofile.CodeJSONDuplicateKey || visited != 1 {
		t.Errorf("duplicate: %v after %d rows", err, visited)
	}
	stop := errors.New("stop")
	visited = 0
	_, err = importprofile.Scan(context.Background(), strings.NewReader("a\n1\n2\n3\n"), importprofile.CSV, small, func(r importprofile.Row) error {
		visited++
		if r.Index == 1 {
			return stop
		}
		return nil
	})
	if err != stop || visited != 2 {
		t.Errorf("visitor error: %v after %d rows", err, visited)
	}
	if _, err := importprofile.Scan(context.Background(), strings.NewReader("{}"), importprofile.JSONModel, small, count); err == nil {
		t.Error("a JSON model was scanned")
	}
	if _, err := importprofile.Scan(context.Background(), strings.NewReader("a\n1\n"), importprofile.CSV, small, nil); err == nil {
		t.Error("a scan without a visitor")
	}
}

// The transforms take exactly the shapes profiling infers (T-017), so the
// profile's inferred types predict what a mapping accepts.
func TestShapesAreTheInferences(t *testing.T) {
	for s, want := range map[string]bool{"2026-10-13T06:12:00+08:00": true, "2026-10-13t06:12:00z": true, "2026-10-13T06:12:00": false,
		"2026/10/13 06:12": false, "2026-13-01T00:00:00Z": false, "": false,
		// Review round 1: RFC 3339's offsets only (M3), and "-00:00", whose
		// offset is unknown, is not a time with an offset (L6).
		"2026-10-13T06:12:00+23:59": true, "2026-10-13T06:12:00-23:59": true, "2026-10-13T06:12:00+00:00": true,
		"2026-10-13T06:12:00+24:00": false, "2026-10-13T06:12:00+05:60": false, "2026-10-13T06:12:00+99:00": false,
		"2026-10-13T06:12:00-00:00": false, "2026-10-13T06:12:00.25-00:00": false} {
		if importprofile.IsTimestamp(s) != want {
			t.Errorf("IsTimestamp(%q) = %v", s, !want)
		}
	}
	for s, want := range map[string]bool{"true": true, "FALSE": true, "True": true, "yes": false, "1": false, "falſe": false, "": false} {
		if importprofile.IsBoolean(s) != want {
			t.Errorf("IsBoolean(%q) = %v", s, !want)
		}
	}
}

// PreviewJSON cuts strings at any depth (never member names) and keeps a
// number short as the database prints it, as the raw preview does.
func TestPreviewJSON(t *testing.T) {
	long := "0." + strings.Repeat("1", 399) // printed longer than 400 characters
	v, extra, err := importprofile.PreviewJSON([]byte(`{"longname":["abcdefgh",{"k":"ijklmnop"}],"n":1e300,"m":12,"f":`+long+`}`), 6)
	if err != nil {
		t.Fatal(err)
	}
	// 1e300 prints 301 digits (304 with sign, point and exponent room);
	// 12 prints 5 at most; the long fraction is its shortest binary64 form.
	if string(v) != `{"longname":["abcde…",{"k":"ijklm…"}],"n":1e300,"m":12,"f":0.1111111111111111}` || extra != (304-5)+(5-2)+(20-18) {
		t.Errorf("%s %d", v, extra)
	}
}
