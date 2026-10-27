#!/usr/bin/env python3
"""T-056 after the first live run: mutants of the latency fix, the photo
smoke's concept override and the floor plan vocabulary.

Each mutant changes one place of a copy of the branch (git archive) in the
directory given as the first argument (the agent's own scratch folder,
never a shared one), runs the named tests, and restores the file byte for
byte. Before any mutant, every test set must pass on the unmutated copy. A
build failure is INVALID, never killed. No database; every AI variable is
unset; no provider is called (the tests use fakes only).

    mutate.py <copy> [mutant ID ...]
"""
import json
import os
import subprocess
import sys

GO = "/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin/go"
AI_VARS = ["AI_PROVIDER", "AI_POLICY_APPROVAL", "AI_MODEL", "GEMINUS_AI_LIVE", "AI_VISION_PROVIDER", "AI_VISION_POLICY_APPROVAL",
           "AI_VISION_MODEL", "GEMINUS_MUSE_API_KEY", "GEMINUS_ANTHROPIC_API_KEY_A", "GEMINUS_ANTHROPIC_API_KEY_B", "MODEL_API_KEY",
           "ANTHROPIC_API_KEY", "AI_EVAL_LIVE_MAX_USD", "AI_SMOKE_PHOTO_CONCEPTS"]

SEG = "internal/ai/provider/segment.go"
EVM = "internal/aievals/main.go"
EVP = "internal/aievals/photo.go"
PACK = "internal/domainpack/electronicsline/photo.go"
CTX = "internal/ai/aicontext/photo.go"
TPL = "prompts/photo/system.md"
REV = "internal/ai/review/photo.go"
GEND = "scripts/gen-photo-demo/main.go"
WORKER = "cmd/worker/main.go"

PROV = (("./internal/ai/provider/",), "TestASegmentation|TestCheckConcepts|TestSegmentation")
SMOKE = (("./internal/aievals/", "./internal/config/aiapproval/"), "Photo|Concept|Frame")
VOC = (("./internal/domainpack/electronicsline/", "./internal/ai/aicontext/", "./internal/proposals/generator/", "./internal/aievals/",
        "./scripts/gen-photo-demo/"), "Photo|Concept|Shape|Reproducible|ImageProposal")
RULES = (("./internal/ai/review/", "./internal/proposals/generator/"), "Photo")

MUTANTS = [
    # The latency fix.
    ("L1 the latency written to a copy (the old bug)", SEG,
     "defer func() { out.Metadata.LatencyMillis = time.Since(start).Milliseconds() }()",
     "defer func() { m := out; m.Metadata.LatencyMillis = time.Since(start).Milliseconds(); _ = m }()", PROV),
    # provider.CheckConcepts and the client.
    ("C1 nine concepts pass", SEG, "if len(concepts) == 0 || len(concepts) > MaxSegmentConcepts {",
     "if len(concepts) == 0 || len(concepts) > MaxSegmentConcepts+1 {", PROV),
    ("C2 any characters pass", SEG, "case !concept.MatchString(k):", "case false && !concept.MatchString(k):", PROV),
    ("C3 a repeated concept passes", SEG, "case seen[k]:", "case false && seen[k]:", PROV),
    ("C4 65 bytes pass", SEG, "case len(k) > MaxSegmentConceptBytes:", "case len(k) > MaxSegmentConceptBytes+1:", PROV),
    ("C5 the client stops using CheckConcepts", SEG, "return CheckConcepts(req.Concepts) == nil", "return len(req.Concepts) > 0", PROV),
    # The smoke's override.
    ("O1 an empty item is skipped", EVM, 'return nil, errors.New("an empty concept (a comma at an end or two in a row)")', "continue", SMOKE),
    ("O2 the override is not checked before the run", EVM,
     "\tif err := provider.CheckConcepts(out); err != nil {\n\t\treturn nil, err\n\t}\n\treturn out, nil", "\treturn out, nil", SMOKE),
    ("O3 Run does not check the override", EVP,
     "\t\tif err := provider.CheckConcepts(cfg.Concepts); err != nil {\n\t\t\treturn nil, fmt.Errorf(\"aievals: the concepts to ask for: %w\", err)\n\t\t}\n",
     "", SMOKE),
    ("O4 the override is ignored", EVP, "concepts, rep.ConceptsOverridden = slices.Clone(cfg.Concepts), true", "rep.ConceptsOverridden = true", SMOKE),
    ("O5 the override is not reported", EVP, "concepts, rep.ConceptsOverridden = slices.Clone(cfg.Concepts), true",
     "concepts = slices.Clone(cfg.Concepts)", SMOKE),
    ("O6 the flag wins over the variable", EVM, 'case flagConcepts != "" && envConcepts != "":', "case false:", SMOKE),
    ("O7 -concepts without -smoke-photo", EVM, 'if *concepts != "" && !*smokePhoto {', "if false {", SMOKE),
    ("O8 sizes over the other side", EVP, "BoxSize{Width: share(b.X2-b.X1+1, w), Height: share(b.Y2-b.Y1+1, h)}",
     "BoxSize{Width: share(b.X2-b.X1+1, h), Height: share(b.Y2-b.Y1+1, w)}", SMOKE),
    ("O9 sizes to 2 places", EVP, "math.Round(float64(n)/float64(of)*1000) / 1000", "math.Round(float64(n)/float64(of)*100) / 100", SMOKE),
    ("O10 no per-concept entries", EVP,
     "\t\trep.Segmenter.ByConcept = append(rep.Segmenter.ByConcept, ConceptBoxes{Concept: c.concept, Answered: c.answered, Boxes: c.objects, Sizes: c.sizes})\n",
     "", SMOKE),
    ("O11 the summary does not name the override", EVM, 'concepts = "concepts overridden " + strings.Join(r.Segmenter.ConceptList, ",")',
     'concepts = "the pack\'s concepts"', SMOKE),
    ("O12 the variable is not read", EVM, "switch envConcepts := getenv(conceptsVar); {", 'switch envConcepts := ""; {', SMOKE),
    ("O13 a box outside the frame is sized", EVP,
     "\t\tif b.X1 < 0 || b.Y1 < 0 || b.X1 > b.X2 || b.Y1 > b.Y2 || b.X2 >= w || b.Y2 >= h {\n\t\t\tcontinue\n\t\t}\n", "", SMOKE),
    ("O14 the worker names the variable", WORKER, "package main\n", "package main\n\n// AI_SMOKE_PHOTO_CONCEPTS\n", SMOKE),
    # The vocabulary and the judge's instructions.
    ("V1 the old concepts", PACK, '"smt line", "aoi machine", "ict tester", "fct tester", "packing station", "conveyor belt",\n\t"rectangle", "arrow"}',
     '"smt line", "aoi machine", "ict tester", "fct tester", "burn-in chamber", "packing station",\n\t"conveyor belt", "workstation"}', VOC),
    ("V2 no arrow", PACK, '"rectangle", "arrow"}', '"rectangle", "workstation"}', VOC),
    ("V3 the server's rules say nothing of shapes", CTX,
     "\t\"- A segment's concept is what the segmenter was asked to find, not the kind of the station: on a drawn floor plan, a rectangle is a station drawn as a box, \" +\n",
     "", VOC),
    ("V4 the template keeps its old version", TPL, "prompt_template_version: photo-0.2.0", "prompt_template_version: photo-0.1.0", VOC),
    ("V5 the template says nothing of shapes", TPL, "segment 的 concept 是 segmenter 被要求找的東西，不是工作站的種類：", "", VOC),
    ("V6 the demo script's concepts are not the pack's", GEND,
     '"packing station", "conveyor belt", "rectangle", "arrow"}', '"packing station", "conveyor belt", "arrow", "rectangle"}', VOC),
    # The photo rules hold for shape boxes.
    ("R1 one step more than boxes passes", REV, "if steps > boxes {", "if steps > boxes+1 {", RULES),
]


