package anthropic

import (
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
)

// T-056 (H-033: Claude's image input is the switchable fallback of the
// photo pipeline): this reviewed adapter does not send images yet, so it is
// not a provider.ImageInput (T-055), and provider.Client refuses a prompt
// with images to it (invalid_request) before any reservation or I/O
// (provider/prompt_images_test.go); the photo generator checks first and
// fails the job with AI_IMAGE_UNSUPPORTED before the segmenter spends
// anything. Wiring the fallback is a reviewed change that makes this test
// fail on purpose: Complete sends the prompt's Images as image content
// blocks before the text, Quote bounds their input tokens, AcceptsImages
// reads the approval record, and the record's data policy names demo
// images.
func TestTheAdapterSendsNoImageYet(t *testing.T) {
	var b any = &Backend{}
	if _, ok := b.(provider.ImageInput); ok {
		t.Fatal("the Anthropic adapter takes images: check its Complete, Quote and the approval record (T-056 seam)")
	}
}
