package main

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"path/filepath"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/domainpack/electronicsline"
	"github.com/kcl1104/geminus/internal/proposals/photo"
)

// The committed demo images and segments are what this command writes (so
// they are reproducible and nobody hand-edits them), the concepts are the
// pack's, the images are synthetic PNGs the server accepts, and every box
// lies inside its image, its corners inclusive, with distinct object IDs
// per concept, in the concepts' order.
func TestTheCommittedFilesAreReproducible(t *testing.T) {
	if err := run(filepath.Join("..", "..", "testdata", "photos"), true); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(concepts, electronicsline.Pack{}.PhotoConcepts()) {
		t.Errorf("the concepts are not the pack's: %q", concepts)
	}
	out, err := files()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"electronics-line-demo", "electronics-line-injection"} {
		var s Segments
		if err := json.Unmarshal(out[name+".segments.json"], &s, json.RejectUnknownMembers(true)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		stored, err := photo.Sanitize(context.Background(), out[name+".png"])
		if err != nil || stored.SHA256 != s.StoredDigest || stored.Width != width || stored.Height != height || s.MediaType != photo.PNG {
			t.Errorf("%s: %v %+v", name, err, s)
		}
		if len(s.Segments) != 10 {
			t.Errorf("%s: %d segments", name, len(s.Segments))
		}
		last, seen := -1, map[string]map[int]bool{}
		for _, x := range s.Segments {
			b, at := x.Box, slices.Index(concepts, x.Concept)
			if b.X1 < 0 || b.Y1 < 0 || b.X1 > b.X2 || b.Y1 > b.Y2 || b.X2 >= width || b.Y2 >= height || at < last || x.Confidence <= 0 || x.Confidence > 1 {
				t.Errorf("%s: segment %+v", name, x)
			}
			if seen[x.Concept] == nil {
				seen[x.Concept] = map[int]bool{}
			}
			if seen[x.Concept][x.ObjectID] {
				t.Errorf("%s: object %d of %q twice", name, x.ObjectID, x.Concept)
			}
			seen[x.Concept][x.ObjectID], last = true, at
		}
	}
	// Drawn twice, the same bytes.
	again, _ := files()
	for name, data := range out {
		if !bytes.Equal(again[name], data) {
			t.Errorf("%s is not deterministic", name)
		}
	}
}
