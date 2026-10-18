#!/usr/bin/env python3
"""T-042 second review round mutation run (after the round-2 CHANGES REQUESTED).

Each mutant weakens one check of the DomainPack conformance suite or the
architecture checker, or makes the kernel read the IR's domain, and must make
a test fail. Applied with `go test -overlay` (the working tree is never
modified); KILLED means a test failed.

Usage: python3 checks/evidence/2026-10-18-T-042-review2/mutants.py [--check] [--only=NAME ...] [repo-root]
--check only confirms that every pattern occurs exactly once; --only runs
the named kernel mutants, pairs (by kernel mutant name) and mutants, and no
finding sites (mutation-addendum.log).

Finding sites: every c.fail(...) call of conformance.go becomes a no-op in
turn. Two sites are unreachable (EQUIVALENT_SITES, with the reason). The
domain-invariance sites are kernel guards: no pack can trigger them, so each
is proven by PAIRS — the kernel mutant alone is killed (by the suite only,
the run uses ./internal/domainpack/...), and the kernel mutant together with
that site's no-op survives, so the site is what kills it.

Earlier rounds' scripts and logs stay in checks/evidence/2026-10-15-T-042/
and checks/evidence/2026-10-15-T-042-review/. This round adds the reviewer's
14 extra mutants (review-t042-r2/probes/extra2.py) and one per new check.
"""
import json
import os
import pathlib
import re
import subprocess
import sys
import tempfile

ONLY = {a.split("=", 1)[1] for a in sys.argv[1:] if a.startswith("--only=")}
ARGS = [a for a in sys.argv[1:] if a != "--check" and not a.startswith("--only=")]
CHECK_ONLY = "--check" in sys.argv[1:]
REPO = pathlib.Path(ARGS[0] if ARGS else pathlib.Path(__file__).resolve().parents[3])
CONF = "internal/domainpack/conformance/conformance.go"
CHECK = "scripts/check-architecture/check.go"
RULES = "scripts/check-architecture/rules.go"
MAIN = "scripts/check-architecture/main.go"
VAL = "internal/model/validate/ir.go"
COMP = "internal/simulation/engine/compile.go"
PACKS = ["./internal/domainpack/..."]
ARCH = ["./scripts/check-architecture/..."]

EQUIVALENT_SITES = {
    ("metrics/run", "model %d: %v\", i, err)\n\t\t\tcontinue\n\t\t}\n\t\tplain"): "freeze cannot fail for a model that passed the generic validator (encoding and hashing a decoded IR)",
}
# Round 2 (review N5): the renamed copy's run failing is a kernel guard
# (metrics/renamed-run), no longer counted as equivalent.
KERNEL_GUARDS = {"validate/generic-ignores-domain", "metrics/engine-ignores-domain", "metrics/renamed-run"}

# Kernel mutants: the validator or the engine reads the domain. The first
# two are the T-042 review's probes that survived the first suite.
KERNEL = [
    ("kernel-validator-error-rules-for-known-domains", VAL,
     "\t\tfound := stage(ir, g)\n\t\tall = append(all, found...)",
     "\t\tif ir.Domain != \"generic_queue\" && ir.Domain != \"software_delivery\" {\n\t\t\tif found := stage(ir, g); found.hasError() {\n\t\t\t\tcontinue\n\t\t\t}\n\t\t}\n\t\tfound := stage(ir, g)\n\t\tall = append(all, found...)"),
    ("kernel-validator-reads-domain-prefix", VAL, "\treturn Sort(all)\n}",
     "\tif strings.HasPrefix(ir.Domain, \"software_delivery\") || strings.HasPrefix(ir.Domain, \"generic_queue\") {\n\t\tall = append(all, model.NewIssue(\"DOMAIN_READ\", \"/domain\", \"x\", model.SeverityWarning))\n\t}\n\treturn Sort(all)\n}"),
    ("kernel-validator-reads-domain-length", VAL, "\treturn Sort(all)\n}",
     "\tif len(ir.Domain) < 20 {\n\t\tall = append(all, model.NewIssue(\"DOMAIN_READ\", \"/domain\", \"x\", model.SeverityWarning))\n\t}\n\treturn Sort(all)\n}"),
    ("kernel-engine-reads-domain-prefix", COMP, "\tif err := p.build(ir, empirical); err != nil {",
     "\tif strings.HasPrefix(ir.Domain, \"generic_queue\") || strings.HasPrefix(ir.Domain, \"software_delivery\") {\n\t\tp.seed++\n\t}\n\tif err := p.build(ir, empirical); err != nil {"),
    ("kernel-engine-reads-domain-length", COMP, "\tif err := p.build(ir, empirical); err != nil {",
     "\tp.seed += uint64(len(ir.Domain))\n\tif err := p.build(ir, empirical); err != nil {"),
    # After the review's K2 probe: the engine rejects a domain (here the
    # suite's second renamed one, which only the pack whose ID is the first
    # renamed domain gets), so only the renamed run fails. The review's own
    # version (reject "." and ":") also fails that pack's own models, which
    # metrics/run reports, so it cannot isolate the site.
    ("kernel-engine-rejects-renamed-domain", COMP, "\tif err := p.build(ir, empirical); err != nil {",
     "\tif strings.HasPrefix(ir.Domain, \"qq.\") {\n\t\treturn nil, fmt.Errorf(\"engine: domain %q is not an identifier\", ir.Domain)\n\t}\n\tif err := p.build(ir, empirical); err != nil {"),
]
# (kernel mutant, guard site): with the site a no-op, the kernel mutant survives.
PAIRS = [
    ("kernel-validator-error-rules-for-known-domains", "validate/generic-ignores-domain"),
    ("kernel-validator-reads-domain-prefix", "validate/generic-ignores-domain"),
    ("kernel-engine-reads-domain-prefix", "metrics/engine-ignores-domain"),
    ("kernel-engine-rejects-renamed-domain", "metrics/renamed-run"),
]

