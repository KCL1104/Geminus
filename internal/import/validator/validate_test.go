package validator

// The handler's validation of one import outside any transaction, on a
// filesystem object store (review round 1, L4 and L5): what the database
// tests cannot reach (the canonical preview bounded by the raw preview's
// rows, the row count compared with the profile's) and the checkpoint
// before the second read.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/import/mapping"
	importprofile "github.com/kcl1104/geminus/internal/import/profile"
	store "github.com/kcl1104/geminus/internal/storage/imports"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/storage/objects"
)

const (
	testOrg = "org_01M4YDQK00TA00000000000001"
	testImp = "imp_01M4YDQK00TA00000000000001"
)

// A synthetic station log of three valid rows.
const log3 = "序號,紀錄類型,開始\r\nSN-1,operation,2026-10-13T06:12:00Z\r\nSN-2,operation,2026-10-13T06:13:00Z\r\nSN-3,operation,2026-10-13T06:14:00Z\r\n"

func target(f mapping.Field) *mapping.Field { return &f }

var logMapping = mapping.Mapping{Mappings: []mapping.Entry{
	{SourceColumn: "序號", TargetField: target(mapping.CaseID), Transform: mapping.Identity},
	{SourceColumn: "紀錄類型", TargetField: target(mapping.EventType), Transform: mapping.Identity},
	{SourceColumn: "開始", TargetField: target(mapping.Timestamp), Transform: mapping.RFC3339},
}}

type fixture struct {
	h    *Handler
	root string
	imp  store.Import
}

