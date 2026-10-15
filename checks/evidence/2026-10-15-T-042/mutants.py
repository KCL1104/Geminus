#!/usr/bin/env python3
"""T-042 mutation run: each mutant weakens one check of the DomainPack
conformance suite or the architecture checker, or makes the kernel read the
IR's domain, and must make a test fail. Mutants are applied with
`go test -overlay` (the working tree is never modified); KILLED means the
named tests failed.

Usage: python3 checks/evidence/2026-10-15-T-042/mutants.py [repo-root]

Finding sites: every c.fail(...) call of conformance.go is a mutant that
turns it into a no-op. Three sites are equivalent (unreachable; listed in
EQUIVALENT_SITES with the reason) and two are kernel guards: no pack can
trigger them, so they are proven by the kernel mutants below, which only the
conformance suite kills (the run uses only ./internal/domainpack/...).
"""
import json
import os
import pathlib
import re
import subprocess
import sys
import tempfile

REPO = pathlib.Path(sys.argv[1] if len(sys.argv) > 1 else pathlib.Path(__file__).resolve().parents[3])
CONF = "internal/domainpack/conformance/conformance.go"
CHECK = "scripts/check-architecture/check.go"
RULES = "scripts/check-architecture/rules.go"
MAIN = "scripts/check-architecture/main.go"
PACKS = ["./internal/domainpack/..."]
ARCH = ["./scripts/check-architecture/..."]

# conformance.go c.fail sites that no input reaches (by check name and the
# start of the message):
EQUIVALENT_SITES = {
    ("metrics/run", "model %d: %v\", i, err)\n\t\t\tcontinue\n\t\t}\n\t\tplain"): "freeze cannot fail for a model that passed the generic validator (encoding and hashing a decoded IR)",
    ("metrics/run", "model %d: %v\", i, err)\n\t\t\tcontinue\n\t\t}\n\t\twithRun"): "the second run uses the same input as the first, which succeeded; a pack panic is caught by metrics/compute first",
    ("metrics/engine-ignores-domain", "model %d: %v\", i, err)"): "renaming the domain cannot make freeze or a successful run fail",
}
KERNEL_GUARDS = {"validate/generic-ignores-domain", "metrics/engine-ignores-domain"}

