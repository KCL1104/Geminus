package packs

import (
	"slices"
	"testing"
)

// The deployment serves the software delivery pack; every call builds the
// same registry.
func TestRegistry(t *testing.T) {
	first, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	second, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	if got := first.Domains(); !slices.Equal(got, []string{"software_delivery"}) || !slices.Equal(got, second.Domains()) {
		t.Errorf("domains %v, then %v", got, second.Domains())
	}
	for _, d := range first.Domains() {
		if p, err := first.Lookup(d); err != nil || p.ID() != d {
			t.Errorf("%s: %v %v", d, p, err)
		}
	}
}