MUT = [
    # Conformance machinery.
    ("no-step-recover", CONF, PACKS, "\t\tc.call(step.name, step.run)", "\t\tstep.run()"),
    ("call-no-recover", CONF, PACKS, "\t\tif v := recover(); v != nil {\n\t\t\tc.fail(check, \"panicked: %v\", v)", "\t\tif v := recover(); false && v != nil {\n\t\t\tc.fail(check, \"panicked: %v\", v)"),
    ("no-suite-precondition", CONF, PACKS, "\t\tif issues := genericIssues(&s.Models[i]); len(issues) > 0 {", "\t\tif issues := genericIssues(&s.Models[i]); false && len(issues) > 0 {"),
    ("empty-suite-accepted", CONF, PACKS, "\tif s.Pack == nil || len(s.Models) == 0 {", "\tif s.Pack == nil {"),
    ("run-ignores-findings", CONF, PACKS, "\t\tt.Errorf(\"%s does not conform: %s\", s.Pack.ID(), f)", "\t\t_ = f"),
    ("run-ignores-invalid-suite", CONF, PACKS, "\tif err != nil {\n\t\tt.Fatal(err)\n\t}", "\tif err != nil {\n\t\treturn\n\t}"),
    ("clone-shares", CONF, PACKS, "func clone(ir *model.ModelIR) model.ModelIR {\n", "func clone(ir *model.ModelIR) model.ModelIR {\n\tif true {\n\t\treturn *ir\n\t}\n"),
    ("no-datasets", CONF, PACKS, "Datasets: c.s.Datasets, Manifest: manifest,", ""),
    ("renamed-by-suffix", CONF, PACKS, "\treturn renamedDomains[0]\n}", "\treturn c.s.Pack.ID() + \"_other\"\n}"),
    ("views-only-as-given", CONF, PACKS, "[]string{\"as given\", \"without positions\", \"with one saved position\", \"with a saved position on the default grid\"}", "[]string{\"as given\"}"),
    ("views-no-grid-variant", CONF, PACKS, "[]string{\"as given\", \"without positions\", \"with one saved position\", \"with a saved position on the default grid\"}", "[]string{\"as given\", \"without positions\", \"with one saved position\"}"),
    ("views-no-stripped-variant", CONF, PACKS, "[]string{\"as given\", \"without positions\", \"with one saved position\", \"with a saved position on the default grid\"}", "[]string{\"as given\", \"with one saved position\", \"with a saved position on the default grid\"}"),
    ("engine-owned-drops-reproducibility", CONF, PACKS, "\tcp.Metrics.DORA = nil\n\tcp.Reproducibility.ResultDigest = \"\"\n\treturn encoded(cp)", "\tcp.Metrics.DORA = nil\n\tcp.Reproducibility = simulation.Reproducibility{}\n\treturn encoded(cp)"),
    ("engine-owned-keeps-domain-metrics", CONF, PACKS, "\tcp.Metrics.DORA = nil\n\tcp.Reproducibility.ResultDigest = \"\"\n\treturn encoded(cp)", "\tcp.Reproducibility.ResultDigest = \"\"\n\treturn encoded(cp)"),
    ("engine-owned-ignores-queues", CONF, PACKS, "\tcp.Metrics.DORA = nil\n\tcp.Reproducibility.ResultDigest = \"\"\n\treturn encoded(cp)", "\tcp.Metrics.DORA = nil\n\tcp.Metrics.Queues = nil\n\tcp.Reproducibility.ResultDigest = \"\"\n\treturn encoded(cp)"),
    ("needs-input-unchecked", CONF, PACKS, "\tif c.call(\"inference/needs-input\", func() { res, err = c.s.Pack.InferFromEvents(ctx, empty) }) && (err == nil || res != nil) {", "\tif c.call(\"inference/needs-input\", func() { res, err = c.s.Pack.InferFromEvents(ctx, empty) }) && false {"),
    ("needs-input-result-with-error-allowed", CONF, PACKS, "\tif c.call(\"inference/needs-input\", func() { res, err = c.s.Pack.InferFromEvents(ctx, empty) }) && (err == nil || res != nil) {", "\tif c.call(\"inference/needs-input\", func() { res, err = c.s.Pack.InferFromEvents(ctx, empty) }) && err == nil {"),
    ("answer-result-with-error-allowed", CONF, PACKS, "\tcase err != nil && res != nil:", "\tcase false:"),
    ("cancel-result-with-error-allowed", CONF, PACKS, "func() { res, err = c.s.Pack.InferFromEvents(cancelled, events) }) && (err == nil || res != nil) {", "func() { res, err = c.s.Pack.InferFromEvents(cancelled, events) }) && err == nil {"),
    ("inference-determinism-unchecked", CONF, PACKS, "\tif encoded(res) != encoded(again) || fmt.Sprint(err) != fmt.Sprint(againErr) {", "\tif false && (encoded(res) != encoded(again) || fmt.Sprint(err) != fmt.Sprint(againErr)) {"),
    ("inference-determinism-ignores-errors", CONF, PACKS, "\tif encoded(res) != encoded(again) || fmt.Sprint(err) != fmt.Sprint(againErr) {", "\tif encoded(res) != encoded(again) || againErr == nil && false {"),
    ("stateless-unchecked", CONF, PACKS, "\t} else if after != before {", "\t} else if false && after != before {"),
    ("stateless-sentinel-unchecked", CONF, PACKS, "\tif strings.Contains(after, sentinel) {", "\tif false {"),
    ("stateless-skips-validate", CONF, PACKS, "\t\t\tout = append(out, c.s.Pack.Validate(&m), c.s.Pack.DefaultViews(&m))", "\t\t\tout = append(out, c.s.Pack.DefaultViews(&m))"),
    ("stateless-skips-views", CONF, PACKS, "\t\t\tout = append(out, c.s.Pack.Validate(&m), c.s.Pack.DefaultViews(&m))", "\t\t\tout = append(out, c.s.Pack.Validate(&m))"),
    ("stateless-skips-metrics", CONF, PACKS, "\t\t\tout = append(out, c.s.Pack.ComputeMetrics(clonedResult(c.result)))", "\t\t\t_ = clonedResult"),
    ("concurrent-method-unnamed", CONF, PACKS, "g, methods[k])", "g, methods[k][:0]+\"a method\")"),
    ("variant-codes-unchecked-depth", CONF, PACKS, "strings.Repeat(`{\"a\":`, validate.MaxContainerDepth+1) + \"1\" + strings.Repeat(\"}\", validate.MaxContainerDepth+1)", "strings.Repeat(`{\"a\":`, validate.MaxContainerDepth) + \"1\" + strings.Repeat(\"}\", validate.MaxContainerDepth)"),
    ("variant-no-depth", CONF, PACKS, "\tout[\"metadata nested too deep\"] = deep\n", ""),
    ("variant-no-constraint", CONF, PACKS, "\t\t\tout[\"constraint not met\"] = v\n", ""),
    ("variant-no-zero-time-cycle", CONF, PACKS, "\t\tout[\"zero-time cycle\"] = v\n", ""),
    ("variant-cycle-not-a-cycle", CONF, PACKS, "model.Flow{ID: loop + \"_again\", From: loop, To: loop,", "model.Flow{ID: loop + \"_again\", From: loop, To: to,"),
    # The review's extra mutants (review-t042-r2/probes/extra2.py).
    ("variant-no-unknown-target", CONF, PACKS, "\t\tout[\"unknown flow target\"] = v\n", ""),
    ("variant-no-probabilities", CONF, PACKS, "\t\tout[\"probabilities not summing to 1\"] = v\n", ""),
    ("variant-no-duplicate", CONF, PACKS, "\t\tout[\"duplicate node ID\"] = v\n", ""),
    ("variant-no-sink", CONF, PACKS, "\tout[\"no sink\"] = v\n", ""),
    ("variant-no-triangular", CONF, PACKS, "\t\t\tout[\"bad triangular distribution\"] = v\n", ""),
    ("static-no-views-exposure", CONF, PACKS, "\t\tc.s.Pack.DefaultViews(&ir)\n\t\t_, _ = c.s.Pack.InferFromEvents(ctx, seen)", "\t\t_, _ = c.s.Pack.InferFromEvents(ctx, seen)"),
    ("static-no-events-exposure", CONF, PACKS, "\t\t_, _ = c.s.Pack.InferFromEvents(ctx, seen)\n", ""),
    ("static-sentinel-not-in-node-names", CONF, PACKS, "\t\tir.Nodes[i].Name = sentinel\n", "\t\t_ = i\n"),
    ("static-sentinel-not-in-events", CONF, PACKS, "domainpack.CanonicalEvent{CaseID: sentinel, EventType: sentinel, Timestamp:", "domainpack.CanonicalEvent{CaseID: \"c\", EventType: \"e\", Timestamp:"),
    ("concurrent-ignores-error-text", CONF, PACKS, "encoded(inferred) + fmt.Sprint(err)}", "encoded(inferred) + fmt.Sprint(err == nil)}"),
    ("renamed-ignores-pack-id", CONF, PACKS, "\tif c.s.Pack.ID() == renamedDomains[0] {", "\tif false {"),
    ("inference-not-cancelled", CONF, PACKS, "\tcancelled, cancel := context.WithCancel(ctx)\n\tcancel()\n", "\tcancelled, cancel := context.WithCancel(ctx)\n\tdefer cancel()\n"),
    ("static-snapshot-skipped", CONF, PACKS, "\t\t{\"prompt/context\", func() { c.initial = c.staticData() }},\n", ""),
    ("static-sentinel-not-shown", CONF, PACKS, "\tir.Name = sentinel\n", ""),
    ("static-change-ignored", CONF, PACKS, "\t} else if c.initial != \"\" && final != c.initial {", "\t} else if false {"),
    ("concurrent-skips-metrics", CONF, PACKS, "\t\tif c.result != nil {\n\t\t\tmetrics = c.s.Pack.ComputeMetrics(clonedResult(c.result))\n\t\t}", "\t\t_ = clonedResult"),
    ("concurrent-skips-inference", CONF, PACKS, "\t\tinferred, err := c.s.Pack.InferFromEvents(ctx, events)\n", "\t\tvar inferred any\n\t\tvar err error\n"),
    ("concurrent-no-result", CONF, PACKS, "\t\tif c.result == nil {\n\t\t\tc.result = plain\n\t\t}\n", ""),
    ("fail-closed-any-path", CONF, PACKS, "return is.Code == DomainMismatch && is.Path == \"/domain\" && is.Severity == model.SeverityError", "return is.Code == DomainMismatch && is.Severity == model.SeverityError"),
    ("fail-closed-any-severity", CONF, PACKS, "return is.Code == DomainMismatch && is.Path == \"/domain\" && is.Severity == model.SeverityError", "return is.Code == DomainMismatch && is.Path == \"/domain\""),
    ("copies-only-kinds", CONF, PACKS, "\t\tfor j := range nodes[i].ResourceTypes {\n\t\t\tnodes[i].ResourceTypes[j] += \"~changed\"\n\t\t}\n", ""),
    ("copies-nodes-only", CONF, PACKS, "\tfor i := range res {\n\t\tres[i].Kind += \"~changed\"\n\t}\n", ""),
    ("prompt-resources-unchecked", CONF, PACKS, " || !reflect.DeepEqual(p.Resources, c.s.Pack.ResourceCatalog())", ""),
    # Architecture checker.
    ("matrix-host-only-by-default", MAIN, ARCH, "\tconfigs := matrix\n", "\tconfigs := host\n"),
    ("matrix-without-arm64", CHECK, ARCH, "\"linux/amd64\", \"linux/arm64\", ", "\"linux/amd64\", "),
    ("matrix-without-windows", CHECK, ARCH, ", \"darwin/arm64\", \"windows/amd64\"}", ", \"darwin/arm64\"}"),
    ("matrix-cgo-on-only", CHECK, ARCH, "range []string{\"1\", \"0\"}", "range []string{\"1\"}"),
    ("imports-not-merged", CHECK, ARCH, "\t\t\t\tq.Imports = union(q.Imports, p.Imports)\n", ""),
    ("platform-absent-package-is-error", CHECK, ARCH, "\t\t\tif p.Error != nil && strings.Contains(p.Error.Err, \"build constraints exclude all Go files\") {", "\t\t\tif false {"),
    ("unbuilt-files-not-reported", CHECK, ARCH, "\t\t\tif n == len(configs) && len(configs) == len(matrix) {", "\t\t\tif n == len(configs) && false {"),
    ("unlisted-dirs-unchecked", CHECK, ARCH, "\t\t\tif !listed[d] {", "\t\t\tif false && !listed[d] {"),
    ("overlay-dirs-ignored", CHECK, ARCH, "\t\t\tif !skip {\n\t\t\t\tset[filepath.Dir(path)] = true", "\t\t\tif false && !skip {\n\t\t\t\tset[filepath.Dir(path)] = true"),
    ("list-errors-ignored", CHECK, ARCH, "\t\t\tif p.Error != nil {\n\t\t\t\treturn nil, fmt.Errorf(\"%s (%s): %s\", p.ImportPath, c, p.Error.Err)\n\t\t\t}\n\t\t\tfor _, e := range p.DepsErrors {\n\t\t\t\treturn nil, fmt.Errorf(\"%s (%s): %s\", p.ImportPath, c, e.Err)\n\t\t\t}\n", ""),
    # No longer equivalent (first review round): with every-package-listed,
    # a test variant in the map is a module path that is not a root.
    ("variant-entries-kept", CHECK, ARCH, "\t\t\tif p.ForTest != \"\" || strings.HasSuffix(p.ImportPath, \".test\") || strings.Contains(p.ImportPath, \" \") {\n\t\t\t\tcontinue // test variants", "\t\t\tif false {\n\t\t\t\tcontinue // test variants"),
    ("std-only-unchecked", CHECK, ARCH, "r.stdOnly != nil && std && !matchAny(r.stdOnly, imp) {", "r.stdOnly != nil && std && !matchAny(r.stdOnly, imp) && false {"),
    ("go-only-unchecked", CHECK, ARCH, "r.goOnly && len(files) > 0 {", "false && r.goOnly && len(files) > 0 {"),
    ("go-only-ignores-ignored-files", CHECK, ARCH, ", p.SysoFiles, p.IgnoredOtherFiles)", ", p.SysoFiles)"),
    ("go-only-ignores-syso", CHECK, ARCH, ", p.SysoFiles, p.IgnoredOtherFiles)", ", p.IgnoredOtherFiles)"),
    ("module-only-unchecked", CHECK, ARCH, "\t\treturn r.moduleOnly != nil && !matchAny(r.moduleOnly, dep)", "\t\treturn false"),
    ("linked-unlisted-unchecked", CHECK, ARCH, "\t\t\tif inModule(path) && !listed[path] {", "\t\t\tif false && inModule(path) && !listed[path] {"),
    ("whole-never-set", CHECK, ARCH, "\tg.whole = slices.Equal(patterns, defaultPatterns)\n", ""),
    ("pack-encapsulation-unchecked", CHECK, ARCH, "\t\t\tif unit, ok := packUnit(imp); ok && imp != unit && !sameUnit(root, imp, 3) {", "\t\t\tif unit, ok := packUnit(imp); false && ok && imp != unit && !sameUnit(root, imp, 3) {"),
    ("pack-encapsulation-ignores-tests", CHECK, ARCH, "\t\tfor _, imp := range slices.Concat(p.Imports, p.TestImports, p.XTestImports) {", "\t\tfor _, imp := range p.Imports {"),
    ("pack-subpackage-interface-unchecked", CHECK, ARCH, "ok && root != unit && reaches(g, root, module+\"/internal/domainpack\") {", "ok && root != unit && false {"),
    ("pack-subpackage-interface-direct-only", CHECK, ARCH, "\t\t\tfor _, imp := range p.Imports {\n\t\t\t\tif !seen[imp] {", "\t\t\tfor _, imp := range p.Imports {\n\t\t\t\tif !seen[imp] && cur == from {"),
    ("testimports-not-merged", CHECK, ARCH, "\t\t\t\tq.TestImports = union(q.TestImports, p.TestImports)\n", ""),
    ("xtestimports-not-merged", CHECK, ARCH, "\t\t\t\tq.XTestImports = union(q.XTestImports, p.XTestImports)\n", ""),
    ("structure-unchecked", CHECK, ARCH, "\tviolations = structure(g)\n", ""),
    ("pack-layout-unchecked", CHECK, ARCH, "\t\t\tif nested && unit != \"conformance\" && !slices.Contains(g.roots, module+\"/internal/domainpack/\"+unit) {", "\t\t\tif nested && false {"),
    ("walk-direct-only", CHECK, ARCH, "\t\t\tif _, seen := parent[imp]; !seen {\n\t\t\t\tparent[imp] = cur", "\t\t\tif _, seen := parent[imp]; !seen && cur == root {\n\t\t\t\tparent[imp] = cur"),
    ("tests-ignored", CHECK, ARCH, "\t\t\t\tstart = slices.Concat(p.TestImports, p.XTestImports)", "\t\t\t\tstart = slices.Concat(p.TestImports[:0], p.XTestImports[:0])"),
    ("xtests-ignored", CHECK, ARCH, "\t\t\t\tstart = slices.Concat(p.TestImports, p.XTestImports)", "\t\t\t\tstart = p.TestImports"),
    ("allow-ignored", CHECK, ARCH, "\tif sameUnit(root, dep, r.unitDepth) || matchAny(r.allow, dep) {", "\tif sameUnit(root, dep, r.unitDepth) {"),
    ("third-party-ignored", CHECK, ARCH, "\treturn !p.Standard && !matchAny(r.thirdParty, dep)", "\treturn false"),
    ("unknown-dep-trusted", CHECK, ARCH, "\t\treturn true // every dependency is in the graph; an unknown one is not trusted", "\t\treturn false"),
    ("unit-always", CHECK, ARCH, "\treturn slices.Equal(ea[:depth+1], eb[:depth+1])", "\treturn true"),
    ("except-ignored", CHECK, ARCH, "\t\t\tif !matchAny(r.applies, root) || matchAny(r.except, root) {", "\t\t\tif !matchAny(r.applies, root) {"),
    ("empty-rules-accepted", CHECK, ARCH, "\t\tif matched == 0 && !r.future {", "\t\tif false {"),
    ("subtree-prefix-loose", CHECK, ARCH, "\t\treturn path == base || strings.HasPrefix(path, base+\"/\")", "\t\treturn strings.HasPrefix(path, base)"),
    ("chain-not-built", CHECK, ARCH, "\t\t\t\tchain = append([]string{at}, chain...)", "\t\t\t\t_ = at"),
    ("violations-stop-walk-dropped", CHECK, ARCH, "\t\t\tcontinue // what it imports is reported through it", "\t\t\t_ = 0"),
    ("patterns-explicit", MAIN, ARCH, "var defaultPatterns = []string{\"./...\"}", "var defaultPatterns = []string{\"./cmd/...\", \"./internal/...\", \"./schemas/...\", \"./scripts/...\"}"),
    ("exit-0-on-violations", MAIN, ARCH, "\t\tfmt.Fprintf(stderr, \"FAIL architecture: %d violation(s)\\n\", len(violations))\n\t\treturn 1", "\t\tfmt.Fprintf(stderr, \"FAIL architecture: %d violation(s)\\n\", len(violations))\n\t\treturn 0"),
    ("exit-0-on-rule-errors", MAIN, ARCH, "\tif len(errs) > 0 {\n\t\treturn 2\n\t}", "\tif len(errs) > 0 {\n\t\treturn 0\n\t}"),
    ("exit-0-on-load-error", MAIN, ARCH, "\t\tfmt.Fprintln(stderr, \"check-architecture:\", err)\n\t\treturn 2", "\t\tfmt.Fprintln(stderr, \"check-architecture:\", err)\n\t\treturn 0"),
    # Rule table: TestEveryRuleEntryIsEnforced makes any removed entry fail;
    # a few representative removals.
    ("net-allowed", RULES, ARCH, "\t\"net\", \"net/http/...\",", "\t\"net/http/...\","),
    ("config-allowed", RULES, ARCH, "\t\"./internal/config/...\",\n}", "}"),
    ("schemas-not-kernel", RULES, ARCH, "\t\"./internal/domainpack\", // the interface and registry only\n\t\"./schemas\",\n", "\t\"./internal/domainpack\", // the interface and registry only\n"),
    ("db-sql-allowed", RULES, ARCH, "\t\tforbid:  cat(dbDrivers, []string{\"database/sql/...\"}),", "\t\tforbid:  dbDrivers,"),
    ("allowlist-any", RULES, ARCH, "\t\tthirdParty: kernelThirdParty,\n\t\tstdOnly:", "\t\tstdOnly:"),
    ("packs-third-party-any", RULES, ARCH, "\t\tthirdParty: kernelThirdParty,\n\t\tunitDepth:  3,", "\t\tunitDepth:  3,"),
    ("packs-may-share", RULES, ARCH, "\t\tunitDepth:  3,", "\t\tunitDepth:  2,"),
    ("kernel-std-any", RULES, ARCH, "\t\tstdOnly:    kernelStd,\n\t\tmoduleOnly: genericKernel,\n\t\tgoOnly:     true,\n\t},\n\t{\n\t\tname:    \"generic-kernel-tests\",", "\t\tmoduleOnly: genericKernel,\n\t\tgoOnly:     true,\n\t},\n\t{\n\t\tname:    \"generic-kernel-tests\","),
    ("kernel-module-any", RULES, ARCH, "\t\tstdOnly:    kernelStd,\n\t\tmoduleOnly: genericKernel,\n\t\tgoOnly:     true,\n\t},\n\t{\n\t\tname:    \"generic-kernel-tests\",", "\t\tstdOnly:    kernelStd,\n\t\tgoOnly:     true,\n\t},\n\t{\n\t\tname:    \"generic-kernel-tests\","),
    ("kernel-any-files", RULES, ARCH, "\t\tstdOnly:    kernelStd,\n\t\tmoduleOnly: genericKernel,\n\t\tgoOnly:     true,\n\t},\n\t{\n\t\tname:    \"generic-kernel-tests\",", "\t\tstdOnly:    kernelStd,\n\t\tmoduleOnly: genericKernel,\n\t},\n\t{\n\t\tname:    \"generic-kernel-tests\","),
    ("packs-std-any", RULES, ARCH, "\t\tstdOnly:    kernelStd,\n\t\tmoduleOnly: genericKernel,\n\t\tgoOnly:     true,\n\t},\n\t{\n\t\tname:    \"conformance-is-generic\",", "\t\tmoduleOnly: genericKernel,\n\t\tgoOnly:     true,\n\t},\n\t{\n\t\tname:    \"conformance-is-generic\","),
    ("packs-module-any", RULES, ARCH, "\t\tstdOnly:    kernelStd,\n\t\tmoduleOnly: genericKernel,\n\t\tgoOnly:     true,\n\t},\n\t{\n\t\tname:    \"conformance-is-generic\",", "\t\tstdOnly:    kernelStd,\n\t\tgoOnly:     true,\n\t},\n\t{\n\t\tname:    \"conformance-is-generic\","),
    ("packs-any-files", RULES, ARCH, "\t\tstdOnly:    kernelStd,\n\t\tmoduleOnly: genericKernel,\n\t\tgoOnly:     true,\n\t},\n\t{\n\t\tname:    \"conformance-is-generic\",", "\t\tstdOnly:    kernelStd,\n\t\tmoduleOnly: genericKernel,\n\t},\n\t{\n\t\tname:    \"conformance-is-generic\","),
    ("std-allowlist-widened", RULES, ARCH, "\"io\", \"iter\",", "\"io\", \"io/ioutil\", \"iter\","),
    ("no-test-rule", RULES, ARCH, "\t\ttests:   true,\n", ""),
]

