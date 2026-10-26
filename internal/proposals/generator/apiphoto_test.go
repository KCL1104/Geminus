package generator

import (
	"testing"

	"github.com/kcl1104/geminus/internal/proposals"
)

// The T-056 follow-up (D-124): when the photo path is off, the API refuses
// a photo up front with the code this handler would fail it with, so a
// client sees the same typed code either way.
func TestTheAPIRefusesAPhotoWithTheWorkersCode(t *testing.T) {
	if proposals.CodeImageUnsupported != ImageUnsupported {
		t.Errorf("the API refuses with %q, the worker fails with %q", proposals.CodeImageUnsupported, ImageUnsupported)
	}
}
