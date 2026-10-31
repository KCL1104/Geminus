package main

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The Eino packages the design uses (docs/design/H-056_INVESTIGATIONS_ON_EINO.md
// §6.3–§6.5): graphs, messages, the ChatModel and tool interfaces, tools from
// functions, callbacks and the ADK agent. go.sum holds what they link at
// v0.9.21 (ADR-031); an Eino package outside this list may need more.
var einoPackages = []string{
	"github.com/cloudwego/eino/compose",
	"github.com/cloudwego/eino/schema",
	"github.com/cloudwego/eino/components/model",
	"github.com/cloudwego/eino/components/tool",
	"github.com/cloudwego/eino/components/tool/utils",
	"github.com/cloudwego/eino/callbacks",
	"github.com/cloudwego/eino/adk",
}

func blankImports(pkg string, imports ...string) string {
	var b strings.Builder
	b.WriteString("package " + pkg + "\n\nimport (\n")
	for _, imp := range imports {
		b.WriteString("\t_ \"" + imp + "\"\n")
	}
	b.WriteString(")\n")
	return b.String()
}

// ADR-031 (H-058, H-059): the role graphs and their ChatModel adapter
// (internal/aigraph) and the investigation loop are the only packages that
// import Eino; the worker's binary and handlers reach it through them; the
// API never links a role graph; the kernel, the packs and the HTTP transport
// never link Eino at all, not even through a gateway. None of these packages
// exists yet (E0 adds the rules only), so each probe adds them through a go
// build overlay.
func TestEinoPlacement(t *testing.T) {
	root := repoRoot(t)
	m := func(p string) string { return module + "/" + p }
	graph := blankImports("proposal", "github.com/cloudwego/eino/compose")
	photo := blankImports("photo", "github.com/cloudwego/eino/compose")
	cases := []struct {
		name  string
		files map[string]string
		// rule, pkg and dep: the violation that must be reported (dep is the
		// last element of its chain, through the gateway named in via).
		rule, pkg, dep, via string
	}{
		{"the API links a role graph", map[string]string{
			"internal/aigraph/proposal/zz_probe.go": graph,
			"cmd/api/zz_probe.go":                   blankImports("main", m("internal/aigraph/proposal")),
		}, "aigraph-only-in-the-worker", "./cmd/api", "./internal/aigraph/proposal", ""},
		{"the route assembly links the ChatModel adapter", map[string]string{
			"internal/aigraph/einomodel/zz_probe.go": blankImports("einomodel", "github.com/cloudwego/eino/components/model"),
			"internal/api/zz_probe.go":               blankImports("api", m("internal/aigraph/einomodel")),
		}, "aigraph-only-in-the-worker", "./internal/api", "./internal/aigraph/einomodel", ""},
		{"a service the API shares links a role graph", map[string]string{
			"internal/aigraph/proposal/zz_probe.go": graph,
			"internal/runs/zz_probe.go":             blankImports("runs", m("internal/aigraph/proposal")),
		}, "aigraph-only-in-the-worker", "./internal/runs", "./internal/aigraph/proposal", ""},
		{"the engine links Eino through a role graph", map[string]string{
			"internal/aigraph/proposal/zz_probe.go":  graph,
			"internal/simulation/engine/zz_probe.go": blankImports("engine", m("internal/aigraph/proposal")),
		}, "no-eino-in-kernel-packs-transport", "./internal/simulation/engine", "github.com/cloudwego/eino/compose", "./internal/aigraph/proposal"},
		{"a pack links Eino through a role graph", map[string]string{
			"internal/aigraph/proposal/zz_probe.go":           graph,
			"internal/domainpack/electronicsline/zz_probe.go": blankImports("electronicsline", m("internal/aigraph/proposal")),
		}, "no-eino-in-kernel-packs-transport", "./internal/domainpack/electronicsline", "github.com/cloudwego/eino/compose", "./internal/aigraph/proposal"},
		{"the HTTP transport links Eino through the investigation loop", map[string]string{
			"internal/investigations/loop/zz_probe.go": blankImports("loop", "github.com/cloudwego/eino/compose"),
			"internal/httpapi/zz_probe.go":             blankImports("httpapi", m("internal/investigations/loop")),
		}, "no-eino-in-kernel-packs-transport", "./internal/httpapi", "github.com/cloudwego/eino/compose", "./internal/investigations/loop"},
		// Only the photo pipeline may front a role graph among the AI
		// packages: the context builder reaching Eino's closure through the
		// photo graph is I/O underneath (Eino links net, os and syscall).
		{"the context builder links the photo graph", map[string]string{
			"internal/aigraph/photo/zz_probe.go": photo,
			"internal/ai/aicontext/zz_probe.go":  blankImports("aicontext", m("internal/aigraph/photo")),
		}, "ai-is-pure-but-the-adapter", "./internal/ai/aicontext", "", "./internal/aigraph/photo"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			files, _ := uniqueProbes(c.files)
			g, err := load(root, []string{"-overlay=" + overlay(t, root, files)}, host, defaultPatterns...)
			if err != nil {
				t.Fatal(err)
			}
			violations, errs := check(g, rules)
			if len(errs) > 0 {
				t.Fatal(errs)
			}
			for _, v := range violations {
				if v.rule != c.rule || rel(v.pkg) != c.pkg || v.detail != "" {
					continue
				}
				last := rel(v.chain[len(v.chain)-1])
				if c.dep != "" && last != c.dep {
					continue
				}
				if c.via != "" && !slices.Contains(mapRel(v.chain), c.via) {
					continue
				}
				return
			}
			t.Errorf("no %s violation of %s on %s (via %s); got %v", c.rule, c.pkg, c.dep, c.via, violations)
		})
	}
}

