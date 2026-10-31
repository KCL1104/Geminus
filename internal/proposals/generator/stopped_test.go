package generator

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/observability"
	"github.com/kcl1104/geminus/internal/storage/jobs"
)

// H-059 (E3 review round 1, finding 1): an attempt whose context has already
// ended when it starts (the worker stopping, or the lease lost, between the
// Checkpoint that read the proposal and the first step) ends as the
// handler's own pipeline ended it before the graph: each step runs and sees
// the cancellation itself, so what the server stored that cannot be read,
// a request that cannot be built into a context and a dataset-bound base on
// a worker without object storage are the same failures, not retried
// (internal/worker records the handler's failure even while it stops), and
// good input ends with the binding stage's own error (context.Canceled, as
// datasets.Resolve returns it); no provider request is made in any of them.
// Characterized on the generator before the graph (the tree at 31ab222):
// every case passes there unchanged.
func TestAStoppedAttemptEndsAsTheHandlerDid(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	const inputs = "the proposal's stored request cannot be built into a context"
	for _, c := range []struct {
		name    string
		edit    func(*started)
		code    string // "" for the context's own error
		message string
		log     string
	}{
		{name: "the base", edit: func(st *started) { st.version.IR = []byte(`{"not":"a model"`) }, code: InputInvalid, message: "the base version does not decode"},
		{name: "the domain", edit: func(st *started) {
			st.version.IR = []byte(strings.Replace(string(st.version.IR), `"domain":"software_delivery"`, `"domain":"no_such_pack"`, 1))
		}, code: InputInvalid, message: "the model's domain is not served by this worker"},
		{name: "the pool", edit: func(st *started) { st.proposal.IDPool = []byte(`[`) }, code: InputInvalid, message: "the stored ID pool does not decode"},
		{name: "the locale", edit: func(st *started) { st.proposal.OutputLocale = "fr" }, code: InputInvalid, message: inputs,
			log: `"msg":"building a proposal context failed"`},
		{name: "a 640 kB intent", edit: func(st *started) { st.proposal.Intent = strings.Repeat("x", 640<<10) }, code: InputInvalid, message: inputs,
			log: `"msg":"building a proposal context failed"`},
		{name: "datasets and no object storage", edit: func(st *started) { *st, _ = withData(t, *st) },
			code: DatasetStorageFailed, message: "the base version binds datasets and this worker has no object storage"},
		{name: "good input", edit: func(*started) {}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			f := fake.New()
			h, err := New(Config{Provider: client(t, f), Packs: packs(t), Actor: "worker-1", Log: observability.NewLogger(&buf, "worker")})
			if err != nil {
				t.Fatal(err)
			}
			st := stored(t, "edit", "Six runners.")
			c.edit(&st)
			gen, err := h.generateAsked(ctx, st.proposal.OrganizationID, st, testAttribution, false, "")
			var jf *jobs.Failure
			switch {
			case c.code != "":
				if !errors.As(err, &jf) || jf.Code != c.code || jf.Message != c.message || jf.Retryable {
					t.Errorf("%v, want %s %q, not retried", err, c.code, c.message)
				}
			case err != context.Canceled:
				t.Errorf("%v (%T), want the binding stage's context.Canceled as it is", err, err)
			}
			if gen.meta != nil || gen.repair != nil || gen.outcome.Status != "" || len(f.Requests()) != 0 {
				t.Errorf("kept %+v, %d requests", gen, len(f.Requests()))
			}
			logs := buf.String()
			if c.log == "" && logs != "" || c.log != "" && !strings.Contains(logs, c.log) {
				t.Errorf("log %q, want %q", logs, c.log)
			}
		})
	}
}
