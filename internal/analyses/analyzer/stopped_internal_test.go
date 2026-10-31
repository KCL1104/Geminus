package analyzer

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/domainpack/softwaredelivery"
	"github.com/kcl1104/geminus/internal/storage/jobs"
)

// H-059 (E6 review round 1, finding 1): an attempt whose context has already
// ended when analyze starts (the worker stopping, or the lease lost, between
// the Checkpoint that read the analysis and the first step) ends as the
// handler's own steps ended it before the graph: each step runs and sees
// the stop itself, so what the server stored that cannot be read, runs the
// server cannot or does not compare, and a request that cannot be built into
// a context are the same failures, not retried (internal/worker records the
// handler's failure even while it stops), with the same log lines; good
// input reaches the client, which refuses the call before any request or
// reservation, and that call is logged as AI_CANCELLED. Characterized on the
// analyzer before the graph (the tree at 88c9191, with
// analyze_internal_test.go's helpers): every case passes there unchanged.
func TestAStoppedAttemptEndsAsTheHandlerDid(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	const notBuilt = "the analysis's stored request cannot be built into a context"
	small := provider.DefaultBudget()
	small.MaxPromptBytes = 2000
	for _, c := range []struct {
		name    string
		edit    func(*started)
		packs   PackLookup
		budget  provider.Budget
		code    string // "" for the client's cancelled call
		message string
		log     map[string]string // the one log line's msg and code; nil for none
	}{
		{name: "the version", edit: func(st *started) { st.version.IR = []byte(`{"model_id":`) },
			code: InputInvalid, message: "the analyzed run's version does not decode"},
		{name: "the domain", packs: noPacks{}, code: InputInvalid, message: "the model's domain is not served by this worker"},
		{name: "the scenario's result", edit: func(st *started) { st.results[tScen] = []byte(`{"run_id":"` + tScen + `","unknown":1}`) },
			code: InputInvalid, message: "a stored result does not decode"},
		{name: "the patch", edit: func(st *started) { st.scenario.ScenarioPatch = []byte(`[`) },
			code: InputInvalid, message: "the analyzed run's patch does not decode"},
		{name: "the baseline's result", edit: func(st *started) { st.results[tBase] = []byte(`[]`) },
			code: InputInvalid, message: "a stored result does not decode"},
		{name: "a comparison that fails", edit: func(st *started) { st.results[tBase] = st.results[tScen] },
			code: InputInvalid, message: "the analysis's runs cannot be compared", log: map[string]string{"msg": "comparing an analysis's runs failed"}},
		{name: "runs the server does not compare", edit: func(st *started) { st.baseline.ModelVersionID = tOther },
			code: NotComparable, message: "the server does not compare the analysis's runs (MODEL_VERSION_MISMATCH)"},
		{name: "a context too large", budget: small, code: "AI_CONTEXT_TOO_LARGE", message: "the results are too large for the AI context budget"},
		{name: "the locale", edit: func(st *started) { st.analysis.OutputLocale = "fr" }, code: InputInvalid, message: notBuilt,
			log: map[string]string{"msg": "building an analysis context failed", "code": "invalid_locale"}},
		{name: "good input", log: map[string]string{"msg": "ai provider call ended", "code": "AI_CANCELLED"}},
		{name: "good input alone", edit: func(st *started) { *st = alone(t) }, log: map[string]string{"msg": "ai provider call ended", "code": "AI_CANCELLED"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := fake.New()
			l := &ledger{m: provider.NewMemoryMeter(1_000_000_000_000)}
			client, err := provider.NewMetered(billed{f}, provider.DefaultBudget(), l)
			if err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			h := setup(t, client, c.packs, c.budget, &buf)
			st := fixture(t)
			if c.edit != nil {
				c.edit(&st)
			}
			_, meta, issues, err := h.analyze(ctx, tOrg, st, tAttr)
			var fl *jobs.Failure
			switch {
			case c.code != "":
				if !errors.As(err, &fl) || fl.Code != c.code || fl.Message != c.message || fl.Retryable {
					t.Errorf("%v, want %s %q, not retried", err, c.code, c.message)
				}
			case errors.As(err, &fl) || provider.CodeOf(err) != provider.CodeCancelled || !errors.Is(err, context.Canceled):
				t.Errorf("%v (%T), want the client's cancelled call as it is", err, err)
			}
			if meta != nil || issues != 0 || len(f.Requests()) != 0 || len(l.charges) != 0 || len(l.settled) != 0 {
				t.Errorf("metadata %v, %d issues, %d requests, spend %+v %+v", meta, issues, len(f.Requests()), l.charges, l.settled)
			}
			wantLines(t, buf.String(), c.log)
		})
	}
}

