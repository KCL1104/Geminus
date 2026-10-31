package aicontext

import (
	"regexp"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/review"
	"github.com/kcl1104/geminus/internal/model"
)

// The context of a proposal's one repair (H-057, provider/revision.go): the
// first call's sealed prompt again (its system text, blocks and images, its
// role and locale; never research: the research already ran), the model's
// prior answer as the assistant turn after it, and the follow-up, a user
// turn of the repair template (prompts/repair) and one server_data block:
//
//   - SERVER_REVIEW: {"issues": [{"code", "path", "message"}...],
//     "issues_not_listed": n}, the issues that made the server refuse the
//     answer, each cleaned again here (review.Clean: a fixed sentence of the
//     server's and a path of known tokens, whatever the issue said), so
//     nothing the answer or a person wrote is echoed into the follow-up.
//
// The prompt's template version names the repair after the first's
// ("modeler-0.2.0+repair-0.1.0"); its InputHash covers the prior answer by
// its digest. The context keeps the first's attribution and chosen model
// (the same spend target, the same model), and no research markers.

// MaxRepairIssues bounds the issues a repair's follow-up lists; the rest
// are counted (issues_not_listed).
const MaxRepairIssues = 50

// repairTemplate is the follow-up's template (prompts/repair).
const repairTemplate = "repair"

// issueCode is the shape of a server issue code.
var issueCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// reviewIssue is one issue of SERVER_REVIEW.
type reviewIssue struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

// Revise is the context of the repair of first, whose answer prior the
// server refused with issues (at most MaxRepairIssues; notListed more were
// left out).
func Revise(first provider.ProposalContext, prior provider.PriorAnswer, issues []model.ValidationIssue, notListed int) (provider.ProposalContext, error) {
	switch {
	case !prior.Present():
		return provider.ProposalContext{}, fail(InvalidInput, "/prior")
	case len(issues) == 0 || len(issues) > MaxRepairIssues:
		return provider.ProposalContext{}, fail(InvalidInput, "/issues")
	case notListed < 0:
		return provider.ProposalContext{}, fail(InvalidInput, "/issues_not_listed")
	}
	if err := first.Prompt.Verify(); err != nil {
		return provider.ProposalContext{}, &Error{Code: InvalidInput, Path: "/prompt", cause: err}
	}
	text, version, err := template(repairTemplate)
	if err != nil {
		return provider.ProposalContext{}, err
	}
	list := make([]reviewIssue, len(issues))
	for i, is := range issues {
		if !issueCode.MatchString(is.Code) {
			// Never a server code's shape: the server's generic code.
			is.Code = review.OutputInvalid
		}
		c := review.Clean(is)
		list[i] = reviewIssue{Code: c.Code, Path: c.Path, Message: c.Message}
	}
	b, err := block(provider.ServerData, "SERVER_REVIEW", struct {
		Issues    []reviewIssue `json:"issues"`
		NotListed int           `json:"issues_not_listed"`
	}{list, notListed})
	if err != nil {
		return provider.ProposalContext{}, err
	}
	p := first.Prompt
	p.Revision = &provider.Revision{Prior: prior, Rules: text, Blocks: []provider.Block{b}, Researched: p.Research}
	p.Research = false
	p.TemplateVersion += "+" + version
	sealed, err := provider.Seal(p)
	if err != nil {
		return provider.ProposalContext{}, &Error{Code: InvalidInput, Path: "/prompt", cause: err}
	}
	return provider.ProposalContext{OrganizationID: first.OrganizationID, Mode: first.Mode, Prompt: sealed, Attribution: first.Attribution,
		Model: first.Model}, nil
}
