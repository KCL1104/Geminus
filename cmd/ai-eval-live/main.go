// Command ai-eval-live runs the AI eval corpus against the live provider
// (make ai-eval-live; with -smoke, the one synthetic case of make
// ai-smoke-live). It is a composition root only: internal/aievals.Main
// refuses, before building any backend, unless the configuration turns
// live AI on exactly as the worker's does (the owner's complete recorded
// approval, AI_MODEL equal to its model, the provider's keys,
// GEMINUS_AI_LIVE=on; T-053, T-055) and the run's cap (AI_EVAL_LIVE_MAX_USD)
// fits the deployment's daily and monthly limits less what the spend
// record checks/ai-evals/live-spend.jsonl holds (review round 1, H1); it
// meters the run by that cap, records the run in the spend record before
// any call and closes it with what it cost, and writes the report to a
// checks/ai-evals/live-*.json file of its own, apart from the fake
// results. The report, the record and the summary keep no prompt, answer
// text or key. AI_PROVIDER picks the adapter: Claude's (anthropic, H-018)
// or the Meta Model API's Muse Spark (meta, H-029 amended by H-033, with
// GEMINUS_MUSE_API_KEY).
package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/anthropic"
	"github.com/kcl1104/geminus/internal/ai/provider/meta"
	"github.com/kcl1104/geminus/internal/aievals"
	"github.com/kcl1104/geminus/internal/config"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	code := aievals.Main(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr, liveBackend)
	stop()
	os.Exit(code)
}

// liveBackend is the live adapter of AI_PROVIDER for a configuration that
// turns live AI on: the approval token, its model and the keys config
// read, by label. Each adapter takes only its own provider's approval.
func liveBackend(ai config.AI) (provider.Billable, error) {
	switch ai.Provider {
	case config.AIProviderAnthropic:
		creds := make([]anthropic.Credential, len(ai.Credentials))
		for i, c := range ai.Credentials {
			creds[i] = anthropic.Credential{Label: c.Label, Key: c.Key}
		}
		b, err := anthropic.New(anthropic.Config{Approval: ai.Approval, Model: ai.Model, Credentials: creds})
		if err != nil {
			return nil, err
		}
		return b, nil
	case config.AIProviderMeta:
		if len(ai.Credentials) != 1 {
			return nil, errors.New("live AI on the Meta Model API takes one key")
		}
		c := ai.Credentials[0]
		b, err := meta.New(meta.Config{Approval: ai.Approval, Model: ai.Model, Credential: meta.Credential{Label: c.Label, Key: c.Key}})
		if err != nil {
			return nil, err
		}
		return b, nil
	}
	return nil, errors.New("no live adapter for this provider")
}
