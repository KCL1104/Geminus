package electronicsline_test

import (
	"regexp"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/domainpack/electronicsline"
)

// T-056 (H-033): the pack names what a segmenter looks for in a photo of
// the line: a machine for each station of §2, the conveyor between them
// and the workstation of a manual step; distinct concepts as T-055's
// segmentation request takes them (at most 8 lowercase noun phrases of at
// most 64 bytes), in a fixed order, and returned as a copy.
func TestPhotoConcepts(t *testing.T) {
	var p domainpack.PhotoVocabulary = electronicsline.Pack{}
	concepts := p.PhotoConcepts()
	want := []string{"smt line", "aoi machine", "ict tester", "fct tester", "burn-in chamber", "packing station", "conveyor belt", "workstation"}
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