MUT = [
    # The kernel reads the domain: only the conformance suite notices.
    ("kernel-validator-reads-domain", "internal/model/validate/ir.go", PACKS,
     "\treturn Sort(all)\n}", "\tif ir.Domain == \"generic_queue_other\" {\n\t\tall = append(all, model.NewIssue(\"DOMAIN_READ\", \"/domain\", \"x\", model.SeverityWarning))\n\t}\n\treturn Sort(all)\n}"),
    ("kernel-engine-reads-domain", "internal/simulation/engine/compile.go", PACKS,
     "\tif err := p.build(ir, empirical); err != nil {",
     "\tp.seed += uint64(len(ir.Domain))\n\tif err := p.build(ir, empirical); err != nil {"),
    # Conformance machinery.
    ("no-step-recover", CONF, PACKS, "\t\tc.call(step.name, step.run)", "\t\tstep.run()"),
    ("call-no-recover", CONF, PACKS, "\t\tif v := recover(); v != nil {\n\t\t\tc.fail(check, \"panicked: %v\", v)", "\t\tif v := recover(); false && v != nil {\n\t\t\tc.fail(check, \"panicked: %v\", v)"),
    ("no-suite-precondition", CONF, PACKS, "\t\tif issues := genericIssues(&s.Models[i]); len(issues) > 0 {", "\t\tif issues := genericIssues(&s.Models[i]); false && len(issues) > 0 {"),
    ("empty-suite-accepted", CONF, PACKS, "\tif s.Pack == nil || len(s.Models) == 0 {", "\tif s.Pack == nil {"),
    ("run-ignores-findings", CONF, PACKS, "\t\tt.Errorf(\"%s does not conform: %s\", s.Pack.ID(), f)", "\t\t_ = f"),
    ("views-only-as-given", CONF, PACKS, "\t\tfor _, variant := range []string{\"as given\", \"without positions\", \"with one saved position\"} {", "\t\tfor _, variant := range []string{\"as given\"} {"),
    ("clone-shares", CONF, PACKS, "func clone(ir *model.ModelIR) model.ModelIR {\n", "func clone(ir *model.ModelIR) model.ModelIR {\n\tif true {\n\t\treturn *ir\n\t}\n"),
    ("generic-part-keeps-domain-metrics", CONF, PACKS, "\tcp.Metrics.DORA = nil\n", ""),
    ("no-datasets", CONF, PACKS, "Datasets: c.s.Datasets, Manifest: manifest,", ""),
    # Architecture checker.
    ("walk-direct-only", CHECK, ARCH, "\t\t\tif _, seen := parent[imp]; !seen {\n\t\t\t\tparent[imp] = cur", "\t\t\tif _, seen := parent[imp]; !seen && cur == root {\n\t\t\t\tparent[imp] = cur"),
    ("tests-ignored", CHECK, ARCH, "\t\t\t\tstart = slices.Concat(p.TestImports, p.XTestImports)", "\t\t\t\tstart = slices.Concat(p.TestImports[:0], p.XTestImports[:0])"),
    ("xtests-ignored", CHECK, ARCH, "\t\t\t\tstart = slices.Concat(p.TestImports, p.XTestImports)", "\t\t\t\tstart = p.TestImports"),
    ("allow-ignored", CHECK, ARCH, "\tif sameUnit(root, dep, r.unitDepth) || matchAny(r.allow, dep) {", "\tif sameUnit(root, dep, r.unitDepth) {"),
    ("third-party-ignored", CHECK, ARCH, "\treturn !p.Standard && !matchAny(r.thirdParty, dep)", "\treturn false"),
    ("unknown-dep-trusted", CHECK, ARCH, "\t\treturn true // every dependency is in the graph; an unknown one is not trusted", "\t\treturn false"),
    ("unit-never", CHECK, ARCH, "\tif depth <= 0 || !inModule(a) || !inModule(b) {", "\tif true {"),
    ("unit-always", CHECK, ARCH, "\treturn slices.Equal(ea[:depth+1], eb[:depth+1])", "\treturn true"),
    ("except-ignored", CHECK, ARCH, "\t\t\tif !matchAny(r.applies, root) || matchAny(r.except, root) {", "\t\t\tif !matchAny(r.applies, root) {"),
    ("future-ignored", CHECK, ARCH, "\t\tif matched == 0 && !r.future {", "\t\tif matched == 0 {"),
    ("empty-rules-accepted", CHECK, ARCH, "\t\tif matched == 0 && !r.future {", "\t\tif false {"),
    ("subtree-prefix-loose", CHECK, ARCH, "\t\treturn path == base || strings.HasPrefix(path, base+\"/\")", "\t\treturn strings.HasPrefix(path, base)"),
    ("list-errors-ignored", CHECK, ARCH, "\t\tif p.Error != nil {\n\t\t\treturn nil, fmt.Errorf(\"%s: %s\", p.ImportPath, p.Error.Err)\n\t\t}\n\t\tfor _, e := range p.DepsErrors {\n\t\t\treturn nil, fmt.Errorf(\"%s: %s\", p.ImportPath, e.Err)\n\t\t}", ""),
    ("chain-not-built", CHECK, ARCH, "\t\t\t\tchain = append([]string{at}, chain...)", "\t\t\t\t_ = at"),
    ("violations-stop-walk-dropped", CHECK, ARCH, "\t\t\tcontinue // what it imports is reported through it", "\t\t\t_ = 0"),
    ("kernel-testing-allowed", RULES, ARCH, "testInfra, ioStd, dbDrivers, llmSDKs, []string{\"testing\"}),\n\t\tallow:      []string{\"./internal/domainpack\"},\n\t\tthirdParty", "testInfra, ioStd, dbDrivers, llmSDKs),\n\t\tallow:      []string{\"./internal/domainpack\"},\n\t\tthirdParty"),
    ("exec-allowed", RULES, ARCH, "\"database/sql/...\", \"os/exec\", \"plugin\", \"crypto/tls\",", "\"database/sql/...\", \"plugin\", \"crypto/tls\","),
    ("storage-allowed", RULES, ARCH, "\t\"./internal/storage/...\",\n\t\"./internal/import/...\",", "\t\"./internal/import/...\","),
    ("tenant-allowed", RULES, ARCH, "\t\"./internal/tenant/...\",\n\t\"./internal/project/...\",", "\t\"./internal/project/...\","),
    ("anthropic-allowed", RULES, ARCH, "\t\"github.com/anthropics/...\", ", "\t"),
    ("allowlist-any", RULES, ARCH, "\t\tthirdParty: kernelThirdParty,", ""),
    ("allowlist-empty", RULES, ARCH, "\t\"github.com/gowebpki/jcs/...\",\n", ""),
    ("no-test-rule", RULES, ARCH, "\t\ttests:   true,\n", ""),
    ("packs-may-share", RULES, ARCH, "\t\tunitDepth: 3,", "\t\tunitDepth: 2,"),
    ("conformance-may-name-packs", RULES, ARCH, "\t\tallow:   []string{\"./internal/domainpack\", \"./internal/domainpack/conformance/...\"},", "\t\tallow:   []string{\"./internal/domainpack/...\"},"),
    ("test-infra-in-production", RULES, ARCH, "\t\tforbid:  cat(testInfra, []string{\"testing\"}),", "\t\tforbid:  []string{\"testing\"},"),
    ("patterns-whole-tree", MAIN, ARCH, "var defaultPatterns = []string{\"./cmd/...\", \"./internal/...\", \"./schemas/...\", \"./scripts/...\"}", "var defaultPatterns = []string{\"./...\"}"),
    ("patterns-skip-scripts", MAIN, ARCH, "var defaultPatterns = []string{\"./cmd/...\", \"./internal/...\", \"./schemas/...\", \"./scripts/...\"}", "var defaultPatterns = []string{\"./cmd/...\", \"./internal/...\", \"./schemas/...\"}"),
]

