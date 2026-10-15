// Command check-architecture enforces the package dependency rules of
// docs/ARCHITECTURE.md §2 (T-042): the generic kernel imports no domain pack,
// application layer, I/O, database or LLM SDK, and links only the listed
// third-party modules; packs build on the kernel only; test helpers never
// reach production code. It reads the build graph from `go list`, so it sees
// exactly what the compiler links, including transitive dependencies.
//
//	go run ./scripts/check-architecture [-C dir] [-overlay file] [patterns]
//
// It exits 1 with each violation and the import chain that causes it.
package main

import (
	"flag"
	"fmt"
	"os"
)

// The module's packages; ./... would also list Go code vendored inside
// apps/web/node_modules.
var defaultPatterns = []string{"./cmd/...", "./internal/...", "./schemas/...", "./scripts/..."}

func main() {
	dir := flag.String("C", ".", "module root")
	overlay := flag.String("overlay", "", "go build overlay file (used by the checker's own tests)")
	flag.Parse()
	patterns := flag.Args()
	if len(patterns) == 0 {
		patterns = defaultPatterns
	}
	var goflags []string
	if *overlay != "" {
		goflags = append(goflags, "-overlay="+*overlay)
	}
	g, err := load(*dir, goflags, patterns...)
	if err != nil {
		fmt.Fprintln(os.Stderr, "check-architecture:", err)
		os.Exit(2)
	}
	violations, errs := check(g, rules)
	for _, e := range errs {
		fmt.Fprintln(os.Stderr, "check-architecture:", e)
	}
	for _, v := range violations {
		fmt.Fprintln(os.Stderr, v)
	}
	if len(errs) > 0 {
		os.Exit(2)
	}
	if len(violations) > 0 {
		fmt.Fprintf(os.Stderr, "FAIL architecture: %d violation(s)\n", len(violations))
		os.Exit(1)
	}
	fmt.Printf("PASS architecture: %d packages, %d rules\n", len(g.roots), len(rules))
}
