// Command api is the composition root of the Go REST API (docs/ARCHITECTURE.md
// §2). It owns configuration and lifecycle only, never business rules.
//
// No HTTP routes exist yet (T-013), so after validating configuration and
// building the token verifier (T-008; it does not contact the provider) it
// exits with status 2 instead of starting a server that would serve nothing.
package main

import (
	"errors"
	"io"
	"log/slog"
	"os"

	"github.com/kcl1104/geminus/internal/auth"
	"github.com/kcl1104/geminus/internal/config"
)

const (
	exitConfig         = 1
	exitNotImplemented = 2
)

func main() { os.Exit(run(os.Getenv, os.Stderr)) }

func run(getenv func(string) string, stderr io.Writer) int {
	log := slog.New(slog.NewJSONHandler(stderr, nil)).With("component", "api")
	cfg, err := config.Load(getenv)
	authCfg, authErr := config.LoadAuth(getenv)
	if err = errors.Join(err, authErr); err != nil {
		log.Error("invalid configuration", "error", err.Error())
		return exitConfig
	}
	if _, err := auth.NewOIDCVerifier(authCfg); err != nil {
		log.Error("invalid configuration", "error", err.Error())
		return exitConfig
	}
	log.Error("NOT IMPLEMENTED: no HTTP routes are wired yet (T-013); refusing to start",
		"ai_provider", string(cfg.AIProvider))
	return exitNotImplemented
}
