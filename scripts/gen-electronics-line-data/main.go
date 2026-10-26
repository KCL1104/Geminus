// Command gen-electronics-line-data writes the synthetic station event log
// of the electronics assembly and test line (T-052; H-016, H-029): the
// steps and breakdowns of one replication of the ground-truth model (the
// electronics line pack's template, ELECTRONICS_LINE §4) through the
// engine, as an MES-style CSV with D-059's fields, every row marked
// synthetic, with the ground truth that produced it and small derived
// negative fixtures (testdata/datasets/electronics-line, README.md there).
//
//	go run ./scripts/gen-electronics-line-data [-seed n] [-weeks n] [-out dir] [-check]
//
// The engine is the only simulation: the generator observes it
// (engine.Observer) and never models the line a second time. The same
// seed gives the same bytes.
//
// Exit status: 0 written (or, with -check, the files on disk are the
// output), 1 -check found a difference, 2 an error.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gen-electronics-line-data", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfg := DefaultConfig()
	fs.Int64Var(&cfg.Seed, "seed", cfg.Seed, "the engine seed of replication 0")
	fs.IntVar(&cfg.Weeks, "weeks", cfg.Weeks, "weeks the log covers after the warm-up week (the committed set is 1)")
	out := fs.String("out", DataDir, "directory to write (relative to the working directory)")
	check := fs.Bool("check", false, "compare the files in -out with the output instead of writing")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return 2
	}
	o, err := Generate(context.Background(), cfg)
	if err != nil {
		fmt.Fprintln(stderr, "gen-electronics-line-data:", err)
		return 2
	}
	files := o.Files()
	names := slices.Sorted(func(yield func(string) bool) {
		for k := range files {
			if !yield(k) {
				return
			}
		}
	})
	differ := 0
	for _, name := range names {
		path := filepath.Join(*out, filepath.FromSlash(name))
		if *check {
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, files[name]) {
				fmt.Fprintf(stdout, "differs: %s\n", path)
				differ++
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			fmt.Fprintln(stderr, "gen-electronics-line-data:", err)
			return 2
		}
		if err := os.WriteFile(path, files[name], 0o644); err != nil {
			fmt.Fprintln(stderr, "gen-electronics-line-data:", err)
			return 2
		}
		fmt.Fprintf(stdout, "wrote %s (%d bytes, %s)\n", path, len(files[name]), digest(files[name]))
	}
	if differ > 0 {
		return 1
	}
	return 0
}
