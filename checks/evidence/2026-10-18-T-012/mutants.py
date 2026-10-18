#!/usr/bin/env python3
"""T-012 mutation run: each mutant removes or weakens one rule of the model
service (internal/models) or its SQL (internal/storage/models) and must make
a test fail.

Applied with `go test -overlay` (the working tree is never modified) to
./internal/models/... and ./internal/storage/models/..., one package at a
time (`-p 1`, as in `make db-go-test`), against the disposable database of
`make db-test` (GEMINUS_TEST_DATABASE_URL; GEMINUS_REQUIRE_DB_TESTS=1 makes
a skip a failure, so a run without the database cannot pass for a kill).

KILLED means a test failed. A mutant that does not build or a run that
timed out is a harness error, never a kill. EQUIVALENT lists mutants that
cannot change behaviour, with the reason; they must survive.

Usage: GEMINUS_TEST_DATABASE_URL=... python3 checks/evidence/2026-10-18-T-012/mutants.py [--check] [--only=NAME ...] [repo-root]
"""
import json
import os
import pathlib
import subprocess
import sys
import tempfile

ONLY = {a.split("=", 1)[1] for a in sys.argv[1:] if a.startswith("--only=")}
ARGS = [a for a in sys.argv[1:] if a != "--check" and not a.startswith("--only=")]
CHECK_ONLY = "--check" in sys.argv[1:]
REPO = pathlib.Path(ARGS[0] if ARGS else pathlib.Path(__file__).resolve().parents[3])
PKGS = ["./internal/models/...", "./internal/storage/models/..."]
SVC = "internal/models/models.go"
ST = "internal/storage/models/models.go"

