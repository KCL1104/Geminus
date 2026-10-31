package proposals

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/httpapi"
	"github.com/kcl1104/geminus/internal/proposals/photo"
	"github.com/kcl1104/geminus/internal/storage/objects"
	store "github.com/kcl1104/geminus/internal/storage/proposals"
)

// H-052: getProposalImage serves the photo a proposal was asked for with,
// the bytes the server stored, only after the stored object is checked
// against its record (digest, size, type, size in pixels); anything else is
// a server error and no byte is sent (fail closed, as the trace).

const (
	readOrg   = "org_01M4YDQK00Y5H2929M9WWZWTVH"
	otherOrg  = "org_01M4YDQK00Y5H2929M9WWZWTVJ"
	readPhoto = "aip_01M4YDQK00ADN261CME3WV5HZB"
)

// storedPNG stores a re-encoded 40×32 PNG under the proposal's key in a
// filesystem store, as createImageProposal does, and returns its record.
func storedPNG(t *testing.T, org string) (*objects.FS, string, store.Image) {
	t.Helper()
	root := t.TempDir()
	fs, err := objects.NewFS(root, "test")
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 40, 32))
	for x := range 40 {
		img.Set(x, x%32, color.RGBA{R: 200, A: 255})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	im, err := photo.Sanitize(context.Background(), buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	key, err := objects.NewKey(org, objects.Assets, "proposal-images", readPhoto, "image")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Put(context.Background(), key, bytes.NewReader(im.Data), photo.MaxBytes); err != nil {
		t.Fatal(err)
	}
	return fs, root, store.Image{ObjectKey: key, SHA256: im.SHA256, Size: im.Size(), MediaType: im.MediaType, Width: im.Width, Height: im.Height}
}

func TestAStoredPhotoIsReadOnlyAsItsRecordNamesIt(t *testing.T) {
	ctx := context.Background()
	fs, root, rec := storedPNG(t, readOrg)
	data, err := readStoredImage(ctx, fs, readOrg, rec)
	if err != nil || photo.Format(data) != photo.PNG || int64(len(data)) != rec.Size {
		t.Fatalf("%d bytes, %v", len(data), err)
	}

	if _, err := readStoredImage(ctx, nil, readOrg, rec); !errors.Is(err, ErrStorageUnavailable) {
		t.Errorf("no storage: %v", err)
	}
	// A key outside the organization's assets is never read.
	if _, err := readStoredImage(ctx, fs, otherOrg, rec); !errors.Is(err, ErrImageCorrupt) {
		t.Errorf("another organization's key: %v", err)
	}
	for name, edit := range map[string]func(*store.Image){
		"another digest":         func(r *store.Image) { r.SHA256 = "sha256:" + strings.Repeat("0", 64) },
		"another size":           func(r *store.Image) { r.Size-- },
		"another type":           func(r *store.Image) { r.MediaType = photo.JPEG },
		"another size in pixels": func(r *store.Image) { r.Width = 41 },
		"a missing object": func(r *store.Image) {
			r.ObjectKey = strings.Replace(r.ObjectKey, readPhoto, "aip_01M4YDQK00ADN261CME3WV5HZC", 1)
		},
	} {
		r := rec
		edit(&r)
		if data, err := readStoredImage(ctx, fs, readOrg, r); !errors.Is(err, ErrImageCorrupt) || data != nil {
			t.Errorf("%s: %d bytes, %v", name, len(data), err)
		}
	}
	// Bytes changed in storage after they were recorded.
	path := filepath.Join(root, filepath.FromSlash(rec.ObjectKey))
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the filesystem store's layout: %v", err)
	}
	stored[len(stored)-1] ^= 0xff
	if err := os.WriteFile(path, stored, 0o600); err != nil {
		t.Fatal(err)
	}
	if data, err := readStoredImage(ctx, fs, readOrg, rec); !errors.Is(err, ErrImageCorrupt) || data != nil {
		t.Errorf("changed bytes: %d bytes, %v", len(data), err)
	}
}

// The photo goes out as itself: its stored media type, its length, never
// sniffed, never kept by a cache.
func TestThePhotoIsServedAsItsType(t *testing.T) {
	for _, media := range []string{photo.PNG, photo.JPEG} {
		w := httptest.NewRecorder()
		writeImage(w, media, []byte{1, 2, 3})
		h := w.Result().Header
		if w.Code != 200 || h.Get("Content-Type") != media || h.Get("Content-Length") != "3" || h.Get("X-Content-Type-Options") != "nosniff" ||
			h.Get("Cache-Control") != "no-store" || !bytes.Equal(w.Body.Bytes(), []byte{1, 2, 3}) {
			t.Errorf("%s: %d %v %v", media, w.Code, h, w.Body.Bytes())
		}
	}
}

// A proposal asked for in words has no photo: 404, as one that does not
// exist. A stored photo that is not its record's is a server error: no
// byte, no detail.
func TestAMissingPhotoIsNotFound(t *testing.T) {
	var he *httpapi.Error
	if err := Refused(ErrNoImage); !errors.As(err, &he) || he.Code != httpapi.NotFound {
		t.Errorf("no image: %v", err)
	}
	if err := Refused(ErrImageCorrupt); errors.As(err, &he) {
		t.Errorf("a corrupt image is %s, want INTERNAL_ERROR", he.Code)
	}
	if err := Refused(ErrStorageUnavailable); !errors.As(err, &he) || he.Code != httpapi.ServiceUnavailable {
		t.Errorf("no storage: %v", err)
	}
	i := slices.IndexFunc(Routes, func(r Route) bool { return r.OperationID == "getProposalImage" })
	if i < 0 || Routes[i].Method != "GET" || Routes[i].Path != "/v1/ai-proposals/{id}/image" || !slices.Equal(Routes[i].Roles, readRoles) {
		t.Errorf("routes %+v", Routes)
	}
}
