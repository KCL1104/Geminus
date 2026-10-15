// Command api is the composition root of the Go REST API (docs/ARCHITECTURE.md
// §2). It owns configuration and lifecycle only, never business rules.
//
// No HTTP routes exist yet (T-013), so after validating configuration it exits
// with status 2 instead of starting a server that would serve nothing.
package main

import (
	"io"
	"log/slog"
	"os"

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
	if err != nil {
		log.Error("invalid configuration", "error", err.Error())
		return exitConfig
	}
	log.Error("NOT IMPLEMENTED: no HTTP routes are wired yet (T-013); refusing to start",
		"ai_provider", string(cfg.AIProvider))
	return exitNotImplemented
}
