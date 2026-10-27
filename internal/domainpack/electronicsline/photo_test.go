package electronicsline_test

import (
	"regexp"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/domainpack/electronicsline"
)

// T-056 (H-033): the pack names what a segmenter looks for in a photo or
// floor plan of the line: a machine of the stations of §2 and the conveyor
// between them, for a photo; and, after the first live run
// (01M5XDJ3T0TTGE55K3A3JVK29N: SAM 3.1 found none of the machine concepts
// in the drawn demo floor plan), the shapes a drawn floor plan is made of,
// a rectangle for a station and an arrow for the flow (§13; D-131 (b)).
// Distinct concepts as T-055's segmentation request takes them (at most 8
// lowercase noun phrases of at most 64 bytes: one SAM image each, so the
// segmentation still costs at most 8 images), in a fixed order, and
// returned as a copy.
func TestPhotoConcepts(t *testing.T) {
	var p domainpack.PhotoVocabulary = electronicsline.Pack{}
	concepts := p.PhotoConcepts()
	want := []string{"smt line", "aoi machine", "ict tester", "fct tester", "packing station", "conveyor belt", "rectangle", "arrow"}
	if !slices.Equal(concepts, want) {
		t.Fatalf("%q", concepts)
	}
	// provider.SegmentClient's rule (T-055), restated: the pack may not
	// import the AI packages; the generator's tests send these concepts
	// through the client itself.
	concept := regexp.MustCompile(`^[a-z0-9]+(?:[ -][a-z0-9]+)*$`)
	seen := map[string]bool{}
	if len(concepts) > 8 {
		t.Errorf("%d concepts", len(concepts))
	}
	for _, s := range concepts {
		if len(s) > 64 || !concept.MatchString(s) || seen[s] {
			t.Errorf("%q is not a short, distinct concept", s)
		}
		seen[s] = true
	}
	concepts[0] = "changed"
	if p.PhotoConcepts()[0] != "smt line" {
		t.Error("the caller changed the pack's concepts")
	}
}
