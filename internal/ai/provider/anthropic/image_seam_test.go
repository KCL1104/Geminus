package anthropic

import (
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
)

// T-056 (H-033: Claude's image input is the switchable fallback of the
// photo pipeline): this reviewed adapter does not send images yet, so it is
// not a provider.ImageBackend, and provider.Client refuses an image request
// to it with image_unsupported before any reservation or I/O
// (provider/image_test.go). Wiring the fallback is a reviewed change that
// makes this test fail on purpose: Complete sends req.Image as an image
// content block before the text, Quote bounds the image's input tokens,
// and the approval record's data policy names demo images.
func TestTheAdapterSendsNoImageYet(t *testing.T) {
	var b any = &Backend{}
	if _, ok := b.(provider.ImageBackend); ok {
		t.Fatal("the Anthropic adapter takes images: check its Complete, Quote and the approval record (T-056 seam)")
	}
}