// H-059 (E6 review round 1, finding 1): an attempt that stops once the paid
// call has answered, before the server's check (the stop lands as the call's
// log line is written, as it was before the graph: right after the call),
// still has the answer checked, as the handler's steps did: an ungrounded
// answer is the final ANALYSIS_UNGROUNDED, not retried (so no attempt asks,
// and pays, again), and a grounded one is the checked analysis, with the
// call's metadata either way. Characterized on 88c9191 as above.
func TestAStopAfterTheAnswerStillChecksIt(t *testing.T) {
	for _, c := range []struct {
		name   string
		script func(*fake.Backend)
		code   string // "" for the checked analysis
	}{
		{name: "an ungrounded answer", script: func(f *fake.Backend) { f.Script(provider.RoleAnalyst, tQuestion, invented(t)) }, code: Ungrounded},
		{name: "a grounded answer"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := fake.New()
			if c.script != nil {
				c.script(f)
			}
			l := &ledger{m: provider.NewMemoryMeter(1_000_000_000_000)}
			client, err := provider.NewMetered(billed{f}, provider.DefaultBudget(), l)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			log := &stopsAtTheCallsLine{stop: cancel}
			reg, err := domainpack.NewRegistry(softwaredelivery.Pack{})
			if err != nil {
				t.Fatal(err)
			}
			h, err := New(Config{Provider: client, Packs: reg, Actor: "test-worker", Log: slog.New(slog.NewJSONHandler(log, nil))})
			if err != nil {
				t.Fatal(err)
			}
			out, meta, issues, err := h.analyze(ctx, tOrg, fixture(t), tAttr)
			if ctx.Err() == nil {
				t.Fatal("the attempt did not stop as the call's line was written")
			}
			var fl *jobs.Failure
			switch {
			case c.code != "":
				if !errors.As(err, &fl) || fl.Code != c.code || fl.Retryable || issues < 1 ||
					!strings.HasPrefix(fl.Message, "the AI answer is not grounded in the results (") {
					t.Errorf("%v (%d issues), want %s, not retried", err, issues, c.code)
				}
			case err != nil || issues != 0 || out.Baseline == nil || out.Scenario.Summary == "":
				t.Errorf("%v (%d issues, a baseline summary %t), want the checked analysis", err, issues, out.Baseline != nil)
			}
			if meta == nil || meta.Provider != "fake" || meta.SpendReservation == "" {
				t.Errorf("metadata %+v, want the call's", meta)
			}
			if len(f.Requests()) != 1 || len(l.charges) != 1 || len(l.settled) != 1 {
				t.Errorf("%d requests, spend %+v %+v: want the one paid call", len(f.Requests()), l.charges, l.settled)
			}
			wantLines(t, log.String(), map[string]string{"msg": "ai provider call ended", "code": "OK"})
		})
	}
}

// stopsAtTheCallsLine is a log that stops the attempt as the provider call's
// line is written: after the call answered, before what follows it.
type stopsAtTheCallsLine struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	stop context.CancelFunc
}

func (s *stopsAtTheCallsLine) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.buf.Write(p)
	if bytes.Contains(p, []byte(`"msg":"ai provider call ended"`)) {
		s.stop()
	}
	return n, err
}

func (s *stopsAtTheCallsLine) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// wantLines checks that logs (JSON lines) are exactly one line with want's
// fields, or none when want is nil.
func wantLines(t *testing.T, logs string, want map[string]string) {
	t.Helper()
	var lines []map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(logs), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("a log line that is not JSON: %q", line)
		}
		lines = append(lines, m)
	}
	if want == nil {
		if len(lines) != 0 {
			t.Errorf("logged %q, want nothing", logs)
		}
		return
	}
	if len(lines) != 1 {
		t.Fatalf("logged %q, want one line %v", logs, want)
	}
	for k, v := range want {
		if lines[0][k] != v {
			t.Errorf("logged %q, want %s %q", logs, k, v)
		}
	}
}