// setup stores file as import testImp's upload and returns the import as the
// profile job left it, mapped by m at revision 1.
func setup(t *testing.T, file string, m mapping.Mapping) fixture {
	t.Helper()
	root := t.TempDir()
	fsys, err := objects.NewFS(root, "test")
	if err != nil {
		t.Fatal(err)
	}
	key, err := objects.NewKey(testOrg, objects.Imports, testImp, "upload")
	if err != nil {
		t.Fatal(err)
	}
	obj, err := fsys.Put(context.Background(), key, strings.NewReader(file), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	res, err := importprofile.Read(context.Background(), strings.NewReader(file), importprofile.CSV, importprofile.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	profile, _ := json.Marshal(res.Profile)
	preview, _ := json.Marshal(res.Preview)
	doc, _ := json.Marshal(m)
	h, err := New(Config{Objects: fsys, Actor: "worker-1", Log: quiet})
	if err != nil {
		t.Fatal(err)
	}
	return fixture{h: h, root: root, imp: store.Import{ID: testImp, OrganizationID: testOrg, Kind: "csv_events", UploadKey: obj.Key,
		UploadSHA256: obj.SHA256, Size: obj.Size, Status: store.Validating, MappingRevision: 1, TotalRows: res.Profile.Rows,
		Profile: profile, Preview: preview, Mapping: doc}}
}

// objectsOf are the names of the stored objects of the import, but its upload.
func (f fixture) objectsOf(t *testing.T) []string {
	t.Helper()
	var out []string
	dir := filepath.Join(f.root, "tenant", testOrg, "imports", testImp)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() != "upload" && !strings.HasPrefix(d.Name(), ".") {
			out = append(out, d.Name())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func current(context.Context) (bool, error) { return true, nil }

func TestAValidImport(t *testing.T) {
	f := setup(t, log3, logMapping)
	calls := 0
	out, err := f.h.validate(context.Background(), testOrg, 1, f.imp, 1, nil, func(context.Context) (bool, error) { calls++; return true, nil })
	if err != nil || out.failure != nil || !out.valid || out.stale || out.dataset == nil || out.rows != 3 || calls != 1 {
		t.Fatalf("%+v %v (checkpoints %d)", out, err, calls)
	}
	var preview []map[string]any
	if err := json.Unmarshal(out.preview, &preview); err != nil || len(preview) != 3 || preview[2]["case_id"] != "SN-3" {
		t.Errorf("preview %s: %v", out.preview, err)
	}
	data, err := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(out.dataset.Key)))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if out.dataset.Key != "tenant/"+testOrg+"/imports/"+testImp+"/canonical-r1-a1.json" || out.dataset.SHA256 != "sha256:"+hex.EncodeToString(sum[:]) ||
		out.dataset.Size != int64(len(data)) {
		t.Errorf("dataset %+v", out.dataset)
	}
}

// R26: the canonical preview covers the raw preview's rows, no more (the
// raw preview can stop before 100 rows at its 1 MiB).
func TestTheCanonicalPreviewFollowsTheRawPreview(t *testing.T) {
	f := setup(t, log3, logMapping)
	var raw []jsontext.Value
	if err := json.Unmarshal(f.imp.Preview, &raw); err != nil {
		t.Fatal(err)
	}
	f.imp.Preview, _ = json.Marshal(raw[:1])
	out, err := f.h.validate(context.Background(), testOrg, 1, f.imp, 1, nil, current)
	if err != nil || !out.valid {
		t.Fatalf("%+v %v", out, err)
	}
	var preview []jsontext.Value
	if err := json.Unmarshal(out.preview, &preview); err != nil || len(preview) != 1 {
		t.Errorf("preview %s: %v", out.preview, err)
	}
}

// R27, R24: the upload must still be the profiled file: another row count
// (with the right SHA-256 and size, which only a defect can cause) is
// UPLOAD_CHANGED; another SHA-256 or size UPLOAD_CORRUPT; no object
// UPLOAD_MISSING. Nothing is stored for any of them.
func TestTheUploadIsTheProfiled(t *testing.T) {
	for name, c := range map[string]struct {
		edit func(*store.Import, string)
		code string
	}{
		"another row count": {func(i *store.Import, _ string) { i.TotalRows = 2 }, store.UploadChanged},
		"another SHA-256": {func(i *store.Import, _ string) {
			i.UploadSHA256 = "sha256:" + strings.Repeat("0", 64)
		}, store.UploadCorrupt},
		"another size": {func(i *store.Import, _ string) { i.Size-- }, store.UploadCorrupt},
		"a key of another import": {func(i *store.Import, _ string) {
			i.UploadKey = "tenant/" + testOrg + "/imports/imp_01M4YDQK00TA00000000000002/upload"
		}, store.UploadCorrupt},
		"no object": {func(_ *store.Import, root string) {
			if err := os.Remove(filepath.Join(root, "tenant", testOrg, "imports", testImp, "upload")); err != nil {
				panic(err)
			}
		}, store.UploadMissing},
	} {
		f := setup(t, log3, logMapping)
		c.edit(&f.imp, f.root)
		out, err := f.h.validate(context.Background(), testOrg, 1, f.imp, 1, nil, current)
		if err != nil || out.failure == nil || out.failure.Code != c.code || out.valid || out.dataset != nil {
			t.Errorf("%s: %+v %v", name, out, err)
		}
		if objs := f.objectsOf(t); len(objs) != 0 {
			t.Errorf("%s: stored %v", name, objs)
		}
	}
}

// L5: a newer mapping saved while the job validated: the checkpoint before
// the second read finds it, and the job stops without writing the dataset.
// A checkpoint that fails ends the attempt with its error.
func TestTheSecondReadIsForTheCurrentRevisionOnly(t *testing.T) {
	f := setup(t, log3, logMapping)
	out, err := f.h.validate(context.Background(), testOrg, 1, f.imp, 1, nil, func(context.Context) (bool, error) { return false, nil })
	if err != nil || !out.stale || out.dataset != nil || out.failure != nil {
		t.Errorf("%+v %v", out, err)
	}
	if objs := f.objectsOf(t); len(objs) != 0 {
		t.Errorf("a stale job stored %v", objs)
	}
	stop := errors.New("the lease is lost")
	if _, err := f.h.validate(context.Background(), testOrg, 1, f.imp, 1, nil, func(context.Context) (bool, error) { return false, stop }); !errors.Is(err, stop) {
		t.Errorf("a failed checkpoint: %v", err)
	}
	// Rows that do not map: the report is stored at once, with no second
	// read and so no checkpoint.
	bad := setup(t, strings.Replace(log3, "2026-10-13T06:13:00Z", "yesterday", 1), logMapping)
	calls := 0
	out, err = bad.h.validate(context.Background(), testOrg, 1, bad.imp, 1, nil, func(context.Context) (bool, error) { calls++; return true, nil })
	if err != nil || out.valid || out.report == nil || out.invalid != 1 || calls != 0 {
		t.Errorf("%+v %v (checkpoints %d)", out, err, calls)
	}
	if objs := bad.objectsOf(t); len(objs) != 1 || objs[0] != "validation-errors-r1-a1.csv" {
		t.Errorf("objects %v", objs)
	}
}

// Without object storage every attempt fails, retryable; nothing is read.
func TestNoObjectStorage(t *testing.T) {
	f := setup(t, log3, logMapping)
	h, err := New(Config{Actor: "worker-1", Log: quiet})
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.validate(context.Background(), testOrg, 1, f.imp, 1, nil, current)
	var fail *jobs.Failure
	if !errors.As(err, &fail) || fail.Code != store.ObjectStorageUnavailable || !fail.Retryable {
		t.Errorf("%v", err)
	}
}
