// Command worker is the composition root of the job worker (docs/ARCHITECTURE.md
// §2, §6). It owns configuration and lifecycle only, never business rules.
//
// No job loop exists yet (T-019), so after validating configuration it exits
// with status 2 instead of idling as if it were processing jobs.
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
	log := slog.New(slog.NewJSONHandler(stderr, nil)).With("component", "worker")
	cfg, err := config.Load(getenv)
	if err != nil {
		log.Error("invalid configuration", "error", err.Error())
		return exitConfig
	}
	log.Error("NOT IMPLEMENTED: no job loop is wired yet (T-019); refusing to start",
		"ai_provider", string(cfg.AIProvider))
	return exitNotImplemented
}
