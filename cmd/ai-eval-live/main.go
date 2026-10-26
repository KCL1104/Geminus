// Command ai-eval-live runs the AI eval corpus against the live provider
// (make ai-eval-live; with -smoke, the one synthetic case of make
// ai-smoke-live). It is a composition root only: internal/aievals.Main
// refuses, before building any backend, unless the configuration turns
// live AI on exactly as the worker's does (the owner's complete recorded
// approval, AI_MODEL equal to its model, both keys, GEMINUS_AI_LIVE=on;
// T-053), meters the run by its own cap (AI_EVAL_LIVE_MAX_USD, within the
// monthly limit), and writes the report to checks/ai-evals/live-*.json,
// apart from the fake results. The report and the summary keep no prompt,
// answer text or key.
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
