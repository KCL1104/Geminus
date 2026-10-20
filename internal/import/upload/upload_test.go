package upload

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/httpapi"
	importprofile "github.com/kcl1104/geminus/internal/import/profile"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/storage/idempotency"
	store "github.com/kcl1104/geminus/internal/storage/imports"
)

// The import routes are the contract's: method, path and roles
// (planning/api-operations.json, from openapi.yaml); mapping, suggestions
// and commit are T-017's, T-027's and T-018's.
func TestRoutesMatchContract(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "planning", "api-operations.json"))
	if err != nil {
		t.Fatal(err)
	}
	type operation struct {
		Method      string   `json:"method"`
		Path        string   `json:"path"`
		OperationID string   `json:"operation_id"`
		Roles       []string `json:"roles"`
	}
	var doc struct {
		Operations []operation `json:"operations"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range Routes {
		got = append(got, r.OperationID)
		i := slices.IndexFunc(doc.Operations, func(o operation) bool { return o.OperationID == r.OperationID })
		if i < 0 {
			t.Errorf("%s is not a contract operation", r.OperationID)
			continue
		}
		o := doc.Operations[i]
		var roles []string
		for _, x := range r.Roles {
			roles = append(roles, string(x))
		}
		if o.Method != r.Method || o.Path != r.Path || !slices.Equal(slices.Sorted(slices.Values(o.Roles)), slices.Sorted(slices.Values(roles))) {
			t.Errorf("%s: route %s %s %v, contract %s %s %v", r.OperationID, r.Method, r.Path, roles, o.Method, o.Path, o.Roles)
		}
	}
	if want := []string{"uploadImport", "getImport", "downloadImportErrors", "saveImportMapping"}; !slices.Equal(got, want) { // T-017 adds the mapping
		t.Errorf("routes %v, want %v", got, want)
	}
}

// The Go resource and profile types are the schemas' (no field more or
// less, the same JSON types).
func TestContractParity(t *testing.T) {
	s := contracttest.LoadSchemas(t)
	s.Parity(t, reflect.TypeFor[Import](), "import.schema.json")
	s.Parity(t, reflect.TypeFor[importprofile.Profile](), "import.schema.json#/$defs/Profile")
	s.Parity(t, reflect.TypeFor[importprofile.Column](), "import.schema.json#/$defs/ProfileColumn")
	s.Parity(t, reflect.TypeFor[Accepted](), "http.schema.json#/$defs/ImportAccepted")
	s.Parity(t, reflect.TypeFor[ErrorEnvelope](), "common.schema.json#/$defs/Error")
}

// P0 refuses archives and executables (SECURITY §6), by their first bytes
// and by their names, before anything is stored; CSV and JSON text,
// Chinese included, is never mistaken for one.
func TestSniff(t *testing.T) {
	tar := make([]byte, 512)
	copy(tar[257:], "ustar\x00")
	refused := map[string][]byte{
		"zip":        []byte("PK\x03\x04rest"),
		"empty zip":  []byte("PK\x05\x06"),
		"gzip":       {0x1f, 0x8b, 0x08, 0x00},
		"bzip2":      []byte("BZh91AY&SYrest"),
		"xz":         {0xfd, '7', 'z', 'X', 'Z', 0x00, 0x00},
		"zstd":       {0x28, 0xb5, 0x2f, 0xfd, 0x00},
		"7z":         {'7', 'z', 0xbc, 0xaf, 0x27, 0x1c, 0x00},
		"rar":        []byte("Rar!\x1a\x07\x00"),
		"tar":        tar,
		"ole":        {0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1},
		"elf":        []byte("\x7fELF\x02\x01"),
		"pe":         []byte("MZ\x90\x00\x03\x00"),
		"mach-o":     {0xcf, 0xfa, 0xed, 0xfe, 0x07},
		"java class": {0xca, 0xfe, 0xba, 0xbe, 0x00},
		"wasm":       []byte("\x00asm\x01\x00"),
		"script":     []byte("#!/bin/sh\nrm -rf /\n"),
		"env script": []byte("#! /usr/bin/env python\n"),
	}
	for name, head := range refused {
		if Sniff("data.csv", head) == "" {
			t.Errorf("%s was not refused", name)
		}
	}
	for _, name := range []string{"report.zip", "events.JSON.gz", "a.XLSX", "tool.exe", "x.tar", "lib.so", "run.sh"} {
		if Sniff(name, []byte("a,b\n1,2\n")) == "" {
			t.Errorf("%s was not refused", name)
		}
	}
	accepted := map[string][]byte{
		"csv":                  []byte("a,b\n1,2\n"),
		"csv with BOM":         []byte("\xef\xbb\xbf案件編號,階段\n"),
		"json events":          []byte(`[{"case_id":"x"}]`),
		"header starting MZ":   []byte("MZ_code,name\n1,2\n"),
		"header starting BZh":  []byte("BZh_rate,x\n"),
		"header starting PK":   []byte("PK,name\n1,2\n"),
		"a hash comment":       []byte("#id,name\n"),
		"Chinese":              []byte("名稱,數量\n電阻,10\n"),
		"ustar in the content": []byte(strings.Repeat("a,b\n", 64) + "ustar,x\n"),
	}
	for name, head := range accepted {
		if got := Sniff("events.csv", head); got != "" {
			t.Errorf("%s was refused as %s", name, got)
		}
	}
	for _, name := range []string{"events.csv", "events.json", "數據.csv", "no-extension", "a.tsv", "a.txt"} {
		if got := Sniff(name, []byte("a\n1\n")); got != "" {
			t.Errorf("%s was refused as %s", name, got)
		}
	}
}

// A file name is kept without its directory, as UTF-8 of 1–255 characters
// without control or format characters (which could disguise it).
func TestCleanName(t *testing.T) {
	for in, want := range map[string]string{
		"events.csv":                  "events.csv",
		`C:\Users\x\Desktop\原始資料.csv`: "原始資料.csv",
		"/home/x/../events.csv":       "events.csv",
		strings.Repeat("名", 255):      strings.Repeat("名", 255),
		"=SUM(A1)+cmd|'x'!A0.csv":     "=SUM(A1)+cmd|'x'!A0.csv",
		"=cmd|' /C calc'!A0.csv":      "C calc'!A0.csv", // "/" separates directories
		"report final (2).csv":        "report final (2).csv",
	} {
		got, err := cleanName(in)
		if err != nil || got != want {
			t.Errorf("cleanName(%q) = %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "dir/", ".", "..", "a\x00.csv", "a\nb.csv", "a\x7f.csv", "evil\u202ecsv.exe", "a\u200b.csv",
		strings.Repeat("名", 256), "\xff.csv"} {
		if got, err := cleanName(in); err == nil {
			t.Errorf("cleanName(%q) = %q", in, got)
		}
	}
}

// Each field has its pointer; all are reported at once.
func TestFieldsCheck(t *testing.T) {
	ok := Fields{ProjectID: "prj_01M4YDQK00K9HVKK0GQKY0MHSA", ModelID: "mdl_01M4YDQK004AGVNEGJQJ1NMM0N",
		BaseVersionID: "mv_01M4YDQK00FRYWV7GS7X1QCXZR", Kind: "csv_events", Filename: "a.csv"}
	if issues := ok.Check(); len(issues) != 0 {
		t.Fatal(issues)
	}
	bad := Fields{ProjectID: "mdl_01M4YDQK004AGVNEGJQJ1NMM0N", ModelID: "", BaseVersionID: "mv_x", Kind: "xlsx"}
	var paths []string
	for _, is := range bad.Check() {
		paths = append(paths, is.Path)
	}
	if strings.Join(paths, ",") != "/project_id,/model_id,/base_version_id,/kind" {
		t.Errorf("%v", paths)
	}
}

// A FAILED import's error is the contract's envelope (CB-39: the interim
// mapping of failure codes to the closed ErrorCode list), the failure's
// own code and JSON Pointer in details[0], under the upload's request.
func TestEnvelope(t *testing.T) {
	// The message names no step (T-017: a validation's failure is shown
	// the same way as a profile's; review round 1, L4).
	const processed, again = "the file could not be processed", "the file could not be processed; upload it again"
	cases := []struct {
		stored, code string
		path         string
		message      string
	}{
		{`{"code":"IMPORT_LIMIT_EXCEEDED","message":"the file has more than 100000 rows","path":"/100000","retryable":false}`, "PAYLOAD_TOO_LARGE", "/100000", "the file is larger than an import may be"},
		{`{"code":"JSON_DUPLICATE_KEY","message":"a member name repeats within one JSON object","path":"/41/case_id","retryable":false}`, "REQUEST_VALIDATION_FAILED", "/41/case_id", "the file cannot be read as uploaded"},
		{`{"code":"CSV_SYNTAX_INVALID","message":"m","path":"/2","retryable":false}`, "REQUEST_VALIDATION_FAILED", "/2", "the file cannot be read as uploaded"},
		{`{"code":"IMPORT_ENCODING_INVALID","message":"line 2: not UTF-8","retryable":false}`, "REQUEST_VALIDATION_FAILED", "", "the file cannot be read as uploaded"},
		{`{"code":"OBJECT_STORAGE_FAILED","message":"object storage failed","retryable":true}`, "SERVICE_UNAVAILABLE", "", again},
		{`{"code":"OBJECT_STORAGE_UNAVAILABLE","message":"this worker has no object storage","retryable":true}`, "SERVICE_UNAVAILABLE", "", again},
		{`{"code":"LEASE_EXPIRED","message":"the lease expired","retryable":true}`, "SERVICE_UNAVAILABLE", "", again},
		{`{"code":"WORKER_STOPPED","message":"stopped","retryable":true}`, "SERVICE_UNAVAILABLE", "", again},
		{`{"code":"UPLOAD_CORRUPT","message":"m","retryable":false}`, "INTERNAL_ERROR", "", processed},
		{`{"code":"UPLOAD_CHANGED","message":"m","retryable":false}`, "INTERNAL_ERROR", "", processed},
		{`{"code":"JOB_CANCELLED","message":"m","retryable":false}`, "INTERNAL_ERROR", "", processed},
		{`{"code":"INTERNAL","message":"","retryable":true}`, "INTERNAL_ERROR", "", processed},
		// Review round 1, L3: the server could not read what it stored
		// (CB-39(a)): not a problem with the file, whatever its prefix.
		{`{"code":"IMPORT_INPUT_INVALID","message":"the import's mapping cannot be read","retryable":false}`, "INTERNAL_ERROR", "", processed},
	}
	for _, c := range cases {
		e, err := envelope([]byte(c.stored), "req_01M4YDQK00V6H8S0ST4F7510KR")
		if err != nil {
			t.Fatal(err)
		}
		var f store.Failure
		_ = json.Unmarshal([]byte(c.stored), &f)
		d := e.Error.Details
		if e.Error.Code != c.code || e.Error.Message != c.message || len(d) != 1 || d[0].Code != f.Code || d[0].Path != c.path || d[0].Reason == "" ||
			e.Error.RequestID != "req_01M4YDQK00V6H8S0ST4F7510KR" {
			t.Errorf("%s: %+v", c.stored, e.Error)
		}
		data, _ := json.Marshal(e)
		if issues, err := validate.Document(data, "common.schema.json#/$defs/Error", "X"); err != nil || len(issues) > 0 {
			t.Errorf("%s: %s %v %v", c.stored, data, issues, err)
		}
	}
	// A very long pointer (names of up to 32,768 characters) is cut to one
	// of its ancestors that fits the schema.
	long := `{"code":"JSON_NUMBER_NOT_FINITE","message":"m","path":"/0/` + strings.Repeat("a", 3000) + `/b","retryable":false}`
	e, err := envelope([]byte(long), "req_01M4YDQK00V6H8S0ST4F7510KR")
	if err != nil || e.Error.Details[0].Path != "/0" {
		t.Errorf("%v %q", err, e.Error.Details[0].Path)
	}
	for _, broken := range []string{`{}`, `{"code":"lower"}`, `[]`, `not json`} {
		if _, err := envelope([]byte(broken), "req_01M4YDQK00V6H8S0ST4F7510KR"); err == nil {
			t.Errorf("%s was shown as a failure", broken)
		}
	}
}

// An import is shown as stored (T-016 review M1: the row follows its
// profile job, 0010): FAILED with the error in its row, under the request
// that uploaded it, whatever the job's error was; any other state with no
// error. A FAILED row without a failure it can show is an error, not a
// guess.
func TestStoredStatus(t *testing.T) {
	base := store.Import{ID: "imp_01M4YDQK0001HSSAXJ76PRHJ58", Status: store.Failed, Preview: []byte("[]"), RequestID: "req_01M4YDQK00V6H8S0ST4F7510KR"}
	for stored, want := range map[string][2]string{
		`{"code":"OBJECT_STORAGE_UNAVAILABLE","message":"this worker has no object storage","retryable":true}`:         {"SERVICE_UNAVAILABLE", "OBJECT_STORAGE_UNAVAILABLE"},
		`{"code":"JOB_CANCELLED","message":"the profile job was ended before it profiled the file","retryable":false}`: {"INTERNAL_ERROR", store.JobCancelled},
		`{"code":"INTERNAL","message":"the profile job failed","retryable":false}`:                                     {"INTERNAL_ERROR", "INTERNAL"},
		`{"code":"JSON_DUPLICATE_KEY","message":"m","path":"/1/b","retryable":false}`:                                  {"REQUEST_VALIDATION_FAILED", "JSON_DUPLICATE_KEY"},
	} {
		failed := base
		failed.Failure = []byte(stored)
		p, err := public(failed, "req_01M4YDQK00TA00000000000099")
		if err != nil || p.Status != "FAILED" || p.Error == nil || p.Error.Error.Code != want[0] || p.Error.Error.Details[0].Code != want[1] ||
			p.Error.Error.RequestID != base.RequestID {
			t.Errorf("%s: %+v %v", stored, p, err)
		}
	}
	for _, s := range []store.Status{store.Uploaded, store.Profiling, store.AwaitingMapping} {
		i := base
		i.Status = s
		if s == store.AwaitingMapping {
			i.Profile = []byte(`{}`)
		}
		if p, err := public(i, ""); err != nil || p.Status != string(s) || p.Error != nil {
			t.Errorf("%s: %+v %v", s, p, err)
		}
	}
	if _, err := public(base, ""); err == nil {
		t.Error("a FAILED import without its failure was shown")
	}
}

// Each service error has its contract code; the others are httpapi's.
func TestRefused(t *testing.T) {
	issue := model.NewIssue(validate.ConstraintFailed, "/model_id", "the model is not in project_id", model.SeverityError)
	for err, want := range map[error]httpapi.Code{
		ErrNotFound:                          httpapi.NotFound,
		ErrNoReport:                          httpapi.NotFound,
		ErrStorageUnavailable:                httpapi.ServiceUnavailable,
		ErrRetry:                             httpapi.ServiceUnavailable,
		ErrTooLarge:                          httpapi.PayloadTooLarge,
		&http.MaxBytesError{Limit: 1}:        httpapi.PayloadTooLarge,
		&SourceError{Err: errors.New("cut")}: httpapi.InvalidRequest,
		&FormatError{Format: "gzip"}:         httpapi.UnsupportedMediaType,
		&RequestError{Issues: []model.ValidationIssue{issue}}: httpapi.RequestValidationFailed,
		idempotency.ErrConflict:                               httpapi.IdempotencyKeyConflict,
		idempotency.ErrInProgress:                             httpapi.IdempotencyKeyConflict,
	} {
		var e *httpapi.Error
		if !errors.As(Refused(err), &e) || e.Code != want {
			t.Errorf("%v: %v, want %s", err, Refused(err), want)
		}
	}
	var e *httpapi.Error
	if errors.As(Refused(&FormatError{Format: "gzip"}), &e); e.Details[0].Code != FormatRejected || e.Details[0].Path != "/file" {
		t.Errorf("%+v", e)
	}
	if errors.As(Refused(ErrReportCorrupt), &e) {
		t.Error("a corrupt report is shown to the client")
	}
	// Review round 1, M1: a stored suggestion that does not match its
	// schema is the server's fault: 500, with its code, and nothing saved.
	if !errors.As(Refused(fmt.Errorf("import x: %w", ErrSuggestionInvalid)), &e) || e.Code != httpapi.InternalError ||
		len(e.Details) != 1 || e.Details[0].Code != CodeSuggestionInvalid || e.Details[0].Path != "" {
		t.Errorf("a malformed suggestion: %+v", e)
	}
}

// Review round 1, M1: the stored suggestion is the worker's (the AI's)
// writing, so it is checked against ai-mapping.schema.json each time a
// save reads it: one that does not match (a confidence below 0.80 that is
// not flagged, a null target that is not flagged, another member, a ready
// status with no suggestion) fails closed; a suggestion that is not ready
// is not read at all.
func TestStoredSuggestion(t *testing.T) {
	good := `{"mappings":[{"source_column":"a","target_field":"case_id","confidence":0.9,"evidence":"e","transform":"identity","requires_confirmation":false},` +
		`{"source_column":"b","target_field":null,"confidence":0.3,"evidence":"e","transform":"identity","requires_confirmation":true}]}`
	s, err := suggestion(store.Import{ID: "imp_x", MappingSuggestionStatus: "ready", MappingSuggestion: []byte(good)})
	if err != nil || s == nil || len(s.Mappings) != 2 || s.Mappings[1].TargetField != nil || s.Mappings[0].Confidence != 0.9 {
		t.Fatalf("%+v %v", s, err)
	}
	for name, doc := range map[string]string{
		"a low confidence not flagged": `{"mappings":[{"source_column":"a","target_field":"case_id","confidence":0.1,"evidence":"e","transform":"identity","requires_confirmation":false}]}`,
		"a null target not flagged":    `{"mappings":[{"source_column":"a","target_field":null,"confidence":0.9,"evidence":"e","transform":"identity","requires_confirmation":false}]}`,
		"another member":               `{"mappings":[{"source_column":"a","target_field":"case_id","confidence":0.9,"evidence":"e","transform":"identity","requires_confirmation":false,"approved":true}]}`,
		"an expression":                `{"mappings":[{"source_column":"a","target_field":"case_id","confidence":0.9,"evidence":"e","transform":"=A1","requires_confirmation":false}]}`,
		"a confidence above 1":         `{"mappings":[{"source_column":"a","target_field":"case_id","confidence":1.5,"evidence":"e","transform":"identity","requires_confirmation":false}]}`,
		"not an object":                `[]`,
		"no suggestion":                ``,
	} {
		var stored []byte
		if doc != "" {
			stored = []byte(doc)
		}
		if s, err := suggestion(store.Import{ID: "imp_x", MappingSuggestionStatus: "ready", MappingSuggestion: stored}); !errors.Is(err, ErrSuggestionInvalid) || s != nil {
			t.Errorf("%s: %+v %v", name, s, err)
		}
	}
	for _, status := range []string{"none", "generating", "failed"} {
		if s, err := suggestion(store.Import{ID: "imp_x", MappingSuggestionStatus: status, MappingSuggestion: []byte(`not json`)}); err != nil || s != nil {
			t.Errorf("%s: %+v %v", status, s, err)
		}
	}
}

// A key is the import's own only under its own prefix of its tenant's
// imports area.
func TestOwnKey(t *testing.T) {
	org, imp := "org_01M4YDQK00Y5H2929M9WWZWTVH", "imp_01M4YDQK0001HSSAXJ76PRHJ58"
	if !store.OwnKey(org, imp, "tenant/"+org+"/imports/"+imp+"/profile-errors-1.csv") {
		t.Error("its own report")
	}
	for _, key := range []string{
		"tenant/" + org + "/imports/imp_01M4YDQK0001HSSAXJ76PRHJ59/upload",
		"tenant/" + org + "/imports/" + imp + "x/upload",
		"tenant/org_01M4YDQK00TA00000000000000/imports/" + imp + "/upload",
		"tenant/" + org + "/simulation-results/" + imp + "/upload",
		"tenant/" + org + "/imports/" + imp + "/../x",
		"tenant/" + org + "/imports/" + imp + "/",
	} {
		if store.OwnKey(org, imp, key) {
			t.Errorf("%s is the import's own", key)
		}
	}
}