// The placements the design agrees on break no rule at all: the adapter and
// a role graph importing the Eino packages and the pure AI packages they
// wrap, the worker, its proposal and analysis handlers, the live eval and
// the photo pipeline linking the graphs (the photo pipeline only through
// the photo graph, its one gateway), and the investigation loop importing
// Eino. Where the loop runs, and with which database role, is the
// architecture delta's (H-060, H-061 pending); this checks the Eino fence
// only. The whole matrix is read, so every platform's closure of Eino must
// be on aigraph-has-no-storage's allowlist and none may link sonic's JIT.
func TestEinoAgreedPlacementsConform(t *testing.T) {
	root := repoRoot(t)
	m := func(p string) string { return module + "/" + p }
	files, _ := uniqueProbes(map[string]string{
		"internal/aigraph/einomodel/a.go": blankImports("einomodel", append(slices.Clone(einoPackages), m("internal/ai/provider"))...),
		"internal/aigraph/proposal/a.go": blankImports("proposal", "github.com/cloudwego/eino/compose", m("internal/aigraph/einomodel"),
			m("internal/ai/aicontext"), m("internal/ai/review"), m("internal/model"), m("internal/domainpack"), m("internal/ids"), m("prompts")),
		"internal/aigraph/analysis/a.go": blankImports("analysis", "github.com/cloudwego/eino/compose", m("internal/aigraph/einomodel"),
			m("internal/ai/analyst"), m("internal/results/compare")),
		"internal/aigraph/photo/a.go":              blankImports("photo", "github.com/cloudwego/eino/compose", m("internal/aigraph/einomodel")),
		"internal/ai/photopipeline/zz_probe.go":    blankImports("photopipeline", m("internal/aigraph/photo")),
		"internal/proposals/generator/zz_probe.go": blankImports("generator", m("internal/aigraph/proposal")),
		"internal/analyses/analyzer/zz_probe.go":   blankImports("analyzer", m("internal/aigraph/analysis")),
		"internal/aievals/zz_probe.go":             blankImports("aievals", m("internal/aigraph/proposal")),
		"cmd/worker/zz_probe.go":                   blankImports("main", m("internal/aigraph/proposal")),
		"cmd/ai-eval-live/zz_probe.go":             blankImports("main", m("internal/aigraph/proposal")),
		"internal/investigations/loop/a.go":        blankImports("loop", "github.com/cloudwego/eino/compose", "github.com/cloudwego/eino/components/tool/utils", "github.com/cloudwego/eino/schema"),
	})
	g, err := load(root, []string{"-overlay=" + overlay(t, root, files)}, matrix, defaultPatterns...)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{m("internal/aigraph/einomodel"), m("internal/aigraph/photo"), m("internal/investigations/loop")} {
		if !slices.Contains(g.roots, want) {
			t.Fatalf("%s is not in the graph: the test would be vacuous", rel(want))
		}
	}
	if !slices.ContainsFunc(g.pkgs[m("internal/aigraph/einomodel")].Imports, func(p string) bool { return p == "github.com/cloudwego/eino/adk" }) {
		t.Fatal("the adapter probe does not import Eino: the test would be vacuous")
	}
	violations, errs := check(g, rules)
	for _, e := range errs {
		t.Error(e)
	}
	for _, v := range violations {
		t.Error(v)
	}
}

