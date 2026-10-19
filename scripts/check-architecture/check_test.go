package main

import (
	"bytes"
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

// The repository conforms on every platform of the matrix, every rule that
// is not marked future guards at least one package, and every Go file is
// compiled by some configuration.
func TestRepositoryConforms(t *testing.T) {
	g, err := load(repoRoot(t), nil, matrix, defaultPatterns...)
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
		module + "/internal/domainpack/conformance", module + "/scripts/check-architecture", module + "/cmd/api"} {
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
		f := filepath.Join(dir, fmt.Sprintf("f%d%s", i, filepath.Ext(path)))
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

// Go code vendored under apps/web/node_modules is not the module's: go.mod
// ignores the directory, so ./... leaves it out although the package exists
// (CB-35).
func TestVendoredGoIsNotChecked(t *testing.T) {
	root := repoRoot(t)
	ov := overlay(t, root, map[string]string{"apps/web/node_modules/zz_probe/probe.go": "package zzprobe\n\nimport _ \"net/http\"\n"})
	probe := module + "/apps/web/node_modules/zz_probe"
	alone, err := load(root, []string{"-overlay=" + ov}, host, "./apps/web/node_modules/zz_probe")
	if err != nil || !slices.Contains(alone.roots, probe) {
		t.Fatalf("the probe package is not there: the test would be vacuous (%v)", err)
	}
	g, err := load(root, []string{"-overlay=" + ov}, host, defaultPatterns...)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(g.roots, probe) {
		t.Errorf("./... lists vendored Go code")
	}
}

// A package that does not build, test files included, is an error, not a
// passing check (T-042 review A5).
func TestBrokenBuildIsAnError(t *testing.T) {
	root := repoRoot(t)
	for name, files := range map[string]map[string]string{
		"missing import":             {"internal/model/zz_probe.go": "package model\n\nimport _ \"example.com/nowhere\"\n"},
		"missing import in a test":   {"internal/model/zz_probe_test.go": "package model\n\nimport _ \"example.com/nowhere\"\n"},
		"syntax error in a test":     {"internal/model/zz_probe_test.go": "package model\n\nfunc {\n"},
		"syntax error in an xtest":   {"internal/model/zz_probe_test.go": "package model_test\n\nfunc {\n"},
		"missing import, arm64 only": {"internal/model/zz_probe_arm64.go": "package model\n\nimport _ \"example.com/nowhere\"\n"},
	} {
		configs := host
		if strings.Contains(name, "arm64") {
			configs = matrix
		}
		if _, err := load(root, []string{"-overlay=" + overlay(t, root, files)}, configs, defaultPatterns...); err == nil {
			t.Errorf("%s: load passed", name)
		}
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
		{"cmd-links-the-token-signer", "cmd/api/zz_probe.go", module + "/internal/auth/authtest", "no-test-infra-in-production", "./cmd/api", "./internal/auth/authtest", false},
		{"auth-reads-the-database", "internal/auth/zz_probe.go", module + "/internal/tenant", "auth-authenticates-only", "./internal/auth", "./internal/tenant", false},
		{"auth-imports-pgx", "internal/auth/zz_probe.go", "github.com/jackc/pgx/v5", "auth-authenticates-only", "./internal/auth", "github.com/jackc/pgx/v5", false},
		{"api-links-the-worker-scope", "cmd/api/zz_probe.go", module + "/internal/tenant/workerscope", "worker-scope-only-in-the-worker", "./cmd/api", "./internal/tenant/workerscope", false},
		{"a-service-links-the-worker-scope", "internal/project/zz_probe.go", module + "/internal/tenant/workerscope", "worker-scope-only-in-the-worker", "./internal/project", "./internal/tenant/workerscope", false},
		{"storage-links-the-worker-scope", "internal/storage/projects/zz_probe.go", module + "/internal/tenant/workerscope", "worker-scope-only-in-the-worker", "./internal/storage/projects", "./internal/tenant/workerscope", false},
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
			g, err := load(root, []string{"-overlay=" + ov}, host, defaultPatterns...)
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
	add(m("cmd/api"), false, m("internal/ai"), m("internal/auth"))
	add(m("internal/auth"), false)
	// A forbidden package's own imports are reported through it, once.
	add(m("internal/results/trace"), false, m("internal/domainpack/gamma"))
	add(m("internal/domainpack/gamma"), false, "net/http")
	add("net/http", true)
	// A dependency missing from the graph is not trusted by an allowlist.
	add(m("internal/simulation/clock"), false, "example.com/unlisted")
	// Assembly in the kernel; a pack's subpackage imported from outside.
	add(m("internal/simulation/asm"), false)
	g.pkgs[m("internal/simulation/asm")].SFiles = []string{"sys_amd64.s"}
	add(m("internal/project"), false, m("internal/domainpack/alpha/rules"))
	add(m("internal/domainpack/alpha/second"), false, m("internal/domainpack/alpha/rules"), m("internal/domainpack"))
	// A service reaches the database through the tenant scope (a gateway of
	// db-only-in-storage), not by importing the driver.
	add(m("internal/tenant"), false, "github.com/jackc/pgx/v5", m("internal/storage/postgres"))
	add("github.com/jackc/pgx/v5", false)
	add(m("internal/service"), false, m("internal/tenant"))
	add(m("internal/rawsql"), false, m("internal/tenant"), "github.com/jackc/pgx/v5")
	// Opening its own handle through the raw foundation is not going through
	// a gateway (T-009 review M2).
	add(m("internal/rawdb"), false, m("internal/storage/postgres"))
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
		"generic-kernel ./internal/simulation/asm ",
		"pack-layout ./internal/project ",
		"pack-layout ./internal/domainpack/alpha/second ",
		"db-only-in-storage ./internal/rawsql ./internal/rawsql>github.com/jackc/pgx/v5",
		"db-handles-only-in-gateways ./internal/rawdb ./internal/rawdb>./internal/storage/postgres",
		"db-handles-only-in-gateways ./internal/ai ./internal/ai>./internal/storage/postgres",
	} {
		if !got[want] {
			t.Errorf("missing violation %q", want)
		}
	}
	for v := range got {
		for _, never := range []string{"domain-packs ./internal/domainpack/alpha ", "llm-sdks-only-in-ai ./internal/ai ", "llm-sdks-only-in-ai ./cmd/api ",
			"db-only-in-storage ./internal/service ", "db-handles-only-in-gateways ./internal/service ", "db-handles-only-in-gateways ./internal/storage/",
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

// Probes from the T-042 review (review-t042/probes/arch_probes.py, ported):
// files that only another platform, cgo setting or a custom tag compiles,
// cgo and direct system calls, packages outside cmd/ and internal/, and two
// packs under one directory. Each must produce its violation.
func TestReviewProbes(t *testing.T) {
	root := repoRoot(t)
	const eng = "internal/simulation/engine/"
	sock := "package engine\n\n/*\n#include <sys/socket.h>\n*/\nimport \"C\"\n\nfunc zzSock() int { return int(C.socket(2, 1, 0)) }\n"
	http := "package engine\n\nimport _ \"net/http\"\n"
	cases := []struct {
		name  string
		files map[string]string
		rule  string
		pkg   string
		want  string // last element of the chain, or the start of the detail
	}{
		{"linux/arm64-only file", map[string]string{eng + "zz_probe_linux_arm64.go": http}, "generic-kernel", "./internal/simulation/engine", "net/http"},
		{"windows-only file", map[string]string{eng + "zz_probe_windows.go": http}, "generic-kernel", "./internal/simulation/engine", "net/http"},
		{"file only without cgo", map[string]string{eng + "zz_probe.go": "//go:build !cgo\n\n" + http}, "generic-kernel", "./internal/simulation/engine", "net/http"},
		{"file behind a custom tag", map[string]string{eng + "zz_probe.go": "//go:build integration\n\n" + http}, "every-file-built", "./internal/simulation/engine", "zz_probe.go"},
		{"arm64-only internal kernel test", map[string]string{"internal/simulation/trace/zz_probe_linux_arm64_test.go": "package trace\n\nimport _ \"" + module + "/internal/domainpack/softwaredelivery\"\n"}, "generic-kernel-tests", "./internal/simulation/trace", "./internal/domainpack/softwaredelivery"},
		{"arm64-only external kernel test", map[string]string{"internal/model/zz_probe_linux_arm64_test.go": "package model_test\n\nimport _ \"" + module + "/internal/domainpack/softwaredelivery\"\n"}, "generic-kernel-tests", "./internal/model", "./internal/domainpack/softwaredelivery"},
		{"kernel test behind a custom tag", map[string]string{"internal/model/zz_probe_test.go": "//go:build integration\n\npackage model_test\n\nimport _ \"" + module + "/internal/domainpack/softwaredelivery\"\n"}, "every-file-built", "./internal/model", "zz_probe_test.go"},
		{"cgo in the kernel", map[string]string{eng + "zz_probe_cgo.go": sock}, "generic-kernel", "./internal/simulation/engine", "C"},
		{"syscall in the kernel", map[string]string{eng + "zz_probe.go": "package engine\n\nimport \"syscall\"\n\nfunc zzSock() (int, error) { return syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0) }\n"}, "generic-kernel", "./internal/simulation/engine", "syscall"},
		{"os.StartProcess in the kernel", map[string]string{eng + "zz_probe.go": "package engine\n\nimport \"os\"\n\nfunc zzExec() { _, _ = os.StartProcess(\"/bin/sh\", nil, &os.ProcAttr{}) }\n"}, "generic-kernel", "./internal/simulation/engine", "os"},
		{"unsafe in a pack", map[string]string{"internal/domainpack/softwaredelivery/zz_probe.go": "package softwaredelivery\n\nimport _ \"unsafe\"\n"}, "domain-packs", "./internal/domainpack/softwaredelivery", "unsafe"},
		{"database/sql in a new top-level package", map[string]string{"pkg/zzprobe/probe.go": "package zzprobe\n\nimport _ \"database/sql\"\n"}, "db-only-in-storage", "./pkg/zzprobe", "database/sql"},
		{"test helpers in tools/", map[string]string{"tools/zzprobe/probe.go": "package zzprobe\n\nimport _ \"" + module + "/internal/contracttest\"\n"}, "no-test-infra-in-production", "./tools/zzprobe", "./internal/contracttest"},
		{"database/sql at the module root", map[string]string{"zz_probe.go": "package geminus\n\nimport _ \"database/sql\"\n"}, "db-only-in-storage", ".", "database/sql"},
		{"windows-only package", map[string]string{"internal/simulation/zzwin/a_windows.go": "package zzwin\n\nimport _ \"net/http\"\n"}, "generic-kernel", "./internal/simulation/zzwin", "net/http"},
		{"package behind a custom tag", map[string]string{"internal/simulation/zztag/a.go": "//go:build integration\n\npackage zztag\n\nimport _ \"net/http\"\n"}, "every-file-built", root + "/internal/simulation/zztag", "a directory with Go files"},
		{"two packs under one directory", map[string]string{"internal/domainpack/packs/alpha/a.go": "package alpha\n\nimport _ \"" + module + "/internal/domainpack/packs/beta\"\n", "internal/domainpack/packs/beta/b.go": "package beta\n"}, "pack-layout", "./internal/domainpack/packs/alpha", "./internal/domainpack/packs is not a package"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			g, err := load(root, []string{"-overlay=" + overlay(t, root, c.files)}, matrix, defaultPatterns...)
			if err != nil {
				t.Fatal(err)
			}
			violations, errs := check(g, rules)
			if len(errs) > 0 {
				t.Fatal(errs)
			}
			for _, v := range violations {
				if v.rule == c.rule && (rel(v.pkg) == c.pkg || v.pkg == c.pkg) && (strings.HasPrefix(v.detail, c.want) || (v.detail == "" && rel(v.chain[len(v.chain)-1]) == c.want)) {
					return
				}
			}
			t.Errorf("no %s violation of %s for %s; got %v", c.rule, c.pkg, c.want, violations)
		})
	}
}

// Probes from the second T-042 review (review-t042-r2/probes/arch2.py,
// arch3.py and asm/): I/O through another module package, packages in
// directories ./... does not list, standard-library packages that reach the
// file system underneath, assembly and object files, a pack's subpackages
// imported from outside it, and a third-party module in a pack. None of
// them depends on the platform, so the host configuration is enough.
func TestRound2Probes(t *testing.T) {
	root := repoRoot(t)
	const eng = "internal/simulation/engine/"
	const sd = "internal/domainpack/softwaredelivery/"
	sock := "import \"syscall\"\n\nfunc ZZSock() (int, error) { return syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0) }\n"
	use := func(pkg, path, name string) string {
		return "package " + pkg + "\n\nimport \"" + module + "/" + path + "\"\n\nvar _ = " + name + ".ZZSock\n"
	}
	asm := "#include \"textflag.h\"\n\nTEXT ·zzSocket(SB),NOSPLIT,$0-32\n\tMOVQ\t$41, AX\n\tSYSCALL\n\tMOVQ\tAX, fd+24(FP)\n\tRET\n"
	cases := []struct {
		name  string
		files map[string]string
		rule  string
		pkg   string
		want  string // last element of the chain, or the start of the detail
	}{
		{"kernel syscall through an internal package", map[string]string{"internal/zzproxy/p.go": "package zzproxy\n\n" + sock, eng + "zz_probe.go": use("engine", "internal/zzproxy", "zzproxy")}, "generic-kernel", "./internal/simulation/engine", "./internal/zzproxy"},
		{"pack process through pkg/", map[string]string{"pkg/zzproc/p.go": "package zzproc\n\nimport \"os\"\n\nfunc ZZSock() { _, _ = os.StartProcess(\"/bin/sh\", nil, &os.ProcAttr{}) }\n", sd + "zz_probe.go": use("softwaredelivery", "pkg/zzproc", "zzproc")}, "domain-packs", "./internal/domainpack/softwaredelivery", "./pkg/zzproc"},
		{"kernel package in an _ directory", map[string]string{"internal/simulation/_zz/p.go": "package zz\n\n" + sock, eng + "zz_probe.go": use("engine", "internal/simulation/_zz", "zz")}, "every-package-listed", "./internal/simulation/_zz", "a module package that ./... does not list"},
		{"kernel package in testdata", map[string]string{"internal/simulation/testdata/zz/p.go": "package zz\n\n" + sock, eng + "zz_probe.go": use("engine", "internal/simulation/testdata/zz", "zz")}, "every-package-listed", "./internal/simulation/testdata/zz", "a module package that ./... does not list"},
		{"tagged file in an _ package", map[string]string{"internal/simulation/_zz/p.go": "package zz\n\nfunc ZZSock() {}\n", "internal/simulation/_zz/q.go": "//go:build integration\n\npackage zz\n\nimport _ \"net/http\"\n", eng + "zz_probe.go": use("engine", "internal/simulation/_zz", "zz")}, "every-package-listed", "./internal/simulation/_zz", "a module package that ./... does not list"},
		{"kernel io/ioutil", map[string]string{eng + "zz_probe.go": "package engine\n\nimport \"io/ioutil\"\n\nfunc zzW() error { return ioutil.WriteFile(\"/tmp/x\", nil, 0o600) }\n"}, "generic-kernel", "./internal/simulation/engine", "io/ioutil"},
		{"kernel path/filepath", map[string]string{eng + "zz_probe.go": "package engine\n\nimport \"path/filepath\"\n\nfunc zzW() error { return filepath.Walk(\"/\", nil) }\n"}, "generic-kernel", "./internal/simulation/engine", "path/filepath"},
		{"kernel text/template", map[string]string{"internal/results/metrics/zz_probe.go": "package metrics\n\nimport _ \"text/template\"\n"}, "generic-kernel", "./internal/results/metrics", "text/template"},
		{"pack archive/zip", map[string]string{sd + "zz_probe.go": "package softwaredelivery\n\nimport _ \"archive/zip\"\n"}, "domain-packs", "./internal/domainpack/softwaredelivery", "archive/zip"},
		{"kernel assembly", map[string]string{eng + "zz_probe_stub.go": "package engine\n\nfunc zzSocket(domain, typ, proto uintptr) (fd uintptr)\n", eng + "zz_probe_amd64.s": asm}, "generic-kernel", "./internal/simulation/engine", "non-Go source files zz_probe_amd64.s"},
		{"kernel assembly for another platform", map[string]string{eng + "zz_probe_stub.go": "package engine\n\nfunc zzSocket(domain, typ, proto uintptr) (fd uintptr)\n", eng + "zz_probe_s390x.s": asm}, "generic-kernel", "./internal/simulation/engine", "non-Go source files zz_probe_s390x.s"},
		{"pack object file", map[string]string{sd + "zz_probe.syso": "\x7fELF"}, "domain-packs", "./internal/domainpack/softwaredelivery", "non-Go source files zz_probe.syso"},
		{"two packs registered from outside one directory", map[string]string{
			"internal/domainpack/packs/doc.go":     "// Package packs groups packs.\npackage packs\n",
			"internal/domainpack/packs/alpha/a.go": "package alpha\n\nimport _ \"" + module + "/internal/domainpack/packs/beta\"\n",
			"internal/domainpack/packs/beta/b.go":  "package beta\n",
			"internal/zzreg/r.go":                  "package zzreg\n\nimport (\n\t_ \"" + module + "/internal/domainpack/packs/alpha\"\n\t_ \"" + module + "/internal/domainpack/packs/beta\"\n)\n",
		}, "pack-layout", "./internal/zzreg", "imports ./internal/domainpack/packs/alpha, a subpackage of the pack ./internal/domainpack/packs"},
		{"two packs under a directory with a doc.go", map[string]string{
			"internal/domainpack/packs/doc.go":     "// Package packs groups packs.\npackage packs\n",
			"internal/domainpack/packs/alpha/a.go": "package alpha\n\nimport (\n\t\"" + module + "/internal/domainpack\"\n\t_ \"" + module + "/internal/domainpack/packs/beta\"\n)\n\nvar _ domainpack.DomainPack\n",
			"internal/domainpack/packs/beta/b.go":  "package beta\n\nimport \"" + module + "/internal/domainpack\"\n\nvar _ domainpack.DomainPack\n",
		}, "pack-layout", "./internal/domainpack/packs/alpha", "a subpackage of ./internal/domainpack/packs depends on ./internal/domainpack"},
		{"pack subpackage reaching the interface through another package", map[string]string{
			"internal/domainpack/packs/doc.go":          "// Package packs groups packs.\npackage packs\n",
			"internal/domainpack/packs/beta/b.go":       "package beta\n\nimport _ \"" + module + "/internal/domainpack/packs/beta/types\"\n",
			"internal/domainpack/packs/beta/types/t.go": "package types\n\nimport \"" + module + "/internal/domainpack\"\n\ntype NodeDefinition = domainpack.NodeDefinition\n",
		}, "pack-layout", "./internal/domainpack/packs/beta", "a subpackage of ./internal/domainpack/packs depends on ./internal/domainpack"},
		{"pack subpackage in another package's tests", map[string]string{sd + "zzsub/a.go": "package zzsub\n", "cmd/api/zz_probe_test.go": "package main\n\nimport _ \"" + module + "/internal/domainpack/softwaredelivery/zzsub\"\n"}, "pack-layout", "./cmd/api", "imports ./internal/domainpack/softwaredelivery/zzsub"},
		{"pack third-party module", map[string]string{sd + "zz_probe.go": "package softwaredelivery\n\nimport _ \"github.com/go-jose/go-jose/v4\"\n"}, "domain-packs", "./internal/domainpack/softwaredelivery", "github.com/go-jose/go-jose/v4"},
		// T-009 review M2: a service opening its own handle, around the tenant scope.
		{"service opening its own database handle", map[string]string{"internal/project/zzrawdb/r.go": "package zzrawdb\n\nimport _ \"" + module + "/internal/storage/postgres\"\n"}, "db-handles-only-in-gateways", "./internal/project/zzrawdb", "./internal/storage/postgres"},
		// Third review L1: an allowed module's other packages are not allowed.
		{"kernel x/text package that reads files", map[string]string{eng + "zz_probe.go": "package engine\n\nimport \"golang.org/x/text/unicode/cldr\"\n\nfunc zzRead() { _, _ = (&cldr.Decoder{}).DecodePath(\"/etc\") }\n"}, "generic-kernel", "./internal/simulation/engine", "golang.org/x/text/unicode/cldr"},
		{"pack x/text package that reads files", map[string]string{sd + "zz_probe.go": "package softwaredelivery\n\nimport _ \"golang.org/x/text/unicode/cldr\"\n"}, "domain-packs", "./internal/domainpack/softwaredelivery", "golang.org/x/text/unicode/cldr"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			g, err := load(root, []string{"-overlay=" + overlay(t, root, c.files)}, host, defaultPatterns...)
			if err != nil {
				t.Fatal(err)
			}
			violations, errs := check(g, rules)
			if len(errs) > 0 {
				t.Fatal(errs)
			}
			for _, v := range violations {
				if v.rule == c.rule && rel(v.pkg) == c.pkg && (strings.HasPrefix(v.detail, c.want) || (v.detail == "" && rel(v.chain[len(v.chain)-1]) == c.want)) {
					return
				}
			}
			t.Errorf("no %s violation of %s for %s; got %v", c.rule, c.pkg, c.want, violations)
		})
	}
	// The pack's own subpackages, and the pack imported from outside, are fine.
	g, err := load(root, []string{"-overlay=" + overlay(t, root, map[string]string{"cmd/api/zz_probe_test.go": "package main\n\nimport _ \"" + module + "/internal/domainpack/softwaredelivery\"\n"})}, host, defaultPatterns...)
	if err != nil {
		t.Fatal(err)
	}
	if violations, _ := check(g, rules); len(violations) > 0 {
		t.Errorf("importing a pack from outside it: %v", violations)
	}
}

// go list reports each package's module: the main module's packages, a
// dependency's, and none for the standard library.
func TestModuleInfoLoaded(t *testing.T) {
	g, err := load(repoRoot(t), nil, host, "./internal/model/validate")
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]*moduleInfo{
		module + "/internal/model/validate":        {module, true},
		"github.com/santhosh-tekuri/jsonschema/v6": {"github.com/santhosh-tekuri/jsonschema/v6", false},
		"strings": nil,
	} {
		p := g.pkgs[path]
		if p == nil {
			t.Fatalf("%s is not in the graph", path)
		}
		if (p.Module == nil) != (want == nil) || (want != nil && *p.Module != *want) {
			t.Errorf("%s: module %+v, want %+v", path, p.Module, want)
		}
	}
}

// A package under the module path that belongs to a nested module (its
// own go.mod, wired in with replace) fails closed, and the report says why
// (T-042 third review L4).
func TestNestedModule(t *testing.T) {
	m := func(p string) string { return module + "/" + p }
	g := &graph{pkgs: map[string]*pkg{}, whole: true}
	main := &moduleInfo{module, true}
	g.pkgs[m("cmd/api")] = &pkg{ImportPath: m("cmd/api"), Module: main, Imports: []string{m("tools/zz"), m("internal/zz/_hidden")}}
	g.pkgs[m("tools/zz")] = &pkg{ImportPath: m("tools/zz"), Module: &moduleInfo{m("tools"), false}}
	g.pkgs[m("internal/zz/_hidden")] = &pkg{ImportPath: m("internal/zz/_hidden"), Module: main}
	g.roots = []string{m("cmd/api")}
	violations, _ := check(g, rules)
	got := map[string]string{}
	for _, v := range violations {
		if v.rule == "every-package-listed" {
			got[rel(v.pkg)] = v.detail
		}
	}
	if d := got["./tools/zz"]; !strings.HasPrefix(d, "a package of the nested module "+m("tools")) {
		t.Errorf("nested module package: %q", d)
	}
	if d := got["./internal/zz/_hidden"]; !strings.HasPrefix(d, "a module package that ./... does not list") {
		t.Errorf("unlisted main-module package: %q", d)
	}
}

// concrete is a package path that pattern matches: its base, or a package
// below it when the base is excluded.
func concrete(pattern string, skip func(string) bool) (string, bool) {
	p := abs(pattern)
	base, subtree := strings.CutSuffix(p, "/...")
	if !skip(base) {
		return base, true
	}
	if subtree && !skip(base+"/zzsynthetic") {
		return base + "/zzsynthetic", true
	}
	return "", false
}

// Every entry of every rule is enforced: for each forbidden pattern (and
// direct-import pattern, and the third-party allowlist), a synthetic package
// the rule applies to that imports a package the pattern matches is a
// violation. Removing an entry from rules.go fails here (T-042 review C).
// via is a third-party package every rule allows, through which tests
// reach a dependency transitively.
const via = "golang.org/x/text/internal/zzvia"

func TestEveryRuleEntryIsEnforced(t *testing.T) {
	isStd := func(p string) bool { return !inModule(p) && !strings.Contains(strings.Split(p, "/")[0], ".") }
	for _, r := range rules {
		var root string
		for _, a := range r.applies {
			if c, ok := concrete(a, func(p string) bool { return matchAny(r.except, p) }); ok {
				root = c
				break
			}
		}
		if root == "" {
			t.Fatalf("%s: no package the rule applies to", r.name)
		}
		// A transitive dependency is reached through an allowed third-party
		// package, so the standard-library allowlist on the package's own
		// imports cannot be what catches it.
		probe := func(dep string, direct bool) {
			t.Helper()
			g := &graph{pkgs: map[string]*pkg{dep: {ImportPath: dep, Standard: isStd(dep)}}, roots: []string{root}}
			first := dep
			if !direct {
				first = via
				g.pkgs[via] = &pkg{ImportPath: via, Imports: []string{dep}}
			}
			rp := &pkg{ImportPath: root}
			if r.tests {
				rp.TestImports = []string{first}
			} else {
				rp.Imports = []string{first}
			}
			g.pkgs[root] = rp
			violations, errs := check(g, []rule{r})
			if len(errs) > 0 {
				t.Fatalf("%s: %v", r.name, errs)
			}
			for _, v := range violations {
				if v.rule == r.name && v.chain[len(v.chain)-1] == dep {
					return
				}
			}
			t.Errorf("%s: %s importing %s (direct %v) is not a violation", r.name, rel(root), rel(dep), direct)
		}
		allowed := func(p string) bool { return matchAny(r.allow, p) || sameUnit(root, p, r.unitDepth) || p == root }
		for _, f := range r.forbid {
			if dep, ok := concrete(f, allowed); ok {
				probe(dep, false)
			}
		}
		if r.thirdParty != nil {
			probe("example.com/zzunlisted", false)
		}
		if r.stdOnly != nil {
			probe("zzunlistedstd", true)
		}
		if r.moduleOnly != nil {
			probe(module+"/zzoutside", false)
		}
	}
}

// run's exit status: 0 conforms, 1 violations, 2 unreadable graph, bad
// flags or a broken rule (T-042 review C).
func TestRunExitCodes(t *testing.T) {
	root := repoRoot(t)
	var out, errb bytes.Buffer
	if code := run([]string{"-C", root, "-host"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "PASS architecture") {
		t.Errorf("conforming repository: %d %s %s", code, out.String(), errb.String())
	}
	errb.Reset()
	ov := overlay(t, root, map[string]string{"internal/simulation/engine/zz_probe.go": "package engine\n\nimport _ \"net/http\"\n"})
	if code := run([]string{"-C", root, "-host", "-overlay", ov}, &out, &errb); code != 1 || !strings.Contains(errb.String(), "[generic-kernel]") || !strings.Contains(errb.String(), "FAIL architecture: ") {
		t.Errorf("violation: %d %s", code, errb.String())
	}
	arm64 := overlay(t, root, map[string]string{"internal/simulation/engine/zz_probe_arm64.go": "package engine\n\nimport _ \"net/http\"\n"})
	if code := run([]string{"-C", root, "-overlay", arm64}, &out, &errb); code != 1 {
		t.Errorf("an arm64-only violation without -host: exit %d (the default must be the whole matrix)", code)
	}
	if code := run([]string{"-C", root, "-host", "-overlay", arm64}, &out, &errb); code != 0 {
		t.Errorf("-host sees only this platform: exit %d", code)
	}
	broken := overlay(t, root, map[string]string{"internal/model/zz_probe.go": "package model\n\nimport _ \"example.com/nowhere\"\n"})
	for name, args := range map[string][]string{
		"unbuildable package": {"-C", root, "-host", "-overlay", broken},
		"not a module":        {"-C", t.TempDir(), "-host"},
		"unknown flag":        {"-nope"},
	} {
		if code := run(args, &out, &errb); code != 2 {
			t.Errorf("%s: exit %d", name, code)
		}
	}
	saved := rules
	defer func() { rules = saved }()
	rules = append(slices.Clone(saved), rule{name: "typo", applies: []string{"./internal/simulaton/..."}, forbid: []string{"net/http"}})
	if code := run([]string{"-C", root, "-host"}, &out, &errb); code != 2 {
		t.Errorf("a rule that guards nothing: exit %d", code)
	}
}

// A package that exists on some platforms only, named explicitly, is read
// where it exists instead of failing the load.
func TestPlatformOnlyPackageNamedExplicitly(t *testing.T) {
	root := repoRoot(t)
	ov := overlay(t, root, map[string]string{"internal/simulation/zzwin/a_windows.go": "package zzwin\n\nimport _ \"net/http\"\n"})
	g, err := load(root, []string{"-overlay=" + ov}, matrix, "./internal/simulation/zzwin")
	if err != nil {
		t.Fatal(err)
	}
	violations, _ := check(g, rules)
	if !slices.ContainsFunc(violations, func(v violation) bool {
		return v.rule == "generic-kernel" && rel(v.chain[len(v.chain)-1]) == "net/http"
	}) {
		t.Errorf("violations %v", violations)
	}
}

// The policy itself, written out independently of rules.go: removing or
// narrowing an entry there fails here (TestEveryRuleEntryIsEnforced only
// checks the entries that exist).
func TestPolicy(t *testing.T) {
	m := func(p string) string { return module + "/" + p }
	kernel := []string{m("internal/model"), m("internal/model/validate"), m("internal/simulation/engine"), m("internal/results/metrics"), m("internal/domainpack"), m("schemas")}
	pack := m("internal/domainpack/softwaredelivery")
	appLayer := []string{m("cmd/api"), m("internal/auth"), m("internal/tenant"), m("internal/project"), m("internal/storage/postgres"), m("internal/import"), m("internal/ai"), m("internal/audit"), m("internal/config"),
		m("internal/httpapi"), m("internal/api")}
	io := []string{"net", "net/http", "net/http/httptest", "net/rpc", "net/smtp", "net/mail", "database/sql", "database/sql/driver", "os/exec", "plugin", "crypto/tls"}
	db := []string{"github.com/jackc/pgx/v5", "github.com/lib/pq", "github.com/go-sql-driver/mysql", "gorm.io/gorm", "github.com/jmoiron/sqlx"}
	llm := []string{"github.com/anthropics/anthropic-sdk-go", "github.com/openai/openai-go", "github.com/sashabaranov/go-openai", "google.golang.org/genai", "github.com/google/generative-ai-go/genai", "cloud.google.com/go/vertexai/genai", "github.com/tmc/langchaingo/llms"}
	direct := []string{"os", "os/signal", "os/user", "syscall", "unsafe", "runtime/cgo", "C", "plugin", "golang.org/x/sys/unix",
		// not on the standard-library allowlist: file system or network underneath
		"io/ioutil", "path/filepath", "text/template", "html/template", "archive/zip", "mime", "crypto/x509", "go/parser",
		"go/build", "debug/elf", "debug/buildinfo", "log", "log/syslog", "runtime/debug", "runtime/pprof", "net/url", "io/fs"}
	// module packages outside the kernel and third-party modules
	outside := []string{m("internal/zzproxy"), m("pkg/zzproc"), m("tools/x"), m("internal/simulation_x"), "github.com/go-jose/go-jose/v4", "example.com/x"}
	infra := []string{m("internal/contracttest"), m("internal/domainpack/conformance"), m("internal/auth/authtest"), m("internal/storage/dbtest")}
	type want struct {
		rule, root string
		deps       []string
		direct     bool
		test       bool
	}
	var wants []want
	for _, k := range kernel {
		wants = append(wants,
			want{"generic-kernel", k, slices.Concat(appLayer, io, db, llm, infra, outside, []string{pack, m("internal/domainpack/other"), "testing", "C", "golang.org/x/sync/errgroup"}), false, false},
			want{"generic-kernel", k, direct, true, false},
			want{"generic-kernel-tests", k, slices.Concat([]string{pack}, appLayer, db, llm, []string{"net/http", "database/sql"}), false, true})
	}
	wants = append(wants,
		want{"domain-packs", pack, slices.Concat(appLayer, io, db, llm, infra, outside, []string{m("internal/domainpack/other"), "testing", "C"}), false, false},
		want{"domain-packs", pack, direct, true, false},
		want{"conformance-is-generic", m("internal/domainpack/conformance"), slices.Concat([]string{pack}, appLayer, db, llm, []string{"net/http", "database/sql"}), false, false},
		want{"no-test-infra-in-production", m("cmd/api"), append(slices.Clone(infra), "testing"), false, false},
		want{"no-test-infra-in-production", m("pkg/anything"), infra, false, false},
		want{"llm-sdks-only-in-ai", m("internal/storage/postgres"), llm, false, false},
		want{"llm-sdks-only-in-ai", m("tools/x"), llm, false, false},
		want{"db-only-in-storage", m("internal/config"), append(slices.Clone(db), "database/sql"), false, false},
		want{"db-only-in-storage", m("pkg/anything"), db, false, false},
		want{"db-handles-only-in-gateways", m("internal/project"), []string{m("internal/storage/postgres")}, false, false},
		want{"db-handles-only-in-gateways", m("internal/httpapi"), []string{m("internal/storage/postgres")}, true, false},
		want{"db-handles-only-in-gateways", m("pkg/anything"), []string{m("internal/storage/postgres")}, false, false},
		want{"auth-authenticates-only", m("internal/auth"), slices.Concat([]string{m("internal/tenant"), m("internal/storage/postgres"), m("internal/ai"), m("cmd/api"),
			m("internal/config"), m("internal/domainpack/softwaredelivery"), m("internal/model"), m("internal/simulation/des"), "database/sql"}, db, llm), false, false},
		want{"ai-suggests-only", m("internal/ai"), slices.Concat([]string{m("internal/storage/postgres"), m("internal/tenant"), m("cmd/api"), "os/exec", "database/sql"}, db), false, false},
	)
	byName := map[string]rule{}
	for _, r := range rules {
		byName[r.name] = r
	}
	for _, name := range []string{"generic-kernel", "domain-packs"} {
		if !byName[name].goOnly {
			t.Errorf("%s allows non-Go source files", name)
		}
	}
	isStd := func(p string) bool { return !inModule(p) && !strings.Contains(strings.Split(p, "/")[0], ".") }
	for _, w := range wants {
		r, ok := byName[w.rule]
		if !ok {
			t.Errorf("rule %s is gone", w.rule)
			continue
		}
		if !matchAny(r.applies, w.root) || matchAny(r.except, w.root) {
			t.Errorf("%s does not apply to %s", w.rule, rel(w.root))
			continue
		}
		for _, dep := range w.deps {
			first := dep
			if !w.direct {
				first = via // transitive, so the allowlist on own imports does not decide
			}
			rp := &pkg{ImportPath: w.root}
			if w.test {
				rp.TestImports = []string{first}
			} else {
				rp.Imports = []string{first}
			}
			g := &graph{pkgs: map[string]*pkg{w.root: rp, via: {ImportPath: via, Imports: []string{dep}}}, roots: []string{w.root}}
			if dep != "C" { // go list never lists cgo's pseudo-package
				g.pkgs[dep] = &pkg{ImportPath: dep, Standard: isStd(dep)}
			}
			if w.direct {
				// A direct-only entry: also reachable transitively from a std package.
				g.pkgs["fmt"] = &pkg{ImportPath: "fmt", Standard: true, Imports: []string{dep}}
			}
			violations, _ := check(g, []rule{r})
			if !slices.ContainsFunc(violations, func(v violation) bool { return v.rule == w.rule && v.chain[len(v.chain)-1] == dep }) {
				t.Errorf("%s: %s importing %s is not a violation", w.rule, rel(w.root), rel(dep))
			}
		}
	}
}
