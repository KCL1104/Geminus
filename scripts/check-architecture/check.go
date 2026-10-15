package main

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"
)

// pkg is one package of the build graph, as `go list -json` reports it.
type pkg struct {
	ImportPath   string
	Standard     bool
	ForTest      string
	Imports      []string
	TestImports  []string
	XTestImports []string
	Error        *struct{ Err string }
	DepsErrors   []*struct{ Err string }
}

// graph maps import paths to packages; roots are the packages listed by the
// patterns (not their dependencies).
type graph struct {
	pkgs  map[string]*pkg
	roots []string
}

// load runs `go list -e -deps -test -json` in dir. A package that does not
// build (a missing import, a syntax error) is an error, not a pass. Test
// variants ("p [q.test]", "q.test") are dropped: a package's test imports are
// on its plain entry, and their dependencies are ordinary packages of the graph.
func load(dir string, goflags []string, patterns ...string) (*graph, error) {
	roots, err := goList(dir, append(append([]string{"list", "-e", "-f", "{{.ImportPath}}"}, goflags...), patterns...))
	if err != nil {
		return nil, err
	}
	out, err := goList(dir, append(append([]string{"list", "-e", "-deps", "-test", "-json=ImportPath,Standard,ForTest,Imports,TestImports,XTestImports,Error,DepsErrors"}, goflags...), patterns...))
	if err != nil {
		return nil, err
	}
	g := &graph{pkgs: map[string]*pkg{}, roots: strings.Fields(string(roots))}
	dec := jsontext.NewDecoder(bytes.NewReader(out))
	for {
		var p pkg
		if err := json.UnmarshalDecode(dec, &p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decoding go list: %w", err)
		}
		if p.ForTest != "" || strings.HasSuffix(p.ImportPath, ".test") || strings.Contains(p.ImportPath, " ") {
			continue
		}
		if p.Error != nil {
			return nil, fmt.Errorf("%s: %s", p.ImportPath, p.Error.Err)
		}
		for _, e := range p.DepsErrors {
			return nil, fmt.Errorf("%s: %s", p.ImportPath, e.Err)
		}
		g.pkgs[p.ImportPath] = &p
	}
	for _, r := range g.roots {
		if g.pkgs[r] == nil {
			return nil, fmt.Errorf("go list did not describe %s", r)
		}
	}
	return g, nil
}

func goList(dir string, args []string) ([]byte, error) {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go %s: %w: %s", strings.Join(args[:1], " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// violation is one forbidden dependency with the import chain that reaches it.
type violation struct {
	rule, why, pkg string
	chain          []string // pkg ... dep
	test           bool
}

func (v violation) String() string {
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
	for _, r := range rules {
		matched := 0
		for _, root := range g.roots {
			if !matchAny(r.applies, root) || matchAny(r.except, root) {
				continue
			}
			matched++
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
		if p == nil {
			continue
		}
		for _, imp := range p.Imports {
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
	if r.thirdParty == nil || inModule(dep) || dep == "C" || dep == "unsafe" {
		return false
	}
	if p == nil {
		return true // every dependency is in the graph; an unknown one is not trusted
	}
	return !p.Standard && !matchAny(r.thirdParty, dep)
}
