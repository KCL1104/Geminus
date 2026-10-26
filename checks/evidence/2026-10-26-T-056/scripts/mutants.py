#!/usr/bin/env python3
"""T-056 mutation check: each mutant changes one place of the photo path,
runs the tests that should catch it, and restores the file. KILLED: the
tests fail; SURVIVED: they pass. Go mutants run with Go 1.27.3; the web
mutants with vitest; the DB mutants need GEMINUS_TEST_DATABASE_URL (a
fresh dt_test_* database; make db-test re-migrates it) and are skipped
(NOT RUN) without it. Run from the repository root:

    python3 checks/evidence/2026-10-26-T-056/scripts/mutants.py > checks/evidence/2026-10-26-T-056/mutants.txt
"""
import os
import pathlib
import subprocess
import sys

ROOT = pathlib.Path(__file__).resolve().parents[4]
GO = "/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin"
ENV = dict(os.environ, PATH=GO + ":" + os.environ["PATH"], GOTOOLCHAIN="local")

GEN = "internal/proposals/generator/photo.go"
PHOTO = "internal/proposals/photo/photo.go"
CTX = "internal/ai/aicontext/photo.go"
REVIEW = "internal/ai/review/photo.go"
FAKE = "internal/ai/provider/fake/fake.go"
ACCEPTS = "internal/ai/provider/accepts.go"
HTTP = "internal/proposals/image_http.go"
MIG = "migrations/0017_proposal_images.up.sql"
CLIENT = "apps/web/lib/api/client.ts"
FORM = "apps/web/features/ai-proposals/photo-form.tsx"
VIEW = "apps/web/features/ai-proposals/proposal-view.tsx"


def go(*pkgs, run=None):
    cmd = ["go", "test", "-count=1"] + (["-run", run] if run else []) + list(pkgs)
    return ("go", cmd)


def web(*files):
    return ("web", ["npx", "vitest", "run"] + list(files))


def db(*pkgs, run=None):
    cmd = ["go", "test", "-p", "1", "-count=1"] + (["-run", run] if run else []) + list(pkgs)
    return ("db", cmd)


DBTEST = ("dbsql", ["make", "db-test"])

