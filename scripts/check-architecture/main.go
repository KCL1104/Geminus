// Command check-architecture enforces the package dependency rules of
// docs/ARCHITECTURE.md §2 (T-042): the generic kernel imports no domain pack,
// application layer, I/O, database or LLM SDK, and links only the listed
// third-party modules; packs build on the kernel only; test helpers never
// reach production code. It reads the build graph from `go list` for every
// platform of a build matrix and merges it, so it sees what the compiler
// links on each of them, transitive dependencies included. It checks
// imports, not calls: see kernelStd in rules.go for what that covers.
//
//	go run ./scripts/check-architecture [-C dir] [-overlay file] [-host] [patterns]
//
// Exit status: 0 conforms, 1 violations (each with the import chain),
// 2 the graph could not be read or a rule is broken. `go run` reports any
// non-zero status as 1; `make architecture-check` runs the built binary.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

// The module's packages; go.mod's ignore directive keeps the Go code
// vendored under apps/web/node_modules out of ./... (CB-35).
var defaultPatterns = []string{"./..."}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("check-architecture", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("C", ".", "module root")
	overlay := fs.String("overlay", "", "go build overlay file (used by the checker's own tests)")
	hostOnly := fs.Bool("host", false, "read the graph for the current platform only (faster; misses platform-specific files)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	patterns := fs.Args()
	if len(patterns) == 0 {
		patterns = defaultPatterns
	}
	var goflags []string
	if *overlay != "" {
		goflags = append(goflags, "-overlay="+*overlay)
	}
	configs := matrix
	if *hostOnly {
		configs = host
	}
	g, err := load(*dir, goflags, configs, patterns...)
	if err != nil {
		fmt.Fprintln(stderr, "check-architecture:", err)
		return 2
	}
	violations, errs := check(g, rules)
	for _, e := range errs {
		fmt.Fprintln(stderr, "check-architecture:", e)
	}
	for _, v := range violations {
		fmt.Fprintln(stderr, v)
	}
	if len(errs) > 0 {
		return 2
	}
	if len(violations) > 0 {
		fmt.Fprintf(stderr, "FAIL architecture: %d violation(s)\n", len(violations))
		return 1
	}
	fmt.Fprintf(stdout, "PASS architecture: %d packages, %d rules, %d build configurations\n", len(g.roots), len(rules), len(configs))
	return 0
}
