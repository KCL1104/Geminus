// Package photopipeline is the AI part of the photo pipeline (T-056, H-033;
// PROPOSED D-131): what a photo proposal does between the server's check of
// its stored image and the dataset binding stage. The worker's
// model_proposal handler (internal/proposals/generator) runs it on every
// photo proposal, and the live photo smoke (cmd/ai-eval-live -smoke-photo,
// T-056 review round 1) on the demo image, so the first live SAM 3.1 call
// goes through the code the worker runs, not a copy:
//
//  1. Concepts: what the model's pack names for a segmenter to look for
//     (domainpack.PhotoVocabulary); none means the domain takes no photo.
//     TakesImages: whether the vision client takes images
//     (provider.Client.AcceptsImages), asked before anything is spent.
//  2. Propose: the segmenter (T-055's provider.SegmentClient; live, every
//     concept reserved for before its call) finds the concepts; the client
//     checks every answer, and the pipeline takes at most
//     review.MaxPhotoSegments in the image's own frame. A segmentation the
//     server refuses is invalid; no segment is the server's own needs_input
//     (the vision model is never asked).
//  3. The vision model (T-055's judge: Muse Spark; live, a request with an
//     image is counted with count_tokens and reserved at the count and 10%
//     before it is sent, H-037) proposes the stations and flow from the
//     image and the segments (aicontext.ImageProposal).
//  4. The server reviews the answer as any edit (review.Review) and then by
//     the photo rules (review.Photo).
//
// It does no I/O of its own and keeps nothing: the callers read the image,
// write the outcome and record what was spent.
package photopipeline

import (
	"context"
	"errors"

	"github.com/kcl1104/geminus/internal/ai/aicontext"
	"github.com/kcl1104/geminus/internal/ai/locale"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/review"
	"github.com/kcl1104/geminus/internal/domainpack"
)

// Concepts is what pack names for a segmenter to look for in a photo, or
// nil when it names nothing (the domain takes no photo).
func Concepts(pack domainpack.DomainPack) []string {
	vocab, ok := pack.(domainpack.PhotoVocabulary)
	if !ok {
		return nil
	}
	if cs := vocab.PhotoConcepts(); len(cs) > 0 {
		return cs
	}
	return nil
}

// TakesImages reports whether the vision client hands a prompt's images to
// its backend (provider.Client.AcceptsImages): asked before the segmenter
// spends anything.
func TakesImages(vision provider.LLMProvider) bool {
	it, ok := vision.(interface{ AcceptsImages() bool })
	return ok && it.AcceptsImages()
}

// TakesModel reports whether the vision client asks model for a photo
// proposal (provider.Client.AcceptsModel, H-049); "" is its own model.
// Asked before the segmenter spends anything.
func TakesModel(vision provider.LLMProvider, model string) bool {
	if model == "" {
		return true
	}
	m, ok := vision.(interface {
		AcceptsModel(provider.Role, string) bool
	})
	return ok && m.AcceptsModel(provider.ModeEdit.Role(), model)
}

// Clients are the photo path's two clients.
type Clients struct {
	Segmenter provider.Segmenter
	Vision    provider.LLMProvider
}

// Request is one photo proposal, its image read and checked against its
// record by the caller.
type Request struct {
	OrganizationID string
	// Base is the base version, its IR decoded; BaseIR the version's
	// canonical IR (what the review applies the patch to).
	Base   aicontext.Version
	BaseIR []byte
	Pack   domainpack.DomainPack
	// Concepts are Concepts(Pack).
	Concepts     []string
	Pool         aicontext.Pool
	Image        provider.Image
	OutputLocale string
	Budget       provider.Budget
	Attribution  provider.Attribution
	// Model is the vision model the person chose (H-049); "" for the
	// vision client's own.
	Model string
}

// Observer is told of each call's end (the worker's log lines); either may
// be nil.
type Observer struct {
	Segmented func(m provider.SegmentMetadata, segments int, err error)
	Answered  func(m provider.Metadata, err error)
}

// Result is what a photo proposal's AI part produced.
type Result struct {
	// Outcome is the server's outcome, when Propose returns no error.
	Outcome review.Outcome
	// Segmenter is the segmentation's metadata once the segmenter was asked
	// (also when it failed); Segmentation its checked answer (nil when it
	// failed); Segments how many objects that answer has.
	Segmenter    *provider.SegmentMetadata
	Segmentation *provider.Segmentation
	Segments     int
	// Vision is the vision model's call once it was asked (also when it
	// failed).
	Vision *provider.Metadata
}

// ContextError: the proposal's context could not be built
// (aicontext.ImageProposal); Code is aicontext's.
type ContextError struct {
	Code aicontext.Code
	Err  error
}

func (e *ContextError) Error() string {
	return "photopipeline: the photo proposal's context: " + string(e.Code)
}
func (e *ContextError) Unwrap() error { return e.Err }

