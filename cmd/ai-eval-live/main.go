// Command ai-eval-live runs the AI eval corpus against the live provider
// (make ai-eval-live; with -smoke, the one synthetic case of make
// ai-smoke-live). It is a composition root only: internal/aievals.Main
// refuses, before building any backend, unless the configuration turns
// live AI on exactly as the worker's does (the owner's complete recorded
// approval, AI_MODEL equal to its model, both keys, GEMINUS_AI_LIVE=on;
// T-053) and the run's cap (AI_EVAL_LIVE_MAX_USD) fits the deployment's
// daily and monthly limits less what the spend record
// checks/ai-evals/live-spend.jsonl holds (review round 1, H1); it meters
// the run by that cap, records the run in the spend record before any call
// and closes it with what it cost, and writes the report to a
// checks/ai-evals/live-*.json file of its own, apart from the fake
// results. The report, the record and the summary keep no prompt, answer
// text or key.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/provider/anthropic"
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

// liveBackend is the live adapter for a configuration that turns live AI
// on: the approval token, its model and the keys config read, by label.
func liveBackend(ai config.AI) (provider.Billable, error) {
	creds := make([]anthropic.Credential, len(ai.Credentials))
	for i, c := range ai.Credentials {
		creds[i] = anthropic.Credential{Label: c.Label, Key: c.Key}
	}
	return anthropic.New(anthropic.Config{Approval: ai.Approval, Model: ai.Model, Credentials: creds})
}
