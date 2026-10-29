// Package fake is the deterministic fake provider backend for tests and
// E2E (AI_SPEC §2, TEST_PLAN §1): it is never a fallback for a live
// provider, and internal/config accepts it only with APP_ENV development or
// test. It reads its input exactly as a live provider would, from the
// rendered user message, and answers from a script keyed by the request's
// untrusted text (USER_INTENT, QUESTION or the column names) or, for a
// prompt with images and none of those (T-056's photo proposal), its first
// image's digest; without a
// scripted answer it answers safely: needs_input for a proposal, no target
// field for a mapping, an analysis without claims. No randomness, no clock,
// no I/O. Its answers still go through provider.Client, which checks them
// against the full output schema like any other.
//
// Research (H-046 (2)): the fake accepts a research prompt of a proposal
// role (provider.Researcher) and searches nothing: it reports the fixed web
// search of ResearchSteps (two searches and the two pages of
// ResearchSources opened), and its safe answer names those two pages and
// one it did not open, so the client's filter keeps two and drops one. A
// scripted answer names what it names; ScriptResearch replaces the steps.
// The pages are example.com addresses (RFC 2606): never a real source.
package fake

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"slices"
	"sync"

	"github.com/kcl1104/geminus/internal/ai/locale"
	"github.com/kcl1104/geminus/internal/ai/provider"
)

// Key block names: what a script entry is matched on.
const (
	IntentBlock   = "USER_INTENT"
	QuestionBlock = "QUESTION"
	ColumnsBlock  = "COLUMN_NAMES"
)

// Answer is a scripted reply: exact answer text, or an error.
type Answer struct {
	Text []byte
	Err  error
}

// Backend is the fake. The zero value is not usable; call New.
type Backend struct {
	mu       sync.Mutex
	script   map[provider.Role]map[string]Answer
	research map[string]provider.Research
	seen     []provider.Request
}

var _ provider.Researcher = (*Backend)(nil)

// New returns an empty fake.
func New() *Backend {
	return &Backend{script: map[provider.Role]map[string]Answer{}, research: map[string]provider.Research{}}
}

// ResearchSources are the fixed pages the fake's research opens (H-046
// (2)): test pages, never a real source.
var ResearchSources = []provider.SourceClaim{
	{URL: "https://example.com/geminus-fake-research/smt-line-balancing", Title: "Fake research page: SMT line balancing (test data)"},
	{URL: "https://example.com/geminus-fake-research/reflow-oven-throughput", Title: "Fake research page: reflow oven throughput (test data)"},
}

// notOpened is the page the fake's safe research answer names without
// having opened it: the client's filter drops it.
const notOpened = "https://example.com/geminus-fake-research/not-opened"

// ResearchSteps is the fake's web search of a research call: two searches
// and the two ResearchSources pages opened.
func ResearchSteps() provider.Research {
	r := provider.Research{Queries: 2, ToolCalls: 2 + len(ResearchSources)}
	for _, s := range ResearchSources {
		r.Opened = append(r.Opened, s.URL)
	}
	return r
}

// AcceptsResearch: the fake researches for the proposal roles (H-046 (2)).
func (b *Backend) AcceptsResearch(role provider.Role) bool { return role.Researches() }

// ScriptResearch makes a research call whose key block is key report r as
// its web search instead of ResearchSteps.
func (b *Backend) ScriptResearch(key string, r provider.Research) {
	b.mu.Lock()
	defer b.mu.Unlock()
	r.Opened = slices.Clone(r.Opened)
	b.research[key] = r
}

func (b *Backend) Name() string { return "fake" }

// Script makes role answer text when the request's key block (the intent,
// the question, or the JSON of the column names) is key.
func (b *Backend) Script(role provider.Role, key string, text []byte) {
	b.set(role, key, Answer{Text: slices.Clone(text)})
}

// Fail makes role fail with err for key.
func (b *Backend) Fail(role provider.Role, key string, err error) {
	b.set(role, key, Answer{Err: err})
}

func (b *Backend) set(role provider.Role, key string, a Answer) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.script[role] == nil {
		b.script[role] = map[string]Answer{}
	}
	b.script[role][key] = a
}

// Requests returns the requests seen so far, in order: what a live
// provider would have been sent.
func (b *Backend) Requests() []provider.Request {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.seen)
}