// sonicCompatLine is the one line sonic v1.15.0 writes to stderr when the
// Eino packages are linked on go1.27 (its compat path: encoding/json, no
// JIT, no executable memory). It comes from an init function of
// github.com/bytedance/sonic/ast, before main runs, so it is not a JSON
// line and does not pass internal/observability's filter: a reviewed
// exception to D-093 (a) and D-094 recorded in ADR-031. It is constant text
// and carries no data. A second line (sonic/decoder or sonic/encoder linked,
// each with its own warning), a changed one, or none (the native JIT path)
// fails here.
const sonicCompatLine = "WARNING: sonic/ast only supports (go1.17~1.26 and amd64 CPU) or (go1.20~1.26 and arm64 CPU), but your environment is not suitable and will fallback to encoding/json\n"

// A program that links the Eino packages the design uses selects sonic's
// compat path (UseStdJSON) and writes exactly sonicCompatLine to stderr at
// init. No product binary links Eino yet (E0); this pins what the first one
// will print.
func TestSonicRunsItsCompatPath(t *testing.T) {
	root := repoRoot(t)
	src := blankImports("main", einoPackages...) + `
import (
	"fmt"

	"github.com/bytedance/sonic"
)

func main() { fmt.Print(sonic.APIKind == sonic.UseStdJSON) }
`
	ov := overlay(t, root, map[string]string{"internal/aigraph/zzsonicline/main.go": src})
	bin := filepath.Join(t.TempDir(), "sonicline")
	build := exec.Command("go", "build", "-overlay="+ov, "-o", bin, "./internal/aigraph/zzsonicline")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v: %s", err, out)
	}
	var stdout, stderr bytes.Buffer
	run := exec.Command(bin)
	run.Stdout, run.Stderr = &stdout, &stderr
	if err := run.Run(); err != nil {
		t.Fatalf("running the probe: %v: %s", err, stderr.String())
	}
	if stdout.String() != "true" {
		t.Errorf("sonic.APIKind == UseStdJSON: %q, want true (the compat path)", stdout.String())
	}
	if stderr.String() != sonicCompatLine {
		t.Errorf("stderr at init:\n%q\nwant exactly\n%q", stderr.String(), sonicCompatLine)
	}
}