MUTANTS = [
    ("M1 the photo rules are not applied to a ready answer", GEN,
     "		outcome = outcome.Refuse(review.Photo(&ir, outcome.Candidate, len(seg.Segments)))\n", "", [go("./internal/proposals/generator/")]),
    ("M2 no segment asks the vision model anyway", GEN,
     "	case len(seg.Segments) == 0:\n		return generation{outcome: noBoxes(pr.OutputLocale), photo: pc, manifest: manifest}, nil\n", "",
     [go("./internal/proposals/generator/")]),
    ("M3 more segments than a photo takes are passed on", GEN,
     "	case len(seg.Segments) > review.MaxPhotoSegments:", "	case len(seg.Segments) > 1<<30:", [go("./internal/proposals/generator/")]),
    ("M4 a turned frame is taken as the image's", GEN,
     "	case seg.Width != img.Width || seg.Height != img.Height:", "	case seg.Width*seg.Height != img.Width*img.Height:",
     [go("./internal/proposals/generator/")]),
    ("M5 the segmenter is asked before the vision client says it takes images", GEN,
     "	if it, ok := h.cfg.Vision.(imageTaker); !ok || !it.AcceptsImages() {", "	if false {", [go("./internal/proposals/generator/")]),
    ("M6 the stored photo is not checked against its record", GEN,
     "	if photo.Verify(data, photo.Record{", "	if false && photo.Verify(data, photo.Record{", [go("./internal/proposals/generator/")]),
    ("M7 the upload is stored as sent (metadata kept)", PHOTO,
     "	return Image{MediaType: mediaType, Data: out.Bytes(), Width: cfg.Width, Height: cfg.Height, SHA256: sum(out.Bytes())}, nil",
     "	return Image{MediaType: mediaType, Data: data, Width: cfg.Width, Height: cfg.Height, SHA256: sum(data)}, nil",
     [go("./internal/proposals/photo/")]),
    ("M8 the segments are server data", CTX,
     '			{provider.UntrustedData, "SEGMENTS", segments},', '			{provider.ServerData, "SEGMENTS", segments},',
     [go("./internal/ai/aicontext/", "./internal/proposals/generator/")]),
    ("M9 a capacity may be any number", REVIEW,
     "		if c := r.Capacity; !known[float64(c)] && (c < 1 || c > boxes) {", "		if c := r.Capacity; false && !known[float64(c)] && (c < 1 || c > boxes) {",
     [go("./internal/ai/review/", "./internal/proposals/generator/")]),
    ("M10 more steps than boxes are allowed", REVIEW,
     "	if steps > boxes {", "	if steps > boxes+100 {", [go("./internal/ai/review/", "./internal/proposals/generator/")]),
    ("M11 the fake does not match a photo on its image", FAKE,
     "		return p.Images[0].Digest, byName, nil", '		return "", byName, nil', [go("./internal/ai/provider/fake/", "./internal/proposals/generator/")]),
    ("M12 every client says it takes images", ACCEPTS,
     "	return ok && ii.AcceptsImages()", "	return true", [go("./internal/ai/provider/", "./internal/proposals/generator/")]),
    ("M13 a part after the file is accepted", HTTP,
     "	case err == nil:\n		return malformed(\"the file part must be the last part\")\n", "	case err == nil:\n",
     [db("./internal/api/", run="TestPhotoRefusals")]),
    ("M14 a proposal with neither intent nor image can commit", MIG,
     "  IF (NEW.intent = '') IS DISTINCT FROM has_image THEN", "  IF false THEN", [DBTEST]),
    ("M15 the client sends the person's file name", CLIENT,
     'form.append("file", file, "photo");', 'form.append("file", file);', [web("tests/photo-api.test.ts", "tests/proposal-photo.test.tsx")]),
    ("M16 a blocked photo still opens the chooser", FORM,
     "      {blockedReason !== null ? (", "      {false ? (", [web("tests/proposal-photo.test.tsx", "tests/proposal-simplicity.test.tsx")]),
    ("M17 a retried photo takes a new key", FORM,
     "    if (sent.current?.request !== request) sent.current = { request, key: newIdempotencyKey() };",
     "    sent.current = { request, key: newIdempotencyKey() };", [web("tests/proposal-photo.test.tsx")]),
    ("M18 the AI's prose is always asked in English", FORM,
     "{ base_version_id: latest.id, output_locale: locale }", '{ base_version_id: latest.id, output_locale: "en" }',
     [web("tests/proposal-photo.test.tsx")]),
    ("M19 the proposal page does not say it came from a photo", VIEW,
     "      {p.source_image && (", "      {false && p.source_image && (", [web("tests/proposal-photo.test.tsx")]),
]


def run(kind, cmd):
    if kind in ("db", "dbsql") and not os.environ.get("GEMINUS_TEST_DATABASE_URL"):
        return None
    cwd = ROOT / "apps/web" if kind == "web" else ROOT
    env = dict(ENV)
    if kind == "db":
        env["GEMINUS_REQUIRE_DB_TESTS"] = "1"
    r = subprocess.run(cmd, cwd=cwd, env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, timeout=1800)
    return r.returncode


def main():
    only = set(sys.argv[1:])
    killed = survived = notrun = 0
    for name, rel, old, new, checks in MUTANTS:
        if only and name.split()[0] not in only:
            continue
        path = ROOT / rel
        src = path.read_text()
        if src.count(old) != 1:
            print(f"{name}: ERROR the text to mutate occurs {src.count(old)} times in {rel}")
            sys.exit(2)
        path.write_text(src.replace(old, new))
        try:
            codes = [run(kind, cmd) for kind, cmd in checks]
        finally:
            path.write_text(src)
        cmds = "; ".join(" ".join(cmd) for _, cmd in checks)
        if any(c is None for c in codes):
            notrun += 1
            print(f"{name}: NOT RUN (no GEMINUS_TEST_DATABASE_URL) [{cmds}]")
        elif any(c != 0 for c in codes):
            killed += 1
            print(f"{name}: KILLED (exit {codes}) [{rel}; {cmds}]")
        else:
            survived += 1
            print(f"{name}: SURVIVED [{rel}; {cmds}]")
    print(f"killed {killed}, survived {survived}, not run {notrun}")
    sys.exit(1 if survived else 0)


if __name__ == "__main__":
    main()
