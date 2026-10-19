#!/usr/bin/env python3
"""T-013c mutation check of the model HTTP adapter: each mutant weakens one
rule of internal/models/http.go or internal/api/api.go and must make the
adapter's tests fail (the unit tests in internal/models, the HTTP tests in
internal/api against a migrated disposable database).

Mutants are applied with `go test -overlay`; the repository is never
modified. A mutant that does not build is a harness error, never a kill.

Usage: GEMINUS_TEST_DATABASE_URL=... python3 checks/evidence/2026-10-19-T-013/http-mutants.py [repo-root]
"""
import json
import os
import pathlib
import subprocess
import sys
import tempfile

REPO = pathlib.Path(sys.argv[1] if len(sys.argv) > 1 else pathlib.Path(__file__).resolve().parents[3]).resolve()
HTTP = "internal/models/http.go"
API = "internal/api/api.go"
TESTS = [
    ("./internal/models/", "TestRoutesMatchContract|TestIfMatch|TestRefused"),
    ("./internal/api/", "TestModel|TestDraftPreconditions|TestPatchBody|TestPublishRefusals"),
]

MUTANTS = [
    ("missing-if-match-is-400", HTTP, "return 0, httpapi.Errorf(httpapi.PreconditionRequired,", "return 0, httpapi.Errorf(httpapi.InvalidRequest,"),
    ("if-match-list-accepted", HTTP, "if len(values) > 1 || len(values[0]) > 100 || m == nil {", "if len(values[0]) > 100 || m == nil {"),
    ("if-match-overflow-ignored", HTTP, "return 0, ErrConflict", "return 0, nil"),
    ("if-match-weak-accepted", HTTP, 'regexp.MustCompile(`^"draft-revision-', 'regexp.MustCompile(`^(?:W/)?"draft-revision-'),
    ("no-etag-on-get", HTTP, "\tsetETag(w, d)\n\thttpapi.WriteJSON(w, 200, d)\n\treturn nil\n}\n\n// patchDraft", "\thttpapi.WriteJSON(w, 200, d)\n\treturn nil\n}\n\n// patchDraft"),
    ("etag-off-by-one", HTTP, '`"draft-revision-%d"`, d.Revision)', '`"draft-revision-%d"`, d.Revision+1)'),
    ("empty-patch-accepted", HTTP, "if len(ops) == 0 {", "if false {"),
    ("patch-limit-1mib", HTTP, '"application/json-patch+json", validate.MaxIRBytes,', '"application/json-patch+json", validate.MaxIRBytes/2,'),
    ("patch-as-plain-json", HTTP, 'DecodeMedia(r, "application/json-patch+json",', 'DecodeMedia(r, "application/json",'),
    ("conflict-as-lifecycle", HTTP, 'httpapi.Errorf(httpapi.ModelVersionConflict, "the draft or the model', 'httpapi.Errorf(httpapi.LifecycleConflict, "the draft or the model'),
    ("no-changes-as-conflict", HTTP, 'httpapi.Errorf(httpapi.LifecycleConflict, "the draft has no changes', 'httpapi.Errorf(httpapi.ModelVersionConflict, "the draft has no changes'),
    ("invalid-without-details", HTTP, "e.Details = append(e.Details, httpapi.Detail{Path: is.Path, Reason: is.Message, Code: is.Code,", "_ = append(e.Details, httpapi.Detail{Path: is.Path, Reason: is.Message, Code: is.Code,"),
    ("invalid-as-request-failure", HTTP, "Code: httpapi.ModelValidationFailed,", "Code: httpapi.RequestValidationFailed,"),
    ("datasets-unmapped", HTTP, "case errors.Is(err, ErrDatasetsUnavailable):", "case false:"),
    ("in-progress-unmapped", HTTP, ", errors.Is(err, idempotency.ErrInProgress):", ":"),
    ("comment-error-unmapped", HTTP, "case errors.Is(err, ErrInvalidComment):", "case false:"),
    ("publish-without-key", HTTP, 'id, err := pathID(r, modelIDs, "model")\n\tif err != nil {\n\t\treturn err\n\t}\n\tkey, err := httpapi.IdempotencyKey(r)', 'id, err := pathID(r, modelIDs, "model")\n\tif err != nil {\n\t\treturn err\n\t}\n\tkey, err := "", error(nil)'),
    ("reset-ignores-if-match", HTTP, "\tvar req ResetRequest\n", "\texpected = 1 << 40\n\tvar req ResetRequest\n"),
    ("version-path-takes-model-id", HTTP, 'pathID(r, versionIDs, "model version")', 'pathID(r, modelIDs, "model version")'),
    ("list-models-serves-versions", HTTP, 'case "listModels":\n\t\t\tf = h.list\n', 'case "listModels":\n\t\t\tf = h.listVersions\n'),
    ("patch-route-for-viewers", HTTP, '{"PATCH", "/v1/models/{id}/draft", "patchDraft", writeRoles}', '{"PATCH", "/v1/models/{id}/draft", "patchDraft", readRoles}'),
    ("models-not-registered", API, "\tmodels.Register(mux, log, v, db, &models.Service{DB: db, Packs: packs})\n", "\t_ = models.Register\n\t_ = packs\n"),
    ("no-packs", API, "domainpack.NewRegistry(softwaredelivery.Pack{})", "domainpack.NewRegistry()\n\t_ = softwaredelivery.Pack{}"),
]
EQUIVALENT: dict[str, str] = {}


def run(overlay: str | None) -> str:
    env = dict(os.environ, GEMINUS_REQUIRE_DB_TESTS="1")
    for pkg, pattern in TESTS:
        cmd = ["go", "test", "-count=1", "-run", pattern, pkg]
        if overlay:
            cmd.insert(2, "-overlay=" + overlay)
        r = subprocess.run(cmd, cwd=REPO, env=env, capture_output=True, text=True)
        if r.returncode:
            if "--- FAIL" in r.stdout:
                return "KILLED\t" + next(l.strip() for l in r.stdout.splitlines() if l.startswith("--- FAIL"))
            return "BUILD-FAILED\t" + (r.stdout + r.stderr).strip().splitlines()[-1]
    return "SURVIVED"


def main() -> int:
    if not os.environ.get("GEMINUS_TEST_DATABASE_URL"):
        print("set GEMINUS_TEST_DATABASE_URL to a migrated dt_test_* database", file=sys.stderr)
        return 2
    sources = {f: (REPO / f).read_text() for f in {m[1] for m in MUTANTS}}
    bad = [n for n, f, old, _ in MUTANTS if sources[f].count(old) != 1]
    if bad:
        print("patterns not found exactly once:", bad)
        return 2
    base = run(None)
    if base != "SURVIVED":
        print("baseline fails:", base)
        return 2
    wrong = 0
    with tempfile.TemporaryDirectory() as tmp:
        for i, (name, f, old, new) in enumerate(MUTANTS):
            mutated = pathlib.Path(tmp) / f"m{i}.go"
            mutated.write_text(sources[f].replace(old, new))
            overlay = pathlib.Path(tmp) / f"o{i}.json"
            overlay.write_text(json.dumps({"Replace": {str(REPO / f): str(mutated)}}))
            v = run(str(overlay))
            ok = not v.startswith("BUILD-FAILED") and (v == "SURVIVED") == (name in EQUIVALENT)
            wrong += not ok
            print(f"{name}\t{v}" + ("" if ok else "\tUNEXPECTED"), flush=True)
    print(f"total={len(MUTANTS)} unexpected={wrong}")
    return 1 if wrong else 0


if __name__ == "__main__":
    sys.exit(main())