MUTANTS = [
    # roles and tenancy
    ("create-no-role", SVC, "\t\tif err := m.Require(writeRoles...); err != nil {\n\t\t\treturn err\n\t\t}\n\t\treplay, err := idempotency.Claim(ctx, tx, k, hash)\n\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n\t\tif replay != nil {\n\t\t\tstatus = replay.Status\n\t\t\tout, err = hashing.Canonical(replay.Body)\n\t\t\treturn err\n\t\t}\n\t\tif _, err := projects.Get", "\t\treplay, err := idempotency.Claim(ctx, tx, k, hash)\n\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n\t\tif replay != nil {\n\t\t\tstatus = replay.Status\n\t\t\tout, err = hashing.Canonical(replay.Body)\n\t\t\treturn err\n\t\t}\n\t\tif _, err := projects.Get"),
    ("write-roles-are-members", SVC, "\twriteRoles = tenant.Writers", "\twriteRoles = tenant.Members"),
    ("create-any-project", SVC, "\t\tif _, err := projects.Get(ctx, tx, m.OrganizationID, projectID); errors.Is(err, projects.ErrNotFound) {\n\t\t\treturn ErrNotFound\n\t\t} else if err != nil {\n\t\t\treturn err\n\t\t}\n\t\tlocale := req.Locale", "\t\tlocale := req.Locale"),
    ("list-any-project", SVC, "\t\tif _, err := projects.Get(ctx, tx, m.OrganizationID, projectID); errors.Is(err, projects.ErrNotFound) {\n\t\t\treturn ErrNotFound\n\t\t} else if err != nil {\n\t\t\treturn err\n\t\t}\n\t\trows, err := store.ListModels", "\t\trows, err := store.ListModels"),
    ("not-found-leaks", SVC, "\tif errors.Is(err, store.ErrNotFound) {\n\t\treturn ErrNotFound\n\t}\n\treturn err\n}", "\treturn err\n}"),
    # create
    ("create-no-idempotency", SVC, "\t\tif replay != nil {\n\t\t\tstatus = replay.Status\n\t\t\tout, err = hashing.Canonical(replay.Body)\n\t\t\treturn err\n\t\t}\n\t\tif _, err := projects.Get", "\t\tif replay != nil && false {\n\t\t\tstatus = replay.Status\n\t\t\tout, err = hashing.Canonical(replay.Body)\n\t\t\treturn err\n\t\t}\n\t\tif _, err := projects.Get"),
    ("hash-ignores-path", SVC, "\tparams[\"body\"] = jsontext.Value(canonical)", "\tparams = map[string]any{\"body\": jsontext.Value(canonical)}"),
    ("create-locale-ignored", SVC, "\t\tir, err := s.template(req.Domain, req.Template, modelID, req.Name, locale)", "\t\tir, err := s.template(req.Domain, req.Template, modelID, req.Name, domainpack.English)"),
    ("create-no-audit", SVC, "\t\t\tAction: \"model.create\",", "\t\t\tAction: \"model.created\","),
    ("create-draft-revision-2", SVC, "\t\t\tRevision: 1, IR: data, Hash: hash, Issues: issues, UpdatedBy: m.UserID})", "\t\t\tRevision: 2, IR: data, Hash: hash, Issues: issues, UpdatedBy: m.UserID})"),
    ("create-response-version-2", SVC, "\t\tresp, err := json.Marshal(publicModel(mdl, d, 1))", "\t\tresp, err := json.Marshal(publicModel(mdl, d, 2))"),
    # patch
    ("patch-revision-unchecked", SVC, "\t\tif d.Revision != expected {\n\t\t\treturn ErrConflict\n\t\t}\n\t\tres, err := patch.Apply", "\t\tres, err := patch.Apply"),
    ("patch-not-applied-saved", SVC, "\t\tif !res.Applied() {\n\t\t\treturn &InvalidError{Issues: res.PatchIssues}\n\t\t}", "\t\tif false && !res.Applied() {\n\t\t\treturn &InvalidError{Issues: res.PatchIssues}\n\t\t}"),
    ("patch-mode-ai", SVC, "patch.Apply(d.IR, patchDoc, patch.ManualDraft)", "patch.Apply(d.IR, patchDoc, patch.AIEdit)"),
    ("patch-issues-dropped", SVC, "\t\t\tRevision: d.Revision + 1, IR: res.Candidate, Hash: hash, Issues: issues, UpdatedBy: m.UserID}, d.Revision)", "\t\t\tRevision: d.Revision + 1, IR: res.Candidate, Hash: hash, Issues: nil, UpdatedBy: m.UserID}, d.Revision)"),
    ("patch-revision-same", SVC, "\t\t\tRevision: d.Revision + 1, IR: res.Candidate,", "\t\t\tRevision: d.Revision, IR: res.Candidate,"),
    ("patch-no-audit", SVC, "\t\t\tAction: \"model.draft.update\",", "\t\t\tAction: \"model.draft.updated\","),
    ("patch-stale-not-conflict", SVC, "\t\tif errors.Is(err, store.ErrStale) {\n\t\t\treturn ErrConflict\n\t\t}\n\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n\t\tif err := auditlog.AppendUser(ctx, tx, m.OrganizationID, m.UserID, auditlog.Event{\n\t\t\tAction: \"model.draft.update\"", "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n\t\tif err := auditlog.AppendUser(ctx, tx, m.OrganizationID, m.UserID, auditlog.Event{\n\t\t\tAction: \"model.draft.update\""),
    # publish
    ("publish-revision-unchecked", SVC, "\t\tif d.Revision != expected || req.ExpectedBaseVersionID != d.BaseVersionID || d.BaseVersionID != mdl.LatestVersionID {", "\t\tif req.ExpectedBaseVersionID != d.BaseVersionID || d.BaseVersionID != mdl.LatestVersionID {"),
    ("publish-expected-base-unchecked", SVC, "\t\tif d.Revision != expected || req.ExpectedBaseVersionID != d.BaseVersionID || d.BaseVersionID != mdl.LatestVersionID {", "\t\tif d.Revision != expected || d.BaseVersionID != mdl.LatestVersionID {"),
    ("publish-out-of-date-allowed", SVC, "\t\tif d.Revision != expected || req.ExpectedBaseVersionID != d.BaseVersionID || d.BaseVersionID != mdl.LatestVersionID {", "\t\tif d.Revision != expected || req.ExpectedBaseVersionID != d.BaseVersionID {"),
    ("publish-clean-allowed", SVC, "\t\tif d.Hash == base.Hash {\n\t\t\treturn ErrNoChanges\n\t\t}", "\t\tif false && d.Hash == base.Hash {\n\t\t\treturn ErrNoChanges\n\t\t}"),
    ("publish-errors-allowed", SVC, "\t\tif slices.ContainsFunc(issues, isError) {\n\t\t\treturn &InvalidError{Issues: issues}\n\t\t}", "\t\tif false && slices.ContainsFunc(issues, isError) {\n\t\t\treturn &InvalidError{Issues: issues}\n\t\t}"),
    ("publish-version-skips", SVC, "\t\t\tVersion: base.Version + 1, SchemaVersion: res.IR.SchemaVersion, ParentVersionID: &base.ID,", "\t\t\tVersion: base.Version + 2, SchemaVersion: res.IR.SchemaVersion, ParentVersionID: &base.ID,"),
    ("publish-no-parent", SVC, "SchemaVersion: res.IR.SchemaVersion, ParentVersionID: &base.ID,", "SchemaVersion: res.IR.SchemaVersion, ParentVersionID: nil,"),
    ("publish-full-manifest", SVC, "\t\t\tDatasetBindings: resolution.Manifest, CreatedBy: m.UserID})", "\t\t\tDatasetBindings: base.DatasetBindings, CreatedBy: m.UserID})"),
    ("publish-no-advance", SVC, "\t\tif err := s.advance(ctx, tx, m, mdl, d, base, v, res.IR.Name); err != nil {\n\t\t\treturn err\n\t\t}", "\t\t_ = s.advance"),
    ("publish-no-audit", SVC, "\t\t\tAction: \"model.publish\",", "\t\t\tAction: \"model.published\","),
    ("publish-no-idempotency", SVC, "\t\tif replay != nil {\n\t\t\tstatus = replay.Status\n\t\t\tout, err = hashing.Canonical(replay.Body)\n\t\t\treturn err\n\t\t}\n\t\tmdl, err := store.LockModel", "\t\tif replay != nil && false {\n\t\t\tstatus = replay.Status\n\t\t\tout, err = hashing.Canonical(replay.Body)\n\t\t\treturn err\n\t\t}\n\t\tmdl, err := store.LockModel"),
    # the draft rule of advance (D-003)
    ("advance-no-latest", SVC, "\tif err := store.SetLatest(ctx, tx, m.OrganizationID, mdl.ID, v.ID, name); err != nil {\n\t\treturn err\n\t}", "\t_ = name"),
    ("advance-name-kept", SVC, "store.SetLatest(ctx, tx, m.OrganizationID, mdl.ID, v.ID, name)", "store.SetLatest(ctx, tx, m.OrganizationID, mdl.ID, v.ID, mdl.Name)"),
    ("advance-dirty-overwritten", SVC, "\tif d.Hash != base.Hash && d.Hash != v.Hash {\n\t\treturn nil // dirty", "\tif false && d.Hash != base.Hash && d.Hash != v.Hash {\n\t\treturn nil // dirty"),
    ("advance-clean-left", SVC, "\tif d.Hash != base.Hash && d.Hash != v.Hash {\n\t\treturn nil // dirty", "\tif d.Hash != v.Hash {\n\t\treturn nil // dirty"),
    ("advance-publish-not-synced", SVC, "\tif d.Hash != base.Hash && d.Hash != v.Hash {\n\t\treturn nil // dirty", "\tif d.Hash != base.Hash {\n\t\treturn nil // dirty"),
    ("sync-revision-same", SVC, "\t\tRevision: d.Revision + 1, IR: v.IR, Hash: v.Hash, Issues: issues, UpdatedBy: m.UserID}, d.Revision)", "\t\tRevision: d.Revision, IR: v.IR, Hash: v.Hash, Issues: issues, UpdatedBy: m.UserID}, d.Revision)"),
    ("sync-base-kept", SVC, "\tnext, err := store.UpdateDraft(ctx, tx, store.Draft{OrganizationID: m.OrganizationID, ModelID: d.ModelID, BaseVersionID: v.ID,", "\tnext, err := store.UpdateDraft(ctx, tx, store.Draft{OrganizationID: m.OrganizationID, ModelID: d.ModelID, BaseVersionID: d.BaseVersionID,"),
    # reset
    ("reset-unconfirmed", SVC, "\tif !req.ConfirmDiscard {", "\tif false && !req.ConfirmDiscard {"),
    ("reset-revision-unchecked", SVC, "\t\tif d.Revision != expected || req.ExpectedLatestVersionID != mdl.LatestVersionID {", "\t\tif req.ExpectedLatestVersionID != mdl.LatestVersionID {"),
    ("reset-latest-unchecked", SVC, "\t\tif d.Revision != expected || req.ExpectedLatestVersionID != mdl.LatestVersionID {", "\t\tif d.Revision != expected {"),
    ("reset-no-audit", SVC, "\t\t\tAction: \"model.draft.reset\",", "\t\t\tAction: \"model.draft.reverted\","),
    # issues and datasets
    ("check-no-pack", SVC, "\tissues = append(issues, pack.Validate(ir)...)", "\t_ = pack"),
    ("check-full-manifest", SVC, "\tmanifest = usedBindings(ir, manifest)\n", ""),
    ("check-no-store-guard", SVC, "\tif len(manifest) > 0 && s.Datasets == nil {", "\tif false && len(manifest) > 0 && s.Datasets == nil {"),
    ("check-no-bindings", SVC, "\tif !slices.ContainsFunc(issues, isError) { // binding checks", "\tif false && !slices.ContainsFunc(issues, isError) { // binding checks"),
    ("check-ir-prefix-kept", SVC, "\t\t\tif rest, ok := strings.CutPrefix(i.Path, \"/ir\"); ok && (rest == \"\" || rest[0] == '/') {", "\t\t\tif rest, ok := strings.CutPrefix(i.Path, \"/ir\"); false && ok && (rest == \"\" || rest[0] == '/') {"),
    ("used-bindings-all", SVC, "\t\tif used[b.LogicalRef] {", "\t\tif true || used[b.LogicalRef] {"),
    # responses and lists
    ("ir-not-canonical", SVC, "\treturn hashing.Canonical(stored)", "\treturn jsontext.Value(stored), nil"),
    ("model-never-out-of-date", SVC, "Draft:         DraftSummary{Revision: d.Revision, BaseVersionID: d.BaseVersionID, OutOfDate: d.BaseVersionID != mdl.LatestVersionID},", "Draft:         DraftSummary{Revision: d.Revision, BaseVersionID: d.BaseVersionID, OutOfDate: false},"),
    ("draft-never-out-of-date", SVC, "\t\tOutOfDate: d.BaseVersionID != latestVersionID, Issues: issues}, nil", "\t\tOutOfDate: false, Issues: issues}, nil"),
    ("models-page-short", SVC, "\t\trows, err := store.ListModels(ctx, tx, m.OrganizationID, projectID, after, limit+1)", "\t\trows, err := store.ListModels(ctx, tx, m.OrganizationID, projectID, after, limit)"),
    ("versions-page-short", SVC, "\t\trows, err := store.ListVersions(ctx, tx, m.OrganizationID, modelID, before, limit+1)", "\t\trows, err := store.ListVersions(ctx, tx, m.OrganizationID, modelID, before, limit)"),
    ("cursor-any-parent", SVC, "c.List != list || c.Parent != parent ||", "c.List != list ||"),
    ("cursor-not-canonical", SVC, " || encodeCursor(c) != s {", " {"),
    # SQL
    ("sql-model-any-org", ST, "FROM twin.models WHERE organization_id=$1 AND id=$2`, org, id), org)\n}\n\n// LockModel", "FROM twin.models WHERE $1::text IS NOT NULL AND id=$2`, org, id), org)\n}\n\n// LockModel"),
    ("sql-lock-any-org", ST, "FROM twin.models WHERE organization_id=$1 AND id=$2 FOR UPDATE`", "FROM twin.models WHERE $1::text IS NOT NULL AND id=$2 FOR UPDATE`"),
    ("sql-lock-no-lock", ST, "AND id=$2 FOR UPDATE`", "AND id=$2`"),
    ("sql-latest-any-org", ST, "WHERE organization_id=$1 AND id=$2`,\n\t\torg, id, versionID, name)", "WHERE $1::text IS NOT NULL AND id=$2`,\n\t\torg, id, versionID, name)"),
    ("sql-list-models-any-org", ST, "FROM twin.models WHERE organization_id=$1 AND project_id=$2\n\t\t\tORDER BY created_at, id LIMIT $3`", "FROM twin.models WHERE $1::text IS NOT NULL AND project_id=$2\n\t\t\tORDER BY created_at, id LIMIT $3`"),
    ("sql-list-models-unordered-ties", ST, "AND (created_at, id) > ($3, $4) ORDER BY created_at, id LIMIT $5`", "AND (created_at, id) >= ($3, $4) ORDER BY created_at, id LIMIT $5`"),
    ("sql-version-any-org", ST, "FROM twin.model_versions WHERE organization_id=$1 AND id=$2`", "FROM twin.model_versions WHERE $1::text IS NOT NULL AND id=$2`"),
    ("sql-versions-oldest-first", ST, "\t\t\tORDER BY version DESC LIMIT $3`", "\t\t\tORDER BY version LIMIT $3`"),
    ("sql-versions-any-org", ST, "FROM twin.model_versions WHERE organization_id=$1 AND model_id=$2\n\t\t\tORDER BY version DESC LIMIT $3`", "FROM twin.model_versions WHERE $1::text IS NOT NULL AND model_id=$2\n\t\t\tORDER BY version DESC LIMIT $3`"),
    ("sql-no-binding-rows", ST, "\tfor _, b := range bindings {\n\t\tif _, err := tx.Exec(ctx, `INSERT INTO twin.model_version_datasets", "\tfor _, b := range bindings[:0] {\n\t\tif _, err := tx.Exec(ctx, `INSERT INTO twin.model_version_datasets"),
    ("sql-draft-any-org", ST, "\t\tWHERE organization_id=$1 AND model_id=$2`, org, modelID).Scan(", "\t\tWHERE $1::text IS NOT NULL AND model_id=$2`, org, modelID).Scan("),
    ("sql-update-any-revision", ST, "WHERE organization_id=$1 AND model_id=$2 AND revision=$9 RETURNING updated_at`", "WHERE organization_id=$1 AND model_id=$2 AND $9::bigint IS NOT NULL RETURNING updated_at`"),
    ("sql-update-any-org", ST, "WHERE organization_id=$1 AND model_id=$2 AND revision=$9 RETURNING updated_at`", "WHERE $1::text IS NOT NULL AND model_id=$2 AND revision=$9 RETURNING updated_at`"),
    ("sql-issues-not-stored", ST, "\tissues, err := encodeIssues(d.Issues)\n\tif err != nil {\n\t\treturn Draft{}, err\n\t}\n\terr = tx.QueryRow(ctx, `UPDATE", "\tissues, err := encodeIssues(nil)\n\tif err != nil {\n\t\treturn Draft{}, err\n\t}\n\terr = tx.QueryRow(ctx, `UPDATE"),
]

