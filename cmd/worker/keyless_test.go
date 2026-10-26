package main

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/config"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/worker/handler"
)

// T-053 I5, the T-055 follow-up (the coordinator's decision): the API
// accepts proposals from the settings alone (config.LoadAPIAI), without a
// key; the worker holds the keys and is unchanged. A worker with the API's
// settings but not its key never asks a provider: with the switch on it
// refuses to start, naming the key's variable (never a value), before any
// handler is built or anything is connected, so it claims no job and
// writes nothing (the proposals wait, generating, for a worker that
// starts); with the switch off it starts, and every proposal is answered
// policy_not_approved, a typed failure that is not retried
// (internal/proposals/generator's TestAProposalTheWorkerCannotAskFailsSafely
// shows the proposal fails with nothing else written).
func TestAProposalTheAPIAcceptsWithoutTheWorkersKey(t *testing.T) {
	ctx := context.Background()
	for name, c := range map[string]struct {
		settings []string
		missing  []string
	}{
		"meta": {[]string{"AI_PROVIDER", "meta", "AI_POLICY_APPROVAL", "H-029", "AI_MODEL", "muse-spark-1.3"}, []string{"GEMINUS_MUSE_API_KEY (required)"}},
		"anthropic": {[]string{"AI_PROVIDER", "anthropic", "AI_POLICY_APPROVAL", "H-018", "AI_MODEL", "claude-opus-5-5"},
			[]string{"GEMINUS_ANTHROPIC_API_KEY_A (required)", "GEMINUS_ANTHROPIC_API_KEY_B (required)"}},
	} {
		on := append(slices.Clone(c.settings), "GEMINUS_AI_LIVE", "on")
		api, err := config.LoadAPIAI(with(on...))
		if err != nil || !api.LiveEnabled() {
			t.Fatalf("%s: the API does not accept proposals: %+v %v", name, api, err)
		}
		built := 0
		build := func(deps) (map[jobs.Type]handler.Handler, error) {
			built++
			return map[jobs.Type]handler.Handler{jobs.ModelProposal: handler.Func(func(context.Context, handler.Run) error { return nil })}, nil
		}
		var stderr bytes.Buffer
		rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		code := run(rctx, with(on...), &stderr, build)
		cancel()
		out := stderr.String()
		if code != exitConfig || built != 0 {
			t.Errorf("%s, the switch on: exit %d, %d builds: %s", name, code, built, out)
		}
		for _, w := range c.missing {
			if !strings.Contains(out, w) {
				t.Errorf("%s, the switch on: no %s in %s", name, w, out)
			}
		}
		if strings.Contains(out, `"msg":"running"`) || strings.Contains(out, "database") || strings.Contains(out, "live AI is on") {
			t.Errorf("%s, the switch on: %s", name, out)
		}
		// The switch off (the owner has not turned the worker on): it
		// starts, and its proposals' client answers policy_not_approved,
		// without asking anything and without the ledger.
		ai := loadAI(t, with(c.settings...))
		client, _, err := aiClient(ai, unusedLedger(t), "test-worker")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		_, err = client.GenerateModelProposal(ctx, provider.ProposalContext{OrganizationID: ids.New("org"), Mode: provider.ModeEdit})
		var pe *provider.Error
		if provider.CodeOf(err) != provider.CodePolicyNotApproved || !errors.As(err, &pe) || pe.Retryable() {
			t.Errorf("%s, the switch off: %v", name, err)
		}
	}
}
