#!/usr/bin/env python3
"""Mutation run of the API's photo decision (the T-056 follow-up, D-124).

Usage: mutate.py <pristine tree (git archive of the commit)> <log> [db]
Each mutant replaces exactly one occurrence of a string in one file of a copy
of the tree and runs the tests of the packages it touches: KILLED when they
fail, SURVIVED when they pass, INVALID when the mutant does not build (an
invalid mutant is never counted as killed). The tree given is never changed.
Mutants marked db run only with the third argument "db" and
GEMINUS_TEST_DATABASE_URL set to a disposable dt_test_* database that
`make db-test` migrated (GEMINUS_REQUIRE_DB_TESTS=1, so a skip fails).
"""
import os
import shutil
import subprocess
import sys
import time
from pathlib import Path

TREE = Path(sys.argv[1]).resolve()
LOG = Path(sys.argv[2]).resolve()
WITH_DB = len(sys.argv) > 3 and sys.argv[3] == "db"
WORK = TREE.parent / "work"
GOBIN = "/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin"
ENV = dict(os.environ, PATH=GOBIN + ":" + os.environ["PATH"], GOTOOLCHAIN="local", GOFLAGS="-count=1")
DB = ENV.get("GEMINUS_TEST_DATABASE_URL", "")
for k in [k for k in ENV if k.startswith(("GEMINUS_", "AI_", "ANTHROPIC", "MODEL_API", "DATABASE_URL"))]:
    ENV.pop(k)  # no AI variable of this shell
if WITH_DB:
    if "/dt_test_" not in DB:
        sys.exit("db mutants need GEMINUS_TEST_DATABASE_URL naming a dt_test_* database")
    ENV.update(GEMINUS_TEST_DATABASE_URL=DB, GEMINUS_REQUIRE_DB_TESTS="1")

CFG = ["./internal/config/...", "./cmd/api/", "./cmd/worker/"]
API = ["./cmd/api/"]
PRO = ["./internal/proposals/", "./internal/proposals/generator/"]
# The DB-backed tests that see the wiring (run alone, -p 1).
DBT = ["-p", "1", "-run", "TestPhotoStaleBaseAndAvailability|TestTheServedHandlerDecidesPhotosOnTheirOwn|TestAPhotoProposalEndToEnd",
       "./internal/api/", "./cmd/api/"]

A = "internal/config/apiai.go"
G = "internal/config/ai.go"
M = "cmd/api/main.go"
I = "internal/proposals/image.go"
H = "internal/proposals/image_http.go"
S = "internal/api/api.go"

