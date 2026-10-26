package electronicsline_test

import (
	"regexp"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/domainpack/electronicsline"
)

// T-056 (H-033): the pack names what a segmenter looks for in a photo of
// the line: a machine for each station of §2, the conveyor between them,
// then the generic words; short, distinct, plain text, in a fixed order
// (a box names its prompt by index), and returned as a copy.
func TestPhotoPrompts(t *testing.T) {
	var p domainpack.PhotoVocabulary = electronicsline.Pack{}
	prompts := p.PhotoPrompts()
	want := []string{"SMT line", "AOI machine", "ICT tester", "FCT tester", "burn-in chamber", "debug workstation", "packing station",
		"conveyor", "workstation", "machine"}
	if !slices.Equal(prompts, want) {
		t.Fatalf("%q", prompts)
	}
	plain := regexp.MustCompile(`^[A-Za-z][A-Za-z -]{0,63}$`)
	seen := map[string]bool{}
	for _, s := range prompts {
		if !plain.MatchString(s) || seen[s] {
			t.Errorf("%q is not a short, distinct prompt", s)
		}
		seen[s] = true
	}
	prompts[0] = "changed"
	if p.PhotoPrompts()[0] != "SMT line" {
		t.Error("the caller changed the pack's prompts")
	}
}
