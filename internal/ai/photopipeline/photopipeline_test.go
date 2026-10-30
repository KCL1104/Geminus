package photopipeline_test

import (
	"context"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/photopipeline"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/contracttest"
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

// imageOnly takes images but asks its own model only (no ModelChooser).
type imageOnly struct{ b *fake.Backend }

func (i imageOnly) Name() string        { return i.b.Name() }
func (i imageOnly) AcceptsImages() bool { return true }
func (i imageOnly) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	return i.b.Complete(ctx, req)
}

// H-049: the vision model the person chose goes with the vision call; a
// vision client that cannot ask it is refused before the segmenter spends
// anything (model_not_approved).
func TestThePipelineAsksTheChosenVisionModel(t *testing.T) {
	pack := electronicsline.Pack{}
	img, err := provider.NewImage(provider.ImagePNG, contracttest.LoadPhotoDemo(t, "electronics-line-demo").Image)
	if err != nil {
		t.Fatal(err)
	}
	req := photopipeline.Request{OrganizationID: "org_01M4YDQK00TA00000000000000", Pack: pack, Concepts: pack.PhotoConcepts(), Image: img,
		Model: "muse-wonder-1.0"}
	seg := fake.NewSegmenter()
	segClient, err := provider.NewSegmentClient(seg, provider.DefaultBudget())
	if err != nil {
		t.Fatal(err)
	}
	only, err := provider.New(imageOnly{b: fake.New()}, provider.DefaultBudget())
	if err != nil {
		t.Fatal(err)
	}
	if !photopipeline.TakesModel(only, "") || photopipeline.TakesModel(only, "muse-wonder-1.0") {
		t.Error("TakesModel of a client without a model choice")
	}
	r, err := photopipeline.Propose(context.Background(), photopipeline.Clients{Segmenter: segClient, Vision: only}, req, photopipeline.Observer{})
	if provider.CodeOf(err) != provider.CodeModelNotApproved || r.Segmenter != nil || len(seg.Calls()) != 0 {
		t.Errorf("a vision client that cannot ask the model: %+v %v, %d segmenter calls", r, err, len(seg.Calls()))
	}
	// The fake asks a chosen model (the vision call carrying it is the
	// generator's photo tests', on a real base).
	visionClient, err := provider.New(fake.New(), provider.DefaultBudget())
	if err != nil {
		t.Fatal(err)
	}
	if !photopipeline.TakesModel(visionClient, "muse-wonder-1.0") || !photopipeline.TakesModel(visionClient, "") {
		t.Error("TakesModel of the fake")
	}
}

// A segmenter that fails is the call's error, and the result still has the
// segmentation's metadata (what it spent: the worker audits it with the
// attempt's failure, the smoke reports it); the vision model is not asked.
func TestASegmenterFailureKeepsItsMetadata(t *testing.T) {
	seg := fake.NewSegmenter()
	pack := electronicsline.Pack{}
	for _, c := range pack.PhotoConcepts() {
		seg.Fail(c, provider.NewError(provider.CodeRateLimited, 429, "meta"))
	}
	segClient, err := provider.NewSegmentClient(seg, provider.DefaultBudget())
	if err != nil {
		t.Fatal(err)
	}
	vision := fake.New()
	visionClient, err := provider.New(vision, provider.DefaultBudget())
	if err != nil {
		t.Fatal(err)
	}
	img, err := provider.NewImage(provider.ImagePNG, contracttest.LoadPhotoDemo(t, "electronics-line-demo").Image)
	if err != nil {
		t.Fatal(err)
	}
	var segmented int
	r, err := photopipeline.Propose(context.Background(), photopipeline.Clients{Segmenter: segClient, Vision: visionClient}, photopipeline.Request{
		OrganizationID: "org_01M4YDQK00TA00000000000000", Pack: pack, Concepts: pack.PhotoConcepts(), Image: img,
	}, photopipeline.Observer{Segmented: func(provider.SegmentMetadata, int, error) { segmented++ }})
	if provider.CodeOf(err) != provider.CodeRateLimited || r.Segmenter == nil || r.Segmenter.Concepts != 8 || r.Segmentation != nil || r.Vision != nil ||
		segmented != 1 || len(vision.Requests()) != 0 {
		t.Errorf("%+v %v (segmented %d)", r, err, segmented)
	}
}
