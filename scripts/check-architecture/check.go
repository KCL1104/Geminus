package main

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// pkg is one package of the build graph, as `go list -json` reports it.
type pkg struct {
	ImportPath     string
	Dir            string
	Standard       bool
	ForTest        string
	Imports        []string
	GoFiles        []string // production Go files (source rules)
	TestImports    []string
	XTestImports   []string
	IgnoredGoFiles []string
	Error          *struct{ Err string }
	DepsErrors     []*struct{ Err string }
	Module         *moduleInfo // nil for the standard library

	// Non-Go sources, built or excluded by constraints (goOnly). The first
	// configuration that lists a package lists every such file, so they
	// need no merging.
	CgoFiles, CFiles, CXXFiles, MFiles, HFiles, FFiles, SFiles []string
	SwigFiles, SwigCXXFiles, SysoFiles, IgnoredOtherFiles      []string
}

// moduleInfo is the module a package belongs to.
type moduleInfo struct {
	Path string
	Main bool // the module being checked
}

// otherFiles are p's non-Go source files.
func (p *pkg) otherFiles() []string {
	return slices.Concat(p.CgoFiles, p.CFiles, p.CXXFiles, p.MFiles, p.HFiles, p.FFiles, p.SFiles, p.SwigFiles, p.SwigCXXFiles, p.SysoFiles, p.IgnoredOtherFiles)
}

// buildConfig is one platform the graph is read for.
type buildConfig struct{ GOOS, GOARCH, CGO string }

func (c buildConfig) String() string { return c.GOOS + "/" + c.GOARCH + " cgo=" + c.CGO }

// matrix is every platform the engine is built or checked for
// (scripts/check_no_fma.sh) plus darwin and windows, each with cgo on and
// off: a file behind a platform suffix or a cgo tag is compiled somewhere,
// so its imports must be checked (T-042 review). go list evaluates build
// constraints without compiling, so no cross toolchain is needed.
var matrix = func() []buildConfig {
	var out []buildConfig
	for _, p := range []string{"linux/amd64", "linux/arm64", "linux/ppc64le", "linux/s390x", "linux/riscv64", "linux/loong64", "darwin/arm64", "windows/amd64"} {
		goos, goarch, _ := strings.Cut(p, "/")
		for _, cgo := range []string{"1", "0"} {
			out = append(out, buildConfig{goos, goarch, cgo})
		}
	}
	return out
}()

// host is the current platform only, for tests that do not need the matrix.
var host = []buildConfig{{GOOS: "", GOARCH: "", CGO: ""}}

// graph is the union of the build graphs of every configuration: a package's
// imports are those of any platform. unbuilt lists, per root, the Go files
// that no configuration compiles (a custom build tag, //go:build ignore):
// their imports are invisible, so they are reported.
type graph struct {
	pkgs    map[string]*pkg
	roots   []string
	unbuilt map[string][]string
	// unlisted are directories of the module with Go files that no
	// configuration lists as a package: a whole package behind a custom tag.
	unlisted []string
	// whole: the graph was read for the whole module (./...), so every
	// module package in it should be a root.
	whole bool
	// overlay: the -overlay replacements (absolute path -> file), which the
	// source rules read instead of the files they replace.
	overlay map[string]string
}

