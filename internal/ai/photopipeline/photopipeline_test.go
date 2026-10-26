package photopipeline_test

import (
	"context"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/photopipeline"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/domainpack/electronicsline"
	"github.com/kcl1104/geminus/internal/domainpack/softwaredelivery"
)

// The pipeline's own checks (its runs, on fakes and on a local server of
// the Meta APIs, are internal/proposals/generator's photo tests, the DB
// tests of internal/api and the photo smoke's tests in internal/aievals
// and internal/ai/provider/meta): the pack's concepts, whether the vision
// client takes images, and no call without both clients.
func TestThePipelinesChecks(t *testing.T) {
	if got := photopipeline.Concepts(electronicsline.Pack{}); !slices.Equal(got, electronicsline.Pack{}.PhotoConcepts()) || len(got) != 8 {
		t.Errorf("electronics line: %v", got)
	}
	if got := photopipeline.Concepts(softwaredelivery.Pack{}); got != nil {
		t.Errorf("software delivery: %v", got)
	}
	takes, err := provider.New(fake.New(), provider.DefaultBudget())
	if err != nil {
		t.Fatal(err)
	}
	off, err := provider.New(provider.Disabled{Code: provider.CodeNotConfigured}, provider.DefaultBudget())
	if err != nil {
		t.Fatal(err)
	}
	if !photopipeline.TakesImages(takes) || photopipeline.TakesImages(off) || photopipeline.TakesImages(nil) {
		t.Error("TakesImages")
	}
	seg := fake.NewSegmenter()
	client, err := provider.NewSegmentClient(seg, provider.DefaultBudget())
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]photopipeline.Clients{"no vision model": {Segmenter: client}, "no segmenter": {Vision: takes}} {
		r, err := photopipeline.Propose(context.Background(), c, photopipeline.Request{}, photopipeline.Observer{})
		if provider.CodeOf(err) != provider.CodeNotConfigured || r.Segmenter != nil || r.Vision != nil || len(seg.Calls()) != 0 {
			t.Errorf("%s: %+v %v", name, r, err)
		}
	}
}