NOOP = "\n\nfunc (c *checker) noop(string, string, ...any) {}\n"


def fail_sites(src):
    for m in re.finditer(r'c\.fail\("([a-z/-]+)", "', src):
        yield m.group(1), m.start(), src[m.end():m.end() + 160]


def noop_site(src, start):
    return src[:start] + "c.noop" + src[start + len("c.fail"):] + NOOP


def run(tmp, i, files, pkgs):
    rep = {}
    for j, (f, src) in enumerate(files.items()):
        m = pathlib.Path(tmp) / f"m{i}_{j}.go"
        m.write_text(src)
        rep[str(REPO / f)] = str(m)
    ov = pathlib.Path(tmp) / f"m{i}.json"
    ov.write_text(json.dumps({"Replace": rep}))
    p = subprocess.run(["go", "test", "-count=1", "-timeout", "600s", "-overlay", str(ov), *pkgs],
                       cwd=REPO, capture_output=True, text=True, env=dict(os.environ))
    o = p.stdout + p.stderr
    if p.returncode == 0:
        return "SURVIVED", ""
    if "build failed" in o or "setup failed" in o or "[build failed]" in o:
        return "BUILD-FAILED", o[-300:]
    first = next((l.strip() for l in o.splitlines() if l.strip().startswith(("--- FAIL", "panic:"))), "")
    return "KILLED", first[:110]