// load runs `go list -e -deps -test -json` in dir for each configuration
// and merges the results. A package that does not build in some
// configuration, test files included, is an error, not a pass; a package
// whose files a platform excludes entirely is simply absent there.
func load(dir string, goflags []string, configs []buildConfig, patterns ...string) (*graph, error) {
	g := &graph{pkgs: map[string]*pkg{}, unbuilt: map[string][]string{}}
	ov, err := readOverlay(goflags)
	if err != nil {
		return nil, err
	}
	g.overlay = ov
	rootSet := map[string]bool{}
	ignored := map[string]map[string]int{} // root -> file -> configurations that ignore it
	for _, c := range configs {
		roots, err := goList(dir, c, append(append([]string{"list", "-e", "-f", "{{.ImportPath}}"}, goflags...), patterns...))
		if err != nil {
			return nil, err
		}
		out, err := goList(dir, c, append(append([]string{"list", "-e", "-deps", "-test", "-json=ImportPath,Dir,Standard,ForTest,Imports,GoFiles,TestImports,XTestImports,IgnoredGoFiles,Module,CgoFiles,CFiles,CXXFiles,MFiles,HFiles,FFiles,SFiles,SwigFiles,SwigCXXFiles,SysoFiles,IgnoredOtherFiles,Error,DepsErrors"}, goflags...), patterns...))
		if err != nil {
			return nil, err
		}
		here := map[string]bool{}
		for _, r := range strings.Fields(string(roots)) {
			here[r] = true
		}
		dec := jsontext.NewDecoder(bytes.NewReader(out))
		for {
			var p pkg
			if err := json.UnmarshalDecode(dec, &p); errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				return nil, fmt.Errorf("decoding go list: %w", err)
			}
			if p.Error != nil && strings.Contains(p.Error.Err, "build constraints exclude all Go files") {
				if here[p.ImportPath] {
					if ignored[p.ImportPath] == nil {
						ignored[p.ImportPath] = map[string]int{}
					}
					for _, f := range p.IgnoredGoFiles {
						ignored[p.ImportPath][f]++
					}
				}
				continue // the package does not exist on this platform
			}
			if p.Error != nil {
				return nil, fmt.Errorf("%s (%s): %s", p.ImportPath, c, p.Error.Err)
			}
			for _, e := range p.DepsErrors {
				return nil, fmt.Errorf("%s (%s): %s", p.ImportPath, c, e.Err)
			}
			if p.ForTest != "" || strings.HasSuffix(p.ImportPath, ".test") || strings.Contains(p.ImportPath, " ") {
				continue // test variants: checked for errors above, merged through the plain entry
			}
			if here[p.ImportPath] {
				rootSet[p.ImportPath] = true
				if ignored[p.ImportPath] == nil {
					ignored[p.ImportPath] = map[string]int{}
				}
				for _, f := range p.IgnoredGoFiles {
					ignored[p.ImportPath][f]++
				}
			}
			if q := g.pkgs[p.ImportPath]; q != nil {
				q.Imports = union(q.Imports, p.Imports)
				q.GoFiles = union(q.GoFiles, p.GoFiles)
				q.TestImports = union(q.TestImports, p.TestImports)
				q.XTestImports = union(q.XTestImports, p.XTestImports)
			} else {
				g.pkgs[p.ImportPath] = &p
			}
		}
	}
	for r := range rootSet {
		g.roots = append(g.roots, r)
		for f, n := range ignored[r] {
			if n == len(configs) && len(configs) == len(matrix) {
				g.unbuilt[r] = append(g.unbuilt[r], f)
			}
		}
		slices.Sort(g.unbuilt[r])
	}
	slices.Sort(g.roots)
	if len(g.roots) == 0 {
		return nil, errors.New("go list found no package")
	}
	g.whole = slices.Equal(patterns, defaultPatterns)
	if len(configs) == len(matrix) && g.whole {
		listed := map[string]bool{}
		for _, r := range g.roots {
			listed[g.pkgs[r].Dir] = true
		}
		dirs, err := goDirs(dir, goflags)
		if err != nil {
			return nil, err
		}
		for _, d := range dirs {
			if !listed[d] {
				g.unlisted = append(g.unlisted, d)
			}
		}
	}
	return g, nil
}

