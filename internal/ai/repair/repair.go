// Package repair is a proposal's one repair after the server's check
// (H-057, under the owner's stability decision H-055): when the server
// refuses the model's first answer for what it says, the model is asked
// once more, with its own answer and the server's issues, and the server's
// review of that second answer is the proposal's outcome. Both paths that
// ask for a proposal run it: the worker's text proposals
// (internal/proposals/generator) and the photo pipeline
// (internal/ai/photopipeline, which the live photo smoke runs too).
//
//   - A repair follows an invalid outcome with at least one error, when the
//     client handed back the model's answer (provider.ProposalResult.Prior:
//     schema-valid and refused by the review, or refused by the client for
//     what it says). Nothing else is repaired: a truncated or refused
//     answer, another model's, a policy, approval or spend-cap refusal, a
//     provider that is unavailable or timed out, a cancellation, a
//     segmentation the server refused (no model answer at all), and every
//     ready or needs_input outcome.
//   - The second request is aicontext.Revise's: the first prompt, the prior
//     answer and the follow-up listing the refusal's errors, cleaned, at
//     most aicontext.MaxRepairIssues. It goes through the same client as
//     the first: checked, metered (its own reservation), never researched.
//   - Its answer goes through exactly the same checks (Check: the review,
//     and the photo rules and the dataset binding stage where the caller
//     runs them); their outcome is the proposal's, whatever it is. A second
//     call that fails leaves the first outcome, and the Record says so.
//   - At most one: Once never asks a third time.
//
// The answers stay in memory: the Record holds the second call's metadata
// (no prompt, no answer) and what came of it.
package repair

import (
	"context"
	"errors"
	"strings"

	"github.com/kcl1104/geminus/internal/ai/aicontext"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/review"
	"github.com/kcl1104/geminus/internal/model"
)

// What a repair came to (Record.Outcome; the stored metadata's
// repair.outcome, the audit's repair).
const (
	// Ready, NeedsInput and Invalid: the second answer, and the server's
	// review of it, is the outcome.
	Ready      = string(review.Ready)
	NeedsInput = string(review.NeedsInput)
	Invalid    = string(review.Invalid)
	// Failed: the second call failed (Record.Code says how); the outcome is
	// the first one's.
	Failed = "failed"
)

// Record is what one repair did: never an answer or a prompt.
type Record struct {
	// Issues is how many issues the follow-up listed, NotListed how many it
	// left out.
	Issues, NotListed int
	// Outcome is Ready, NeedsInput, Invalid or Failed; "" when the attempt
	// stopped during the second call.
	Outcome string
	// Code is a failed second call's code: "AI_" and the provider client's
	// code in upper case, as a job failure names it.
	Code string
	// Call is the second call's metadata (also when it failed: what it
	// cost).
	Call *provider.Metadata
}

// Revised reports whether the proposal's outcome is the second answer's:
// the second call answered and the server reviewed what it said.
func (r *Record) Revised() bool {
	return r != nil && (r.Outcome == Ready || r.Outcome == NeedsInput || r.Outcome == Invalid)
}

// Check is the server's review of a schema-valid answer, as the caller runs
// it on the first: the review and every later stage. Its error is the
// server's own fault.
type Check func(provider.ProposalOutput) (review.Outcome, error)

// Issues are the errors of a refused outcome a repair lists, in the
// outcome's order, at most aicontext.MaxRepairIssues, and how many it
// leaves out; none for an outcome that is not invalid.
func Issues(o review.Outcome) ([]model.ValidationIssue, int) {
	if o.Status != review.Invalid || o.Validation == nil {
		return nil, 0
	}
	var errs []model.ValidationIssue
	for _, is := range o.Validation.Issues {
		if is.Severity == model.SeverityError {
			errs = append(errs, is)
		}
	}
	if n := len(errs); n > aicontext.MaxRepairIssues {
		return errs[:aicontext.MaxRepairIssues], n - aicontext.MaxRepairIssues
	}
	return errs, 0
}

// Once repairs refused, the server's outcome of the answer prior to the
// request first, once: it returns refused itself and no record when there
// is nothing to repair. answered, when set, is told of the second call's
// end (the worker's log line). The error is the attempt's when ctx ended
// (the record says what the call spent), check's, or the follow-up's that
// could not be built (an *aicontext.Error: the server's own fault); a
// second call that fails otherwise is no error.
func Once(ctx context.Context, client provider.LLMProvider, first provider.ProposalContext, prior provider.PriorAnswer, refused review.Outcome,
	check Check, answered func(provider.Metadata, error)) (review.Outcome, *Record, error) {
	issues, notListed := Issues(refused)
	if len(issues) == 0 || !prior.Present() {
		return refused, nil, nil
	}
	rc, err := aicontext.Revise(first, prior, issues, notListed)
	if err != nil {
		return refused, nil, err
	}
	if err := ctx.Err(); err != nil {
		// Stopping between the two calls: no second call.
		return refused, nil, provider.NewError(provider.CodeCancelled, 0, "").WithCause(err)
	}
	rec := &Record{Issues: len(issues), NotListed: notListed}
	res, err := client.GenerateModelProposal(ctx, rc)
	meta := res.Metadata
	rec.Call = &meta
	if answered != nil {
		answered(meta, err)
	}
	if err != nil {
		if ctx.Err() != nil {
			return refused, rec, err
		}
		var pe *provider.Error
		if errors.As(err, &pe) && pe.Code == provider.CodeInvalidOutput {
			// The second answer refused for what it says: its own issues,
			// cleaned like every other, and nothing of it.
			rec.Outcome = Invalid
			return review.InvalidOutput(pe.Issues), rec, nil
		}
		rec.Outcome, rec.Code = Failed, codeOf(err)
		return refused, rec, nil
	}
	outcome, err := check(res.Output)
	if err != nil {
		return refused, rec, err
	}
	rec.Outcome = string(outcome.Status)
	return outcome, rec, nil
}

// codeOf is a failed call's code as a job failure names it; INTERNAL for an
// error that is not the provider client's.
func codeOf(err error) string {
	if c := provider.CodeOf(err); c != "" {
		return "AI_" + strings.ToUpper(string(c))
	}
	return "INTERNAL"
}
