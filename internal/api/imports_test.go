package api_test

// Integration tests of the import routes (T-016) through the whole stack:
// the handler cmd/api serves as the plain API login, and the job loop
// cmd/worker runs as the plain worker login with the import profile
// handler, on the disposable database and a filesystem object store
// (TEST-012, TEST-029, TEST-009 for imports; D-009, D-043, D-044).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/api"
	"github.com/kcl1104/geminus/internal/import/profile"
	"github.com/kcl1104/geminus/internal/import/profiler"
	"github.com/kcl1104/geminus/internal/import/report"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/storage/postgres"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/worker"
	"github.com/kcl1104/geminus/internal/worker/handler"
)

// part is one part of a multipart/form-data body.
type part struct {
	name, filename string
	file           bool
	content        string
	header         map[string]string
}

func multipartBody(t *testing.T, parts []part) (*bytes.Buffer, string) {
	t.Helper()
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	for _, p := range parts {
		h := textproto.MIMEHeader{}
		cd := fmt.Sprintf(`form-data; name="%s"`, p.name)
		if p.file {
			cd += fmt.Sprintf(`; filename="%s"`, p.filename)
			h.Set("Content-Type", "application/octet-stream")
		}
		h.Set("Content-Disposition", cd)
		for k, v := range p.header {
			h.Set(k, v)
		}
		pw, err := w.CreatePart(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(pw, p.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &b, w.FormDataContentType()
}

// fields are an upload's form fields for model m, then its file.
func fields(m modelSummary, kind, filename, content string) []part {
	return []part{
		{name: "project_id", content: m.ProjectID}, {name: "model_id", content: m.ID},
		{name: "base_version_id", content: m.LatestVersion.ID}, {name: "kind", content: kind},
		{name: "file", file: true, filename: filename, content: content},
	}
}

// upload sends parts as subject in org with key ("" for none).
func (e *runEnv) upload(t *testing.T, subject, org, key string, parts []part) resp {
	t.Helper()
	body, ct := multipartBody(t, parts)
	req, err := http.NewRequest("POST", e.srv.URL+"/v1/imports", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+e.token(subject))
	req.Header.Set("X-Organization-ID", org)
	req.Header.Set("Content-Type", ct)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	return e.send(req)
}

// profileLoop is a job loop with the import profile handler (and wrap, if
// given, around it).
func (e *runEnv) profileLoop(t *testing.T, cfg profiler.Config, wrap func(handler.Handler) handler.Handler) *worker.Worker {
	t.Helper()
	if cfg.Actor == "" {
		cfg.Actor = "test-worker"
	}
	cfg.Log = slog.New(slog.NewJSONHandler(lockedWriter{e.logs, e.logsMu}, nil))
	p, err := profiler.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var h handler.Handler = p
	if wrap != nil {
		h = wrap(p)
	}
	w, err := worker.New(e.scope, worker.Config{ID: cfg.Actor, Policy: fastPolicy, Heartbeat: time.Second, Idle: 20 * time.Millisecond,
		ReapLimit: 10, Handlers: map[jobs.Type]handler.Handler{jobs.ImportProfile: h}, Log: cfg.Log})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

type accepted struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type importBody struct {
	ID          string           `json:"id"`
	Status      string           `json:"status"`
	Filename    string           `json:"filename"`
	TotalRows   int              `json:"total_rows"`
	InvalidRows int              `json:"invalid_rows"`
	Profile     *profile.Profile `json:"profile"`
	Preview     []map[string]any `json:"preview"`
	Error       *struct {
		Error struct {
			Code    string `json:"code"`
			Details []struct {
				Path   string `json:"path"`
				Reason string `json:"reason"`
				Code   string `json:"code"`
			} `json:"details"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	} `json:"error"`
}

func (e *runEnv) getImport(t *testing.T, subject, id string) importBody {
	t.Helper()
	r := e.do("GET", "/v1/imports/"+id, subject, e.a.ID, nil, "")
	if r.status != 200 {
		t.Fatalf("get %s: %d %s", id, r.status, r.body)
	}
	valid(t, r, "import.schema.json")
	return decode[importBody](t, r)
}

// twin is the model's state the import must leave as it is (D-009): its
// versions, its draft and its latest version.
func (e *runEnv) twin(t *testing.T, modelID string) string {
	t.Helper()
	var s string
	if err := e.admin.QueryRow(context.Background(), `SELECT
		  (SELECT string_agg(id || ':' || hash, ',' ORDER BY version) FROM twin.model_versions WHERE model_id=$1) || '|' ||
		  (SELECT base_version_id || ':' || revision || ':' || hash FROM twin.model_drafts WHERE model_id=$1) || '|' ||
		  (SELECT latest_version_id || ':' || updated_at FROM twin.models WHERE id=$1)`, modelID).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

// objectsUnder lists the stored objects of org's imports.
func (e *runEnv) objectsUnder(t *testing.T, org string) []string {
	t.Helper()
	var out []string
	root := filepath.Join(e.root, "tenant", org, "imports")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !d.IsDir() && !strings.HasPrefix(d.Name(), ".") {
			rel, _ := filepath.Rel(e.root, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// A synthetic steps file in UTF-8 with a BOM, Chinese column names and
// values, a CRLF file with a quoted field over two lines, and two rows
// whose field counts differ from the header's, one carrying a formula.
const stepsCSV = "\ufeff案件編號,階段,開始時間,耗時毫秒,\"=HYPERLINK(\"\"http://x.invalid\"\")\"\r\n" +
	"CASE-01,檢驗,2026-10-15T08:30:00+08:00,7200000,\r\n" +
	"CASE-02,\"組裝\r\n第二行\",2026-10-15T08:31:00+08:00,,\r\n" +
	"CASE-03,包裝,2026-10-15T08:32:00+08:00,60000,,=cmd|' /C calc'!A0\r\n" +
	"CASE-04,出貨,2026-10-15T08:33:00+08:00,10\r\n"

// The closed loop of T-016: an editor uploads; the file is stored as sent
// under the server's key and the import, its job and its audit event are
// committed; the worker profiles it (Chinese and BOM as written, invalid
// rows listed); the error report downloads with every formula neutralised;
// the Twin never changes (D-009).
func TestImportClosedLoop(t *testing.T) {
	e := setupRuns(t)
	m := e.newModel(t)
	before := e.twin(t, m.ID)
	editor, viewer := e.a.Members[tenant.Editor], e.a.Members[tenant.Viewer]
	r := e.upload(t, editor.Subject, e.a.ID, "key-closed-loop", fields(m, "csv_events", "原始資料.csv", stepsCSV))
	if r.status != 202 {
		t.Fatalf("upload: %d %s", r.status, r.body)
	}
	valid(t, r, "http.schema.json#/$defs/ImportAccepted")
	acc := decode[accepted](t, r)
	if acc.Status != "UPLOADED" {
		t.Fatalf("%+v", acc)
	}
	// Stored as sent, under the import's own key, with its hash and size.
	sum := sha256.Sum256([]byte(stepsCSV))
	var key, hash, status string
	var size int64
	if err := e.admin.QueryRow(context.Background(), `SELECT upload_object_key, upload_content_hash, size_bytes, status FROM twin.imports WHERE id=$1`,
		acc.ID).Scan(&key, &hash, &size, &status); err != nil {
		t.Fatal(err)
	}
	if key != "tenant/"+e.a.ID+"/imports/"+acc.ID+"/upload" || hash != "sha256:"+hex.EncodeToString(sum[:]) || size != int64(len(stepsCSV)) || status != "UPLOADED" {
		t.Errorf("row %s %s %d %s", key, hash, size, status)
	}
	stored, err := os.ReadFile(filepath.Join(e.root, filepath.FromSlash(key)))
	if err != nil || string(stored) != stepsCSV {
		t.Errorf("stored object: %v", err)
	}
	// The job and the audit event committed with the import; the audit
	// names the upload by hash and size, never its name or content.
	var payload []byte
	if err := e.admin.QueryRow(context.Background(), `SELECT payload FROM twin.jobs WHERE import_id=$1 AND type='import_profile' AND status='queued'`, acc.ID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var meta string
	if err := e.admin.QueryRow(context.Background(), `SELECT metadata::text FROM twin.audit_events WHERE target_id=$1 AND action='import.upload' AND actor_type='user' AND actor_id=$2 AND request_id=$3`,
		acc.ID, editor.UserID, r.header.Get("X-Request-ID")).Scan(&meta); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(meta, hash) || strings.Contains(meta, "原始資料") || strings.Contains(meta, "CASE") {
		t.Errorf("audit metadata %s", meta)
	}
	var p map[string]string
	if err := json.Unmarshal(payload, &p); err != nil || p["import_id"] != acc.ID || p["request_id"] != r.header.Get("X-Request-ID") || len(p) != 2 {
		t.Errorf("payload %s", payload)
	}
	// Before profiling: no profile, no preview.
	if got := e.getImport(t, viewer.Subject, acc.ID); got.Status != "UPLOADED" || got.Profile != nil || len(got.Preview) != 0 || got.Filename != "原始資料.csv" {
		t.Errorf("before profiling %+v", got)
	}
	if r := e.do("GET", "/v1/imports/"+acc.ID+"/errors", viewer.Subject, e.a.ID, nil, ""); r.status != 404 || code(t, r) != "NOT_FOUND" {
		t.Errorf("report before profiling: %d %s", r.status, r.body)
	}
	drain(t, e.profileLoop(t, profiler.Config{Objects: e.objects}, nil), 10*time.Second)
	got := e.getImport(t, viewer.Subject, acc.ID)
	if got.Status != "AWAITING_MAPPING" || got.TotalRows != 4 || got.InvalidRows != 2 || got.Error != nil {
		if got.Error != nil {
			t.Logf("error %+v", got.Error.Error)
		}
		t.Fatalf("after profiling %+v", got)
	}
	pr := got.Profile
	if pr.Format != profile.CSV || pr.Encoding != profile.UTF8BOM || pr.Rows != 4 || len(pr.Columns) != 5 ||
		pr.Columns[0].Name != "案件編號" || pr.Columns[1].Name != "階段" || pr.Columns[1].MaxLength != 6 || pr.Columns[2].Inferred != profile.Timestamp ||
		pr.Columns[3].Inferred != profile.Integer || pr.Columns[3].NonEmpty != 1 || pr.Columns[4].Name != `=HYPERLINK("http://x.invalid")` {
		t.Errorf("profile %+v", pr)
	}
	if len(got.Preview) != 4 || got.Preview[1]["階段"] != "組裝\n第二行" || got.Preview[3]["階段"] != "出貨" || len(got.Preview[3]) != 4 ||
		got.Preview[0][`=HYPERLINK("http://x.invalid")`] != "" {
		t.Errorf("preview %v", got.Preview)
	}
	var worker string
	if err := e.admin.QueryRow(context.Background(), `SELECT metadata::text FROM twin.audit_events WHERE target_id=$1 AND action='import.profile' AND actor_type='worker' AND request_id=$2`,
		acc.ID, r.header.Get("X-Request-ID")).Scan(&worker); err != nil || strings.Contains(worker, "CASE") || strings.Contains(worker, "階段") {
		t.Errorf("worker audit %s: %v", worker, err)
	}
	// The error report: CSV with a BOM, the two invalid rows, every cell
	// safe to open in a spreadsheet (TEST-029).
	rep := e.do("GET", "/v1/imports/"+acc.ID+"/errors", viewer.Subject, e.a.ID, nil, "")
	if rep.status != 200 || rep.header.Get("Content-Type") != "text/csv; charset=utf-8" || rep.header.Get("Location") != "" ||
		rep.header.Get("Content-Disposition") != `attachment; filename="`+acc.ID+`-errors.csv"` || rep.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("report: %d %v", rep.status, rep.header)
	}
	rows, err := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(rep.body, []byte("\xef\xbb\xbf")))).ReadAll()
	if err != nil || !bytes.HasPrefix(rep.body, []byte("\xef\xbb\xbf")) {
		t.Fatalf("report %q: %v", rep.body, err)
	}
	want := [][]string{
		report.Header,
		{"/2", "5", "", profile.CodeFieldCount, "the row has 6 fields; the header has 5", "'=cmd|' /C calc'!A0"},
		{"/3", "6", "'=HYPERLINK(\"http://x.invalid\")", profile.CodeFieldCount, "the row has 4 fields; the header has 5", ""},
	}
	if fmt.Sprint(rows) != fmt.Sprint(want) {
		t.Errorf("report rows %q, want %q", rows, want)
	}
	for _, row := range rows {
		for _, cell := range row {
			if report.Safe(cell) != cell {
				t.Errorf("unsafe cell %q", cell)
			}
		}
	}
	if after := e.twin(t, m.ID); after != before {
		t.Errorf("the Twin changed: %s, then %s", before, after)
	}
	// The stored report is served only as recorded: changed or gone, it is
	// a 500 and none of its bytes are sent (D-044, fail closed).
	var reportKey string
	if err := e.admin.QueryRow(context.Background(), `SELECT error_report_object_key FROM twin.imports WHERE id=$1`, acc.ID).Scan(&reportKey); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(e.root, filepath.FromSlash(reportKey))
	if err := os.WriteFile(path, bytes.Replace(rep.body, []byte("CSV_FIELD_COUNT"), []byte("CSV_FIELD_C0UNT"), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := e.do("GET", "/v1/imports/"+acc.ID+"/errors", viewer.Subject, e.a.ID, nil, ""); r.status != 500 || bytes.Contains(r.body, []byte("CSV_FIELD")) {
		t.Errorf("tampered report: %d %s", r.status, r.body)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if r := e.do("GET", "/v1/imports/"+acc.ID+"/errors", viewer.Subject, e.a.ID, nil, ""); r.status != 500 || code(t, r) != "INTERNAL_ERROR" {
		t.Errorf("missing report: %d %s", r.status, r.body)
	}
}

// TEST-009 for imports, TEST-029: owners and editors upload, viewers read;
// another organization's member and a stranger see 404 for every route,
// and an upload refused for its role stores nothing.
func TestImportRoleMatrix(t *testing.T) {
	e := setupRuns(t)
	m := e.newModel(t)
	owner, editor, viewer := e.a.Members[tenant.Owner], e.a.Members[tenant.Editor], e.a.Members[tenant.Viewer]
	outsider := e.b.Members[tenant.Owner]
	file := "a,b\n1,2\n1\n"
	for _, c := range []struct {
		who    string
		org    string
		status int
	}{{viewer.Subject, e.a.ID, 403}, {outsider.Subject, e.a.ID, 404}, {outsider.Subject, e.b.ID, 404}} {
		r := e.upload(t, c.who, c.org, "key-role-"+c.who, fields(m, "csv_events", "a.csv", file))
		if r.status != c.status {
			t.Errorf("%s in %s: %d %s", c.who, c.org, r.status, r.body)
		}
	}
	if n := e.count(`SELECT count(*) FROM twin.imports WHERE model_id=$1`, m.ID); n != 0 || len(e.objectsUnder(t, e.a.ID))+len(e.objectsUnder(t, e.b.ID)) != 0 {
		t.Fatalf("refused uploads left %d imports, objects %v %v", n, e.objectsUnder(t, e.a.ID), e.objectsUnder(t, e.b.ID))
	}
	var ids []string
	for _, who := range []string{owner.Subject, editor.Subject} {
		r := e.upload(t, who, e.a.ID, "key-role-ok-"+who, fields(m, "csv_events", "a.csv", file))
		if r.status != 202 {
			t.Fatalf("%s: %d %s", who, r.status, r.body)
		}
		ids = append(ids, decode[accepted](t, r).ID)
	}
	drain(t, e.profileLoop(t, profiler.Config{Objects: e.objects}, nil), 10*time.Second)
	for _, id := range ids {
		for _, path := range []string{"/v1/imports/" + id, "/v1/imports/" + id + "/errors"} {
			if r := e.do("GET", path, viewer.Subject, e.a.ID, nil, ""); r.status != 200 {
				t.Errorf("viewer %s: %d %s", path, r.status, r.body)
			}
			// Another tenant's member: whether or not it names the right
			// organization, the import is not found (never forbidden).
			for _, org := range []string{e.a.ID, e.b.ID} {
				if r := e.do("GET", path, outsider.Subject, org, nil, ""); r.status != 404 || code(t, r) != "NOT_FOUND" {
					t.Errorf("outsider in %s %s: %d %s", org, path, r.status, r.body)
				}
			}
			if r := e.do("GET", path, "", e.a.ID, nil, ""); r.status != 401 {
				t.Errorf("no token %s: %d", path, r.status)
			}
		}
	}
	for _, bad := range []string{"/v1/imports/imp_x", "/v1/imports/sim_01M4YDQK00TA00000000000001/errors", "/v1/imports/" + strings.ToLower(ids[0])} {
		if r := e.do("GET", bad, viewer.Subject, e.a.ID, nil, ""); r.status != 400 || code(t, r) != "INVALID_REQUEST" {
			t.Errorf("%s: %d %s", bad, r.status, r.body)
		}
	}
	// A stranger with a token but no user is 403; with a user and no
	// membership, 404.
	stranger := "sub-nobody-" + t.Name()
	if r := e.upload(t, stranger, e.a.ID, "key-stranger", fields(m, "csv_events", "a.csv", file)); r.status != 403 {
		t.Errorf("unknown identity: %d %s", r.status, r.body)
	}
}

// TEST-012: what an upload may not be, refused before anything is
// committed: the right status and code, a JSON Pointer at the field, and
// no import, job or audit event.
func TestUploadRefusals(t *testing.T) {
	e := setupRuns(t, api.WithUploadMaxBytes(1024))
	m := e.newModel(t)
	other := e.newModelIn(t, "-other")
	editor := e.a.Members[tenant.Editor]
	ok := "a,b\n1,2\n"
	type expect struct {
		status int
		code   string
		path   string
	}
	swap := func(parts []part, i int, p part) []part {
		out := append([]part(nil), parts...)
		out[i] = p
		return out
	}
	base := fields(m, "csv_events", "a.csv", ok)
	cases := map[string]struct {
		parts []part
		want  expect
	}{
		"an empty file":               {fields(m, "csv_events", "a.csv", ""), expect{422, "REQUEST_VALIDATION_FAILED", "/file"}},
		"a ZIP archive":               {fields(m, "csv_events", "a.csv", "PK\x03\x04data"), expect{415, "UNSUPPORTED_MEDIA_TYPE", "/file"}},
		"a gzip file":                 {fields(m, "json_events", "a.json", "\x1f\x8b\x08\x00"), expect{415, "UNSUPPORTED_MEDIA_TYPE", "/file"}},
		"an ELF executable":           {fields(m, "csv_events", "a.csv", "\x7fELF\x02\x01"), expect{415, "UNSUPPORTED_MEDIA_TYPE", "/file"}},
		"a script":                    {fields(m, "csv_events", "a.csv", "#!/bin/sh\necho\n"), expect{415, "UNSUPPORTED_MEDIA_TYPE", "/file"}},
		"an .exe name":                {fields(m, "csv_events", "tool.exe", ok), expect{415, "UNSUPPORTED_MEDIA_TYPE", "/file"}},
		"an .xlsx name":               {fields(m, "csv_events", "book.xlsx", ok), expect{415, "UNSUPPORTED_MEDIA_TYPE", "/file"}},
		"a file over the limit":       {fields(m, "csv_events", "a.csv", "a\n"+strings.Repeat("1\n", 600)), expect{413, "PAYLOAD_TOO_LARGE", "/file"}},
		"an unknown kind":             {fields(m, "xlsx", "a.csv", ok), expect{422, "REQUEST_VALIDATION_FAILED", "/kind"}},
		"a draft for a version":       {swap(base, 2, part{name: "base_version_id", content: m.ID}), expect{422, "REQUEST_VALIDATION_FAILED", "/base_version_id"}},
		"a malformed project":         {swap(base, 0, part{name: "project_id", content: "prj_x"}), expect{422, "REQUEST_VALIDATION_FAILED", "/project_id"}},
		"a project that is not there": {swap(base, 0, part{name: "project_id", content: "prj_01M4YDQK00TA00000000000099"}), expect{404, "NOT_FOUND", ""}},
		"another project's model":     {swap(base, 0, part{name: "project_id", content: other.ProjectID}), expect{422, "REQUEST_VALIDATION_FAILED", "/model_id"}},
		"another model's version":     {swap(base, 2, part{name: "base_version_id", content: other.LatestVersion.ID}), expect{422, "REQUEST_VALIDATION_FAILED", "/base_version_id"}},
		"a field after the file":      {append(append([]part(nil), base[:4]...), base[4], part{name: "kind", content: "csv_events"}), expect{400, "INVALID_REQUEST", ""}},
		"the file before a field":     {[]part{base[0], base[1], base[2], base[4], base[3]}, expect{422, "REQUEST_VALIDATION_FAILED", "/kind"}},
		"no file":                     {base[:4], expect{422, "REQUEST_VALIDATION_FAILED", "/file"}},
		"an unknown part":             {append([]part{{name: "comment", content: "x"}}, base...), expect{422, "REQUEST_VALIDATION_FAILED", "/comment"}},
		"a repeated field":            {append([]part{base[0]}, base...), expect{400, "INVALID_REQUEST", ""}},
		"a field sent as a file":      {swap(base, 3, part{name: "kind", file: true, filename: "k.txt", content: "csv_events"}), expect{400, "INVALID_REQUEST", ""}},
		"a file without a name":       {swap(base, 4, part{name: "file", content: ok}), expect{422, "REQUEST_VALIDATION_FAILED", "/file"}},
		"a file name with a control":  {swap(base, 4, part{name: "file", file: true, filename: "a\tb.csv", content: ok}), expect{422, "REQUEST_VALIDATION_FAILED", "/file"}},
		"an encoded part":             {swap(base, 4, part{name: "file", file: true, filename: "a.csv", content: ok, header: map[string]string{"Content-Transfer-Encoding": "base64"}}), expect{400, "INVALID_REQUEST", ""}},
		"a field longer than an ID":   {swap(base, 1, part{name: "model_id", content: strings.Repeat("m", 200)}), expect{422, "REQUEST_VALIDATION_FAILED", "/model_id"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := e.upload(t, editor.Subject, e.a.ID, "key-refusal-"+strings.ReplaceAll(name, " ", "-"), c.parts)
			if r.status != c.want.status || code(t, r) != c.want.code {
				t.Fatalf("%d %s", r.status, r.body)
			}
			var body struct {
				Error struct {
					Details []struct {
						Path string `json:"path"`
						Code string `json:"code"`
					} `json:"details"`
				} `json:"error"`
			}
			_ = json.Unmarshal(r.body, &body)
			if c.want.path != "" && (len(body.Error.Details) == 0 || body.Error.Details[0].Path != c.want.path) {
				t.Errorf("details %s", r.body)
			}
			if c.want.status == 415 && body.Error.Details[0].Code != "IMPORT_FORMAT_REJECTED" {
				t.Errorf("details %s", r.body)
			}
		})
	}
	// None of them committed anything, and only a part sent after the file
	// left an object behind (stored before the part could be seen; nothing
	// names it).
	if n := e.count(`SELECT (SELECT count(*) FROM twin.imports WHERE organization_id=$1) + (SELECT count(*) FROM twin.jobs WHERE organization_id=$1 AND type='import_profile')
		+ (SELECT count(*) FROM twin.audit_events WHERE organization_id=$1 AND action='import.upload')`, e.a.ID); n != 0 {
		t.Errorf("refused uploads left %d rows", n)
	}
	if objs := e.objectsUnder(t, e.a.ID); len(objs) != 1 {
		t.Errorf("objects left: %v", objs)
	}
	// Transport: no key, another media type, a declared length over the
	// limit, and a body over the limit without any declared length.
	body, ct := multipartBody(t, base)
	for name, c := range map[string]struct {
		headers map[string]string
		length  int64
		chunked bool
		status  int
	}{
		"no Idempotency-Key":         {map[string]string{"Content-Type": ct}, 0, false, 400},
		"JSON":                       {map[string]string{"Content-Type": "application/json", "Idempotency-Key": "key-json-body"}, 0, false, 415},
		"a multipart without bounds": {map[string]string{"Content-Type": "multipart/form-data", "Idempotency-Key": "key-no-boundary"}, 0, false, 415},
		"a declared length too long": {map[string]string{"Content-Type": ct, "Idempotency-Key": "key-long-length"}, 1024 + 64<<10 + 1, false, 413},
	} {
		req, _ := http.NewRequest("POST", e.srv.URL+"/v1/imports", bytes.NewReader(body.Bytes()))
		req.Header.Set("Authorization", "Bearer "+e.token(editor.Subject))
		req.Header.Set("X-Organization-ID", e.a.ID)
		for k, v := range c.headers {
			req.Header.Set(k, v)
		}
		if c.length > 0 {
			req.ContentLength = c.length
			req.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body.Bytes()), strings.NewReader(strings.Repeat("x", int(c.length)-body.Len()))))
		}
		if r := e.send(req); r.status != c.status {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}
	// TEST-012: the limit holds without Content-Length: a chunked body
	// whose file is over the limit is refused and nothing is kept.
	big, ct2 := multipartBody(t, fields(m, "csv_events", "a.csv", "a\n"+strings.Repeat("9\n", 5000)))
	req, _ := http.NewRequest("POST", e.srv.URL+"/v1/imports", io.NopCloser(big))
	req.ContentLength = -1 // chunked
	req.Header.Set("Authorization", "Bearer "+e.token(editor.Subject))
	req.Header.Set("X-Organization-ID", e.a.ID)
	req.Header.Set("Content-Type", ct2)
	req.Header.Set("Idempotency-Key", "key-chunked-over")
	if r := e.send(req); r.status != 413 || code(t, r) != "PAYLOAD_TOO_LARGE" {
		t.Errorf("chunked over the limit: %d %s", r.status, r.body)
	}
	if objs := e.objectsUnder(t, e.a.ID); len(objs) != 1 {
		t.Errorf("a refused chunked upload kept an object: %v", objs)
	}
	if n := e.count(`SELECT count(*) FROM twin.imports WHERE organization_id=$1`, e.a.ID); n != 0 {
		t.Errorf("%d imports", n)
	}
	// Within the limit, chunked works.
	small, ct3 := multipartBody(t, fields(m, "csv_events", "a.csv", ok))
	req, _ = http.NewRequest("POST", e.srv.URL+"/v1/imports", io.NopCloser(small))
	req.ContentLength = -1
	req.Header.Set("Authorization", "Bearer "+e.token(editor.Subject))
	req.Header.Set("X-Organization-ID", e.a.ID)
	req.Header.Set("Content-Type", ct3)
	req.Header.Set("Idempotency-Key", "key-chunked-ok")
	if r := e.send(req); r.status != 202 {
		t.Errorf("chunked within the limit: %d %s", r.status, r.body)
	}
}

// D-011 for uploads: the same key and file replay the first response (a
// retry stores nothing new); another file under the key is 409; racing
// retries make one import; a member who lost the role gets no replay.
func TestUploadIdempotency(t *testing.T) {
	e := setupRuns(t)
	m := e.newModel(t)
	editor := e.a.Members[tenant.Editor]
	first := e.upload(t, editor.Subject, e.a.ID, "key-idem-1", fields(m, "csv_events", "a.csv", "a\n1\n"))
	if first.status != 202 {
		t.Fatalf("%d %s", first.status, first.body)
	}
	again := e.upload(t, editor.Subject, e.a.ID, "key-idem-1", fields(m, "csv_events", "a.csv", "a\n1\n"))
	if again.status != 202 || string(again.body) != string(first.body) {
		t.Errorf("replay: %d %s, first %s", again.status, again.body, first.body)
	}
	if objs := e.objectsUnder(t, e.a.ID); len(objs) != 1 {
		t.Errorf("a replay stored the file again: %v", objs)
	}
	for name, parts := range map[string][]part{
		"another file":      fields(m, "csv_events", "a.csv", "a\n2\n"),
		"another file name": fields(m, "csv_events", "b.csv", "a\n1\n"),
		"another kind":      fields(m, "json_events", "a.csv", "a\n1\n"),
	} {
		if r := e.upload(t, editor.Subject, e.a.ID, "key-idem-1", parts); r.status != 409 || code(t, r) != "IDEMPOTENCY_KEY_CONFLICT" {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}
	// The key is the caller's: another editor's same key is a new upload.
	owner := e.a.Members[tenant.Owner]
	if r := e.upload(t, owner.Subject, e.a.ID, "key-idem-1", fields(m, "csv_events", "a.csv", "a\n1\n")); r.status != 202 || string(r.body) == string(first.body) {
		t.Errorf("another caller: %d %s", r.status, r.body)
	}
	// Racing retries of one request: one import, the same answer.
	var wg sync.WaitGroup
	answers := make([]resp, 8)
	for i := range answers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			answers[i] = e.upload(t, editor.Subject, e.a.ID, "key-idem-race", fields(m, "csv_events", "race.csv", "a\n1\n"))
		}()
	}
	wg.Wait()
	for _, a := range answers {
		if a.status != 202 || string(a.body) != string(answers[0].body) {
			t.Errorf("race: %d %s, first %s", a.status, a.body, answers[0].body)
		}
	}
	if n := e.count(`SELECT count(*) FROM twin.imports WHERE organization_id=$1 AND filename='race.csv'`, e.a.ID); n != 1 {
		t.Errorf("%d imports for one request", n)
	}
	// Replay needs the role still: a demoted member gets 403, never the
	// stored answer (API_DATABASE §7).
	if _, err := e.admin.Exec(context.Background(), `UPDATE twin.memberships SET role='viewer' WHERE organization_id=$1 AND user_id=$2`, e.a.ID, editor.UserID); err != nil {
		t.Fatal(err)
	}
	if r := e.upload(t, editor.Subject, e.a.ID, "key-idem-1", fields(m, "csv_events", "a.csv", "a\n1\n")); r.status != 403 {
		t.Errorf("demoted replay: %d %s", r.status, r.body)
	}
}

// TEST-012 through the worker: a file that cannot be profiled is FAILED
// with a typed code and a JSON Pointer under the upload's request, has no
// profile, preview or report, and leaves the Twin as it was; a JSON model
// is profiled as a document.
func TestProfileFailures(t *testing.T) {
	e := setupRuns(t)
	m := e.newModel(t)
	before := e.twin(t, m.ID)
	editor := e.a.Members[tenant.Editor]
	cases := []struct {
		name, kind, file   string
		code, detail, path string
	}{
		{"a duplicate member", "json_events", `[{"a":1},{"b":1,"b":2}]`, "REQUEST_VALIDATION_FAILED", profile.CodeJSONDuplicateKey, "/1/b"},
		{"an infinite number", "json_events", `[{"duration_ms":1e400}]`, "REQUEST_VALIDATION_FAILED", profile.CodeJSONNumberNotFinite, "/0/duration_ms"},
		{"Big5 text", "csv_events", "名稱\n\xa4\xa4\xa4\xe5\n", "REQUEST_VALIDATION_FAILED", profile.CodeEncodingInvalid, ""},
		{"UTF-16", "csv_events", "\xff\xfea\x00\n\x00", "REQUEST_VALIDATION_FAILED", profile.CodeEncodingInvalid, ""},
		{"a bare quote", "csv_events", "a,b\n1,x\"y\n", "REQUEST_VALIDATION_FAILED", profile.CodeCSVSyntax, "/0"},
		{"a repeated column", "csv_events", "a,a\n1,2\n", "REQUEST_VALIDATION_FAILED", profile.CodeColumnInvalid, ""},
		{"too many rows", "csv_events", "a\n1\n2\n3\n4\n", "PAYLOAD_TOO_LARGE", profile.CodeLimitExceeded, "/3"},
		{"too deep", "json_events", `[{"m":[[[[1]]]]}]`, "PAYLOAD_TOO_LARGE", profile.CodeLimitExceeded, "/0/m/0/0/0"},
		{"not an array", "json_events", `{"case_id":"x"}`, "REQUEST_VALIDATION_FAILED", profile.CodeShapeInvalid, ""},
		{"a model array", "json_model", `[]`, "REQUEST_VALIDATION_FAILED", profile.CodeShapeInvalid, ""},
	}
	lim := profile.DefaultLimits
	lim.MaxRows, lim.MaxDepth = 3, 5
	ids := map[string]string{}
	requests := map[string]string{}
	for _, c := range cases {
		r := e.upload(t, editor.Subject, e.a.ID, "key-pf-"+strings.ReplaceAll(c.name, " ", "-"), fields(m, c.kind, "f.txt", c.file))
		if r.status != 202 {
			t.Fatalf("%s: %d %s", c.name, r.status, r.body)
		}
		ids[c.name], requests[c.name] = decode[accepted](t, r).ID, r.header.Get("X-Request-ID")
	}
	model := e.upload(t, editor.Subject, e.a.ID, "key-pf-model", fields(m, "json_model", "model.json", string(readContract(t, "valid-minimal-ir.json"))))
	if model.status != 202 {
		t.Fatalf("%d %s", model.status, model.body)
	}
	drain(t, e.profileLoop(t, profiler.Config{Objects: e.objects, Limits: lim}, nil), 20*time.Second)
	for _, c := range cases {
		got := e.getImport(t, editor.Subject, ids[c.name])
		if got.Status != "FAILED" || got.Error == nil || got.Profile != nil || len(got.Preview) != 0 {
			t.Errorf("%s: %+v", c.name, got)
			continue
		}
		er := got.Error.Error
		if er.Code != c.code || len(er.Details) != 1 || er.Details[0].Code != c.detail || er.Details[0].Path != c.path || er.RequestID != requests[c.name] {
			t.Errorf("%s: %+v", c.name, er)
		}
		if r := e.do("GET", "/v1/imports/"+ids[c.name]+"/errors", editor.Subject, e.a.ID, nil, ""); r.status != 404 {
			t.Errorf("%s: report %d", c.name, r.status)
		}
	}
	got := e.getImport(t, editor.Subject, decode[accepted](t, model).ID)
	if got.Status != "AWAITING_MAPPING" || got.Profile == nil || got.Profile.Format != profile.JSONModel || got.Profile.Rows != 0 ||
		len(got.Profile.Columns) != 0 || len(got.Preview) != 0 || got.TotalRows != 0 {
		t.Errorf("json model: %+v", got)
	}
	if after := e.twin(t, m.ID); after != before {
		t.Errorf("the Twin changed: %s, then %s", before, after)
	}
}

func readContract(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "contracts", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// The worker profiles only the bytes that were uploaded: an object that
// was changed or removed after the upload fails the import (fail closed),
// and a key outside the import's own prefix is never read.
func TestProfileIntegrity(t *testing.T) {
	e := setupRuns(t)
	m := e.newModel(t)
	editor := e.a.Members[tenant.Editor]
	up := func(key string) string {
		r := e.upload(t, editor.Subject, e.a.ID, key, fields(m, "csv_events", "a.csv", "a,b\n1,2\n"))
		if r.status != 202 {
			t.Fatalf("%d %s", r.status, r.body)
		}
		return decode[accepted](t, r).ID
	}
	changed, removed, shorter := up("key-int-1"), up("key-int-2"), up("key-int-3")
	file := func(id string) string { return filepath.Join(e.root, "tenant", e.a.ID, "imports", id, "upload") }
	if err := os.WriteFile(file(changed), []byte("a,b\n1,3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file(removed)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file(shorter), []byte("a,b\n1,"), 0o600); err != nil {
		t.Fatal(err)
	}
	drain(t, e.profileLoop(t, profiler.Config{Objects: e.objects}, nil), 10*time.Second)
	for id, want := range map[string]string{changed: "UPLOAD_CORRUPT", removed: "UPLOAD_MISSING", shorter: "UPLOAD_CORRUPT"} {
		got := e.getImport(t, editor.Subject, id)
		if got.Status != "FAILED" || got.Error.Error.Code != "INTERNAL_ERROR" || got.Error.Error.Details[0].Code != want || got.Profile != nil {
			t.Errorf("%s: %+v", want, got)
		}
	}
}

// A profile job that fails is retried; once its attempts are spent the
// import shows FAILED with the job's failure (the import's row keeps the
// state the job left it in, 0010). A retry after a crash profiles from
// PROFILING.
func TestProfileJobFailures(t *testing.T) {
	e := setupRuns(t)
	m := e.newModel(t)
	editor := e.a.Members[tenant.Editor]
	r := e.upload(t, editor.Subject, e.a.ID, "key-job-1", fields(m, "csv_events", "a.csv", "a,b\n1,2\n"))
	if r.status != 202 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	id := decode[accepted](t, r).ID
	// A worker without object storage: every attempt fails, retryable.
	drain(t, e.profileLoop(t, profiler.Config{}, nil), 10*time.Second)
	got := e.getImport(t, editor.Subject, id)
	if got.Status != "FAILED" || got.Error.Error.Code != "SERVICE_UNAVAILABLE" || got.Error.Error.Details[0].Code != "OBJECT_STORAGE_UNAVAILABLE" ||
		got.Error.Error.RequestID != r.header.Get("X-Request-ID") {
		t.Errorf("%+v", got)
	}
	var status string
	var attempts int
	if err := e.admin.QueryRow(context.Background(), `SELECT i.status, j.attempt FROM twin.imports i JOIN twin.jobs j ON j.import_id=i.id WHERE i.id=$1`, id).Scan(&status, &attempts); err != nil ||
		status != "PROFILING" || attempts != fastPolicy.MaxAttempts {
		t.Errorf("row %s after %d attempts: %v", status, attempts, err)
	}
	// A first attempt that marks the import and then dies; the retry
	// profiles it from PROFILING.
	r = e.upload(t, editor.Subject, e.a.ID, "key-job-2", fields(m, "csv_events", "a.csv", "a,b\n1,2\n"))
	if r.status != 202 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	id = decode[accepted](t, r).ID
	crashed := false
	w := e.profileLoop(t, profiler.Config{Objects: e.objects}, func(h handler.Handler) handler.Handler {
		return handler.Func(func(ctx context.Context, run handler.Run) error {
			if !crashed {
				crashed = true
				if err := run.Checkpoint(ctx, func(ctx context.Context, tx tenant.Tx) error {
					_, err := tx.Exec(ctx, `UPDATE twin.imports SET status='PROFILING' WHERE id=$1`, run.Job().Resource.ImportID)
					return err
				}); err != nil {
					return err
				}
				return &jobs.Failure{Code: "WORKER_STOPPED", Message: "the attempt was cut short", Retryable: true}
			}
			return h.Handle(ctx, run)
		})
	})
	drain(t, w, 10*time.Second)
	if got := e.getImport(t, editor.Subject, id); got.Status != "AWAITING_MAPPING" || got.Profile == nil || got.Profile.Rows != 1 {
		t.Errorf("after a retry: %+v", got)
	}
}

// TEST-029: an error report is served only to members of its import's
// organization, only from the import's own key, and only as recorded; the
// client names the import, never a key.
func TestErrorReportAuthorization(t *testing.T) {
	e := setupRuns(t)
	m := e.newModel(t)
	editor := e.a.Members[tenant.Editor]
	up := func(key, file string) string {
		r := e.upload(t, editor.Subject, e.a.ID, key, fields(m, "csv_events", "a.csv", file))
		if r.status != 202 {
			t.Fatalf("%d %s", r.status, r.body)
		}
		return decode[accepted](t, r).ID
	}
	one, two := up("key-auth-1", "a,b\n1\n"), up("key-auth-2", "a,b\n1,2,3\n")
	drain(t, e.profileLoop(t, profiler.Config{Objects: e.objects}, nil), 10*time.Second)
	if r := e.do("GET", "/v1/imports/"+one+"/errors", editor.Subject, e.a.ID, nil, ""); r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	// A row that names another import's report (same tenant, so the
	// database's prefix check allows it) is not served: the report must be
	// under the import's own key.
	var twoKey, twoHash string
	var twoSize int64
	if err := e.admin.QueryRow(context.Background(), `SELECT error_report_object_key, error_report_content_hash, error_report_size_bytes FROM twin.imports WHERE id=$1`, two).
		Scan(&twoKey, &twoHash, &twoSize); err != nil {
		t.Fatal(err)
	}
	e.ownerTx(t, e.a.ID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE twin.imports SET error_report_object_key=$2, error_report_content_hash=$3, error_report_size_bytes=$4 WHERE id=$1`,
			one, twoKey, twoHash, twoSize)
		return err
	})
	if r := e.do("GET", "/v1/imports/"+one+"/errors", editor.Subject, e.a.ID, nil, ""); r.status != 500 || bytes.Contains(r.body, []byte("CSV_FIELD")) {
		t.Errorf("another import's report: %d %s", r.status, r.body)
	}
	// Another tenant's key is refused by the database itself (D-035).
	err := ownerTxErr(e, e.a.ID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE twin.imports SET error_report_object_key=$2 WHERE id=$1`, two, "tenant/"+e.b.ID+"/imports/"+two+"/x.csv")
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "23514") {
		t.Errorf("another tenant's key: %v", err)
	}
	// The route takes no key and no URL.
	for _, q := range []string{"?key=tenant/" + e.b.ID + "/imports/x", "?url=http://x.invalid/a"} {
		if r := e.do("GET", "/v1/imports/"+two+"/errors"+q, editor.Subject, e.a.ID, nil, ""); r.status != 200 || !bytes.Contains(r.body, []byte("CSV_FIELD_COUNT")) {
			t.Errorf("%s: %d", q, r.status)
		}
	}
}

// ownerTxErr is ownerTx returning fn's error instead of failing the test.
func ownerTxErr(e *runEnv, org string, fn func(ctx context.Context, tx pgx.Tx) error) error {
	return postgres.OpenPool(e.admin).Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL ROLE twin_owner`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('app.organization_id',$1,true)`, org); err != nil {
			return err
		}
		return fn(ctx, tx)
	})
}

// slowReader sends data in chunks with a pause before each.
type slowReader struct {
	data  []byte
	chunk int
	pause time.Duration
}

func (s *slowReader) Read(p []byte) (int, error) {
	if len(s.data) == 0 {
		return 0, io.EOF
	}
	time.Sleep(s.pause)
	n := copy(p[:min(len(p), s.chunk)], s.data)
	s.data = s.data[n:]
	return n, nil
}

// An upload may take longer than the server's read and write timeouts
// (cmd/api: 30 s and 60 s; here 300 ms): its route sets its own deadline
// (5 min by default; here 3 s), and an upload slower than that is cut off
// with nothing committed.
func TestASlowUploadHasItsOwnDeadline(t *testing.T) {
	e := setupRuns(t, api.WithUploadTimeout(3*time.Second))
	srv := httptest.NewUnstartedServer(e.srv.Config.Handler)
	srv.Config.ReadHeaderTimeout = 300 * time.Millisecond
	srv.Config.ReadTimeout = 300 * time.Millisecond
	srv.Config.WriteTimeout = 300 * time.Millisecond
	srv.Start()
	defer srv.Close()
	m := e.newModel(t)
	editor := e.a.Members[tenant.Editor]
	send := func(key string, pause time.Duration) (int, error) {
		body, ct := multipartBody(t, fields(m, "csv_events", key+".csv", "a,b\n"+strings.Repeat("1,2\n", 40)))
		req, err := http.NewRequest("POST", srv.URL+"/v1/imports", &slowReader{data: body.Bytes(), chunk: 64, pause: pause})
		if err != nil {
			t.Fatal(err)
		}
		req.ContentLength = int64(body.Len())
		req.Header.Set("Authorization", "Bearer "+e.token(editor.Subject))
		req.Header.Set("X-Organization-ID", e.a.ID)
		req.Header.Set("Content-Type", ct)
		req.Header.Set("Idempotency-Key", key)
		res, err := srv.Client().Do(req)
		if err != nil {
			return 0, err
		}
		defer res.Body.Close()
		_, _ = io.Copy(io.Discard, res.Body)
		return res.StatusCode, nil
	}
	start := time.Now()
	if status, err := send("key-slow-ok", 100*time.Millisecond); err != nil || status != 202 {
		t.Errorf("an upload of %v: %d %v", time.Since(start), status, err)
	}
	if took := time.Since(start); took < time.Second {
		t.Errorf("the slow upload took only %v", took)
	}
	if status, err := send("key-slow-cut", 400*time.Millisecond); err == nil && status == 202 {
		t.Error("an upload slower than its deadline was accepted")
	}
	if n := e.count(`SELECT count(*) FROM twin.imports WHERE organization_id=$1 AND filename='key-slow-cut.csv'`, e.a.ID); n != 0 {
		t.Errorf("%d imports of the cut upload", n)
	}
}