// ReviewError: the server could not review the answer against the base (a
// fault of the server's own, never the provider's).
type ReviewError struct{ Err error }

func (e *ReviewError) Error() string { return "photopipeline: the review: " + e.Err.Error() }
func (e *ReviewError) Unwrap() error { return e.Err }

// noBoxes is the server's request for input when the segmenter found
// nothing: the photo shows no station to propose.
func noBoxes(outputLocale string) review.Outcome {
	summary := "The segmenter found no machine or conveyor, and no drawn station or arrow, in the image, so no station can be proposed from it."
	question := "Which stations does the line have, in which order? Upload a clearer photo or floor plan of the line, or describe it in words."
	if outputLocale == string(locale.TraditionalChinese) {
		summary = "影像辨識在這張影像中找不到機台、輸送帶，也找不到畫出的工作站或箭頭，無法從影像提出工作站。"
		question = "這條產線有哪些工作站、順序為何？請上傳更清楚的產線照片或平面圖，或以文字描述產線。"
	}
	return review.Outcome{Status: review.NeedsInput, Summary: summary, Assumptions: []string{}, Questions: []string{question}}
}

// Propose runs the segmenter, the server's checks of its answer, the
// vision model and the server's review (steps 2 to 4). A provider failure
// of either client is its *provider.Error (a cancellation is the client's
// cancelled error: the caller tells it by its own context), a context that
// cannot be built a *ContextError, a review that cannot run a
// *ReviewError; the Result says which calls were made either way. An
// answer the server refuses is an outcome (invalid), not an error.
func Propose(ctx context.Context, c Clients, req Request, obs Observer) (Result, error) {
	var r Result
	if c.Segmenter == nil || c.Vision == nil {
		return r, provider.NewError(provider.CodeNotConfigured, 0, "")
	}
	if !TakesModel(c.Vision, req.Model) {
		// A vision model the client may not ask (H-049): refused before the
		// segmenter spends anything.
		return r, provider.NewError(provider.CodeModelNotApproved, 0, "")
	}
	seg, err := c.Segmenter.Segment(ctx, provider.SegmentRequest{OrganizationID: req.OrganizationID, Role: provider.ModeEdit.Role(),
		Attribution: req.Attribution, Image: req.Image, Concepts: req.Concepts})
	r.Segmenter, r.Segments = &seg.Metadata, len(seg.Segments)
	if obs.Segmented != nil {
		obs.Segmented(seg.Metadata, len(seg.Segments), err)
	}
	if err != nil {
		var pe *provider.Error
		if ctx.Err() == nil && errors.As(err, &pe) && pe.Code == provider.CodeInvalidOutput {
			r.Outcome = review.InvalidOutput(review.Segmentation(pe.Issues))
			return r, nil
		}
		return r, err
	}
	r.Segmentation = &seg
	switch {
	case len(seg.Segments) == 0:
		r.Outcome = noBoxes(req.OutputLocale)
		return r, nil
	case len(seg.Segments) > review.MaxPhotoSegments:
		r.Outcome = review.InvalidOutput(review.TooManySegments())
		return r, nil
	case seg.Width != req.Image.Width || seg.Height != req.Image.Height:
		r.Outcome = review.InvalidOutput(review.FrameMismatch())
		return r, nil
	}

	pctx, err := aicontext.ImageProposal(aicontext.ImageProposalInput{
		Scope:        aicontext.Scope{OrganizationID: req.OrganizationID, ModelID: req.Base.ModelID},
		Base:         req.Base,
		Domain:       req.Pack.PromptContext(),
		Pool:         req.Pool,
		Photo:        aicontext.Photo{OrganizationID: req.OrganizationID, Image: req.Image, Concepts: req.Concepts, Segments: seg.Segments},
		OutputLocale: req.OutputLocale,
		Budget:       req.Budget,
	})
	if err != nil {
		return r, &ContextError{Code: aicontext.CodeOf(err), Err: err}
	}
	pctx.Attribution, pctx.Model = req.Attribution, req.Model
	res, err := c.Vision.GenerateModelProposal(ctx, pctx)
	meta := res.Metadata
	r.Vision = &meta
	if obs.Answered != nil {
		obs.Answered(meta, err)
	}
	if err != nil {
		var pe *provider.Error
		if ctx.Err() == nil && errors.As(err, &pe) && pe.Code == provider.CodeInvalidOutput {
			r.Outcome = review.InvalidOutput(pe.Issues)
			return r, nil
		}
		return r, err
	}
	outcome, err := review.Review(review.Input{Mode: provider.ModeEdit, Base: req.BaseIR, Pool: req.Pool.IDs, Pack: req.Pack, Output: res.Output})
	if err != nil {
		return r, &ReviewError{Err: err}
	}
	if outcome.Status == review.Ready {
		outcome = outcome.Refuse(review.Photo(req.Base.IR, outcome.Candidate, len(seg.Segments)))
	}
	r.Outcome = outcome
	return r, nil
}