// goDirs lists the module's directories that hold Go files, as ./... walks
// them (no testdata, vendor, node_modules, nested modules, or names starting
// with "." or "_"), including files a -overlay adds.
func goDirs(dir string, goflags []string) ([]string, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata" || name == "vendor" || name == "node_modules") {
				return filepath.SkipDir
			}
			if path != root {
				if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") {
			set[filepath.Dir(path)] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, f := range goflags {
		ov, ok := strings.CutPrefix(f, "-overlay=")
		if !ok {
			continue
		}
		data, err := os.ReadFile(ov)
		if err != nil {
			return nil, err
		}
		var o struct{ Replace map[string]string }
		if err := json.Unmarshal(data, &o); err != nil {
			return nil, fmt.Errorf("overlay: %w", err)
		}
		for path, src := range o.Replace {
			rel, err := filepath.Rel(root, path)
			if err != nil || src == "" || !strings.HasSuffix(path, ".go") || strings.HasPrefix(rel, "..") {
				continue
			}
			skip := false
			for _, e := range strings.Split(filepath.Dir(rel), string(filepath.Separator)) {
				if strings.HasPrefix(e, ".") && e != "." || strings.HasPrefix(e, "_") || e == "testdata" || e == "vendor" || e == "node_modules" {
					skip = true
				}
			}
			if !skip {
				set[filepath.Dir(path)] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for d := range set {
		out = append(out, d)
	}
	slices.Sort(out)
	return out, nil
}

func union(a, b []string) []string {
	for _, s := range b {
		if !slices.Contains(a, s) {
			a = append(a, s)
		}
	}
	return a
}

func goList(dir string, c buildConfig, args []string) ([]byte, error) {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	if c.GOOS != "" {
		cmd.Env = append(os.Environ(), "GOOS="+c.GOOS, "GOARCH="+c.GOARCH, "CGO_ENABLED="+c.CGO)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go %s (%s): %w: %s", strings.Join(args[:1], " "), c, err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// violation is one forbidden dependency with the import chain that reaches
// it, or (detail set) a structural problem of one package.
type violation struct {
	rule, why, pkg string
	chain          []string // pkg ... dep
	test           bool
	detail         string
}

func (v violation) String() string {
	if v.detail != "" {
		return fmt.Sprintf("[%s] %s: %s\n    rule: %s", v.rule, rel(v.pkg), v.detail, v.why)
	}
	kind := "depends on"
	if v.test {
		kind = "tests depend on"
	}
	return fmt.Sprintf("[%s] %s %s %s\n    via %s\n    rule: %s",
		v.rule, rel(v.pkg), kind, rel(v.chain[len(v.chain)-1]), strings.Join(mapRel(v.chain), " -> "), v.why)
}

func rel(p string) string {
	if p == module {
		return "."
	}
	if r, ok := strings.CutPrefix(p, module+"/"); ok {
		return "./" + r
	}
	return p
}

func mapRel(ps []string) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = rel(p)
	}
	return out
}

// abs turns a module-relative pattern into an import-path pattern.
func abs(pattern string) string {
	if r, ok := strings.CutPrefix(pattern, "./"); ok {
		if r == "..." {
			return module + "/..."
		}
		return module + "/" + r
	}
	return pattern
}

func match(pattern, path string) bool {
	p := abs(pattern)
	if base, ok := strings.CutSuffix(p, "/..."); ok {
		return path == base || strings.HasPrefix(path, base+"/")
	}
	return path == p
}

func matchAny(patterns []string, path string) bool {
	return slices.ContainsFunc(patterns, func(p string) bool { return match(p, path) })
}

func inModule(path string) bool { return path == module || strings.HasPrefix(path, module+"/") }

func sameUnit(a, b string, depth int) bool {
	if depth <= 0 || !inModule(a) || !inModule(b) {
		return false
	}
	ea, eb := strings.Split(rel(a), "/"), strings.Split(rel(b), "/")
	// rel paths start with "."; the unit is "." plus depth elements.
	if len(ea) <= depth || len(eb) <= depth {
		return false
	}
	return slices.Equal(ea[:depth+1], eb[:depth+1])
}

// check applies every rule to every root package. A rule whose applies
// patterns match no root is reported unless it is marked future, so a typo
// cannot silently guard nothing.
func check(g *graph, rules []rule) (violations []violation, errs []error) {
	violations = structure(g)
	if sv, err := sourceViolations(g); err != nil {
		errs = append(errs, err)
	} else {
		violations = append(violations, sv...)
	}
	for _, r := range rules {
		matched := 0
		for _, root := range g.roots {
			if !matchAny(r.applies, root) || matchAny(r.except, root) {
				continue
			}
			matched++
			for _, imp := range g.pkgs[root].Imports {
				if std := g.pkgs[imp] != nil && g.pkgs[imp].Standard; r.stdOnly != nil && std && !matchAny(r.stdOnly, imp) {
					violations = append(violations, violation{rule: r.name, why: r.why, pkg: root, chain: []string{root, imp}})
				}
			}
			if files := g.pkgs[root].otherFiles(); r.goOnly && len(files) > 0 {
				slices.Sort(files)
				violations = append(violations, violation{rule: r.name, why: r.why, pkg: root,
					detail: "non-Go source files " + strings.Join(slices.Compact(files), ", ") + ": assembly, C and object files can make system calls that no import shows"})
			}
			start := []string{root}
			if r.tests {
				p := g.pkgs[root]
				start = slices.Concat(p.TestImports, p.XTestImports)
			}
			violations = append(violations, walk(g, r, root, start)...)
		}
		if matched == 0 && !r.future {
			errs = append(errs, fmt.Errorf("rule %s applies to no package: fix its patterns or mark it future", r.name))
		}
	}
	return violations, errs
}

// structure reports what no import rule can see: Go files that no
// configuration of the matrix compiles, and a pack that is not a direct
// child of internal/domainpack (two packs under one directory would count
// as one unit and could import each other).
func structure(g *graph) []violation {
	var out []violation
	for _, d := range g.unlisted {
		out = append(out, violation{rule: "every-file-built", pkg: d, detail: "a directory with Go files that no platform of the matrix builds as a package, so its imports are not checked",
			why: "a package only a custom tag compiles could import anything (T-042 review); build it on a matrix platform or remove it"})
	}
	if g.whole {
		listed := map[string]bool{}
		for _, r := range g.roots {
			listed[r] = true
		}
		var linked []string
		for path := range g.pkgs {
			if inModule(path) && !listed[path] {
				linked = append(linked, path)
			}
		}
		slices.Sort(linked)
		for _, p := range linked {
			detail := "a module package that ./... does not list (testdata, or a name starting with \".\" or \"_\"), so no rule checks it as a package, but something imports it"
			if m := g.pkgs[p].Module; m != nil && !m.Main {
				// A nested go.mod under the module path, wired in with replace
				// (T-042 third review L4): still refused, for the real reason.
				detail = "a package of the nested module " + m.Path + " under this module's path, which ./... does not list, so no rule checks it as a package, but something imports it"
			}
			out = append(out, violation{rule: "every-package-listed", pkg: p, detail: detail,
				why: "every package that is linked must be checked (T-042 review N2); move it to a listed directory"})
		}
	}
	for _, root := range g.roots {
		p := g.pkgs[root]
		// A subpackage that uses the DomainPack interface is refused, so it
		// does not implement a second pack directly. This is no proof: a
		// generic declared in a subpackage, or a second pack type in the
		// pack's own package, passes (T-042 third review L2), which
		// domainpack.NewRegistry refuses at registration instead.
		if unit, ok := packUnit(root); ok && root != unit && reaches(g, root, module+"/internal/domainpack") {
			out = append(out, violation{rule: "pack-layout", pkg: root, detail: "a subpackage of " + rel(unit) + " depends on ./internal/domainpack: only a pack's own package implements the DomainPack interface",
				why: "one directory holds one pack; its subpackages are its internals (T-042 review N4)"})
		}
		for _, imp := range slices.Concat(p.Imports, p.TestImports, p.XTestImports) {
			if unit, ok := packUnit(imp); ok && imp != unit && !sameUnit(root, imp, 3) {
				out = append(out, violation{rule: "pack-layout", pkg: root, detail: "imports " + rel(imp) + ", a subpackage of the pack " + rel(unit) + "; only the pack itself is imported from outside it",
					why: "a pack's subpackages are its own, so two packs under one directory can only be one pack (T-042 review N4)"})
			}
		}
		for _, f := range g.unbuilt[root] {
			out = append(out, violation{rule: "every-file-built", pkg: root, detail: f + " is excluded by build constraints on every platform of the matrix, so its imports are not checked",
				why: "a file only a custom tag compiles could import anything (T-042 review); build it on a matrix platform or remove it"})
		}
		if r, ok := strings.CutPrefix(root, module+"/internal/domainpack/"); ok {
			unit, _, nested := strings.Cut(r, "/")
			if nested && unit != "conformance" && !slices.Contains(g.roots, module+"/internal/domainpack/"+unit) {
				out = append(out, violation{rule: "pack-layout", pkg: root, detail: "./internal/domainpack/" + unit + " is not a package: a pack is a direct child of internal/domainpack, and its subpackages are its own",
					why: "domain-packs groups a pack with its subpackages; a directory holding several packs would let them import each other"})
			}
		}
	}
	return out
}

// reaches reports whether target is in the production dependency closure
// of from.
func reaches(g *graph, from, target string) bool {
	seen := map[string]bool{from: true}
	queue := []string{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == target {
			return true
		}
		if p := g.pkgs[cur]; p != nil {
			for _, imp := range p.Imports {
				if !seen[imp] {
					seen[imp] = true
					queue = append(queue, imp)
				}
			}
		}
	}
	return false
}

// packUnit is the direct child of internal/domainpack that path is in: a
// pack, or the conformance suite, whose subpackages are equally its own.
func packUnit(path string) (string, bool) {
	r, ok := strings.CutPrefix(path, module+"/internal/domainpack/")
	if !ok {
		return "", false
	}
	unit, _, _ := strings.Cut(r, "/")
	return module + "/internal/domainpack/" + unit, true
}

// walk visits the dependency closure from start in import order and reports
// each forbidden package once, with the first chain that reaches it.
func walk(g *graph, r rule, root string, start []string) []violation {
	parent := map[string]string{root: ""}
	queue := []string{root}
	if r.tests {
		queue = nil
		for _, s := range start {
			if _, seen := parent[s]; !seen {
				parent[s] = root
				queue = append(queue, s)
			}
		}
	}
	var out []violation
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		p := g.pkgs[cur]
		if cur != root && bad(r, root, cur, p) {
			chain := []string{cur}
			for at := parent[cur]; at != ""; at = parent[at] {
				chain = append([]string{at}, chain...)
			}
			out = append(out, violation{rule: r.name, why: r.why, pkg: root, chain: chain, test: r.tests})
			continue // what it imports is reported through it
		}
		if p == nil || (cur != root && matchAny(r.through, cur)) {
			continue // a gateway's own dependencies are its business
		}
		for _, imp := range p.Imports {
			if cur != root && slices.Contains(r.bring[cur], imp) {
				continue // what this gateway may bring, and only through it
			}
			if _, seen := parent[imp]; !seen {
				parent[imp] = cur
				queue = append(queue, imp)
			}
		}
	}
	return out
}

func bad(r rule, root, dep string, p *pkg) bool {
	if sameUnit(root, dep, r.unitDepth) || matchAny(r.allow, dep) {
		return false
	}
	if matchAny(r.forbid, dep) {
		return true
	}
	if inModule(dep) {
		return r.moduleOnly != nil && !matchAny(r.moduleOnly, dep)
	}
	if r.thirdParty == nil {
		return false
	}
	if p == nil {
		return true // every dependency is in the graph; an unknown one is not trusted
	}
	return !p.Standard && !matchAny(r.thirdParty, dep)
}