def edit(f, old, new):
    src = (REPO / f).read_text()
    if src.count(old) != 1:
        return None, src.count(old)
    if f in (VAL, COMP) and "strings." in new and '"strings"' not in src:
        src = src.replace("import (\n", "import (\n\t\"strings\"\n", 1)
    return src.replace(old, new), 1


def main() -> int:
    if CHECK_ONLY:
        bad = 0
        for name, f, *rest in KERNEL + MUT:
            old = rest[0] if f in (VAL, COMP) else rest[1]
            n = (REPO / f).read_text().count(old)
            if n != 1:
                bad += 1
                print(f"{name}: pattern found {n} times")
        print(f"{len(KERNEL) + len(MUT) - bad}/{len(KERNEL) + len(MUT)} patterns occur exactly once")
        return 1 if bad else 0
    results = []
    with tempfile.TemporaryDirectory() as tmp:
        i = 0
        conf = (REPO / CONF).read_text()
        sites = {}
        for name, start, after in fail_sites(conf):
            i += 1
            sites.setdefault(name, start)
            if ONLY:
                continue
            label = f"site:{name}@{conf[:start].count(chr(10)) + 1}"
            status, first = run(tmp, i, {CONF: noop_site(conf, start)}, PACKS)
            eq = next((why for (n, prefix), why in EQUIVALENT_SITES.items() if n == name and after.startswith(prefix)), None)
            if eq:
                status = "EQUIVALENT" if status == "SURVIVED" else f"UNEXPECTED-{status}"
                first = eq
            elif name in KERNEL_GUARDS and status == "SURVIVED":
                status, first = "KERNEL-GUARD", "proven by PAIRS below"
            results.append((label, f"{status} {first}"))
            print("\t".join(results[-1]), flush=True)
        kernel = {}
        for name, f, old, new in KERNEL:
            i += 1
            src, n = edit(f, old, new)
            if src is None:
                results.append((name, f"BAD-MUTANT (pattern found {n} times)"))
            else:
                kernel[name] = (f, src)
                if ONLY and name not in ONLY:
                    continue
                status, first = run(tmp, i, {f: src}, PACKS)
                results.append((name, f"{status} {first}"))
            print("\t".join(results[-1]), flush=True)
        pairs = [(k, site) for k, site in PAIRS if not ONLY or k in ONLY]
        for kname, site in pairs:
            i += 1
            f, src = kernel[kname]
            status, first = run(tmp, i, {f: src, CONF: noop_site(conf, sites[site])}, PACKS)
            # The pair is evidence, not a mutant: it must survive.
            results.append((f"pair:{kname}+no-op {site}", ("GUARD-PROVEN" if status == "SURVIVED" else f"GUARD-UNPROVEN-{status}") + f" {first}"))
            print("\t".join(results[-1]), flush=True)
        for name, f, pkgs, old, new, *flags in MUT:
            i += 1
            if ONLY and name not in ONLY:
                continue
            src, n = edit(f, old, new)
            if src is None:
                results.append((name, f"BAD-MUTANT (pattern found {n} times)"))
            else:
                status, first = run(tmp, i, {f: src}, pkgs)
                if "equivalent" in flags:
                    status = "EQUIVALENT" if status == "SURVIVED" else f"UNEXPECTED-{status}"
                results.append((name, f"{status} {first}"))
            print("\t".join(results[-1]), flush=True)
    count = lambda p: sum(1 for r in results if r[1].startswith(p))
    killed, equivalent, guards, proven = count("KILLED"), count("EQUIVALENT"), count("KERNEL-GUARD"), count("GUARD-PROVEN")
    mutants = len(results) - equivalent - guards - len(pairs)
    print(f"{killed}/{mutants} killed, {equivalent} equivalent, {guards} kernel guards ({proven}/{len(pairs)} pairs prove them)")
    return 0 if killed == mutants and proven == len(pairs) else 1


if __name__ == "__main__":
    sys.exit(main())
