// Package fake is the deterministic fake segmenter for tests, E2E and the
// development deployment's fake AI mode (T-056): never a fallback for a
// live segmenter. It answers from a script keyed by the image's SHA-256;
// without a script it finds nothing (no box), so the pipeline asks for
// input instead of inventing a station. No randomness, no clock, no I/O.
// Its answers go through vision.Client like any other segmenter's.
package fake

import (
	"context"
	"slices"
	"sync"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/vision"
)

type answer struct {
	boxes []vision.Box
	err   error
}

// Backend is the fake. The zero value is not usable; call New.
type Backend struct {
	mu     sync.Mutex
	script map[string]answer
	seen   []vision.Request
}

var _ vision.Segmenter = (*Backend)(nil)

// New returns an empty fake.
func New() *Backend { return &Backend{script: map[string]answer{}} }

func (b *Backend) Name() string { return "fake" }

// Script makes the fake answer boxes for the image whose SHA-256 is sha.
func (b *Backend) Script(sha string, boxes []vision.Box) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.script[sha] = answer{boxes: slices.Clone(boxes)}
}

// Fail makes the fake fail with err for the image whose SHA-256 is sha.
func (b *Backend) Fail(sha string, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.script[sha] = answer{err: err}
}

// Requests returns the requests seen so far, in order.
func (b *Backend) Requests() []vision.Request {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.seen)
}

// Segment answers req. It bills nothing.
func (b *Backend) Segment(ctx context.Context, req vision.Request) (vision.Response, error) {
	if err := ctx.Err(); err != nil {
		return vision.Response{}, provider.NewError(provider.CodeCancelled, 0, "").WithCause(err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seen = append(b.seen, req)
	a := b.script[req.Image.SHA256]
	if a.err != nil {
		return vision.Response{Model: "fake"}, a.err
	}
	return vision.Response{Boxes: slices.Clone(a.boxes), Model: "fake"}, nil
}
