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
//  4. The server reviews the answer as any edit (review.Review), then by
//     the photo rules (review.Photo), then by the caller's later stage
//     (Request.Bind: the worker's dataset binding stage).
//  5. An answer the server refused for what it says gets one repair
//     (H-057, internal/ai/repair): the vision model is asked once more,
//     with the same image and the same segments (the segmenter is not asked
//     again), its own answer and the server's issues; the same checks of
//     step 4 decide the outcome.
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
	"github.com/kcl1104/geminus/internal/ai/repair"
	"github.com/kcl1104/geminus/internal/ai/review"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/model"
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
	// Bind, when set, is the caller's later stage on a candidate the
	// review and the photo rules found ready (the worker's dataset binding
	// stage, over the snapshots it read before any call; H-057): its issues
	// refuse the outcome or stay with it as warnings (review.Outcome.Refuse),
	// before a repair is considered; its error ends Propose (a *BindError).
	Bind func(candidate *model.ModelIR) ([]model.ValidationIssue, error)
}

// Observer is told of each call's end (the worker's log lines); either may
// be nil. Answered's call is 1 for the vision model's first call, 2 for its
// repair (H-057).
type Observer struct {
	Segmented func(m provider.SegmentMetadata, segments int, err error)
	Answered  func(call int, m provider.Metadata, err error)
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
	// Vision is the vision model's first call once it was asked (also when
	// it failed).
	Vision *provider.Metadata
	// Repair is what the one repair did (H-057), when the server refused the
	// first answer for what it says; nil when there was none. Its Call is the
	// vision model's second call.
	Repair *repair.Record
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

// BindError: the caller's later stage (Request.Bind) failed: a fault of
// the caller's own, never the provider's.
type BindError struct{ Err error }

func (e *BindError) Error() string { return "photopipeline: the later stage: " + e.Err.Error() }
func (e *BindError) Unwrap() error { return e.Err }

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
// vision model, the server's review and the one repair (steps 2 to 5). A
// provider failure of either client is its *provider.Error (a cancellation
// is the client's cancelled error: the caller tells it by its own context),
// a context that cannot be built a *ContextError, a review that cannot run
// a *ReviewError, a later stage that fails a *BindError; the Result says
// which calls were made either way. An answer the server refuses is an
// outcome (invalid), not an error; so is a repair call that fails (the
// first outcome stays, Result.Repair says so).
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
		obs.Answered(1, meta, err)
	}
	// check is the server's checks of a schema-valid answer: the review, the
	// photo rules, the caller's later stage. The repair's answer goes through
	// exactly these.
	check := func(out provider.ProposalOutput) (review.Outcome, error) {
		outcome, err := review.Review(review.Input{Mode: provider.ModeEdit, Base: req.BaseIR, Pool: req.Pool.IDs, Pack: req.Pack, Output: out})
		if err != nil {
			return review.Outcome{}, &ReviewError{Err: err}
		}
		if outcome.Status == review.Ready {
			outcome = outcome.Refuse(review.Photo(req.Base.IR, outcome.Candidate, len(seg.Segments)))
		}
		if outcome.Status == review.Ready && req.Bind != nil {
			issues, err := req.Bind(outcome.Candidate)
			if err != nil {
				return review.Outcome{}, &BindError{Err: err}
			}
			outcome = outcome.Refuse(issues)
		}
		return outcome, nil
	}
	var outcome review.Outcome
	if err != nil {
		var pe *provider.Error
		if ctx.Err() != nil || !errors.As(err, &pe) || pe.Code != provider.CodeInvalidOutput {
			return r, err
		}
		outcome = review.InvalidOutput(pe.Issues)
	} else if outcome, err = check(res.Output); err != nil {
		return r, err
	}
	// H-057: one repair of an answer refused for what it says, on the same
	// image and segments; the segmenter is not asked again.
	answered := func(m provider.Metadata, err error) {
		if obs.Answered != nil {
			obs.Answered(2, m, err)
		}
	}
	outcome, r.Repair, err = repair.Once(ctx, c.Vision, pctx, res.Prior, outcome, check, answered)
	if err != nil {
		var ae *aicontext.Error
		if errors.As(err, &ae) {
			return r, &ContextError{Code: ae.Code, Err: err}
		}
		return r, err
	}
	r.Outcome = outcome
	return r, nil
}
