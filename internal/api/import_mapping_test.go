package api_test

// Integration tests of saveImportMapping and the import_validate job
// (T-017) through the whole stack: the handler cmd/api serves as the plain
// API login, and the job loop cmd/worker runs as the plain worker login
// with the import profile and validation handlers, on the disposable
// database and a filesystem object store (TEST-012, TEST-013; APPROVED
// D-009, D-011, D-043 (c); D-070, D-071, D-072 PROPOSED). Every file here
// is synthetic.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/import/mapping"
	"github.com/kcl1104/geminus/internal/import/profiler"
	"github.com/kcl1104/geminus/internal/import/report"
	importupload "github.com/kcl1104/geminus/internal/import/upload"
	"github.com/kcl1104/geminus/internal/import/validator"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/worker"
	"github.com/kcl1104/geminus/internal/worker/handler"
)

// validateLoop is a job loop with the import validation handler (and wrap,
// if given, around it).
func (e *runEnv) validateLoop(t *testing.T, cfg validator.Config, wrap func(handler.Handler) handler.Handler) *worker.Worker {
	t.Helper()
	if cfg.Actor == "" {
		cfg.Actor = "test-worker"
	}
	cfg.Log = slog.New(slog.NewJSONHandler(lockedWriter{e.logs, e.logsMu}, nil))
	v, err := validator.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var h handler.Handler = v
	if wrap != nil {
		h = wrap(v)
	}
	w, err := worker.New(e.scope, worker.Config{ID: cfg.Actor, Policy: fastPolicy, Heartbeat: time.Second, Idle: 20 * time.Millisecond,
		ReapLimit: 10, Handlers: map[jobs.Type]handler.Handler{jobs.ImportValidate: h}, Log: cfg.Log})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// col is one entry of a mapping: a source column, its target field (""
// to leave the column out) and its transform ("" for identity).
type col struct{ source, target, transform string }

// mappingBody is a SaveMapping body for the revision the person saw.
func mappingBody(t *testing.T, rev int64, confirmed []string, cols ...col) string {
	t.Helper()
	entries := make([]map[string]any, len(cols))
	for i, c := range cols {
		var target any
		if c.target != "" {
			target = c.target
		}
		tr := c.transform
		if tr == "" {
			tr = "identity"
		}
		entries[i] = map[string]any{"source_column": c.source, "target_field": target, "transform": tr}
	}
	body := map[string]any{"expected_mapping_revision": rev, "mapping": map[string]any{"mappings": entries}, "confirmed": true}
	if confirmed != nil {
		body["confirmed_low_confidence"] = confirmed
	}
	data, err := json.Marshal(body, json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// saveMapping posts body as subject in org a under key ("" for none).
func (e *runEnv) saveMapping(subject, id, key, body string) resp {
	h := map[string]string{}
	if key != "" {
		h["Idempotency-Key"] = key
	}
	return e.do("POST", "/v1/imports/"+id+"/mapping", subject, e.a.ID, h, body)
}

type issue struct {
	Code     string `json:"code"`
	Path     string `json:"path"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
}

// mappedImport is the part of an import (import.schema.json) a mapping
// and its validation write.
type mappedImport struct {
	ID              string `json:"id"`
	Status          string `json:"status"`
	MappingRevision int64  `json:"mapping_revision"`
	TotalRows       int    `json:"total_rows"`
	InvalidRows     int    `json:"invalid_rows"`
	Mapping         *struct {
		Mappings []map[string]any `json:"mappings"`
	} `json:"mapping"`
	Validation *struct {
		Valid  bool    `json:"valid"`
		Issues []issue `json:"issues"`
	} `json:"validation"`
	CanonicalPreview []map[string]any `json:"canonical_preview"`
	DatasetVersionID *string          `json:"dataset_version_id"`
	Error            *struct {
		Error struct {
			Code      string  `json:"code"`
			Details   []issue `json:"details"`
			RequestID string  `json:"request_id"`
		} `json:"error"`
	} `json:"error"`
}

func (e *runEnv) getMapped(t *testing.T, subject, id string) mappedImport {
	t.Helper()
	r := e.do("GET", "/v1/imports/"+id, subject, e.a.ID, nil, "")
	if r.status != 200 {
		t.Fatalf("get %s: %d %s", id, r.status, r.body)
	}
	valid(t, r, "import.schema.json")
	return decode[mappedImport](t, r)
}

// firstDetail is the path and code of an error response's first detail.
func firstDetail(t *testing.T, r resp) (string, string) {
	t.Helper()
	var b struct {
		Error struct {
			Details []issue `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(r.body, &b); err != nil || len(b.Error.Details) == 0 {
		t.Fatalf("no details: %s", r.body)
	}
	return b.Error.Details[0].Path, b.Error.Details[0].Code
}

// uploaded uploads file as kind for model m as the editor of org a.
func (e *runEnv) uploaded(t *testing.T, m modelSummary, key, kind, filename, file string) string {
	t.Helper()
	r := e.upload(t, e.a.Members[tenant.Editor].Subject, e.a.ID, key, fields(m, kind, filename, file))
	if r.status != 202 {
		t.Fatalf("upload: %d %s", r.status, r.body)
	}
	return decode[accepted](t, r).ID
}

// profiled is uploaded, then profiled: the import awaits its mapping.
func (e *runEnv) profiled(t *testing.T, m modelSummary, key, kind, filename, file string) string {
	t.Helper()
	id := e.uploaded(t, m, key, kind, filename, file)
	drain(t, e.profileLoop(t, profiler.Config{Objects: e.objects}, nil), 10*time.Second)
	if got := e.getMapped(t, e.a.Members[tenant.Editor].Subject, id); got.Status != "AWAITING_MAPPING" || got.MappingRevision != 0 || got.Mapping != nil {
		t.Fatalf("after profiling: %+v", got)
	}
	return id
}

// A synthetic station log (ELECTRONICS_LINE §6) in UTF-8 with a BOM and
// CRLF, Chinese column names and values: one start column in local time
// whose second value is not RFC 3339, the same starts in UTC, a column a
// mapping leaves out (作業員), a lot number for metadata, and a serial
// that looks like a spreadsheet formula.
const stationCSV = "\ufeff序號,紀錄類型,站別,開始,開始UTC,結束,結果,作業員,批號\r\n" +
	"SN-000123,operation,fct,2026-10-13T06:12:00+08:00,2026-10-12T22:12:00Z,2026-10-12T22:15:10Z,pass,op-7,L-7\r\n" +
	"SN-000124,operation,fct,2026/10/13 06:16,2026-10-12T22:16:00Z,2026-10-12T22:19:02Z,fail,op-7,\r\n" +
	"\"=HYPERLINK(\"\"http://x.invalid\"\")\",operation,ict,2026-10-13T06:20:00+08:00,2026-10-12T22:20:00Z,2026-10-12T22:20:30Z,pass,op-8,\"L-7, 第二批\"\r\n"

// station maps stationCSV with start (開始 or 開始UTC) as the timestamp
// and the other start column, and 作業員, left out.
func station(start string) []col {
	other := map[string]string{"開始": "開始UTC", "開始UTC": "開始"}[start]
	return []col{{"序號", "case_id", ""}, {"紀錄類型", "event_type", ""}, {"站別", "stage", ""}, {start, "timestamp", "rfc3339"},
		{other, "", ""}, {"結束", "end_timestamp", "rfc3339"}, {"結果", "outcome", ""}, {"作業員", "", ""}, {"批號", "metadata", ""}}
}

// The closed loop of T-017 (TEST-012, TEST-013): an editor maps a
// profiled import; the mapping, its validation job and its audit event
// commit together and a retry replays; the worker validates every row (an
// invalid one is reported with its pointer, the report never echoing a
// cell); the editor maps again from the revision they saw, and a save from
// the older one is 409; every row maps, so the import is VALIDATED with
// the canonical dataset stored under its own key, the columns left out
// absent from it, the stale report gone, and the Twin as it was (D-009).
func TestImportMappingClosedLoop(t *testing.T) {
	e := setupRuns(t)
	m := e.newModel(t)
	ctx := context.Background()
	before := e.twin(t, m.ID)
	editor, viewer := e.a.Members[tenant.Editor], e.a.Members[tenant.Viewer]
	id := e.profiled(t, m, "key-map-loop", "csv_events", "站別紀錄.csv", stationCSV)

	first := mappingBody(t, 0, nil, station("開始")...)
	r := e.saveMapping(editor.Subject, id, "key-map-first", first)
	if r.status != 202 {
		t.Fatalf("save: %d %s", r.status, r.body)
	}
	valid(t, r, "import.schema.json")
	got := decode[mappedImport](t, r)
	if got.Status != "VALIDATING" || got.MappingRevision != 1 || got.Validation != nil || len(got.CanonicalPreview) != 0 || got.InvalidRows != 0 ||
		got.Mapping == nil || len(got.Mapping.Mappings) != 9 || got.Mapping.Mappings[3]["source_column"] != "開始" {
		t.Fatalf("saved: %s", r.body)
	}
	firstRequest := r.header.Get("X-Request-ID")
	// The job and the audit event committed with the mapping; the audit
	// has counts, never a column name or value (SECURITY §9).
	var payload []byte
	if err := e.admin.QueryRow(ctx, `SELECT payload FROM twin.jobs WHERE import_id=$1 AND type='import_validate' AND status='queued'`, id).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var p struct {
		ImportID        string `json:"import_id"`
		MappingRevision int64  `json:"mapping_revision"`
		RequestID       string `json:"request_id"`
	}
	if err := json.Unmarshal(payload, &p, json.RejectUnknownMembers(true)); err != nil || p.ImportID != id || p.MappingRevision != 1 || p.RequestID != firstRequest {
		t.Errorf("payload %s: %v", payload, err)
	}
	var meta []byte
	if err := e.admin.QueryRow(ctx, `SELECT metadata FROM twin.audit_events WHERE target_id=$1 AND action='import.map' AND actor_type='user' AND actor_id=$2 AND request_id=$3`,
		id, editor.UserID, firstRequest).Scan(&meta); err != nil {
		t.Fatal(err)
	}
	var counts map[string]int
	if err := json.Unmarshal(meta, &counts); err != nil || fmt.Sprint(counts) != fmt.Sprint(map[string]int{"mapping_revision": 1, "columns_imported": 7,
		"columns_left_out": 2, "metadata_columns": 1, "confirmed_low_confidence": 0, "validations_cancelled": 0}) {
		t.Errorf("audit metadata %s: %v", meta, err)
	}
	// A retry replays the answer; another body under the key is 409; a
	// viewer may not map.
	if again := e.saveMapping(editor.Subject, id, "key-map-first", first); again.status != 202 || string(again.body) != string(r.body) {
		t.Errorf("replay: %d %s", again.status, again.body)
	}
	if r := e.saveMapping(editor.Subject, id, "key-map-first", mappingBody(t, 0, nil, station("開始UTC")...)); r.status != 409 || code(t, r) != "IDEMPOTENCY_KEY_CONFLICT" {
		t.Errorf("another body: %d %s", r.status, r.body)
	}
	if r := e.saveMapping(viewer.Subject, id, "key-map-viewer", mappingBody(t, 1, nil, station("開始UTC")...)); r.status != 403 || code(t, r) != "FORBIDDEN" {
		t.Errorf("viewer: %d %s", r.status, r.body)
	}
	if n := e.count(`SELECT count(*) FROM twin.jobs WHERE import_id=$1 AND type='import_validate'`, id); n != 1 {
		t.Errorf("%d validation jobs", n)
	}

	drain(t, e.validateLoop(t, validator.Config{Objects: e.objects}, nil), 10*time.Second)
	got = e.getMapped(t, viewer.Subject, id)
	if got.Status != "AWAITING_MAPPING" || got.MappingRevision != 1 || got.TotalRows != 3 || got.InvalidRows != 1 || got.Validation == nil ||
		got.Validation.Valid || len(got.Validation.Issues) != 1 || got.Error != nil {
		t.Fatalf("after the first validation: %+v", got)
	}
	if is := got.Validation.Issues[0]; is.Code != mapping.CodeValueInvalid || is.Path != "/1/開始" || is.Severity != "error" || strings.Contains(is.Message, "2026/10/13") {
		t.Errorf("issue %+v", is)
	}
	if len(got.CanonicalPreview) != 3 || got.CanonicalPreview[1] != nil || got.CanonicalPreview[0]["case_id"] != "SN-000123" ||
		got.CanonicalPreview[0]["timestamp"] != "2026-10-13T06:12:00+08:00" || got.CanonicalPreview[2]["case_id"] != `=HYPERLINK("http://x.invalid")` {
		t.Errorf("canonical preview %v", got.CanonicalPreview)
	}
	// The error report names the row, line, column and code; it never
	// echoes the cell (TEST-029), and every cell is safe in a spreadsheet.
	rep := e.do("GET", "/v1/imports/"+id+"/errors", viewer.Subject, e.a.ID, nil, "")
	if rep.status != 200 || rep.header.Get("Content-Type") != "text/csv; charset=utf-8" {
		t.Fatalf("report: %d %s", rep.status, rep.body)
	}
	rows, err := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(rep.body, []byte("\xef\xbb\xbf")))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{report.Header,
		{"/1/開始", "3", "開始", mapping.CodeValueInvalid, "timestamp: the value is not an RFC 3339 date-time with an offset", ""}}
	if fmt.Sprint(rows) != fmt.Sprint(want) || bytes.Contains(rep.body, []byte("2026/10/13")) || bytes.Contains(rep.body, []byte("SN-000124")) {
		t.Errorf("report rows %q, want %q", rows, want)
	}
	for _, row := range rows {
		for _, cell := range row {
			if report.Safe(cell) != cell {
				t.Errorf("unsafe cell %q", cell)
			}
		}
	}
	if n := e.count(`SELECT count(*) FROM twin.imports WHERE id=$1 AND canonical_object_key IS NULL`, id); n != 1 {
		t.Errorf("an import with invalid rows has a canonical dataset")
	}

	// Mapped again from the revision the person saw.
	r = e.saveMapping(editor.Subject, id, "key-map-second", mappingBody(t, 1, nil, station("開始UTC")...))
	if r.status != 202 || decode[mappedImport](t, r).MappingRevision != 2 || decode[mappedImport](t, r).Validation != nil {
		t.Fatalf("second save: %d %s", r.status, r.body)
	}
	secondRequest := r.header.Get("X-Request-ID")
	if rep := e.do("GET", "/v1/imports/"+id+"/errors", viewer.Subject, e.a.ID, nil, ""); rep.status != 404 {
		t.Errorf("the earlier revision's report: %d", rep.status)
	}
	// A save from the older revision is 409, never an overwrite or a merge.
	stale := e.saveMapping(editor.Subject, id, "key-map-stale", mappingBody(t, 1, nil, station("開始")...))
	if stale.status != 409 || code(t, stale) != "LIFECYCLE_CONFLICT" {
		t.Fatalf("stale save: %d %s", stale.status, stale.body)
	}
	if path, c := firstDetail(t, stale); path != "/expected_mapping_revision" || c != importupload.CodeRevisionConflict {
		t.Errorf("stale save detail %s %s", path, c)
	}

	drain(t, e.validateLoop(t, validator.Config{Objects: e.objects}, nil), 10*time.Second)
	got = e.getMapped(t, viewer.Subject, id)
	if got.Status != "VALIDATED" || got.MappingRevision != 2 || got.InvalidRows != 0 || got.Validation == nil || !got.Validation.Valid ||
		len(got.Validation.Issues) != 0 || got.DatasetVersionID != nil || got.Mapping.Mappings[3]["source_column"] != "開始UTC" {
		t.Fatalf("after the second validation: %+v", got)
	}
	if rep := e.do("GET", "/v1/imports/"+id+"/errors", viewer.Subject, e.a.ID, nil, ""); rep.status != 404 {
		t.Errorf("a validated import's report: %d", rep.status)
	}
	// The canonical dataset: under the import's own key, the bytes its row
	// names, one schema-valid event per row in row order.
	var key, hash string
	var size int64
	if err := e.admin.QueryRow(ctx, `SELECT canonical_object_key, canonical_content_hash, canonical_size_bytes FROM twin.imports WHERE id=$1`, id).
		Scan(&key, &hash, &size); err != nil {
		t.Fatal(err)
	}
	if key != "tenant/"+e.a.ID+"/imports/"+id+"/canonical-r2-a1.json" {
		t.Errorf("canonical key %s", key)
	}
	data, err := os.ReadFile(filepath.Join(e.root, filepath.FromSlash(key)))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if hash != "sha256:"+hex.EncodeToString(sum[:]) || size != int64(len(data)) {
		t.Errorf("the dataset is not the bytes its row names: %s %d", hash, size)
	}
	var events []map[string]any
	if err := json.Unmarshal(data, &events); err != nil || len(events) != 3 {
		t.Fatalf("dataset %s: %v", data, err)
	}
	for i, ev := range events {
		doc, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		if issues, err := validate.Document(doc, "canonical-event.schema.json", "EVENT"); err != nil || len(issues) > 0 {
			t.Errorf("event %d %s: %v %v", i, doc, issues, err)
		}
	}
	want0 := map[string]any{"case_id": "SN-000123", "event_type": "operation", "timestamp": "2026-10-12T22:12:00Z",
		"end_timestamp": "2026-10-12T22:15:10Z", "stage": "fct", "outcome": "pass", "metadata": map[string]any{"批號": "L-7"}}
	if fmt.Sprint(events[0]) != fmt.Sprint(want0) {
		t.Errorf("event 0 %v, want %v", events[0], want0)
	}
	if _, ok := events[1]["metadata"]; ok || events[1]["outcome"] != "fail" || events[1]["timestamp"] != "2026-10-12T22:16:00Z" {
		t.Errorf("event 1 %v", events[1])
	}
	if fmt.Sprint(events[2]["metadata"]) != fmt.Sprint(map[string]any{"批號": "L-7, 第二批"}) {
		t.Errorf("event 2 %v", events[2])
	}
	if bytes.Contains(data, []byte("op-7")) || bytes.Contains(data, []byte("2026-10-13T06")) {
		t.Errorf("a column left out is in the dataset: %s", data)
	}
	if fmt.Sprint(got.CanonicalPreview) != fmt.Sprint(events) {
		t.Errorf("canonical preview %v, dataset %v", got.CanonicalPreview, events)
	}
	// One audit event per validation, by the worker, under the save's
	// request; counts and codes only.
	audit, err := e.admin.Query(ctx, `SELECT request_id, metadata->>'mapping_revision', metadata->>'outcome', metadata::text FROM twin.audit_events
		WHERE target_id=$1 AND action='import.validate' AND actor_type='worker' AND actor_id='test-worker' ORDER BY (metadata->>'mapping_revision')::int`, id)
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	for audit.Next() {
		var req, rev, outcome, text string
		if err := audit.Scan(&req, &rev, &outcome, &text); err != nil {
			t.Fatal(err)
		}
		seen = append(seen, req+":"+rev+":"+outcome)
		for _, leak := range []string{"SN-", "開始", "批號", "站別紀錄", "op-7"} {
			if strings.Contains(text, leak) {
				t.Errorf("audit metadata %s names %q", text, leak)
			}
		}
	}
	audit.Close()
	if want := []string{firstRequest + ":1:AWAITING_MAPPING", secondRequest + ":2:VALIDATED"}; !slices.Equal(seen, want) {
		t.Errorf("validation audit %v, want %v", seen, want)
	}
	// The objects: the upload, the first revision's report and the second
	// revision's dataset, each under the import's own key.
	objs := e.objectsUnder(t, e.a.ID)
	slices.Sort(objs)
	prefix := "tenant/" + e.a.ID + "/imports/" + id + "/"
	if want := []string{prefix + "canonical-r2-a1.json", prefix + "upload", prefix + "validation-errors-r1-a1.csv"}; !slices.Equal(objs, want) {
		t.Errorf("objects %v, want %v", objs, want)
	}
	if after := e.twin(t, m.ID); after != before {
		t.Errorf("the Twin changed: %s, then %s", before, after)
	}
}

const simpleCSV = "case,type,at\nC-1,step,2026-10-13T06:12:00Z\n"

var simple = []col{{"case", "case_id", ""}, {"type", "event_type", ""}, {"at", "timestamp", "rfc3339"}}

// The mapping revision is a compare-and-set (D-011, API_DATABASE §7):
// racing saves of one revision make one next revision and 409 for the
// rest, none of which leaves a job or an audit event; racing retries of
// one request make one revision with one answer; a revision ahead of the
// import's is 409; a member who lost the role gets no replay.
func TestMappingRevisionConflicts(t *testing.T) {
	e := setupRuns(t)
	m := e.newModel(t)
	editor := e.a.Members[tenant.Editor]
	id := e.profiled(t, m, "key-cas-upload", "csv_events", "a.csv", simpleCSV)
	body := mappingBody(t, 0, nil, simple...)
	answers := make([]resp, 8)
	var wg sync.WaitGroup
	for i := range answers {
		wg.Go(func() { answers[i] = e.saveMapping(editor.Subject, id, fmt.Sprintf("key-cas-%02d", i), body) })
	}
	wg.Wait()
	won := 0
	for _, a := range answers {
		switch {
		case a.status == 202:
			won++
		case a.status == 409 && code(t, a) == "LIFECYCLE_CONFLICT":
			if path, c := firstDetail(t, a); path != "/expected_mapping_revision" || c != importupload.CodeRevisionConflict {
				t.Errorf("detail %s %s", path, c)
			}
		default:
			t.Errorf("racing save: %d %s", a.status, a.body)
		}
	}
	if won != 1 || e.count(`SELECT mapping_revision FROM twin.imports WHERE id=$1`, id) != 1 ||
		e.count(`SELECT count(*) FROM twin.jobs WHERE import_id=$1 AND type='import_validate'`, id) != 1 ||
		e.count(`SELECT count(*) FROM twin.audit_events WHERE target_id=$1 AND action='import.map'`, id) != 1 {
		t.Fatalf("%d saves won", won)
	}
	// Racing retries of one request: one revision, one answer.
	retry := mappingBody(t, 1, nil, simple...)
	for i := range answers {
		wg.Go(func() { answers[i] = e.saveMapping(editor.Subject, id, "key-cas-retry", retry) })
	}
	wg.Wait()
	for _, a := range answers {
		if a.status != 202 || string(a.body) != string(answers[0].body) {
			t.Errorf("retry: %d %s, first %s", a.status, a.body, answers[0].body)
		}
	}
	if e.count(`SELECT mapping_revision FROM twin.imports WHERE id=$1`, id) != 2 ||
		e.count(`SELECT count(*) FROM twin.jobs WHERE import_id=$1 AND type='import_validate'`, id) != 2 {
		t.Errorf("racing retries made more than one revision")
	}
	// A revision the import never had is as stale as an old one.
	if r := e.saveMapping(editor.Subject, id, "key-cas-ahead", mappingBody(t, 7, nil, simple...)); r.status != 409 || code(t, r) != "LIFECYCLE_CONFLICT" {
		t.Errorf("a revision ahead: %d %s", r.status, r.body)
	}
	if e.count(`SELECT count(*) FROM twin.audit_events WHERE target_id=$1 AND action='import.map'`, id) != 2 {
		t.Errorf("a refused save left an audit event")
	}
	// Replay needs the role still (API_DATABASE §7).
	if _, err := e.admin.Exec(context.Background(), `UPDATE twin.memberships SET role='viewer' WHERE organization_id=$1 AND user_id=$2`, e.a.ID, editor.UserID); err != nil {
		t.Fatal(err)
	}
	if r := e.saveMapping(editor.Subject, id, "key-cas-retry", retry); r.status != 403 {
		t.Errorf("demoted replay: %d %s", r.status, r.body)
	}
}

// beforeFinish is a run that calls hook before it finishes.
type beforeFinish struct {
	handler.Run
	hook func()
}

func (b *beforeFinish) Finish(ctx context.Context, end jobs.Status, fn func(ctx context.Context, tx tenant.Tx) error) error {
	b.hook()
	return b.Run.Finish(ctx, end, fn)
}

// beforeCheckpoint is a run that calls hook before its n-th checkpoint.
type beforeCheckpoint struct {
	handler.Run
	n, seen int
	hook    func()
}

func (b *beforeCheckpoint) Checkpoint(ctx context.Context, fn func(ctx context.Context, tx tenant.Tx) error) error {
	if b.seen++; b.seen == b.n {
		b.hook()
	}
	return b.Run.Checkpoint(ctx, fn)
}

// The stale-job fence (migration 0014): a save cancels the queued
// validation of an older revision, which never runs and which the import
// does not follow; a running one completes without writing, whether the
// newer mapping was saved before it read the import, after it validated
// every row and before the second read (which it then does not do: no
// dataset, review round 1 L5), or after it wrote the dataset (an orphan no
// row names, D-044); only the current revision's outcome is recorded.
func TestStaleValidationJobs(t *testing.T) {
	e := setupRuns(t)
	m := e.newModel(t)
	editor := e.a.Members[tenant.Editor]
	id := e.profiled(t, m, "key-fence-upload", "csv_events", "a.csv", stationCSV)
	save := func(key string, rev int64, start string) {
		if r := e.saveMapping(editor.Subject, id, key, mappingBody(t, rev, nil, station(start)...)); r.status != 202 {
			t.Errorf("save %s: %d %s", key, r.status, r.body)
		}
	}
	jobsByRevision := func() string {
		var s string
		if err := e.admin.QueryRow(context.Background(), `SELECT string_agg((payload->>'mapping_revision') || ':' || status, ',' ORDER BY (payload->>'mapping_revision')::int)
			FROM twin.jobs WHERE import_id=$1 AND type='import_validate'`, id).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	save("key-fence-1", 0, "開始")
	save("key-fence-2", 1, "開始UTC")
	if s := jobsByRevision(); s != "1:cancelled,2:queued" {
		t.Errorf("jobs %s", s)
	}
	if n := e.count(`SELECT (metadata->>'validations_cancelled')::int FROM twin.audit_events WHERE target_id=$1 AND action='import.map'
		AND (metadata->>'mapping_revision')::int = 2`, id); n != 1 {
		t.Errorf("the save recorded %d cancelled validations", n)
	}
	if got := e.getMapped(t, editor.Subject, id); got.Status != "VALIDATING" || got.MappingRevision != 2 || got.Error != nil {
		t.Errorf("after an older revision's job was cancelled: %+v", got)
	}
	drain(t, e.validateLoop(t, validator.Config{Objects: e.objects}, nil), 10*time.Second)
	if got := e.getMapped(t, editor.Subject, id); got.Status != "VALIDATED" || got.MappingRevision != 2 {
		t.Fatalf("revision 2: %+v", got)
	}

	save("key-fence-3", 2, "開始UTC")
	calls := 0
	w := e.validateLoop(t, validator.Config{Objects: e.objects}, func(h handler.Handler) handler.Handler {
		return handler.Func(func(ctx context.Context, run handler.Run) error {
			calls++
			switch calls {
			case 1: // revision 3, saved over before it reads the import
				save("key-fence-4", 3, "開始UTC")
			case 2: // revision 4, saved over after it validated every row, before the second read
				run = &beforeCheckpoint{Run: run, n: 2, hook: func() { save("key-fence-5", 4, "開始UTC") }}
			case 3: // revision 5, saved over after it wrote the dataset
				run = &beforeFinish{Run: run, hook: func() { save("key-fence-6", 5, "開始") }}
			}
			return h.Handle(ctx, run)
		})
	})
	drain(t, w, 20*time.Second)
	if calls != 4 {
		t.Errorf("%d attempts", calls)
	}
	if s := jobsByRevision(); s != "1:cancelled,2:completed,3:completed,4:completed,5:completed,6:completed" {
		t.Errorf("jobs %s", s)
	}
	// Revisions 4 and 5 would have been VALIDATED; revision 6 is what the
	// import records.
	got := e.getMapped(t, editor.Subject, id)
	if got.Status != "AWAITING_MAPPING" || got.MappingRevision != 6 || got.InvalidRows != 1 || got.Validation.Issues[0].Path != "/1/開始" {
		t.Errorf("after the fence: %+v", got)
	}
	var outcomes string
	if err := e.admin.QueryRow(context.Background(), `SELECT string_agg((metadata->>'mapping_revision') || ':' || (metadata->>'outcome'), ','
		  ORDER BY (metadata->>'mapping_revision')::int) FROM twin.audit_events WHERE target_id=$1 AND action='import.validate'`, id).Scan(&outcomes); err != nil {
		t.Fatal(err)
	}
	if outcomes != "2:VALIDATED,3:stale,4:stale,5:stale,6:AWAITING_MAPPING" {
		t.Errorf("outcomes %s", outcomes)
	}
	prefix := "tenant/" + e.a.ID + "/imports/" + id + "/"
	if n := e.count(`SELECT count(*) FROM twin.imports WHERE id=$1 AND canonical_object_key IS NULL AND error_report_object_key=$2`, id,
		prefix+"validation-errors-r6-a1.csv"); n != 1 {
		t.Errorf("the import names another revision's objects")
	}
	objs := e.objectsUnder(t, e.a.ID)
	if !slices.Contains(objs, prefix+"canonical-r5-a1.json") || slices.ContainsFunc(objs, func(o string) bool {
		return strings.Contains(o, "-r3-") || strings.Contains(o, "-r4-")
	}) {
		t.Errorf("objects %v", objs)
	}
}

// TEST-012 through the worker: a validation job that cannot validate is
// retried; once its attempts are spent the import is FAILED with the
// job's failure under the save's request (0014: the import follows the
// current revision's job), and so when an operator cancels it; an upload
// changed or removed since it was profiled fails the import at once; an
// import has one validation job per revision, and a FAILED import is not
// mapped.
func TestValidationJobFailures(t *testing.T) {
	e := setupRuns(t)
	m := e.newModel(t)
	editor := e.a.Members[tenant.Editor]
	ids := map[string]string{}
	for _, name := range []string{"unavailable", "changed", "removed", "cancelled", "twice"} {
		ids[name] = e.uploaded(t, m, "key-vf-"+name, "csv_events", "a.csv", simpleCSV)
	}
	drain(t, e.profileLoop(t, profiler.Config{Objects: e.objects}, nil), 10*time.Second)
	requests := map[string]string{}
	save := func(name string) {
		r := e.saveMapping(editor.Subject, ids[name], "key-vf-save-"+name, mappingBody(t, 0, nil, simple...))
		if r.status != 202 {
			t.Fatalf("%s: %d %s", name, r.status, r.body)
		}
		requests[name] = r.header.Get("X-Request-ID")
	}
	// A worker without object storage: every attempt fails, retryable.
	save("unavailable")
	drain(t, e.validateLoop(t, validator.Config{}, nil), 10*time.Second)
	for _, name := range []string{"changed", "removed", "cancelled", "twice"} {
		save(name)
	}
	// An import has one validation job per revision.
	err := ownerTxErr(e, e.a.ID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO twin.jobs(id,organization_id,type,status,payload,import_id) VALUES ($1,$2,'import_validate','queued',$3,$4)`,
			"job_01M4YDQK00TA00000000000078", e.a.ID, `{"import_id":"`+ids["twice"]+`","mapping_revision":1,"request_id":"`+requests["twice"]+`"}`, ids["twice"])
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "23505") {
		t.Errorf("a second job for a revision: %v", err)
	}
	// An operator cancelling the current revision's queued job: the import
	// is FAILED at once.
	e.ownerTx(t, e.a.ID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE twin.jobs SET status='cancelled' WHERE import_id=$1 AND type='import_validate'`, ids["cancelled"])
		return err
	})
	if got := e.getMapped(t, editor.Subject, ids["cancelled"]); got.Status != "FAILED" {
		t.Errorf("after the job was cancelled: %+v", got)
	}
	// Uploads changed and removed since they were profiled.
	file := func(id string) string { return filepath.Join(e.root, "tenant", e.a.ID, "imports", id, "upload") }
	if err := os.WriteFile(file(ids["changed"]), []byte(strings.Replace(simpleCSV, "C-1", "C-9", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file(ids["removed"])); err != nil {
		t.Fatal(err)
	}
	drain(t, e.validateLoop(t, validator.Config{Objects: e.objects}, nil), 10*time.Second)
	for name, want := range map[string][2]string{
		"unavailable": {"SERVICE_UNAVAILABLE", "OBJECT_STORAGE_UNAVAILABLE"},
		"changed":     {"INTERNAL_ERROR", "UPLOAD_CORRUPT"},
		"removed":     {"INTERNAL_ERROR", "UPLOAD_MISSING"},
		"cancelled":   {"INTERNAL_ERROR", "JOB_CANCELLED"},
	} {
		got := e.getMapped(t, editor.Subject, ids[name])
		if got.Status != "FAILED" || got.Error == nil || got.Error.Error.Code != want[0] || len(got.Error.Error.Details) != 1 ||
			got.Error.Error.Details[0].Code != want[1] || got.Error.Error.RequestID != requests[name] || got.MappingRevision != 1 {
			t.Errorf("%s: %+v", name, got)
		}
	}
	var rowError, jobError string
	var attempts int
	if err := e.admin.QueryRow(context.Background(), `SELECT i.error::text, j.error::text, j.attempt FROM twin.imports i
		JOIN twin.jobs j ON j.import_id=i.id AND j.type='import_validate' WHERE i.id=$1`, ids["unavailable"]).Scan(&rowError, &jobError, &attempts); err != nil ||
		rowError != jobError || attempts != fastPolicy.MaxAttempts {
		t.Errorf("row %s (the job: %s) after %d attempts: %v", rowError, jobError, attempts, err)
	}
	if got := e.getMapped(t, editor.Subject, ids["twice"]); got.Status != "VALIDATED" {
		t.Errorf("twice: %+v", got)
	}
	// A FAILED import is final.
	r := e.saveMapping(editor.Subject, ids["unavailable"], "key-vf-final", mappingBody(t, 1, nil, simple...))
	if r.status != 409 || code(t, r) != "LIFECYCLE_CONFLICT" {
		t.Fatalf("a FAILED import: %d %s", r.status, r.body)
	}
	if _, c := firstDetail(t, r); c != importupload.CodeFinal {
		t.Errorf("detail %s", c)
	}
}

// TEST-012 for the route: what a save may not be, refused before anything
// is committed with the right status and code and a JSON Pointer at the
// field: a mapping the import cannot take, a body the schema refuses
// (the AI's suggestion is not a mapping, an expression is not a
// transform, an unconfirmed save), the transport, the caller and the
// import's state.
func TestMappingRefusals(t *testing.T) {
	e := setupRuns(t)
	m := e.newModel(t)
	editor, viewer, outsider := e.a.Members[tenant.Editor], e.a.Members[tenant.Viewer], e.b.Members[tenant.Owner]
	id := e.uploaded(t, m, "key-mr-csv", "csv_events", "a.csv", simpleCSV)
	failed := e.uploaded(t, m, "key-mr-failed", "csv_events", "a.csv", "a,a\n1,2\n")
	model := e.uploaded(t, m, "key-mr-model", "json_model", "model.json", string(readContract(t, "valid-minimal-ir.json")))
	drain(t, e.profileLoop(t, profiler.Config{Objects: e.objects}, nil), 10*time.Second)
	unprofiled := e.uploaded(t, m, "key-mr-new", "csv_events", "a.csv", simpleCSV)

	ok := mappingBody(t, 0, nil, simple...)
	member := func(body, after, add string) string { return strings.Replace(body, after, after+add, 1) }
	cases := map[string]struct {
		body         string
		status       int
		code         string
		path, detail string
	}{
		"an unknown column": {mappingBody(t, 0, nil, col{"nope", "case_id", ""}, simple[1], simple[2]), 422, "REQUEST_VALIDATION_FAILED",
			"/mapping/mappings/0/source_column", mapping.CodeUnknownColumn},
		"a column twice": {mappingBody(t, 0, nil, simple[0], col{"case", "metadata", ""}, simple[1], simple[2]), 422, "REQUEST_VALIDATION_FAILED",
			"/mapping/mappings/1/source_column", mapping.CodeDuplicateColumn},
		"a field twice": {mappingBody(t, 0, nil, simple[0], col{"type", "case_id", ""}, simple[2]), 422, "REQUEST_VALIDATION_FAILED",
			"/mapping/mappings/1/target_field", mapping.CodeDuplicateTarget},
		"no timestamp": {mappingBody(t, 0, nil, simple[0], simple[1]), 422, "REQUEST_VALIDATION_FAILED", "/mapping/mappings", mapping.CodeRequiredTarget},
		"a confirmation of nothing": {mappingBody(t, 0, []string{"case"}, simple...), 422, "REQUEST_VALIDATION_FAILED",
			"/confirmed_low_confidence/0", mapping.CodeConfirmationUnknown},
		"a timestamp taken as text":     {mappingBody(t, 0, nil, simple[0], simple[1], col{"at", "timestamp", "identity"}), 422, "REQUEST_VALIDATION_FAILED", "", ""},
		"an unknown field":              {mappingBody(t, 0, nil, simple[0], simple[1], col{"at", "start_time", "rfc3339"}), 422, "REQUEST_VALIDATION_FAILED", "", ""},
		"an expression":                 {member(ok, `"source_column":"case",`, `"expression":"=A1",`), 422, "REQUEST_VALIDATION_FAILED", "", ""},
		"the AI's confidence":           {member(ok, `"source_column":"case",`, `"confidence":0.99,`), 422, "REQUEST_VALIDATION_FAILED", "", ""},
		"an unconfirmed save":           {strings.Replace(ok, `"confirmed":true`, `"confirmed":false`, 1), 422, "REQUEST_VALIDATION_FAILED", "", ""},
		"no expected revision":          {strings.Replace(ok, `"expected_mapping_revision":0,`, "", 1), 422, "REQUEST_VALIDATION_FAILED", "", ""},
		"a negative revision":           {strings.Replace(ok, `"expected_mapping_revision":0`, `"expected_mapping_revision":-1`, 1), 422, "REQUEST_VALIDATION_FAILED", "", ""},
		"not JSON":                      {`{"mapping":`, 400, "INVALID_REQUEST", "", ""},
		"a body over the limit":         {member(ok, `"confirmed":true`, `,"pad":"`+strings.Repeat("x", 1<<20)+`"`), 413, "PAYLOAD_TOO_LARGE", "", ""},
		"a NUL in a column name":        {mappingBody(t, 0, nil, col{"case\x00", "case_id", ""}, simple[1], simple[2]), 422, "REQUEST_VALIDATION_FAILED", "", ""},
		"a left-out column transformed": {mappingBody(t, 0, nil, simple[0], simple[1], simple[2], col{"at", "", "rfc3339"}), 422, "REQUEST_VALIDATION_FAILED", "", ""},
		"metadata with a transform":     {mappingBody(t, 0, nil, simple[0], simple[1], simple[2], col{"at", "metadata", "rfc3339"}), 422, "REQUEST_VALIDATION_FAILED", "", ""},
		"an empty mapping":              {mappingBody(t, 0, nil), 422, "REQUEST_VALIDATION_FAILED", "", ""},
		"a repeated confirmation":       {mappingBody(t, 0, []string{"type", "type"}, simple...), 422, "REQUEST_VALIDATION_FAILED", "", ""},
		"a column name over 256 long":   {mappingBody(t, 0, nil, col{strings.Repeat("欄", 257), "case_id", ""}, simple[1], simple[2]), 422, "REQUEST_VALIDATION_FAILED", "", ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := e.saveMapping(editor.Subject, id, "key-mr-"+strings.ReplaceAll(name, " ", "-"), c.body)
			if r.status != c.status || code(t, r) != c.code {
				t.Fatalf("%d %s", r.status, r.body)
			}
			if c.path != "" || c.detail != "" {
				if path, d := firstDetail(t, r); path != c.path || d != c.detail {
					t.Errorf("detail %s %s", path, d)
				}
			}
		})
	}
	// The transport, the caller and the import.
	for name, c := range map[string]struct {
		subject, org, path string
		headers            map[string]string
		status             int
		code               string
	}{
		"no Idempotency-Key":          {editor.Subject, e.a.ID, "/v1/imports/" + id + "/mapping", nil, 400, "IDEMPOTENCY_KEY_REQUIRED"},
		"another media type":          {editor.Subject, e.a.ID, "/v1/imports/" + id + "/mapping", map[string]string{"Idempotency-Key": "key-mr-media", "Content-Type": "text/plain"}, 415, "UNSUPPORTED_MEDIA_TYPE"},
		"a malformed import":          {editor.Subject, e.a.ID, "/v1/imports/imp_x/mapping", map[string]string{"Idempotency-Key": "key-mr-bad-id"}, 400, "INVALID_REQUEST"},
		"an import that is not there": {editor.Subject, e.a.ID, "/v1/imports/imp_01M4YDQK00TA00000000000099/mapping", map[string]string{"Idempotency-Key": "key-mr-none"}, 404, "NOT_FOUND"},
		"a viewer":                    {viewer.Subject, e.a.ID, "/v1/imports/" + id + "/mapping", map[string]string{"Idempotency-Key": "key-mr-viewer"}, 403, "FORBIDDEN"},
		"another tenant's member":     {outsider.Subject, e.a.ID, "/v1/imports/" + id + "/mapping", map[string]string{"Idempotency-Key": "key-mr-outsider"}, 404, "NOT_FOUND"},
		"another tenant's import":     {outsider.Subject, e.b.ID, "/v1/imports/" + id + "/mapping", map[string]string{"Idempotency-Key": "key-mr-outsider-b"}, 404, "NOT_FOUND"},
		"no token":                    {"", e.a.ID, "/v1/imports/" + id + "/mapping", map[string]string{"Idempotency-Key": "key-mr-anon"}, 401, "UNAUTHENTICATED"},
	} {
		if r := e.do("POST", c.path, c.subject, c.org, c.headers, ok); r.status != c.status || code(t, r) != c.code {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}
	for name, c := range map[string]struct {
		id, detail string
		status     int
	}{
		"an import not yet profiled": {unprofiled, importupload.CodeNotProfiled, 409},
		"a FAILED import":            {failed, importupload.CodeFinal, 409},
		"a JSON model":               {model, importupload.CodeNotApplicable, 422},
	} {
		r := e.saveMapping(editor.Subject, c.id, "key-mr-state-"+strings.ReplaceAll(name, " ", "-"), ok)
		if r.status != c.status {
			t.Errorf("%s: %d %s", name, r.status, r.body)
			continue
		}
		if _, d := firstDetail(t, r); d != c.detail {
			t.Errorf("%s: detail %s", name, d)
		}
	}
	// None of them committed anything.
	if n := e.count(`SELECT (SELECT count(*) FROM twin.imports WHERE organization_id=$1 AND (mapping_revision <> 0 OR mapping IS NOT NULL))
		+ (SELECT count(*) FROM twin.jobs WHERE organization_id=$1 AND type='import_validate')
		+ (SELECT count(*) FROM twin.audit_events WHERE organization_id=$1 AND action='import.map')`, e.a.ID); n != 0 {
		t.Errorf("refused saves left %d rows", n)
	}
}

// D-070 (PROPOSED): the AI only suggests (T-027). Each entry of its ready
// suggestion that requires confirmation and that the mapping takes as
// suggested must be named in confirmed_low_confidence by the person who
// saves, and nothing else may be named there; a column the person maps
// otherwise needs no confirmation; the suggestion is never the mapping.
func TestLowConfidenceSuggestion(t *testing.T) {
	e := setupRuns(t)
	m := e.newModel(t)
	editor := e.a.Members[tenant.Editor]
	id := e.profiled(t, m, "key-lc-upload", "csv_events", "a.csv", "case,type,at,station\nC-1,step,2026-10-13T06:12:00Z,fct\n")
	suggestion := `{"mappings":[
		{"source_column":"case","target_field":"case_id","confidence":0.95,"evidence":"values look like case IDs","transform":"identity","requires_confirmation":false},
		{"source_column":"type","target_field":"event_type","confidence":0.6,"evidence":"a short repeated text","transform":"identity","requires_confirmation":true},
		{"source_column":"at","target_field":"timestamp","confidence":0.99,"evidence":"RFC 3339 values","transform":"rfc3339","requires_confirmation":false},
		{"source_column":"station","target_field":"stage","confidence":0.5,"evidence":"a short repeated text","transform":"identity","requires_confirmation":true}]}`
	e.ownerTx(t, e.a.ID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE twin.imports SET mapping_suggestion_status='ready', mapping_suggestion=$2 WHERE id=$1`, id, suggestion)
		return err
	})
	// The suggestion is not the mapping.
	if got := e.getMapped(t, editor.Subject, id); got.Status != "AWAITING_MAPPING" || got.Mapping != nil || got.MappingRevision != 0 {
		t.Fatalf("with a suggestion: %+v", got)
	}
	suggested := append(append([]col(nil), simple...), col{"station", "stage", ""})
	for name, c := range map[string]struct {
		confirmed []string
		paths     []string
		codes     []string
	}{
		"nothing confirmed": {nil, []string{"/mapping/mappings/1", "/mapping/mappings/3"},
			[]string{mapping.CodeConfirmationRequired, mapping.CodeConfirmationRequired}},
		"one of two confirmed": {[]string{"type"}, []string{"/mapping/mappings/3"}, []string{mapping.CodeConfirmationRequired}},
		"a confident entry confirmed": {[]string{"type", "station", "at"}, []string{"/confirmed_low_confidence/2"},
			[]string{mapping.CodeConfirmationUnknown}},
	} {
		r := e.saveMapping(editor.Subject, id, "key-lc-"+strings.ReplaceAll(name, " ", "-"), mappingBody(t, 0, c.confirmed, suggested...))
		if r.status != 422 || code(t, r) != "REQUEST_VALIDATION_FAILED" {
			t.Errorf("%s: %d %s", name, r.status, r.body)
			continue
		}
		var b struct {
			Error struct {
				Details []issue `json:"details"`
			} `json:"error"`
		}
		if err := json.Unmarshal(r.body, &b); err != nil {
			t.Fatal(err)
		}
		var paths, codes []string
		for _, d := range b.Error.Details {
			paths, codes = append(paths, d.Path), append(codes, d.Code)
		}
		if !slices.Equal(paths, c.paths) || !slices.Equal(codes, c.codes) {
			t.Errorf("%s: %v %v", name, paths, codes)
		}
	}
	// Both confirmed by the person: saved, with the count in the audit.
	if r := e.saveMapping(editor.Subject, id, "key-lc-confirmed", mappingBody(t, 0, []string{"type", "station"}, suggested...)); r.status != 202 {
		t.Fatalf("confirmed: %d %s", r.status, r.body)
	}
	// The person maps station otherwise: only type needs confirmation.
	mine := append(append([]col(nil), suggested[:3]...), col{"station", "metadata", ""})
	if r := e.saveMapping(editor.Subject, id, "key-lc-mine", mappingBody(t, 1, []string{"type"}, mine...)); r.status != 202 {
		t.Fatalf("the person's own mapping: %d %s", r.status, r.body)
	}
	var confirmed string
	if err := e.admin.QueryRow(context.Background(), `SELECT string_agg(metadata->>'confirmed_low_confidence', ',' ORDER BY (metadata->>'mapping_revision')::int)
		FROM twin.audit_events WHERE target_id=$1 AND action='import.map'`, id).Scan(&confirmed); err != nil || confirmed != "2,1" {
		t.Errorf("confirmations in the audit %q: %v", confirmed, err)
	}
	// The suggestion stays as the AI wrote it; the mapping is the person's.
	got := e.getMapped(t, editor.Subject, id)
	if got.MappingRevision != 2 || got.Mapping.Mappings[3]["target_field"] != "metadata" ||
		e.count(`SELECT count(*) FROM twin.imports WHERE id=$1 AND mapping_suggestion_status='ready' AND mapping_suggestion = $2::jsonb`, id, suggestion) != 1 {
		t.Errorf("after the saves: %+v", got)
	}
}

// JSON events (TEST-013): members map like columns, a JSON number is text
// for identity and integer_ms, a JSON boolean (or "true"/"false") for
// boolean_literal, a member a row does not have is an empty value, and a
// JSON value goes to metadata as it is; a negative duration is reported at
// its member's pointer.
func TestJSONEventsMapping(t *testing.T) {
	e := setupRuns(t)
	m := e.newModel(t)
	editor := e.a.Members[tenant.Editor]
	file := `[{"case":"C-1","kind":"step","at":"2026-10-13T06:12:00Z","ms":7200000,"ok":true,"extra":{"a":[1,2]}},` +
		`{"case":"C-2","kind":"step","at":"2026-10-13T06:13:00Z","ms":"60000","ok":"false"},` +
		`{"case":"C-3","kind":"step","at":"2026-10-13T06:14:00Z","ms":-1}]`
	id := e.profiled(t, m, "key-je-upload", "json_events", "events.json", file)
	cols := []col{{"case", "case_id", ""}, {"kind", "event_type", ""}, {"at", "timestamp", "rfc3339"}, {"ms", "duration_ms", "integer_ms"},
		{"ok", "is_failure", "boolean_literal"}, {"extra", "metadata", ""}}
	if r := e.saveMapping(editor.Subject, id, "key-je-1", mappingBody(t, 0, nil, cols...)); r.status != 202 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	drain(t, e.validateLoop(t, validator.Config{Objects: e.objects}, nil), 10*time.Second)
	got := e.getMapped(t, editor.Subject, id)
	if got.Status != "AWAITING_MAPPING" || got.InvalidRows != 1 || len(got.Validation.Issues) != 1 || got.Validation.Issues[0].Path != "/2/ms" ||
		got.Validation.Issues[0].Code != mapping.CodeValueInvalid || got.CanonicalPreview[1]["duration_ms"] != float64(60000) {
		t.Fatalf("%+v", got)
	}
	cols[3].target, cols[3].transform = "", ""
	if r := e.saveMapping(editor.Subject, id, "key-je-2", mappingBody(t, 1, nil, cols...)); r.status != 202 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	drain(t, e.validateLoop(t, validator.Config{Objects: e.objects}, nil), 10*time.Second)
	got = e.getMapped(t, editor.Subject, id)
	if got.Status != "VALIDATED" || len(got.CanonicalPreview) != 3 {
		t.Fatalf("%+v", got)
	}
	var key string
	if err := e.admin.QueryRow(context.Background(), `SELECT canonical_object_key FROM twin.imports WHERE id=$1`, id).Scan(&key); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(e.root, filepath.FromSlash(key)))
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"case_id":"C-1","event_type":"step","timestamp":"2026-10-13T06:12:00Z","is_failure":true,"metadata":{"extra":{"a":[1,2]}}},` +
		`{"case_id":"C-2","event_type":"step","timestamp":"2026-10-13T06:13:00Z","is_failure":false},` +
		`{"case_id":"C-3","event_type":"step","timestamp":"2026-10-13T06:14:00Z"}]`
	var gotEvents, wantEvents []map[string]any
	if json.Unmarshal(data, &gotEvents) != nil || json.Unmarshal([]byte(want), &wantEvents) != nil || fmt.Sprint(gotEvents) != fmt.Sprint(wantEvents) {
		t.Errorf("dataset %s, want %s", data, want)
	}
}

// Review round 1, M1, M2: the server, not the stored flag, decides which
// suggestion entries need a person. A stored suggestion that does not
// match ai-mapping.schema.json (here a 0.1 confidence the AI did not flag)
// fails the save closed: 500 with its code, nothing committed. A column
// the AI would leave out with low confidence is left out only with the
// person's confirmation, whether the mapping lists it as not imported or
// does not list it.
func TestTheServerJudgesTheSuggestion(t *testing.T) {
	e := setupRuns(t)
	m := e.newModel(t)
	editor := e.a.Members[tenant.Editor]
	id := e.profiled(t, m, "key-sj-upload", "csv_events", "a.csv", "case,type,at,end\nC-1,step,2026-10-13T06:12:00Z,2026-10-13T06:13:00Z\n")
	suggest := func(doc string) {
		e.ownerTx(t, e.a.ID, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE twin.imports SET mapping_suggestion_status='ready', mapping_suggestion=$2 WHERE id=$1`, id, doc)
			return err
		})
	}
	suggest(`{"mappings":[{"source_column":"at","target_field":"timestamp","confidence":0.1,"evidence":"e","transform":"rfc3339","requires_confirmation":false}]}`)
	r := e.saveMapping(editor.Subject, id, "key-sj-malformed", mappingBody(t, 0, nil, simple...))
	if r.status != 500 || code(t, r) != "INTERNAL_ERROR" {
		t.Fatalf("a malformed suggestion: %d %s", r.status, r.body)
	}
	if _, c := firstDetail(t, r); c != importupload.CodeSuggestionInvalid {
		t.Errorf("detail %s", c)
	}
	if n := e.count(`SELECT (SELECT mapping_revision FROM twin.imports WHERE id=$1) + (SELECT count(*) FROM twin.jobs WHERE import_id=$1 AND type='import_validate')
		+ (SELECT count(*) FROM twin.audit_events WHERE target_id=$1 AND action='import.map')`, id); n != 0 {
		t.Errorf("a refused save left %d rows", n)
	}
	suggest(`{"mappings":[{"source_column":"end","target_field":null,"confidence":0.3,"evidence":"e","transform":"identity","requires_confirmation":true}]}`)
	for name, body := range map[string]string{
		"left out by a null entry": mappingBody(t, 0, nil, append(append([]col(nil), simple...), col{"end", "", ""})...),
		"left out by not listing":  mappingBody(t, 0, nil, simple...),
	} {
		r := e.saveMapping(editor.Subject, id, "key-sj-"+strings.ReplaceAll(name, " ", "-"), body)
		if r.status != 422 || code(t, r) != "REQUEST_VALIDATION_FAILED" {
			t.Errorf("%s: %d %s", name, r.status, r.body)
			continue
		}
		want := "/mapping/mappings/3"
		if name == "left out by not listing" {
			want = "/mapping/mappings"
		}
		if path, c := firstDetail(t, r); path != want || c != mapping.CodeConfirmationRequired {
			t.Errorf("%s: %s %s", name, path, c)
		}
	}
	if r := e.saveMapping(editor.Subject, id, "key-sj-confirmed", mappingBody(t, 0, []string{"end"}, simple...)); r.status != 202 {
		t.Errorf("confirmed: %d %s", r.status, r.body)
	}
}