// keyOf is the request's untrusted key text, read from the rendered
// message.
func keyOf(p provider.Prompt) (string, map[string]jsontext.Value, error) {
	blocks, err := provider.ParseUser(p.User())
	if err != nil {
		return "", nil, err
	}
	byName := map[string]jsontext.Value{}
	for _, blk := range blocks {
		byName[blk.Name] = blk.Content
	}
	for _, name := range []string{IntentBlock, QuestionBlock} {
		if v, ok := byName[name]; ok {
			var s string
			if err := json.Unmarshal(v, &s); err != nil {
				return "", nil, err
			}
			return s, byName, nil
		}
	}
	if v, ok := byName[ColumnsBlock]; ok {
		return string(v), byName, nil
	}
	// T-056: a photo proposal has no intent; it is matched on its first
	// image's digest.
	if len(p.Images) > 0 {
		return p.Images[0].Digest, byName, nil
	}
	return "", byName, nil
}

// Complete answers req.
func (b *Backend) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	if err := ctx.Err(); err != nil {
		return provider.Response{}, provider.NewError(provider.CodeCancelled, 0, "").WithCause(err)
	}
	b.mu.Lock()
	b.seen = append(b.seen, req)
	b.mu.Unlock()
	key, blocks, err := keyOf(req.Prompt)
	if err != nil {
		return provider.Response{}, provider.NewError(provider.CodeRejected, 400, "")
	}
	b.mu.Lock()
	a, scripted := b.script[req.Prompt.Role][key]
	steps, stepsScripted := b.research[key]
	b.mu.Unlock()
	// No usage: the fake runs no model and counts no tokens, and bytes
	// reported as tokens would mislead usage and quota records (review L8).
	resp := provider.Response{Model: "fake"}
	if req.Prompt.Research {
		if !b.AcceptsResearch(req.Prompt.Role) || len(req.Prompt.Images) > 0 {
			return provider.Response{}, provider.NewError(provider.CodePolicyNotApproved, 0, "")
		}
		resp.Research = ResearchSteps()
		if stepsScripted {
			resp.Research = steps
			resp.Research.Opened = slices.Clone(steps.Opened)
		}
	}
	switch {
	case scripted && a.Err != nil:
		return resp, a.Err
	case scripted:
		resp.Text = slices.Clone(a.Text)
	default:
		text, err := defaultAnswer(req.Prompt, blocks)
		if err != nil {
			return provider.Response{}, err
		}
		resp.Text = text
	}
	return resp, nil
}

var errUnknownRole = errors.New("fake: unknown role")

// defaultAnswer is the safe answer in the request's locale.
func defaultAnswer(p provider.Prompt, blocks map[string]jsontext.Value) ([]byte, error) {
	zh := p.Locale == locale.TraditionalChinese
	pick := func(en, zhHant string) string {
		if zh {
			return zhHant
		}
		return en
	}
	switch p.Role {
	case provider.RoleModeler, provider.RoleScenario:
		var sources []provider.SourceClaim
		if p.Research {
			sources = append(slices.Clone(ResearchSources), provider.SourceClaim{URL: notOpened, Title: "A page the fake did not open"})
		}
		return json.Marshal(provider.ProposalOutput{
			Status:               "needs_input",
			Summary:              pick("The deterministic fake provider has no scripted answer for this request.", "測試用的確定性回答器沒有這個請求的預設回答。"),
			Assumptions:          []string{},
			Questions:            []string{pick("Which change should the proposal make?", "這個提案應該做什麼修改？")},
			Sources:              sources,
			RequiresConfirmation: true,
		})
	case provider.RoleImporter:
		var cols []string
		v, ok := blocks[ColumnsBlock]
		if !ok || json.Unmarshal(v, &cols) != nil {
			return nil, provider.NewError(provider.CodeRejected, 400, "")
		}
		out := provider.MappingOutput{Mappings: []provider.Mapping{}}
		for _, c := range cols {
			out.Mappings = append(out.Mappings, provider.Mapping{SourceColumn: c, Confidence: 0,
				Evidence:  pick("No scripted evidence; a person must map this column.", "沒有預設的依據；需要由人確認這個欄位。"),
				Transform: "identity", RequiresConfirmation: true})
		}
		return json.Marshal(out)
	case provider.RoleAnalyst:
		var baseline *provider.AnalysisSummary
		if v, ok := blocks["BASELINE_RESULTS"]; ok && string(v) != "null" {
			baseline = &provider.AnalysisSummary{Summary: pick("Baseline results were supplied.", "已提供 baseline 結果。")}
		}
		return json.Marshal(provider.AnalysisOutput{
			Baseline:                 baseline,
			Scenario:                 provider.AnalysisSummary{Summary: pick("The deterministic fake provider makes no claims about the results.", "測試用的確定性回答器不對結果做任何判斷。")},
			Delta:                    []provider.AnalysisDelta{},
			Bottleneck:               []provider.Bottleneck{},
			Assumptions:              []string{},
			Uncertainty:              []string{pick("This analysis comes from the fake provider.", "這份分析來自測試用的確定性回答器。")},
			SuggestedNextExperiments: []provider.Experiment{},
		})
	}
	return nil, errUnknownRole
}