MUTANTS = [
    # LoadAPIAI: what the API keeps of the photo path
    ("V1 the photo path not kept", A, "v := ai.Vision", "v := AIVision{}", CFG, False),
    ("V2 the photo approval dropped", A, "Approval: v.Approval, Model: v.Model", "Model: v.Model", CFG, False),
    ("V3 the photo reason the proposals'", A, "LiveOff: v.LiveOff}}", "LiveOff: ai.LiveOff}}", CFG, False),
    ("V4 the photo provider the proposals'", A, "Vision: APIVision{Provider: v.Provider,", "Vision: APIVision{Provider: ai.Provider,", CFG, False),
    ("V5 the segmentation model dropped", A, "SegmentationModel: v.SegmentationModel,", "", CFG, False),
    ("V6 the API's gate reads the keys", A, "decide(getenv, p, fakeOK, keyFree)", "decide(getenv, p, fakeOK, loadLive)", CFG, False),
    ("V7 the decision's errors dropped", A, "errs = append(errs, derrs...)", "_ = derrs", CFG, False),
    ("V8 the photo path's own fakeOK ignored", A, "decide(getenv, p, fakeOK, keyFree)", "decide(getenv, p, true, keyFree)", CFG, False),
    # The photo path's LiveEnabled and the shared liveFor
    ("L1 APIVision without images", A, "return liveFor(v.Provider, v.Approval, true)", "return liveFor(v.Provider, v.Approval, false)", CFG, False),
    ("L2 APIVision any valid approval", A, "return liveFor(v.Provider, v.Approval, true)", "return v.Approval.Valid()", CFG, False),
    ("L3 liveFor without the provider", G, "a.Valid() && a.Provider() == string(p) && (!images", "a.Valid() && (!images", CFG, False),
    ("L4 liveFor without images", G, "(!images || a.AllowsImages())", "true", CFG, False),
    ("L5 liveFor without a valid approval", G, "p.live() && a.Valid() && a.Provider()", "p.live() && a.Provider()", CFG, False),
    # The shared decision (both sides change: the explicit tests must see it)
    ("D1 keyFree keeps no approval", G, "return live{approval: a, model: model}, \"\", nil", "_ = a\n\treturn live{model: model}, \"\", nil", CFG, False),
    ("D2 keyFree past an off gate", G, "if why != \"\" || len(errs) > 0 {\n\t\treturn live{}, why, errs\n\t}\n\ta, ok := aiapproval.Recorded(approval)",
     "if len(errs) > 0 {\n\t\treturn live{}, why, errs\n\t}\n\ta, ok := aiapproval.Recorded(approval)", CFG, False),
    ("D3 another photo provider without images", G, "visionApprovalVar, visionModelVar, visionApprovalVar, on, true)",
     "visionApprovalVar, visionModelVar, visionApprovalVar, on, false)", CFG, False),
    ("D4 the proposals' provider's photos without images", G, "case !ai.Approval.AllowsImages():", "case false:", CFG, False),
    ("D5 the photo path never follows", G, "case \"\":\n\t\tvp = ai.Provider", "case \"\":\n\t\tvp = AIProviderDisabled", CFG, False),
    # cmd/api
    ("P1 photos follow the proposals", M, "return availability(ai.Vision.Provider, ai.Vision.LiveEnabled())", "return aiAvailability(ai)", API, False),
    ("P2 photos on with the proposals", M, "availability(ai.Vision.Provider, ai.Vision.LiveEnabled())", "availability(ai.Vision.Provider, ai.LiveEnabled())", API, False),
    ("P3 photos of the proposals' provider", M, "availability(ai.Vision.Provider, ai.Vision.LiveEnabled())", "availability(ai.Provider, ai.Vision.LiveEnabled())", API, False),
    ("P4 a live provider not on is not configured", M, "\t\treturn proposals.PolicyNotApproved\n\t}\n\treturn proposals.NotConfigured",
     "\t\treturn proposals.NotConfigured\n\t}\n\treturn proposals.NotConfigured", API, False),
    ("P5 no photo log when on", M, "case v.LiveEnabled():\n\t\tlog.Info(\"photo proposals are accepted", "case false:\n\t\tlog.Info(\"photo proposals are accepted", API, False),
    ("P6 the photo log's reason the proposals'", M, "\"code\", strings.ToUpper(v.LiveOff))", "\"code\", strings.ToUpper(ai.LiveOff))", API, False),
    ("P7 no photo log for a live photo path", M, "case v.Provider != config.AIProviderAnthropic && v.Provider != config.AIProviderMeta:",
     "case true:", API, False),
    # internal/proposals: the refusal
    ("R1 the photo refusal is the proposals'", H, "case errors.As(err, &ue) && ue.Photo:", "case false && errors.As(err, &ue) && ue.Photo:", PRO, False),
    ("R2 the detail's code", H, "Code: CodeImageUnsupported}}}", "Code: CodeImageInvalid}}}", PRO, False),
    ("R3 not approved as not configured", H, "if ue.Availability == PolicyNotApproved {\n\t\t\tcode, message = httpapi.AIPolicyNotApproved",
     "if false {\n\t\t\tcode, message = httpapi.AIPolicyNotApproved", PRO, False),
    ("R4 the detail at /file", H, "Details: []httpapi.Detail{{Path: \"\",\n", "Details: []httpapi.Detail{{Path: \"/file\",\n", PRO, False),
    ("R5 another code", I, "CodeImageUnsupported = \"AI_IMAGE_UNSUPPORTED\"", "CodeImageUnsupported = \"AI_PHOTO_OFF\"", PRO, False),
    # The wiring: seen only through the handler, on the database
    ("W1 BeginImage asks the proposals' availability", I,
     "if s.Photo != Available {\n\t\treturn &UnavailableError{Availability: s.Photo, Photo: true}\n\t}\n\tif s.Objects == nil {",
     "if s.AI != Available {\n\t\treturn &UnavailableError{Availability: s.AI, Photo: true}\n\t}\n\tif s.Objects == nil {", DBT, True),
    ("W2 BeginImage's refusal not a photo's", I,
     "return &UnavailableError{Availability: s.Photo, Photo: true}\n\t}\n\tif s.Objects == nil {",
     "return &UnavailableError{Availability: s.Photo}\n\t}\n\tif s.Objects == nil {", DBT, True),
    ("W3 CreateFromImage asks the proposals' availability", I,
     "\t\tif s.Photo != Available {\n\t\t\treturn &UnavailableError{Availability: s.Photo, Photo: true}\n\t\t}\n\t\t// The model's row first",
     "\t\tif s.AI != Available {\n\t\t\treturn &UnavailableError{Availability: s.AI, Photo: true}\n\t\t}\n\t\t// The model's row first", DBT, True),
    ("W4 the service's photo path the proposals'", S, "Photo: o.photo,", "Photo: o.ai,", DBT, True),
    ("W5 WithPhotoAI ignored", S, "return func(o *options) { o.photo = a }", "return func(o *options) { _ = a }", DBT, True),
    ("W6 the served photos as the proposals", M, "api.WithPhotoAI(photoAvailability(ai))", "api.WithPhotoAI(aiAvailability(ai))", DBT, True),
    ("W7 the served handler without WithPhotoAI", M, ", api.WithPhotoAI(photoAvailability(ai))}", "}", DBT, True),
]


