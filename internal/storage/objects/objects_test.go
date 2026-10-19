package objects_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/storage/objects"
)

// Keys follow twin.object_key_ok (D-035): the same cases as
// db/tests/constraints.sql, and what NewKey builds passes both.
func TestKeys(t *testing.T) {
	org, other := ids.New("org"), ids.New("org")
	wild := strings.Replace(org, "_", "X", 1)
	good := []string{
		"tenant/" + org + "/datasets/x.json",
		"tenant/" + org + "/simulation-results/sim_1/art_2.json",
		"tenant/" + org + "/imports/a-errors.csv",
		"tenant/" + org + "/assets/_a/-b/c.d.e",
	}
	for _, k := range good {
		if !objects.ValidKey(org, k) {
			t.Errorf("%q refused", k)
		}
	}
	bad := []string{
		"tenant/" + wild + "/datasets/x.json",
		"tenant/" + other + "/datasets/x.json",
		"tenant/" + org + "/datasets/../x.json",
		"tenant/" + org + "//x.json",
		"tenant/" + org + `/datasets\x.json`,
		"tenant/" + org + "/datasets/x\n.json",
		"tenant/" + org + "/datasets/x\x7f",
		"tenant/" + org + "/",
		"tenant/" + org + "/datasets/" + strings.Repeat("a", 1100),
		"tenant/" + org + "/datasets/x.json/",
		"tenant/" + org + "/datasets/./x.json",
		"tenant/" + org + "/datasets/.hidden",
		"tenant/" + org + "/datasets/%2e%2e/x.json",
		"tenant/" + org + "/datasets/x%2Fy.json",
		"tenant/" + org + "/datasets/．．/x.json",
		"tenant/" + org + "/datasets/x∕y.json",
		"tenant/" + org + "/datasets/x‮.json",
		"tenant/" + org + "/datasets/x​.json",
		"tenant/" + org + "/datasets/x y.json",
		"tenant/" + org + "/datasets/" + strings.Repeat("é", 600),
		"TENANT/" + org + "/datasets/x.json",
		"",
		"/tenant/" + org + "/datasets/x.json",
	}
	for _, k := range bad {
		if objects.ValidKey(org, k) {
			t.Errorf("%q accepted", k)
		}
	}
	if objects.ValidKey("org_bad", "tenant/org_bad/datasets/x.json") {
		t.Error("a key of a malformed organization ID was accepted")
	}

	k, err := objects.NewKey(org, objects.SimulationResults, "sim_01", "art_02.json")
	if err != nil || k != "tenant/"+org+"/simulation-results/sim_01/art_02.json" || !objects.InArea(org, objects.SimulationResults, k) || objects.InArea(org, objects.Datasets, k) {
		t.Errorf("NewKey: %q %v", k, err)
	}
	for name, c := range map[string]struct {
		org  string
		area objects.Area
		segs []string
	}{
		"no segment":       {org, objects.Datasets, nil},
		"an empty one":     {org, objects.Datasets, []string{""}},
		"a dot":            {org, objects.Datasets, []string{"."}},
		"dot dot":          {org, objects.Datasets, []string{".."}},
		"a hidden one":     {org, objects.Datasets, []string{".x"}},
		"a separator":      {org, objects.Datasets, []string{"a/b"}},
		"an unknown area":  {org, objects.Area("other"), []string{"x"}},
		"a bad org":        {"org_x", objects.Datasets, []string{"x"}},
		"a traversal pair": {org, objects.Datasets, []string{"a..b"}},
		"too long":         {org, objects.Datasets, []string{strings.Repeat("a", 1100)}},
	} {
		if k, err := objects.NewKey(c.org, c.area, c.segs...); !errors.Is(err, objects.ErrInvalidKey) {
			t.Errorf("%s: %q %v", name, k, err)
		}
	}
}

func newFS(t *testing.T) *objects.FS {
	t.Helper()
	f, err := objects.NewFS(t.TempDir(), "test")
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:])
}

// D-044: the filesystem adapter is for development and tests only; its
// root is an existing directory named by a clean absolute path.
func TestNewFS(t *testing.T) {
	dir := t.TempDir()
	if _, err := objects.NewFS(dir, "production"); !errors.Is(err, objects.ErrProduction) {
		t.Errorf("production: %v", err)
	}
	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{"relative/dir", dir + "/../" + filepath.Base(dir), filepath.Join(dir, "missing"), file} {
		if _, err := objects.NewFS(root, "development"); err == nil {
			t.Errorf("%q accepted", root)
		}
	}
	for _, env := range []string{"development", "test"} {
		if _, err := objects.NewFS(dir, env); err != nil {
			t.Errorf("%s: %v", env, err)
		}
	}
}