// The AI rules let Eino's closure bring exactly the imports that open
// nothing (einoBrings, ai-suggests-only's uuid), and only through the
// package that has them: database/sql or a driver through uuid; the driver,
// net or golang.org/x/sys through any other package, or imported directly;
// and net/http through anything are still violations (as for
// db-only-in-storage, TestTheUUIDGatewayBringsOnlyTheDriver).
func TestEinoBringsOnlyTheirImports(t *testing.T) {
	byName := map[string]rule{}
	for _, r := range rules {
		byName[r.name] = r
	}
	const (
		uuid, schema, logrus = "github.com/google/uuid", "github.com/eino-contrib/jsonschema", "github.com/sirupsen/logrus"
		driver, sql, pgx     = "database/sql/driver", "database/sql", "github.com/jackc/pgx/v5"
		unix, windows        = "golang.org/x/sys/unix", "golang.org/x/sys/windows"
		easyjson             = "github.com/mailru/easyjson/buffer"
	)
	std := map[string]bool{driver: true, sql: true, "net": true, "net/http": true, "database/sql/internal": true}
	type tc struct {
		name    string
		imports map[string][]string // the root's are under ""
		wantBad []string
	}
	common := []tc{
		{"the driver through uuid", map[string][]string{"": {uuid}, uuid: {driver}, driver: {"database/sql/internal"}}, nil},
		{"database/sql through uuid", map[string][]string{"": {uuid}, uuid: {driver, sql}}, []string{sql}},
		{"a driver through uuid", map[string][]string{"": {uuid}, uuid: {pgx}}, []string{pgx}},
		{"the driver through another package", map[string][]string{"": {schema}, schema: {driver}}, []string{driver}},
		{"the driver itself", map[string][]string{"": {driver}}, []string{driver}},
	}
	graphOnly := []tc{
		{"x/sys through logrus", map[string][]string{"": {logrus}, logrus: {unix, windows}, windows: {"net"}}, nil},
		{"x/sys through another package", map[string][]string{"": {schema}, schema: {unix}}, []string{unix}},
		{"x/sys itself", map[string][]string{"": {windows}}, []string{windows}},
		{"net/http through logrus", map[string][]string{"": {logrus}, logrus: {"net/http"}}, []string{"net/http"}},
		// net with cgo imports "C", which no allowlist trusts: brought, it is
		// not walked.
		{"net through uuid, jsonschema and easyjson's buffer", map[string][]string{"": {uuid, schema, easyjson}, uuid: {"net"}, schema: {"net"}, easyjson: {"net"}, "net": {"C"}}, nil},
		{"net through logrus", map[string][]string{"": {logrus}, logrus: {"net"}}, []string{"net"}},
		{"net through x/sys", map[string][]string{"": {schema}, schema: {windows}, windows: {"net"}}, []string{windows}},
		{"net itself", map[string][]string{"": {"net"}}, []string{"net"}},
		{"net/http through jsonschema", map[string][]string{"": {schema}, schema: {"net/http"}}, []string{"net/http"}},
	}
	for _, c := range []struct {
		rule, root string
		cases      []tc
	}{
		{"aigraph-has-no-storage", module + "/internal/aigraph/einomodel", slices.Concat(common, graphOnly)},
		{"ai-suggests-only", module + "/internal/ai/photopipeline", common},
	} {
		r, ok := byName[c.rule]
		if !ok {
			t.Fatalf("rule %s is gone", c.rule)
		}
		for _, k := range c.cases {
			g := &graph{pkgs: map[string]*pkg{}, roots: []string{c.root}}
			for from, imps := range k.imports {
				path := from
				if from == "" {
					path = c.root
				}
				g.pkgs[path] = &pkg{ImportPath: path, Standard: std[path], Imports: imps}
				for _, imp := range imps {
					if g.pkgs[imp] == nil {
						g.pkgs[imp] = &pkg{ImportPath: imp, Standard: std[imp]}
					}
				}
			}
			violations, errs := check(g, []rule{r})
			if len(errs) > 0 {
				t.Fatalf("%s, %s: %v", c.rule, k.name, errs)
			}
			var got []string
			for _, v := range violations {
				got = append(got, v.chain[len(v.chain)-1])
			}
			slices.Sort(got)
			got = slices.Compact(got)
			if !slices.Equal(got, k.wantBad) {
				t.Errorf("%s, %s: violations on %v, want %v", c.rule, k.name, got, k.wantBad)
			}
		}
	}
}

// The photo pipeline's own rule keeps every list of the rule it is excepted
// from (design §6.2): only the gateway is new.
func TestPhotopipelineRuleKeepsTheAIPackagesLists(t *testing.T) {
	byName := map[string]rule{}
	for _, r := range rules {
		byName[r.name] = r
	}
	pure, photo := byName["ai-is-pure-but-the-adapter"], byName["photopipeline-is-pure-but-its-graph"]
	if photo.name == "" {
		t.Fatal("photopipeline-is-pure-but-its-graph is gone")
	}
	if !slices.Contains(pure.except, "./internal/ai/photopipeline") || !slices.Equal(photo.applies, []string{"./internal/ai/photopipeline"}) {
		t.Errorf("the photo pipeline moves between the rules as a whole: except %v, applies %v", pure.except, photo.applies)
	}
	if !slices.Equal(photo.forbid, pure.forbid) || !slices.Equal(photo.thirdParty, pure.thirdParty) || !slices.Equal(photo.stdOnly, pure.stdOnly) ||
		photo.goOnly != pure.goOnly || !slices.Equal(photo.allow, pure.allow) || photo.moduleOnly != nil || photo.bring != nil {
		t.Errorf("photopipeline-is-pure-but-its-graph differs from ai-is-pure-but-the-adapter beyond its gateway")
	}
	if !slices.Equal(photo.through, []string{"./internal/aigraph/photo"}) || pure.through != nil {
		t.Errorf("gateways: photo pipeline %v, AI packages %v; want only ./internal/aigraph/photo, for the photo pipeline", photo.through, pure.through)
	}
}
