package main

import (
	"encoding/json/v2"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Source rules: what the import graph cannot show, read from the code of a
// package's production files (go list's GoFiles, every platform of the
// matrix; a -overlay replaces files as it does for the build).

// guardedVar is a package variable only that package's tests may replace.
type guardedVar struct {
	pkg, name, why string
}

// guardedVars: engine.validateIR is the validator Compile runs; a test
// stands in another to show that the kernel's own list refuses what it
// does not execute (T-046 review M2). A production file that assigns it, or
// takes its address, could run models the validator refuses (T-046 phase 2
// review I2, closed by T-047 before it lifted the first primitive gate).
var guardedVars = []guardedVar{
	{"./internal/simulation/engine", "validateIR", "Compile always runs validate.IR; only the package's tests may stand in another validator (T-046 review I2)"},
}

// readOverlay returns the -overlay replacements among goflags (absolute
// path -> replacement file), nil without one.
func readOverlay(goflags []string) (map[string]string, error) {
	for _, f := range goflags {
		path, ok := strings.CutPrefix(f, "-overlay=")
		if !ok {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("overlay: %w", err)
		}
		var o struct{ Replace map[string]string }
		if err := json.Unmarshal(data, &o); err != nil {
			return nil, fmt.Errorf("overlay: %w", err)
		}
		return o.Replace, nil
	}
	return nil, nil
}

// sourceViolations checks the guarded variables of every root they name.
func sourceViolations(g *graph) ([]violation, error) {
	var out []violation
	for _, gv := range guardedVars {
		p := g.pkgs[abs(gv.pkg)]
		if p == nil {
			if g.whole {
				return nil, fmt.Errorf("source rule for %s: no such package", gv.pkg)
			}
			continue
		}
		if p.Dir == "" {
			// A synthetic graph's package (tests of the import rules): it
			// has no files to read. go list always reports a directory.
			continue
		}
		files := slices.Clone(p.GoFiles)
		slices.Sort(files)
		declared := 0
		for _, f := range files {
			path := filepath.Join(p.Dir, f)
			src := path
			if r, ok := g.overlay[path]; ok {
				src = r
			}
			data, err := os.ReadFile(src)
			if err != nil {
				return nil, fmt.Errorf("source rule for %s: %w", gv.pkg, err)
			}
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, data, 0)
			if err != nil {
				return nil, fmt.Errorf("source rule for %s: %w", gv.pkg, err)
			}
			ast.Inspect(file, func(n ast.Node) bool {
				bad := ""
				switch n := n.(type) {
				case *ast.ValueSpec:
					for _, id := range n.Names {
						if id.Name == gv.name {
							declared++
						}
					}
				case *ast.AssignStmt:
					for _, lhs := range n.Lhs {
						if id, ok := lhs.(*ast.Ident); ok && id.Name == gv.name {
							bad = "assigns " + gv.name
						}
					}
				case *ast.UnaryExpr:
					if id, ok := n.X.(*ast.Ident); ok && n.Op == token.AND && id.Name == gv.name {
						bad = "takes the address of " + gv.name
					}
				}
				if bad != "" {
					pos := fset.Position(n.Pos())
					out = append(out, violation{rule: "guarded-variables", why: gv.why, pkg: p.ImportPath,
						detail: fmt.Sprintf("%s:%d %s in production code", f, pos.Line, bad)})
				}
				return true
			})
		}
		if declared != 1 {
			return nil, fmt.Errorf("source rule for %s: %s is declared %d times in its production files", gv.pkg, gv.name, declared)
		}
	}
	return out, nil
}