EQUIVALENT: dict[str, str] = {}

HARNESS_ERRORS = {
    "BUILD-FAILED": ("build failed", "setup failed"),
    "TIMEOUT": ("panic: test timed out",),
}


def verdict(p):
    o = p.stdout + p.stderr
    if p.returncode == 0:
        return "SURVIVED", ""
    for kind, marks in HARNESS_ERRORS.items():
        if any(m in o for m in marks):
            return kind, o[-300:].replace("\n", " ")
    first = next((l.strip() for l in o.splitlines() if l.strip().startswith(("--- FAIL", "panic:"))), "")
    return "KILLED", first[:110]


def main() -> int:
    bad = []
    for name, f, old, new in MUTANTS:
        n = (REPO / f).read_text().count(old)
        if n != 1:
            bad.append(f"{name}: pattern occurs {n} times in {f}")
    if bad:
        print("\n".join(bad))
        return 2
    if CHECK_ONLY:
        print(f"ok: {len(MUTANTS)} mutants")
        return 0
    if not os.environ.get("GEMINUS_TEST_DATABASE_URL"):
        print("set GEMINUS_TEST_DATABASE_URL to the disposable database of `make db-test`", file=sys.stderr)
        return 2
    env = dict(os.environ, GEMINUS_REQUIRE_DB_TESTS="1", GOTOOLCHAIN=os.environ.get("GOTOOLCHAIN", "go1.27.2"))
    results = []
    with tempfile.TemporaryDirectory() as tmp:
        for i, (name, f, old, new) in enumerate(MUTANTS):
            if ONLY and name not in ONLY:
                continue
            src = (REPO / f).read_text().replace(old, new)
            m = pathlib.Path(tmp) / f"m{i}.go"
            m.write_text(src)
            ov = pathlib.Path(tmp) / f"m{i}.json"
            ov.write_text(json.dumps({"Replace": {str(REPO / f): str(m)}}))
            p = subprocess.run(["go", "test", "-p", "1", "-count=1", "-timeout", "600s", "-overlay", str(ov), *PKGS],
                               cwd=REPO, capture_output=True, text=True, env=env)
            results.append((name, *verdict(p)))
            print("\t".join(results[-1]), flush=True)
    wrong = [r for r in results if r[1] in HARNESS_ERRORS or (r[1] == "SURVIVED") != (r[0] in EQUIVALENT)]
    print(f"total={len(results)} killed={sum(r[1] == 'KILLED' for r in results)} equivalent={sum(r[0] in EQUIVALENT for r in results)} unexpected={len(wrong)}")
    for r in wrong:
        print("UNEXPECTED\t" + "\t".join(r))
    return 1 if wrong else 0


if __name__ == "__main__":
    sys.exit(main())
