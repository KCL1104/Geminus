package fake_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/vision"
	"github.com/kcl1104/geminus/internal/ai/vision/fake"
)

const org = "org_01M4YDQK00TA00000000000000"

func image(data string) provider.Image {
	s := sha256.Sum256([]byte(data))
	return provider.Image{MediaType: "image/png", Data: []byte(data), Width: 200, Height: 100, SHA256: "sha256:" + hex.EncodeToString(s[:])}
}

// The fake segmenter answers from a script keyed by the image's SHA-256,
// and finds nothing in an image it has no script for (the safe answer: no
// box, so no station is ever invented). Its answers go through the
// checked client like any segmenter's.
func TestFake(t *testing.T) {
	ctx := context.Background()
	b := fake.New()
	im, other := image("line"), image("another")
	b.Script(im.SHA256, []vision.Box{{Prompt: 0, X: 1, Y: 2, Width: 30, Height: 40, Score: 0.8, Label: "SMT"}})
	b.Fail(other.SHA256, provider.NewError(provider.CodeRateLimited, 429, "fake"))
	c, err := vision.New(b, vision.DefaultBudget())
	if err != nil {
		t.Fatal(err)
	}
	in := vision.Input{OrganizationID: org, Image: im, Prompts: []string{"SMT line"}}
	res, err := c.Segment(ctx, in)
	if err != nil || len(res.Boxes) != 1 || res.Boxes[0].Label != "SMT" || res.Metadata.Provider != "fake" || res.Metadata.Model != "fake" {
		t.Fatalf("scripted: %+v %v", res, err)
	}
	in.Image = other
	if _, err := c.Segment(ctx, in); provider.CodeOf(err) != provider.CodeRateLimited {
		t.Errorf("scripted failure: %v", err)
	}
	in.Image = image("unscripted")
	if res, err := c.Segment(ctx, in); err != nil || len(res.Boxes) != 0 {
		t.Errorf("unscripted: %+v %v", res, err)
	}
	if got := b.Requests(); len(got) != 3 || got[0].Image.SHA256 != im.SHA256 {
		t.Errorf("requests %+v", got)
	}
	// The script is a copy: changing the boxes afterwards changes nothing.
	boxes := []vision.Box{{Prompt: 0, Width: 1, Height: 1, Score: 1}}
	b.Script(im.SHA256, boxes)
	boxes[0].X = 1e9
	in.Image = im
	if _, err := c.Segment(ctx, in); err != nil {
		t.Errorf("the script was shared: %v", err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := b.Segment(cctx, vision.Request{Image: im}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
}
