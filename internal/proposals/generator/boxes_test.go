package generator

// H-052: the boxes SAM 3.1 found, kept in a photo proposal's stored
// provider metadata so the proposal page can draw them over the photo.

import (
	"context"
	"encoding/json/v2"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/review"
	"github.com/kcl1104/geminus/internal/contracttest"
	store "github.com/kcl1104/geminus/internal/storage/proposals"
)

// storedPhotoMetadata is the provider metadata the job would store for g:
// the vision call's (or none), then the photo's.
func storedPhotoMetadata(t *testing.T, g generation) []byte {
	t.Helper()
	var meta provider.Metadata
	if g.meta != nil {
		meta = *g.meta
	}
	row, err := generated(g.outcome, meta, g.manifest)
	if err != nil {
		t.Fatal(err)
	}
	out, err := photoMetadata(row.ProviderMetadata, g)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// The stored metadata keeps the boxes of the checked segmentation: each
// object's concept, its box's corners in the image's pixels and the
// segmenter's confidence, in the segmenter's order (the request's
// concepts, then by object), and nothing else of the answer (no object
// ID). The audit still holds no box.
func TestTheBoxesAreKept(t *testing.T) {
	r := newPhotoRig(t, "electronics-line-demo")
	r.script(-1)
	digest := r.demo.Segments.StoredDigest
	r.vision.Script(provider.RoleModeler, digest, contracttest.PhotoAnswer(t, "electronics-line-demo", poolIDs(t, r.st)))
	g, err := r.run(t, context.Background())
	if err != nil || g.outcome.Status != review.Ready {
		t.Fatalf("%v %v", g.outcome.Status, err)
	}
	stored := storedPhotoMetadata(t, g)
	var m struct {
		Segmenter struct {
			Segments int        `json:"segments"`
			Boxes    []photoBox `json:"boxes"`
		} `json:"segmenter"`
	}
	if err := json.Unmarshal(stored, &m); err != nil {
		t.Fatal(err)
	}
	var want []photoBox
	for _, s := range r.demo.Segments.Segments {
		want = append(want, photoBox{Concept: s.Concept, X1: s.Box.X1, Y1: s.Box.Y1, X2: s.Box.X2, Y2: s.Box.Y2, Confidence: s.Confidence})
	}
	if m.Segmenter.Segments != 10 || len(want) != 10 || !slices.Equal(m.Segmenter.Boxes, want) {
		t.Errorf("stored boxes %d %+v", m.Segmenter.Segments, m.Segmenter.Boxes)
	}
	if strings.Contains(string(stored), "object_id") {
		t.Errorf("an object ID is stored: %s", stored)
	}
	am, err := json.Marshal(photoAuditMeta(*g.meta, g.photo))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(am), `"x1"`) || strings.Contains(string(am), `"boxes"`) || strings.Contains(string(am), "fct tester") {
		t.Errorf("the audit holds a box: %s", am)
	}
}

// No box is kept when the segmentation is not one the photo path takes:
// none at all (the server asks for input), one the server refused (more
// objects than a photo takes), or one in another frame.
func TestNoBoxIsKeptForARefusedSegmentation(t *testing.T) {
	none := newPhotoRig(t, "electronics-line-demo")
	g, err := none.run(t, context.Background())
	if err != nil || g.outcome.Status != review.NeedsInput {
		t.Fatalf("no box: %v %v", g.outcome.Status, err)
	}
	if s := storedPhotoMetadata(t, g); strings.Contains(string(s), `"boxes"`) || !strings.Contains(string(s), `"segments":0`) {
		t.Errorf("no box: %s", s)
	}

	many := newPhotoRig(t, "electronics-line-demo")
	var segs []provider.Segment
	for i := range review.MaxPhotoSegments + 1 {
		segs = append(segs, provider.Segment{ObjectID: i, Box: provider.Box{X1: i, Y1: 10, X2: i + 5, Y2: 20}, Confidence: 0.8})
	}
	many.seg.Script("conveyor belt", 1200, 520, segs...)
	g, err = many.run(t, context.Background())
	if err != nil || g.outcome.Status != review.Invalid {
		t.Fatalf("too many: %v %v", g.outcome.Status, err)
	}
	if s := storedPhotoMetadata(t, g); strings.Contains(string(s), `"boxes"`) || !strings.Contains(string(s), `"segments":65`) {
		t.Errorf("too many: %s", s)
	}

	im := store.Image{Width: 1200, Height: 520}
	one := []provider.Segment{{Concept: "arrow", ObjectID: 7, Box: provider.Box{X1: 1, Y1: 2, X2: 3, Y2: 4}, Confidence: 0.5}}
	if got := keptBoxes(&provider.Segmentation{Width: 1200, Height: 520, Segments: one}, im); !slices.Equal(got, []photoBox{{Concept: "arrow", X1: 1, Y1: 2, X2: 3, Y2: 4, Confidence: 0.5}}) {
		t.Errorf("one box: %+v", got)
	}
	for name, s := range map[string]*provider.Segmentation{
		"no segmentation": nil,
		"no segment":      {Width: 1200, Height: 520},
		"a turned frame":  {Width: 520, Height: 1200, Segments: one},
		"another frame":   {Width: 600, Height: 260, Segments: one},
	} {
		if got := keptBoxes(s, im); got != nil {
			t.Errorf("%s: %+v", name, got)
		}
	}
}
