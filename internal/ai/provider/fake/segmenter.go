package fake

import (
	"context"
	"slices"
	"sync"

	"github.com/kcl1104/geminus/internal/ai/provider"
)

// AcceptsImages: the fake takes a prompt's images (T-055) and ignores
// them, so the photo path (T-056) runs end to end on fakes; the request it
// records holds them.
func (b *Backend) AcceptsImages() bool { return true }

var _ provider.ImageInput = (*Backend)(nil)

// Segmenter is the deterministic fake segmentation backend (T-055, for
// T-056's tests and E2E): it answers each concept from its script, finds
// nothing for any other, and does no I/O. Its answers go through
// provider.SegmentClient like any backend's, so a scripted box outside the
// image is refused there.
type Segmenter struct {
	mu     sync.Mutex
	script map[string]segScript
	calls  []provider.SegmentCall
}

type segScript struct {
	width, height int
	segments      []provider.Segment
	err           error
}

var _ provider.SegmentBackend = (*Segmenter)(nil)

// NewSegmenter returns an empty fake segmenter.
func NewSegmenter() *Segmenter { return &Segmenter{script: map[string]segScript{}} }

func (s *Segmenter) Name() string { return "fake" }

// Script makes concept answer segments in a width x height frame (each
// segment's Concept is set to concept).
func (s *Segmenter) Script(concept string, width, height int, segments ...provider.Segment) {
	out := slices.Clone(segments)
	for i := range out {
		out[i].Concept = concept
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.script[concept] = segScript{width: width, height: height, segments: out}
}

// Fail makes concept fail with err.
func (s *Segmenter) Fail(concept string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.script[concept] = segScript{err: err}
}

// Calls returns the calls seen so far, in order.
func (s *Segmenter) Calls() []provider.SegmentCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.calls)
}

// SegmentOne answers call from the script.
func (s *Segmenter) SegmentOne(ctx context.Context, call provider.SegmentCall) (provider.SegmentAnswer, error) {
	if err := ctx.Err(); err != nil {
		return provider.SegmentAnswer{}, provider.NewError(provider.CodeCancelled, 0, "").WithCause(err)
	}
	s.mu.Lock()
	s.calls = append(s.calls, call)
	sc, scripted := s.script[call.Concept]
	s.mu.Unlock()
	if scripted && sc.err != nil {
		return provider.SegmentAnswer{Model: "fake"}, sc.err
	}
	// No images billed: the fake bills nothing (as its LLM backend counts
	// no tokens).
	return provider.SegmentAnswer{Model: "fake", Answered: true, Width: sc.width, Height: sc.height, Segments: slices.Clone(sc.segments)}, nil
}