// Put stores the bytes and reports their hash and size; Get reads them
// back; an object is never replaced, a missing one is ErrNotFound, and an
// object over the limit leaves nothing behind.
func TestPutGet(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	f, err := objects.NewFS(root, "test")
	if err != nil {
		t.Fatal(err)
	}
	org := ids.New("org")
	key, _ := objects.NewKey(org, objects.SimulationResults, ids.New("sim"), ids.New("art")+".json")
	data := []byte(`{"format":"geminus.trace.v1"}`)
	obj, err := f.Put(ctx, key, bytes.NewReader(data), int64(len(data)))
	if err != nil || obj != (objects.Object{Key: key, SHA256: sum(data), Size: int64(len(data))}) {
		t.Fatalf("Put: %+v %v", obj, err)
	}
	rc, err := f.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(rc)
	rc.Close()
	if err != nil || !bytes.Equal(got, data) {
		t.Errorf("Get: %q %v", got, err)
	}
	if _, err := f.Put(ctx, key, strings.NewReader("other"), 100); !errors.Is(err, objects.ErrExists) {
		t.Errorf("a second Put: %v", err)
	}
	if rc, _ := f.Get(ctx, key); rc != nil {
		again, _ := io.ReadAll(rc)
		rc.Close()
		if !bytes.Equal(again, data) {
			t.Errorf("the object was replaced: %q", again)
		}
	}
	missing, _ := objects.NewKey(org, objects.SimulationResults, "nothing.json")
	if _, err := f.Get(ctx, missing); !errors.Is(err, objects.ErrNotFound) {
		t.Errorf("a missing object: %v", err)
	}
	big, _ := objects.NewKey(org, objects.SimulationResults, "big.json")
	if _, err := f.Put(ctx, big, bytes.NewReader(make([]byte, 11)), 10); !errors.Is(err, objects.ErrTooLarge) {
		t.Errorf("11 bytes for a limit of 10: %v", err)
	}
	if _, err := f.Get(ctx, big); !errors.Is(err, objects.ErrNotFound) {
		t.Errorf("an object over the limit was kept: %v", err)
	}
	if obj, err := f.Put(ctx, big, bytes.NewReader(make([]byte, 10)), 10); err != nil || obj.Size != 10 {
		t.Errorf("exactly the limit: %+v %v", obj, err)
	}
	empty, _ := objects.NewKey(org, objects.Assets, "empty")
	if obj, err := f.Put(ctx, empty, bytes.NewReader(nil), 0); err != nil || obj.SHA256 != sum(nil) || obj.Size != 0 {
		t.Errorf("an empty object: %+v %v", obj, err)
	}
	for _, k := range []string{"tenant/" + org + "/../x", "tenant/" + org + "/datasets/./x", "../x", "tenant/" + org, ""} {
		if _, err := f.Put(ctx, k, strings.NewReader("x"), 1); !errors.Is(err, objects.ErrInvalidKey) {
			t.Errorf("Put %q: %v", k, err)
		}
		if _, err := f.Get(ctx, k); !errors.Is(err, objects.ErrInvalidKey) {
			t.Errorf("Get %q: %v", k, err)
		}
	}
	// No Put, failed or not, leaves its temporary file behind.
	if err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && strings.HasPrefix(d.Name(), ".put-") {
			t.Errorf("a temporary file stayed: %s", p)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// Concurrent Puts of one key: exactly one wins; the object is the winner's.
func TestConcurrentPuts(t *testing.T) {
	ctx := context.Background()
	f := newFS(t)
	key, _ := objects.NewKey(ids.New("org"), objects.Datasets, "x.json")
	var wg sync.WaitGroup
	var mu sync.Mutex
	won := map[string]bool{}
	for i := range 8 {
		wg.Go(func() {
			body := strings.Repeat(string(rune('a'+i)), 1000)
			if _, err := f.Put(ctx, key, strings.NewReader(body), 1000); err == nil {
				mu.Lock()
				won[body] = true
				mu.Unlock()
			} else if !errors.Is(err, objects.ErrExists) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	rc, err := f.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if len(won) != 1 || !won[string(got)] {
		t.Errorf("%d winners; object %.10q", len(won), got)
	}
}

// A cancelled context stops Put (keeping nothing), Get and Delete; Delete
// removes an object and reports a missing one.
func TestContextAndDelete(t *testing.T) {
	f := newFS(t)
	key, _ := objects.NewKey(ids.New("org"), objects.Imports, "a.csv")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.Put(ctx, key, strings.NewReader("x"), 1); !errors.Is(err, context.Canceled) {
		t.Errorf("Put: %v", err)
	}
	if _, err := f.Get(context.Background(), key); !errors.Is(err, objects.ErrNotFound) {
		t.Errorf("a cancelled Put kept the object: %v", err)
	}
	r := &cancelling{cancel: nil}
	ctx2, cancel2 := context.WithCancel(context.Background())
	r.cancel = cancel2
	if _, err := f.Put(ctx2, key, r, 1<<20); !errors.Is(err, context.Canceled) {
		t.Errorf("Put cancelled midway: %v", err)
	}
	if _, err := f.Get(context.Background(), key); !errors.Is(err, objects.ErrNotFound) {
		t.Errorf("a Put cancelled midway kept the object: %v", err)
	}
	if _, err := f.Put(context.Background(), key, strings.NewReader("x"), 1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Get(ctx, key); !errors.Is(err, context.Canceled) {
		t.Errorf("Get: %v", err)
	}
	if err := f.Delete(ctx, key); !errors.Is(err, context.Canceled) {
		t.Errorf("Delete: %v", err)
	}
	if err := f.Delete(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	if err := f.Delete(context.Background(), key); !errors.Is(err, objects.ErrNotFound) {
		t.Errorf("Delete twice: %v", err)
	}
}

// cancelling yields a chunk, then cancels its context.
type cancelling struct {
	cancel context.CancelFunc
	n      int
}

func (c *cancelling) Read(p []byte) (int, error) {
	c.n++
	if c.n > 1 {
		c.cancel()
	}
	return copy(p, "chunk"), nil
}
