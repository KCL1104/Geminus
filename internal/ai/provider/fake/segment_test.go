package fake_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/locale"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
)

// T-055: the fake segmenter for T-056's tests and E2E: scripted boxes per
// concept, nothing found for any other, no I/O; its answers go through
// provider.SegmentClient like any backend's. The fake LLM backend takes a
// prompt's images (and ignores them), so the photo path runs end to end on
// fakes.

func testImage(t *testing.T, w, h int) provider.Image {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	im, err := provider.NewImage(provider.ImagePNG, b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return im
}

func TestTheFakeSegmenter(t *testing.T) {
	s := fake.NewSegmenter()
	s.Script("machine", 64, 48, provider.Segment{ObjectID: 1, Box: provider.Box{X1: 2, Y1: 3, X2: 20, Y2: 30}, Confidence: 0.9},
		provider.Segment{ObjectID: 0, Box: provider.Box{X1: 30, Y1: 3, X2: 40, Y2: 30}, Confidence: 0.75})
	boom := provider.NewError(provider.CodeUnavailable, 0, "")
	s.Fail("robot", boom)
	c, err := provider.NewSegmentClient(s, provider.DefaultBudget())
	if err != nil {
		t.Fatal(err)
	}
	img := testImage(t, 64, 48)
	req := provider.SegmentRequest{OrganizationID: org, Role: provider.RoleModeler, Image: img, Concepts: []string{"machine", "conveyor"}}
	res, err := c.Segment(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Width != 64 || res.Height != 48 || len(res.Segments) != 2 || res.Segments[0].ObjectID != 0 || res.Segments[0].Concept != "machine" ||
		res.Metadata.Provider != "fake" || res.Metadata.Model != "fake" {
		t.Errorf("result %+v", res)
	}
	if calls := s.Calls(); len(calls) != 2 || calls[1].Concept != "conveyor" || calls[0].Image.Digest != img.Digest {
		t.Errorf("calls %+v", calls)
	}
	req.Concepts = []string{"robot"}
	if _, err := c.Segment(context.Background(), req); !errors.Is(err, boom) {
		t.Errorf("scripted failure: %v", err)
	}
	// A scripted box outside the image is still refused by the client.
	s.Script("conveyor", 64, 48, provider.Segment{Box: provider.Box{X1: 0, Y1: 0, X2: 64, Y2: 1}, Confidence: 0.5})
	req.Concepts = []string{"conveyor"}
	if _, err := c.Segment(context.Background(), req); provider.CodeOf(err) != provider.CodeInvalidOutput {
		t.Errorf("a box outside the image: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.SegmentOne(ctx, provider.SegmentCall{Concept: "machine"}); provider.CodeOf(err) != provider.CodeCancelled {
		t.Errorf("cancelled: %v", err)
	}
}

func TestTheFakeTakesImages(t *testing.T) {
	b := fake.New()
	p, err := provider.Seal(provider.Prompt{OrganizationID: org, Role: provider.RoleModeler, TemplateVersion: "t", Locale: locale.English, System: "rules",
		Blocks: []provider.Block{{Kind: provider.UntrustedData, Name: fake.IntentBlock, Content: quote("draw the line")}},
		Images: []provider.Image{testImage(t, 8, 8)}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := client(t, b).GenerateModelProposal(context.Background(), provider.ProposalContext{OrganizationID: org, Mode: provider.ModeEdit, Prompt: p})
	if err != nil || res.Output.Status != "needs_input" {
		t.Fatalf("%+v %v", res.Output, err)
	}
	if seen := b.Requests(); len(seen) != 1 || len(seen[0].Prompt.Images) != 1 {
		t.Errorf("requests %+v", seen)
	}
}
