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

// The committed demo images and boxes are what this command writes (so
// they are reproducible and nobody hand-edits them), the prompts are the
// pack's, the images are synthetic PNGs the server accepts, and every box
// lies inside its image.
func TestTheCommittedFilesAreReproducible(t *testing.T) {
	if err := run(filepath.Join("..", "..", "testdata", "photos"), true); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(prompts, electronicsline.Pack{}.PhotoPrompts()) {
		t.Errorf("the prompts are not the pack's: %q", prompts)
	}
	out, err := files()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"electronics-line-demo", "electronics-line-injection"} {
		var b Boxes
		if err := json.Unmarshal(out[name+".boxes.json"], &b, json.RejectUnknownMembers(true)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		stored, err := photo.Sanitize(context.Background(), out[name+".png"])
		if err != nil || stored.SHA256 != b.StoredSHA256 || stored.Width != width || stored.Height != height || b.MediaType != photo.PNG {
			t.Errorf("%s: %v %+v", name, err, b)
		}
		if len(b.Boxes) != 10 {
			t.Errorf("%s: %d boxes", name, len(b.Boxes))
		}
		for _, x := range b.Boxes {
			if x.X < 0 || x.Y < 0 || x.Width <= 0 || x.Height <= 0 || x.X+x.Width > width || x.Y+x.Height > height || x.Prompt >= len(prompts) {
				t.Errorf("%s: box %+v", name, x)
			}
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