def env():
    e = {k: v for k, v in os.environ.items() if k not in AI_VARS}
    e.update({"GOTOOLCHAIN": "local", "GOFLAGS": "", "PATH": os.path.dirname(GO) + ":" + e.get("PATH", "")})
    return e


def run(src, pkgs, pattern):
    cmd = [GO, "test", "-count=1"]
    if pattern:
        cmd += ["-run", pattern]
    cmd += list(pkgs)
    p = subprocess.run(cmd, cwd=src, env=env(), capture_output=True, text=True, timeout=1800)
    return p.returncode, p.stdout + p.stderr


def main():
    src = os.path.abspath(sys.argv[1])
    if "/scratchpad/t056c2-mut/" not in src + "/":
        sys.exit("refusing: the copy must be in scratchpad/t056c2-mut/")
    only = sys.argv[2:]
    chosen = [m for m in MUTANTS if not only or any(m[0].startswith(o + " ") for o in only)]
    for pkgs, pattern in {m[4] for m in chosen}:
        code, out = run(src, pkgs, pattern)
        print("BASELINE", " ".join(pkgs), pattern or "(all)", "exit", code, flush=True)
        if code != 0:
            print(out[-3000:])
            sys.exit(3)
    results = []
    for mid, path, old, new, (pkgs, pattern) in chosen:
        full = os.path.join(src, path)
        orig = open(full, "rb").read()
        text = orig.decode()
        n = text.count(old)
        if n != 1:
            results.append({"id": mid, "result": "NOT APPLIED", "matches": n})
            print(mid, "NOT APPLIED", n, flush=True)
            continue
        open(full, "w").write(text.replace(old, new))
        try:
            code, out = run(src, pkgs, pattern)
        finally:
            open(full, "wb").write(orig)
        if open(full, "rb").read() != orig:
            print("RESTORE FAILED", mid)
            sys.exit(2)
        if "build failed" in out or "[setup failed]" in out:
            result = "INVALID"
        elif code != 0:
            result = "KILLED"
        else:
            result = "SURVIVED"
        fails = [l for l in out.splitlines() if l.startswith("--- FAIL") or l.startswith("FAIL") or "panic:" in l][:6]
        results.append({"id": mid, "file": path, "result": result, "exit": code, "failing": fails})
        print(mid, result, fails[:3], flush=True)
    json.dump(results, open(os.path.join(os.path.dirname(src), "mutants.json"), "w"), indent=1)
    k = sum(r["result"] == "KILLED" for r in results)
    print(f"{k}/{len(results)} killed; survived: {[r['id'] for r in results if r['result'] == 'SURVIVED']}; "
          f"invalid or not applied: {[r['id'] for r in results if r['result'] in ('INVALID', 'NOT APPLIED')]}")
    sys.exit(0 if k == len(results) else 1)


if __name__ == "__main__":
    main()
