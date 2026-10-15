package main

import (
	"encoding/json/v2"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(strings.TrimSpace(string(out)))
}

// The repository conforms, and every rule that is not marked future guards
// at least one package.
func TestRepositoryConforms(t *testing.T) {
	g, err := load(repoRoot(t), nil, defaultPatterns...)
	if err != nil {
		t.Fatal(err)
	}
	violations, errs := check(g, rules)
	for _, e := range errs {
		t.Error(e)
	}
	for _, v := range violations {
		t.Error(v)
	}
	for _, want := range []string{module + "/internal/simulation/engine", module + "/internal/domainpack/softwaredelivery",
		module + "/internal/domainpack/conformance", module + "/scripts/check-architecture"} {
		if !slices.Contains(g.roots, want) {
			t.Errorf("%s is not checked", want)
		}
	}
	for _, r := range g.roots {
		if strings.Contains(r, "node_modules") {
			t.Errorf("%s: vendored frontend Go code is not the module's", r)
		}
	}
}

// overlay writes a go build overlay adding files (repository path -> source)
// and returns its path.
func overlay(t *testing.T, root string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	replace := map[string]string{}
	i := 0
	for path, src := range files {
		i++
		f := filepath.Join(dir, fmt.Sprintf("f%d.go", i))
		if err := os.WriteFile(f, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
		replace[filepath.Join(root, path)] = f
	}
	ov, err := json.Marshal(map[string]any{"Replace": replace})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "overlay.json")
	if err := os.WriteFile(path, ov, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Go code vendored under apps/web/node_modules is not the module's: the
// default patterns leave it out, although ./... would list it (CB-35).
func TestVendoredGoIsNotChecked(t *testing.T) {
	root := repoRoot(t)
	ov := overlay(t, root, map[string]string{"apps/web/node_modules/zz_probe/probe.go": "package zzprobe\n\nimport _ \"net/http\"\n"})
	probe := module + "/apps/web/node_modules/zz_probe"
	all, err := load(root, []string{"-overlay=" + ov}, "./...")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(all.roots, probe) {
		t.Fatalf("./... does not list the probe: the test would be vacuous")
	}
	g, err := load(root, []string{"-overlay=" + ov}, defaultPatterns...)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(g.roots, probe) {
		t.Errorf("the default patterns check vendored Go code")
	}
}

// A package that does not build is an error, not a passing check.
func TestBrokenBuildIsAnError(t *testing.T) {
	root := repoRoot(t)
	ov := overlay(t, root, map[string]string{"internal/model/zz_probe.go": "package model\n\nimport _ \"example.com/nowhere\"\n"})
	if _, err := load(root, []string{"-overlay=" + ov}, defaultPatterns...); err == nil || !strings.Contains(err.Error(), "example.com/nowhere") {
		t.Errorf("load: %v", err)
	}
}

// Each probe adds one file through a go build overlay (the tree is never
// changed) and must produce a violation of the named rule for the named
// dependency, with the chain from the package to it.
func TestProbesAreCaught(t *testing.T) {
	root := repoRoot(t)
	probes := []struct {
		name, file, imp, rule, pkg, dep string
		test                            bool
	}{
		{"kernel-http", "internal/simulation/engine/zz_probe.go", "net/http", "generic-kernel", "./internal/simulation/engine", "net/http", false},
		{"kernel-imports-a-pack", "internal/simulation/des/zz_probe.go", module + "/internal/domainpack/softwaredelivery", "generic-kernel", "./internal/simulation/des", "./internal/domainpack/softwaredelivery", false},
		{"kernel-imports-storage", "internal/results/metrics/zz_probe.go", module + "/internal/storage/datasets", "generic-kernel", "./internal/results/metrics", "./internal/storage/datasets", false},
		{"kernel-imports-pgx", "internal/model/zz_probe.go", "github.com/jackc/pgx/v5", "generic-kernel", "./internal/model", "github.com/jackc/pgx/v5", false},
		{"pgx-outside-storage", "internal/model/zz_probe.go", "github.com/jackc/pgx/v5", "db-only-in-storage", "./internal/model", "github.com/jackc/pgx/v5", false},
		{"kernel-unlisted-module", "internal/simulation/engine/zz_probe.go", "golang.org/x/sync/errgroup", "generic-kernel", "./internal/simulation/engine", "golang.org/x/sync/errgroup", false},
		{"kernel-process-exec", "internal/simulation/trace/zz_probe.go", "os/exec", "generic-kernel", "./internal/simulation/trace", "os/exec", false},
		{"kernel-testing", "internal/simulation/engine/zz_probe.go", "testing", "generic-kernel", "./internal/simulation/engine", "testing", false},
		{"kernel-test-imports-a-pack", "internal/simulation/des/zz_probe_test.go", module + "/internal/domainpack/softwaredelivery", "generic-kernel-tests", "./internal/simulation/des", "./internal/domainpack/softwaredelivery", true},
		{"interface-imports-a-pack-in-tests", "internal/domainpack/zz_probe_test.go", module + "/internal/domainpack/softwaredelivery", "generic-kernel-tests", "./internal/domainpack", "./internal/domainpack/softwaredelivery", true},
		{"pack-http", "internal/domainpack/softwaredelivery/zz_probe.go", "net/http", "domain-packs", "./internal/domainpack/softwaredelivery", "net/http", false},
		{"pack-imports-tenant", "internal/domainpack/softwaredelivery/zz_probe.go", module + "/internal/tenant", "domain-packs", "./internal/domainpack/softwaredelivery", "./internal/tenant", false},
		{"pack-imports-conformance", "internal/domainpack/softwaredelivery/zz_probe.go", module + "/internal/domainpack/conformance", "domain-packs", "./internal/domainpack/softwaredelivery", "./internal/domainpack/conformance", false},
		{"conformance-names-a-pack", "internal/domainpack/conformance/zz_probe.go", module + "/internal/domainpack/softwaredelivery", "conformance-is-generic", "./internal/domainpack/conformance", "./internal/domainpack/softwaredelivery", false},
		{"cmd-links-test-helpers", "cmd/api/zz_probe.go", module + "/internal/contracttest", "no-test-infra-in-production", "./cmd/api", "./internal/contracttest", false},
		{"storage-links-testing", "internal/storage/datasets/zz_probe.go", "testing", "no-test-infra-in-production", "./internal/storage/datasets", "testing", false},
	}
	for _, p := range probes {
		t.Run(p.name, func(t *testing.T) {
			pkgName := filepath.Base(filepath.Dir(p.file))
			switch pkgName {
			case "api":
				pkgName = "main"
			case "domainpack":
				pkgName = "domainpack_test" // an external test: the pack imports the interface
			}
			ov := overlay(t, root, map[string]string{p.file: "package " + pkgName + "\n\nimport _ \"" + p.imp + "\"\n"})
			g, err := load(root, []string{"-overlay=" + ov}, defaultPatterns...)
			if err != nil {
				t.Fatal(err)
			}
			violations, errs := check(g, rules)
			if len(errs) > 0 {
				t.Fatal(errs)
			}
			for _, v := range violations {
				if v.rule == p.rule && rel(v.pkg) == p.pkg && rel(v.chain[len(v.chain)-1]) == p.dep && v.test == p.test {
					if rel(v.chain[0]) != p.pkg && !p.test {
						t.Errorf("chain does not start at the package: %v", v.chain)
					}
					return
				}
			}
			t.Errorf("no %s violation of %s on %s; got %v", p.rule, p.pkg, p.dep, violations)
		})
	}
}

// Rules on a synthetic graph: what the repository cannot show yet (an LLM
// SDK, the AI package) and the rule machinery itself.
func TestSyntheticGraph(t *testing.T) {
	m := func(p string) string { return module + "/" + p }
	g := &graph{pkgs: map[string]*pkg{}}
	add := func(path string, std bool, imports ...string) {
		g.pkgs[path] = &pkg{ImportPath: path, Standard: std, Imports: imports}
		if strings.HasPrefix(path, module) {
			g.roots = append(g.roots, path)
		}
	}
	add(m("internal/model"), false)
	add(m("internal/simulation/engine"), false, m("internal/model"), m("internal/simulation/helper"))
	add(m("internal/simulation/helper"), false, "github.com/anthropics/anthropic-sdk-go")
	add("github.com/anthropics/anthropic-sdk-go", false)
	add(m("internal/domainpack"), false, m("internal/model"))
	add(m("internal/domainpack/alpha"), false, m("internal/domainpack"), m("internal/domainpack/alpha/rules"))
	add(m("internal/domainpack/alpha/rules"), false, m("internal/model"))
	add(m("internal/domainpack/beta"), false, m("internal/domainpack/alpha"))
	add(m("internal/domainpack/conformance"), false, m("internal/domainpack"))
	add(m("internal/ai"), false, m("internal/storage/postgres"), "github.com/anthropics/anthropic-sdk-go")
	add(m("internal/storage/postgres"), false, "database/sql")
	add("database/sql", true)
	add(m("internal/results/metrics"), false, m("internal/model"))
	add(m("schemas"), false)
	add(m("internal/contracttest"), false, "os/exec", "testing")
	add("os/exec", true)
	add("testing", true)
	add(m("cmd/api"), false, m("internal/ai"))
	// A forbidden package's own imports are reported through it, once.
	add(m("internal/results/trace"), false, m("internal/domainpack/gamma"))
	add(m("internal/domainpack/gamma"), false, "net/http")
	add("net/http", true)
	// A dependency missing from the graph is not trusted by an allowlist.
	add(m("internal/simulation/clock"), false, "example.com/unlisted")
	violations, errs := check(g, rules)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	got := map[string]bool{}
	for _, v := range violations {
		got[v.rule+" "+rel(v.pkg)+" "+strings.Join(mapRel(v.chain), ">")] = true
	}
	for _, want := range []string{
		// transitive, with the chain through the helper
		"generic-kernel ./internal/simulation/engine ./internal/simulation/engine>./internal/simulation/helper>github.com/anthropics/anthropic-sdk-go",
		"llm-sdks-only-in-ai ./internal/simulation/helper ./internal/simulation/helper>github.com/anthropics/anthropic-sdk-go",
		// one pack may not use another; its own subpackage is fine
		"domain-packs ./internal/domainpack/beta ./internal/domainpack/beta>./internal/domainpack/alpha",
		"ai-suggests-only ./internal/ai ./internal/ai>./internal/storage/postgres",
		"generic-kernel ./internal/results/trace ./internal/results/trace>./internal/domainpack/gamma",
		"domain-packs ./internal/domainpack/gamma ./internal/domainpack/gamma>net/http",
		"generic-kernel ./internal/simulation/clock ./internal/simulation/clock>example.com/unlisted",
	} {
		if !got[want] {
			t.Errorf("missing violation %q", want)
		}
	}
	for v := range got {
		for _, never := range []string{"domain-packs ./internal/domainpack/alpha ", "llm-sdks-only-in-ai ./internal/ai ", "llm-sdks-only-in-ai ./cmd/api ",
			"no-test-infra-in-production ./internal/contracttest ", "db-only-in-storage ./internal/storage/postgres "} {
			if strings.HasPrefix(v, never) {
				t.Errorf("unexpected violation %q", v)
			}
		}
	}

	n := 0
	for _, v := range violations {
		if v.rule == "generic-kernel" && rel(v.pkg) == "./internal/results/trace" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d generic-kernel violations for ./internal/results/trace, want 1 (through gamma)", n)
	}

	// A rule that guards nothing is an error unless it is marked future.
	typo := []rule{{name: "typo", applies: []string{"./internal/simulaton/..."}, forbid: []string{"net/http"}}}
	if _, errs := check(g, typo); len(errs) != 1 {
		t.Errorf("a rule matching no package was accepted: %v", errs)
	}
	typo[0].future = true
	if _, errs := check(g, typo); len(errs) != 0 {
		t.Errorf("a future rule was rejected: %v", errs)
	}
}

func TestPatterns(t *testing.T) {
	for _, c := range []struct {
		pattern, path string
		want          bool
	}{
		{"./internal/simulation/...", module + "/internal/simulation", true},
		{"./internal/simulation/...", module + "/internal/simulation/des", true},
		{"./internal/simulation/...", module + "/internal/simulationx", false},
		{"./internal/domainpack", module + "/internal/domainpack/softwaredelivery", false},
		{"net/http/...", "net/http/httptest", true},
		{"net", "net/url", false},
		{"./...", module + "/cmd/api", true},
		{"./...", "github.com/other/x", false},
	} {
		if got := match(c.pattern, c.path); got != c.want {
			t.Errorf("match(%q, %q) = %v", c.pattern, c.path, got)
		}
	}
	if !sameUnit(module+"/internal/domainpack/alpha", module+"/internal/domainpack/alpha/rules", 3) ||
		sameUnit(module+"/internal/domainpack/alpha", module+"/internal/domainpack/beta", 3) ||
		sameUnit(module+"/internal/domainpack", module+"/internal/domainpack/beta", 3) {
		t.Error("sameUnit")
	}
}
