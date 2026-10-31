package aievals_test

import (
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/fake"
	"github.com/kcl1104/geminus/internal/ai/spend"
)

// H-059 (slice E3): the live eval runs the worker's proposal graph, and
// still scores first answers only (H-057's checks/H-057 report: "make
// ai-eval-live still scores first answers only"): an answer the server
// refuses, by its review or by the client's strict decoding, is the case's
// invalid outcome after one call, with no repair asked.
func TestTheEvalScoresFirstAnswersOnly(t *testing.T) {
	_, cases := corpus(t)
	b := &billed{Backend: fake.New()}
	b.Script(provider.RoleModeler, intent(t, cases, "MOD-002"),
		[]byte(`{"status":"needs_input","summary":7,"assumptions":[],"questions":["q"],"requires_confirmation":true}`))
	r := run(t, b, 10*spend.NanoPerUSD, "MOD-002")
	o := outcome(r, "MOD-002")
	if o.Status != "invalid" || o.Passed || len(o.Issues) == 0 {
		t.Errorf("%+v", o)
	}
	reqs := b.Requests()
	if b.calls.Load() != 1 || len(reqs) != 1 || reqs[0].Prompt.Revision != nil {
		t.Errorf("%d calls, %d requests", b.calls.Load(), len(reqs))
	}
}