NOOP = "\n\nfunc (c *checker) noop(string, string, ...any) {}\n"


def fail_sites(src):
    """Every c.fail call as (check name, offset, the text after the name)."""
    for m in re.finditer(r'c\.fail\("([a-z/-]+)", "', src):
        yield m.group(1), m.start(), src[m.end():m.end() + 160]


def run(tmp, i, f, pkgs, src):
    m = pathlib.Path(tmp) / f"m{i}.go"
    m.write_text(src)
    ov = pathlib.Path(tmp) / f"m{i}.json"
    ov.write_text(json.dumps({"Replace": {str(REPO / f): str(m)}}))
    p = subprocess.run(["go", "test", "-count=1", "-timeout", "300s", "-overlay", str(ov), *pkgs],
                       cwd=REPO, capture_output=True, text=True, env=dict(os.environ))
    o = p.stdout + p.stderr
    if p.returncode == 0:
        return "SURVIVED", ""
    if "build failed" in o or "setup failed" in o or "[build failed]" in o:
        return "BUILD-FAILED", o[-300:]
    first = next((l.strip() for l in o.splitlines() if l.strip().startswith(("--- FAIL", "panic:"))), "")
    return "KILLED", first[:110]


def main() -> int:
    results = []
    with tempfile.TemporaryDirectory() as tmp:
        i = 0
        conf = (REPO / CONF).read_text()
        for name, start, after in fail_sites(conf):
            i += 1
            label = f"site:{name}@{conf[:start].count(chr(10)) + 1}"
            mutated = conf[:start] + "c.noop" + conf[start + len("c.fail"):] + NOOP
            status, first = run(tmp, i, CONF, PACKS, mutated)
            eq = next((why for (n, prefix), why in EQUIVALENT_SITES.items() if n == name and after.startswith(prefix)), None)
            if eq:
                status = "EQUIVALENT" if status == "SURVIVED" else f"UNEXPECTED-{status}"
                first = eq
            elif name in KERNEL_GUARDS and status == "SURVIVED":
                status, first = "KERNEL-GUARD", "proven by the kernel-*-reads-domain mutants"
            results.append((label, f"{status} {first}"))
            print("\t".join(results[-1]), flush=True)
        for name, f, pkgs, old, new in MUT:
            i += 1
            src = (REPO / f).read_text()
            if src.count(old) != 1:
                results.append((name, f"BAD-MUTANT (pattern found {src.count(old)} times)"))
            else:
                status, first = run(tmp, i, f, pkgs, src.replace(old, new))
                results.append((name, f"{status} {first}"))
            print("\t".join(results[-1]), flush=True)
    killed = sum(1 for r in results if r[1].startswith("KILLED"))
    equivalent = sum(1 for r in results if r[1].startswith("EQUIVALENT"))
    guards = sum(1 for r in results if r[1].startswith("KERNEL-GUARD"))
    print(f"{killed}/{len(results) - equivalent - guards} killed, {equivalent} equivalent, {guards} kernel guards (documented above)")
    return 0 if killed + equivalent + guards == len(results) else 1


if __name__ == "__main__":
    sys.exit(main())
