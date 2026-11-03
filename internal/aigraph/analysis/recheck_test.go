package analysis_test

// H-086 (R): the one re-ask of an explanation the server's grounding check
// refused, on the analyst graph, over the real provider.Client and the
// deterministic fake backend (no live call). The check itself is
// internal/ai/analyst's, unchanged; the follow-up's context is
// internal/ai/aicontext's (reground_test.go).

import (
	"context"
	"encoding/json/v2"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/aicontext"
	"github.com/kcl1104/geminus/internal/ai/analyst"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/aigraph/analysis"
)

// explanation is an answer for input(t) (a baseline, one replication):
// summary as the scenario's, and the experiments' names.
func explanation(t *testing.T, summary string, experiments ...string) []byte {
	t.Helper()
	return explanationWith(t, summary, []string{}, experiments...)
}

// explanationWith is explanation with its assumptions.
func explanationWith(t *testing.T, summary string, assumptions []string, experiments ...string) []byte {
	t.Helper()
	out := provider.AnalysisOutput{Baseline: &provider.AnalysisSummary{Summary: "The baseline ran with one server."},
		Scenario: provider.AnalysisSummary{Summary: summary}, Delta: []provider.AnalysisDelta{}, Bottleneck: []provider.Bottleneck{},
		Assumptions: assumptions, Uncertainty: []string{"Only one replication was run."}, SuggestedNextExperiments: []provider.Experiment{}}
	for _, name := range experiments {
		out.SuggestedNextExperiments = append(out.SuggestedNextExperiments,
			provider.Experiment{Name: name, Hypothesis: "It may shorten the queue.", RequiresConfirmation: true})
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

const (
	// firstSummary holds a figure nobody supplied (ANALYSIS_NUMBER_UNVERIFIED
	// at /scenario/summary); the live refusals of 2026-11-03 were this code.
	firstSummary = "Throughput rose by 4242.5 with the second server."
	fixedSummary = "The second server shortened the queue."
)

// followUp is the SERVER_CHECK block of a re-ask's request.
func followUp(t *testing.T, req provider.Request) (issues []map[string]string, notListed int) {
	t.Helper()
	r := req.Prompt.Revision
	if r == nil || len(r.Blocks) != 1 || r.Blocks[0].Name != "SERVER_CHECK" || r.Blocks[0].Kind != provider.ServerData {
		t.Fatalf("the re-ask's follow-up: %+v", r)
	}
	var v struct {
		Issues    []map[string]string `json:"issues"`
		NotListed int                 `json:"issues_not_listed"`
	}
	if err := json.Unmarshal(r.Blocks[0].Content, &v); err != nil {
		t.Fatal(err)
	}
	return v.Issues, v.NotListed
}

// An answer the check refuses is asked again once, and the second answer,
// grounded, is the analysis: checked exactly as a first answer is (the same
// stored analysis the check makes of it), with the first call's metadata
// and the re-ask's record. The second request is the first prompt again
// (its system text and user message, byte for byte), the model's own
// answer as its turn, and the follow-up naming each issue's code, path and
// rule, nothing of the answer's prose; the observer sees calls 1 and 2.
func TestARefusedAnswerIsAskedAgainOnce(t *testing.T) {
	in := input(t)
	f := fake.New()
	first, fixed := explanation(t, firstSummary), explanation(t, fixedSummary)
	f.Script(provider.RoleAnalyst, question, first)
	f.ScriptRevision(provider.RoleAnalyst, question, fixed)
	obs := &calls{}
	out, err := graph(t, client(t, f), packs(t), provider.DefaultBudget(), obs).Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	// The analysis is the check's of the second answer, as a first answer's would be.
	g := fake.New()
	g.Script(provider.RoleAnalyst, question, fixed)
	want, wantMeta, issues := direct(t, client(t, g), in)
	if len(issues) > 0 {
		t.Fatalf("the fixed answer is not grounded: %+v", issues)
	}
	if !reflect.DeepEqual(out.Analysis, want) || out.Analysis.Scenario.Summary != fixedSummary {
		t.Errorf("the analysis %+v, want %+v", out.Analysis, want)
	}
	reqs := f.Requests()
	if len(reqs) != 2 {
		t.Fatalf("%d provider requests", len(reqs))
	}
	r1, r2 := reqs[0].Prompt, reqs[1].Prompt
	if r1.Revision != nil || r2.Revision == nil || r2.System != r1.System || r2.User() != r1.User() || r2.Role != provider.RoleAnalyst ||
		r2.Locale != r1.Locale || r2.Research || r2.InputHash == r1.InputHash || r2.TemplateVersion != "analyst-0.3.3+reground-0.1.0" ||
		string(r2.Revision.Prior.Bytes()) != string(first) {
		t.Errorf("the re-ask's request: %q", r2.TemplateVersion)
	}
	if reqs[1].MaxOutputTokens != reqs[0].MaxOutputTokens || reqs[1].AttemptTimeout != reqs[0].AttemptTimeout || reqs[1].MaxRetries != reqs[0].MaxRetries {
		t.Errorf("the re-ask's bounds differ from the first call's: %+v", reqs[1])
	}
	listed, notListed := followUp(t, reqs[1])
	rule, _ := aicontext.CheckRule(analyst.NumberUnverified)
	if len(listed) != 1 || notListed != 0 || listed[0]["code"] != analyst.NumberUnverified || listed[0]["path"] != "/scenario/summary" || listed[0]["rule"] != rule {
		t.Errorf("the follow-up lists %+v (%d not listed)", listed, notListed)
	}
	if follow := r2.Revision.User(); strings.Contains(follow, "4242") || strings.Contains(follow, question) || strings.Contains(follow, "second server") {
		t.Errorf("the follow-up holds the answer's or the question's text: %s", follow)
	}
	// The metadata is the first call's; the re-ask's is in the record.
	if out.Metadata.TemplateVersion != "analyst-0.3.3" || out.Metadata.InputHash != r1.InputHash || out.Metadata.OutputHash == wantMeta.OutputHash {
		t.Errorf("the metadata %+v", out.Metadata)
	}
	rc := out.Recheck
	if rc == nil || rc.Outcome != analysis.RecheckGrounded || !rc.Revised() || rc.Issues != 1 || rc.NotListed != 0 || rc.Code != "" || rc.Call == nil ||
		rc.Call.InputHash != r2.InputHash || rc.Call.TemplateVersion != r2.TemplateVersion || rc.Call.OutputHash != wantMeta.OutputHash {
		t.Errorf("the re-ask's record %+v", rc)
	}
	seen := obs.all()
	if len(seen) != 2 || seen[0].call != 1 || seen[1].call != 2 || seen[0].err != nil || seen[1].err != nil ||
		seen[0].md.InputHash != r1.InputHash || seen[1].md.InputHash != r2.InputHash {
		t.Errorf("observed %+v", seen)
	}
}

// When the check refuses the second answer too, the analysis is ungrounded
// with the second answer's issues (not the first's), and nothing asks a
// third time.
func TestASecondRefusalIsFinal(t *testing.T) {
	f := fake.New()
	f.Script(provider.RoleAnalyst, question, explanation(t, firstSummary))
	f.ScriptRevision(provider.RoleAnalyst, question, explanation(t, "Adding a server is guaranteed to help.", "Add 4242 servers"))
	obs := &calls{}
	_, err := graph(t, client(t, f), packs(t), provider.DefaultBudget(), obs).Run(context.Background(), input(t))
	var e *analysis.Error
	if !errors.As(err, &e) || e.Kind != analysis.Ungrounded {
		t.Fatalf("%v, want ungrounded", err)
	}
	got := map[string]bool{}
	for _, is := range e.Issues {
		got[is.Code+" "+is.Path] = true
	}
	if !got[analyst.Certainty+" /scenario/summary"] || !got[analyst.NumberUnverified+" /suggested_next_experiments/0/name"] ||
		got[analyst.NumberUnverified+" /scenario/summary"] {
		t.Errorf("the issues %+v, want the second answer's", e.Issues)
	}
	if r := e.Recheck; r == nil || r.Outcome != analysis.RecheckUngrounded || !r.Revised() || r.Issues != 1 || r.Call == nil ||
		e.Metadata == nil || e.Metadata.TemplateVersion != "analyst-0.3.3" {
		t.Errorf("the re-ask's record %+v, metadata %+v", r, e.Metadata)
	}
	if n := len(f.Requests()); n != 2 || len(obs.all()) != 2 {
		t.Errorf("%d provider requests, %d observed", n, len(obs.all()))
	}
	if strings.Contains(err.Error(), "4242") || strings.Contains(err.Error(), "guaranteed") {
		t.Errorf("the error's text carries content: %q", err.Error())
	}
}

// A second call that fails, whatever the error (the provider's, a spend
// limit, a truncated or schema-invalid answer, an error that is not the
// client's), leaves the first refusal as it was: ungrounded with the first
// answer's issues; the record says how the call failed, and that the
// second answer was never checked. Nothing asks a third time.
func TestAFailedReaskLeavesTheFirstRefusal(t *testing.T) {
	truncated := provider.NewError(provider.CodeTruncated, 0, "")
	for name, c := range map[string]struct {
		script func(*fake.Backend)
		code   string
	}{
		"unavailable": {func(f *fake.Backend) {
			f.FailRevision(provider.RoleAnalyst, question, provider.NewError(provider.CodeUnavailable, 503, ""))
		}, "AI_UNAVAILABLE"},
		"truncated": {func(f *fake.Backend) { f.FailRevision(provider.RoleAnalyst, question, truncated) }, "AI_OUTPUT_TRUNCATED"},
		"rate limited": {func(f *fake.Backend) {
			f.FailRevision(provider.RoleAnalyst, question, provider.NewError(provider.CodeRateLimited, 429, ""))
		}, "AI_RATE_LIMITED"},
		"not the client": {func(f *fake.Backend) { f.FailRevision(provider.RoleAnalyst, question, errors.New("a backend error")) }, "AI_UNAVAILABLE"},
		"schema-invalid": {func(f *fake.Backend) {
			f.ScriptRevision(provider.RoleAnalyst, question, []byte(`{"scenario":{}}`))
		}, "AI_INVALID_OUTPUT"},
	} {
		t.Run(name, func(t *testing.T) {
			f := fake.New()
			f.Script(provider.RoleAnalyst, question, explanation(t, firstSummary))
			c.script(f)
			obs := &calls{}
			_, err := graph(t, client(t, f), packs(t), provider.DefaultBudget(), obs).Run(context.Background(), input(t))
			var e *analysis.Error
			if !errors.As(err, &e) || e.Kind != analysis.Ungrounded {
				t.Fatalf("%v, want ungrounded", err)
			}
			if len(e.Issues) != 1 || e.Issues[0].Code != analyst.NumberUnverified || e.Issues[0].Path != "/scenario/summary" {
				t.Errorf("the issues %+v, want the first answer's", e.Issues)
			}
			if r := e.Recheck; r == nil || r.Outcome != analysis.RecheckFailed || r.Revised() || r.Code != c.code || r.Call == nil || r.Issues != 1 {
				t.Errorf("the re-ask's record %+v, want failed %s", r, c.code)
			}
			if seen := obs.all(); len(f.Requests()) != 2 || len(seen) != 2 || seen[1].call != 2 || seen[1].err == nil {
				t.Errorf("%d requests, observed %+v", len(f.Requests()), seen)
			}
		})
	}
}

// Only the check's refusal is asked again: a first call the client refused
// (a schema-invalid answer, a truncation, a spend limit, an unavailable
// provider) is one call, as before.
func TestAFailedFirstCallIsNotAskedAgain(t *testing.T) {
	for name, script := range map[string]func(*fake.Backend){
		"schema-invalid": func(f *fake.Backend) { f.Script(provider.RoleAnalyst, question, []byte(`{"scenario":{}}`)) },
		"truncated": func(f *fake.Backend) {
			f.Fail(provider.RoleAnalyst, question, provider.NewError(provider.CodeTruncated, 0, ""))
		},
		"unavailable": func(f *fake.Backend) {
			f.Fail(provider.RoleAnalyst, question, provider.NewError(provider.CodeUnavailable, 503, ""))
		},
		"a spend limit": func(f *fake.Backend) {
			f.Fail(provider.RoleAnalyst, question, &provider.Error{Code: provider.CodeQuotaExceeded, Limit: provider.LimitInvestigation})
		},
	} {
		f := fake.New()
		script(f)
		f.ScriptRevision(provider.RoleAnalyst, question, explanation(t, fixedSummary))
		_, err := graph(t, client(t, f), packs(t), provider.DefaultBudget(), nil).Run(context.Background(), input(t))
		var e *analysis.Error
		if !errors.As(err, &e) || e.Kind != analysis.CallFailed || e.Recheck != nil || len(f.Requests()) != 1 {
			t.Errorf("%s: %v, %d requests", name, err, len(f.Requests()))
		}
	}
}

// billed is the fake as a billable backend of a priced model, quoting each
// request by its size (the re-ask's holds the prior answer and the
// follow-up too), so a metered client reserves before each call and
// settles after it.
type billed struct{ *fake.Backend }

func (b billed) Quote(_ context.Context, req provider.Request) (provider.Quote, error) {
	n := len(req.Prompt.System) + len(req.Prompt.User())
	if r := req.Prompt.Revision; r != nil {
		n += r.Prior.Len() + len(r.User())
	}
	return provider.Quote{Model: "muse-spark-1.3", MaxInputTokens: int64(n)}, nil
}

func (b billed) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	r, err := b.Backend.Complete(ctx, req)
	r.Model, r.Usage = "muse-spark-1.3", provider.Usage{InputTokens: 700, OutputTokens: 90}
	return r, err
}

// ledger records a meter's charges and settlements; refuse, when set,
// refuses the n-th charge (from 1) as an investigation's ceiling does.
type ledger struct {
	mu      sync.Mutex
	m       *provider.MemoryMeter
	refuse  int
	charges []provider.Charge
	settled []provider.Settlement
}

func (l *ledger) Reserve(ctx context.Context, c provider.Charge) (provider.Reservation, error) {
	l.mu.Lock()
	l.charges = append(l.charges, c)
	n := len(l.charges)
	l.mu.Unlock()
	if n == l.refuse {
		return provider.Reservation{}, &provider.Error{Code: provider.CodeQuotaExceeded, Limit: provider.LimitInvestigation}
	}
	return l.m.Reserve(ctx, c)
}

func (l *ledger) Settle(ctx context.Context, r provider.Reservation, s provider.Settlement) error {
	l.mu.Lock()
	l.settled = append(l.settled, s)
	l.mu.Unlock()
	return l.m.Settle(ctx, r, s)
}

// The re-ask is metered like the first call: its own reservation before it
// is sent (larger: its request holds the prior answer and the follow-up),
// charged to the same analysis, and its own settlement; each call's
// metadata names its own reservation. A spend limit that refuses the
// second reservation (an investigation's USD 2.50 ceiling, H-061 §12)
// sends nothing more and leaves the first refusal, the record saying
// AI_QUOTA_EXCEEDED.
func TestTheReaskIsMetered(t *testing.T) {
	for _, refuse := range []int{0, 2} {
		f := fake.New()
		f.Script(provider.RoleAnalyst, question, explanation(t, firstSummary))
		f.ScriptRevision(provider.RoleAnalyst, question, explanation(t, fixedSummary))
		l := &ledger{m: provider.NewMemoryMeter(1_000_000_000_000), refuse: refuse}
		c, err := provider.NewMetered(billed{f}, provider.DefaultBudget(), l)
		if err != nil {
			t.Fatal(err)
		}
		out, err := graph(t, c, packs(t), provider.DefaultBudget(), nil).Run(context.Background(), input(t))
		if len(l.charges) != 2 || l.charges[0].Attribution != attribution || l.charges[1].Attribution != attribution ||
			l.charges[1].Role != provider.RoleAnalyst || l.charges[1].NanoUSD <= l.charges[0].NanoUSD {
			t.Fatalf("refuse %d: charges %+v", refuse, l.charges)
		}
		if refuse == 0 {
			if err != nil || out.Recheck == nil || out.Recheck.Outcome != analysis.RecheckGrounded || len(l.settled) != 2 || len(f.Requests()) != 2 ||
				out.Metadata.SpendReservation == "" || out.Recheck.Call.SpendReservation == "" || out.Recheck.Call.SpendReservation == out.Metadata.SpendReservation ||
				out.Recheck.Call.CostNanoUSD <= 0 {
				t.Errorf("both calls metered: %v %+v, settled %+v", err, out.Recheck, l.settled)
			}
			continue
		}
		var e *analysis.Error
		if !errors.As(err, &e) || e.Kind != analysis.Ungrounded || e.Recheck == nil || e.Recheck.Outcome != analysis.RecheckFailed ||
			e.Recheck.Code != "AI_QUOTA_EXCEEDED" || len(f.Requests()) != 1 || len(l.settled) != 1 || e.Issues[0].Path != "/scenario/summary" {
			t.Errorf("a refused second reservation: %v %+v, %d requests, settled %+v", err, e, len(f.Requests()), l.settled)
		}
	}
}

// An attempt that stops once the first answer is in (the worker stopping,
// the lease lost) still has it checked, and its refusal is final, as before
// H-086: no second call, no reservation, no record. One that stops during
// the second call leaves the first refusal too (the record says
// AI_CANCELLED), so no later attempt asks, and pays, a third time.
func TestAStopAndTheReask(t *testing.T) {
	t.Run("before the re-ask", func(t *testing.T) {
		f := fake.New()
		f.Script(provider.RoleAnalyst, question, explanation(t, firstSummary))
		f.ScriptRevision(provider.RoleAnalyst, question, explanation(t, fixedSummary))
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		g, err := analysis.New(analysis.Config{Provider: client(t, f), Budget: provider.DefaultBudget(), Packs: packs(t),
			Called: func(context.Context, int, provider.Metadata, error) { cancel() }})
		if err != nil {
			t.Fatal(err)
		}
		_, err = g.Run(ctx, input(t))
		var e *analysis.Error
		if !errors.As(err, &e) || e.Kind != analysis.Ungrounded || e.Recheck != nil || len(f.Requests()) != 1 || e.Issues[0].Path != "/scenario/summary" {
			t.Errorf("%v %+v, %d requests", err, e, len(f.Requests()))
		}
	})
	t.Run("during the re-ask", func(t *testing.T) {
		f := fake.New()
		f.Script(provider.RoleAnalyst, question, explanation(t, firstSummary))
		f.ScriptRevision(provider.RoleAnalyst, question, explanation(t, fixedSummary))
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		obs := &calls{}
		_, err := graph(t, client(t, stopsOnTheReask{Backend: f, cancel: cancel}), packs(t), provider.DefaultBudget(), obs).Run(ctx, input(t))
		var e *analysis.Error
		if !errors.As(err, &e) || e.Kind != analysis.Ungrounded || e.Recheck == nil || e.Recheck.Outcome != analysis.RecheckFailed ||
			e.Recheck.Code != "AI_CANCELLED" || e.Issues[0].Path != "/scenario/summary" {
			t.Errorf("%v %+v", err, e)
		}
		if seen := obs.all(); len(seen) != 2 || provider.CodeOf(seen[1].err) != provider.CodeCancelled {
			t.Errorf("observed %+v", seen)
		}
	})
}

// stopsOnTheReask stops the attempt while the re-ask is being asked.
type stopsOnTheReask struct {
	*fake.Backend
	cancel context.CancelFunc
}

func (s stopsOnTheReask) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	if req.Prompt.Revision != nil {
		s.cancel()
		<-ctx.Done()
	}
	return s.Backend.Complete(ctx, req)
}