def run(rel, old, new, pkgs):
    if WORK.exists():
        shutil.rmtree(WORK)
    shutil.copytree(TREE, WORK, symlinks=True)
    f = WORK / rel
    text = f.read_text(encoding="utf-8")
    n = text.count(old)
    if n != 1:
        return "NOT APPLIED (%d occurrences)" % n, ""
    f.write_text(text.replace(old, new, 1), encoding="utf-8")
    start = time.time()
    try:
        p = subprocess.run(["go", "test", "-timeout", "30m", *pkgs], cwd=WORK, env=ENV, capture_output=True, text=True, timeout=2400)
        out, code = p.stdout + p.stderr, p.returncode
    except subprocess.TimeoutExpired:
        return "TIMEOUT (not counted)", ""
    failed = [l for l in out.splitlines() if l.startswith(("--- FAIL", "FAIL\t", "# ", "--- SKIP"))]
    if "[build failed]" in out or "[setup failed]" in out or "build failed" in out:
        return "INVALID (does not build)", "\n".join(failed[:6])
    verdict = ("KILLED" if code != 0 else "SURVIVED") + " (%.0f s)" % (time.time() - start)
    return verdict, "\n".join(failed[:12])


def main():
    counts = {"KILLED": 0, "SURVIVED": 0, "INVALID": 0, "other": 0}
    only = os.environ.get("ONLY")
    chosen = [m for m in MUTANTS if m[5] == WITH_DB and (not only or any(m[0].startswith(o + " ") for o in only.split(",")))]
    with LOG.open("w") as log:
        pkgs = DBT if WITH_DB else sorted(set(CFG + API + PRO))
        verdict, detail = run(A, "package config", "package config", pkgs)
        log.write("baseline (no change, %s): %s\n%s\n" % (" ".join(pkgs), verdict, detail))
        log.flush()
        if not verdict.startswith("SURVIVED"):
            log.write("the baseline does not pass: stopping\n")
            return 1
        for name, rel, old, new, pkgs, _ in chosen:
            verdict, detail = run(rel, old, new, pkgs)
            key = verdict.split(" ")[0]
            counts[key if key in counts else "other"] += 1
            log.write("%s | %s | %s\n" % (name, rel, verdict))
            if detail:
                log.write("    " + detail.replace("\n", "\n    ") + "\n")
            log.flush()
        log.write("\n%d mutants (%s): %d killed, %d survived, %d invalid (do not build), %d not applied or timed out\n" % (
            len(chosen), "database" if WITH_DB else "no database", counts["KILLED"], counts["SURVIVED"], counts["INVALID"], counts["other"]))
    shutil.rmtree(WORK, ignore_errors=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