// An answer with more issues than a follow-up lists has the first
// aicontext.MaxRepairIssues listed, in the check's order, and the rest
// counted.
func TestTheFollowUpListsAtMostItsBound(t *testing.T) {
	var assumptions []string
	for i := range aicontext.MaxRepairIssues + 7 {
		assumptions = append(assumptions, "Service takes "+strconv.Itoa(9000+i)+" minutes.")
	}
	f := fake.New()
	f.Script(provider.RoleAnalyst, question, explanationWith(t, fixedSummary, assumptions))
	_, err := graph(t, client(t, f), packs(t), provider.DefaultBudget(), nil).Run(context.Background(), input(t))
	var e *analysis.Error
	if !errors.As(err, &e) || e.Kind != analysis.Ungrounded || e.Recheck == nil || e.Recheck.Issues != aicontext.MaxRepairIssues || e.Recheck.NotListed != 7 {
		t.Fatalf("%v %+v", err, e)
	}
	reqs := f.Requests()
	listed, notListed := followUp(t, reqs[len(reqs)-1])
	if len(listed) != aicontext.MaxRepairIssues || notListed != 7 || listed[0]["path"] != "/assumptions/0" ||
		listed[len(listed)-1]["path"] != "/assumptions/"+strconv.Itoa(aicontext.MaxRepairIssues-1) {
		t.Errorf("listed %d (first %v), %d not listed", len(listed), listed[0], notListed)
	}
	if follow := reqs[len(reqs)-1].Prompt.Revision.User(); strings.Contains(follow, "900") || strings.Contains(follow, "minutes") {
		t.Error("the follow-up quotes the answer")
	}
}
