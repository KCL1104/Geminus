#!/usr/bin/env python3
"""T-009 mutation run: each mutant removes or weakens one check of the HTTP
transport (error envelope, request IDs, JSON bodies, scope middleware), the
role policy, the project service and handlers, the idempotency, project and
audit storage, D-001 IDs, the listener configuration, the API command, or
the architecture checker's gateway rule, and must make a test fail.
Applied with `go test -overlay` (the working tree is never modified);
KILLED means a test failed. Packages run one at a time (`-p 1`, as in
`make db-go-test`): the DB-backed tests share one database, and a test that
changes database-wide state must not make another package's test fail.

Usage: GEMINUS_TEST_DATABASE_URL=... python3 checks/evidence/2026-10-18-T-009/mutants.py [--check] [--only=NAME ...] [repo-root]
--check only confirms that every pattern occurs exactly once; --only runs
the named mutants. Most mutants are caught by the DB-backed integration
tests, so the disposable database of `make db-test` is required up front
(GEMINUS_REQUIRE_DB_TESTS=1 turns a skip into a failure, which would count
as a kill).
"""
import json
import os
import pathlib
import subprocess
import sys
import tempfile

ONLY = [a.split("=", 1)[1] for a in sys.argv[1:] if a.startswith("--only=")]
ARGS = [a for a in sys.argv[1:] if a != "--check" and not a.startswith("--only=")]
CHECK = "--check" in sys.argv[1:]
REPO = pathlib.Path(ARGS[0] if ARGS else pathlib.Path(__file__).resolve().parents[3])
E = "internal/httpapi/errors.go"
R = "internal/httpapi/request.go"
S = "internal/httpapi/scope.go"
P = "internal/tenant/policy.go"
TX = "internal/tenant/transaction.go"
PJ = "internal/project/project.go"
PH = "internal/project/http.go"
API = "internal/api/api.go"
I = "internal/storage/idempotency/idempotency.go"
PS = "internal/storage/projects/projects.go"
AL = "internal/storage/auditlog/auditlog.go"
ID = "internal/ids/ids.go"
CS = "internal/config/server.go"
M = "cmd/api/main.go"
AC = "scripts/check-architecture/check.go"
AR = "scripts/check-architecture/rules.go"
PKGS = ["./internal/httpapi/...", "./internal/project/...", "./internal/api/...", "./internal/ids/...",
        "./internal/tenant/...", "./internal/config/...", "./internal/storage/...", "./cmd/api/..."]
CHECKER = ["./scripts/check-architecture/..."]

OPEN = "\tif err != nil || media != \"application/json\" || !utf8Charset(params) {"
CHARSET = "\t\tif k != \"charset\" || !strings.EqualFold(v, \"utf-8\") {"
CAT = "\tcase errors.Is(err, tenant.ErrRetryable), errors.Is(err, tenant.ErrOutcomeUnknown):"
CANCEL = "\tcase errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):"
SEL = "\t\tif len(values) != 1 || !orgID.MatchString(values[0]) {"
MEMBERS_GET = "\t\tif err := m.Require(readRoles...); err != nil {\n\t\t\treturn err\n\t\t}\n\t\tp, err := projects.Get"
MEMBERS_LIST = "\t\tif err := m.Require(readRoles...); err != nil {\n\t\t\treturn err\n\t\t}\n\t\trows, err"
REQUIRE_WRITERS = "\t\tif err := m.Require(createRoles...); err != nil {\n\t\t\treturn err\n\t\t}\n"
CLAIM_REPLAY = ("\t\treplay, err := idempotency.Claim(ctx, tx, k, hash)\n\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n"
                "\t\tif replay != nil {\n\t\t\t// jsonb keeps the value, not the bytes: both answers are sent\n"
                "\t\t\t// in canonical form, so a replay is byte-identical.\n\t\t\tstatus = replay.Status\n"
                "\t\t\tout, err = hashing.Canonical(replay.Body)\n\t\t\treturn err\n\t\t}\n")
CURSOR_CHECK = "\tif err != nil || c.Version != 1 || c.Org != org || c.List != cursorList || !ValidID(c.ID) {"
KEY = "\tif len(values) > 1 || len(k) < 8 || len(k) > 128 || strings.ContainsFunc(k, func(c rune) bool { return c < 0x21 || c > 0x7e }) {"
LIMIT = "\t\tif len(vs) != 1 || err != nil || n < 1 || n > 100 || strconv.Itoa(n) != vs[0] {"
CURSOR_Q = "\t\tif len(vs) != 1 || vs[0] == \"\" || len(vs[0]) > 2048 {"
CONFLICT = "\tif errors.Is(err, idempotency.ErrConflict) || errors.Is(err, idempotency.ErrInProgress) {"
KEYSET = "AND (created_at, id) > ($2, $3)"
PORT = "\tif err != nil || perr != nil || n < 0 || n > 65535 {"
THROUGH = "\t\tforbid:  cat(dbDrivers, []string{\"database/sql/...\"}),\n\t\tthrough: []string{\"./internal/storage/...\", \"./internal/tenant/...\"},"

DB_HANDLES_RULE = '\t{\n\t\t// The gateways above include the raw foundation, whose Open takes\n\t\t// any connection string: through it a package could open its own\n\t\t// handle around tenant.DB.Run, CheckRuntimeRole and Guard (T-009\n\t\t// review M2). Only the gateways themselves and cmd use it.\n\t\tname:    "db-handles-only-in-gateways",\n\t\twhy:     "connections are opened by cmd and used through internal/tenant and the storage packages, so every query runs in the tenant scope",\n\t\tapplies: []string{"./..."},\n\t\texcept:  []string{"./internal/storage/...", "./internal/tenant/...", "./cmd/..."},\n\t\tforbid:  []string{"./internal/storage/postgres/..."},\n\t\tthrough: []string{"./internal/storage/...", "./internal/tenant/..."},\n\t},\n'

MUT = [
    # --- Error envelope (errors.go).
    ("status-map-wrong", E, "ModelVersionConflict: 409, IdempotencyKeyConflict: 409,", "ModelVersionConflict: 409, IdempotencyKeyConflict: 400,"),
    ("keys-unavailable-unmapped", E, "\tcase errors.Is(err, auth.ErrKeysUnavailable):", "\tcase errors.Is(err, auth.ErrKeysUnavailable) && false:"),
    ("invalid-token-unmapped", E, "\tcase errors.Is(err, auth.ErrInvalidToken):", "\tcase errors.Is(err, auth.ErrInvalidToken) && false:"),
    ("unknown-user-unmapped", E, "\tcase errors.Is(err, tenant.ErrUnknownUser):", "\tcase errors.Is(err, tenant.ErrUnknownUser) && false:"),
    ("no-membership-unmapped", E, "\tcase errors.Is(err, tenant.ErrNoMembership):", "\tcase errors.Is(err, tenant.ErrNoMembership) && false:"),
    ("no-membership-as-403", E, "\t\treturn Errorf(NotFound, \"not found\"), true", "\t\treturn Errorf(Forbidden, \"not found\"), true"),
    ("forbidden-unmapped", E, "\tcase errors.Is(err, tenant.ErrForbidden):", "\tcase errors.Is(err, tenant.ErrForbidden) && false:"),
    ("retryable-unmapped", E, CAT, "\tcase errors.Is(err, tenant.ErrOutcomeUnknown):"),
    ("outcome-unknown-unmapped", E, CAT, "\tcase errors.Is(err, tenant.ErrRetryable):"),
    ("canceled-unmapped", E, CANCEL, "\tcase errors.Is(err, context.DeadlineExceeded):"),
    ("deadline-unmapped", E, CANCEL, "\tcase errors.Is(err, context.Canceled):"),
    ("internal-message-leaks", E, "\treturn Errorf(InternalError, \"internal error\"), false", "\treturn Errorf(InternalError, err.Error()), false"),
    ("internal-not-logged", E, "\tif !known {\n", "\tif !known && false {\n"),
    ("log-without-request-id", E, "\"request failed\", \"request_id\", id, \"error\", err.Error()", "\"request failed\", \"error\", err.Error()"),
    ("log-without-cause", E, "\"request failed\", \"request_id\", id, \"error\", err.Error()", "\"request failed\", \"request_id\", id"),
    ("unknown-code-not-caught", E, "\tif !ok {\n\t\te, status = Errorf", "\tif !ok && false {\n\t\te, status = Errorf"),
    ("challenge-dropped", E, "\t\tif c := auth.Challenge(err); c != \"\" {", "\t\tif c := auth.Challenge(err); false && c != \"\" {"),
    ("challenge-on-every-error", E, "\tif e.Code == Unauthenticated {", "\tif true {"),
    ("details-dropped", E, "Details: e.Details, RequestID: id}", "RequestID: id}"),
    ("request-id-not-in-body", E, "Message: e.Message, Details: e.Details, RequestID: id}", "Message: e.Message, Details: e.Details, RequestID: \"\"}"),
    ("marshal-failure-keeps-status", E, "\t\tstatus = 500\n", ""),
    ("content-type-dropped", E, "\tw.Header().Set(\"Content-Type\", \"application/json\")\n", ""),
    ("nosniff-dropped", E, "\tw.Header().Set(\"X-Content-Type-Options\", \"nosniff\")\n", ""),
    ("no-store-dropped", E, "\tw.Header().Set(\"Cache-Control\", \"no-store\")\n", ""),
    # --- Request IDs, recovery, bodies (request.go).
    ("client-request-id-used", R, "\t\tid := ids.New(\"req\")\n", "\t\tid := r.Header.Get(\"X-Request-ID\")\n\t\tif id == \"\" {\n\t\t\tid = ids.New(\"req\")\n\t\t}\n"),
    ("request-id-header-dropped", R, "\t\tw.Header().Set(\"X-Request-ID\", id)\n", ""),
    ("request-id-not-in-context", R, "\t\tr = r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id))\n", ""),
    ("panic-not-recovered", R, "\t\t\tif v := recover(); v != nil {", "\t\t\tif v := any(nil); v != nil {"),
    ("abort-swallowed", R, "\t\t\t\tif v == http.ErrAbortHandler {\n\t\t\t\t\tpanic(v)\n\t\t\t\t}\n", ""),
    ("panic-not-logged", R, "\t\t\t\tlog.ErrorContext(r.Context(), \"handler panicked\", \"request_id\", id, \"panic\", fmt.Sprint(v))\n", "\t\t\t\t_ = fmt.Sprint\n"),
    ("returned-error-dropped", R, "\t\tif err := next(w, r); err != nil {\n\t\t\tWriteError(w, r, log, err)\n\t\t}", "\t\t_ = next(w, r)"),
    ("media-type-unchecked", R, OPEN, OPEN.replace("media != \"application/json\"", "(media != \"application/json\" && false)")),
    ("media-params-unchecked", R, OPEN, OPEN.replace("!utf8Charset(params)", "(!utf8Charset(params) && false)")),
    ("media-parse-error-ignored", R, OPEN, "\tif media != \"application/json\" || !utf8Charset(params) {"),
    ("charset-name-unchecked", R, CHARSET, CHARSET.replace("k != \"charset\"", "(k != \"charset\" && false)")),
    ("charset-value-unchecked", R, CHARSET, CHARSET.replace("!strings.EqualFold(v, \"utf-8\")", "(!strings.EqualFold(v, \"utf-8\") && false)")),
    ("charset-case-sensitive", R, CHARSET, CHARSET.replace("!strings.EqualFold(v, \"utf-8\")", "v != strings.ToLower(\"utf-8\")")),
    ("body-unbounded", R, "io.ReadAll(http.MaxBytesReader(nil, r.Body, max))", "io.ReadAll(r.Body)"),
    ("body-limit-off-by-one", R, "io.ReadAll(http.MaxBytesReader(nil, r.Body, max))", "io.ReadAll(http.MaxBytesReader(nil, r.Body, max+1))"),
    ("too-large-as-400", R, "\tif errors.As(err, &tooLarge) {", "\tif errors.As(err, &tooLarge) && false {"),
    ("read-error-ignored", R, "\tif err != nil {\n\t\treturn nil, Errorf(InvalidRequest, \"the request body could not be read\")", "\tif false {\n\t\treturn nil, Errorf(InvalidRequest, \"the request body could not be read\")"),
    ("validator-error-ignored", R, "\tif err != nil {\n\t\treturn nil, err\n\t}\n\tif len(issues) > 0 {", "\tif false {\n\t\treturn nil, err\n\t}\n\tif len(issues) > 0 {"),
    ("json-invalid-as-422", R, "\t\tif issues[0].Code == validate.JSONInvalid {", "\t\tif false {"),
    ("json-invalid-detail-dropped", R, ",\n\t\t\t\tDetails: []Detail{{Path: issues[0].Path, Reason: issues[0].Message}}}", "}"),
    ("issues-ignored", R, "\tif len(issues) > 0 {", "\tif false {"),
    ("only-first-issue", R, "\t\tfor _, is := range issues {", "\t\tfor _, is := range issues[:1] {"),
    ("severity-dropped", R, ", Severity: string(is.Severity)})", "})"),
    ("decode-error-ignored", R, "\tif err := json.Unmarshal(data, dst); err != nil {", "\tif err := json.Unmarshal(data, dst); false && err != nil {"),
    ("raw-body-not-returned", R, "\treturn data, nil\n}", "\treturn nil, nil\n}"),
    # --- Scope middleware (scope.go).
    ("bearer-error-ignored", S, "\t\ttoken, err := auth.BearerToken(r)\n\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n", "\t\ttoken, _ := auth.BearerToken(r)\n"),
    ("verify-error-ignored", S, "\t\tid, err := v.Verify(r.Context(), token)\n\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n", "\t\tid, err := v.Verify(r.Context(), token)\n\t\t_ = err\n"),
    ("selector-count-unchecked", S, SEL, "\t\tif len(values) < 1 || !orgID.MatchString(values[0]) {"),
    ("selector-format-unchecked", S, SEL, "\t\tif len(values) != 1 || values[0] == \"\" {"),
    ("selector-regex-loose", S, "`^org_[0-7][0-9A-HJKMNP-TV-Z]{25}$`", "`^org_[0-9A-Z]{26}$`"),
    ("selector-regex-open-end", S, "`^org_[0-7][0-9A-HJKMNP-TV-Z]{25}$`", "`^org_[0-7][0-9A-HJKMNP-TV-Z]{25}`"),
    ("selector-regex-open-start", S, "`^org_[0-7][0-9A-HJKMNP-TV-Z]{25}$`", "`org_[0-7][0-9A-HJKMNP-TV-Z]{25}$`"),
    ("resolve-error-ignored", S, "\t\tuser, err := users.ResolveUser(r.Context(), id.Issuer, id.Subject)\n\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n", "\t\tuser, _ := users.ResolveUser(r.Context(), id.Issuer, id.Subject)\n"),
    ("identity-not-in-context", S, "\t\tctx := auth.WithIdentity(r.Context(), id)\n", "\t\tctx := r.Context()\n"),
    # --- Role policy and transaction modes (tenant).
    ("require-always-passes", P, "\tif !slices.Contains(roles, m.Role) {", "\tif false && !slices.Contains(roles, m.Role) {"),
    ("writers-include-viewer", P, "\tWriters = []Role{Owner, Editor}         // creates and edits", "\tWriters = []Role{Owner, Editor, Viewer} // creates and edits"),
    ("members-exclude-viewer", P, "\tMembers = []Role{Owner, Editor, Viewer} // any member: reads", "\tMembers = []Role{Owner, Editor}         // any member: reads"),
    ("read-only-is-read-write", TX, "\tReadOnly  = TxOptions{AccessMode: pgx.ReadOnly}", "\tReadOnly  = TxOptions{}"),
    ("read-write-is-read-only", TX, "\tReadWrite = TxOptions{}", "\tReadWrite = TxOptions{AccessMode: pgx.ReadOnly}"),
    # --- Project service (project.go).
    ("create-role-unchecked", PJ, REQUIRE_WRITERS, ""),
    ("replay-before-role-check", PJ, REQUIRE_WRITERS + CLAIM_REPLAY, CLAIM_REPLAY + REQUIRE_WRITERS),
    ("get-requires-writer", PJ, MEMBERS_GET, MEMBERS_GET.replace("readRoles", "tenant.Writers")),
    ("list-requires-writer", PJ, MEMBERS_LIST, MEMBERS_LIST.replace("readRoles", "tenant.Writers")),
    ("hash-of-raw-body", PJ, "\thash := hashing.Sum(canonical)", "\thash := hashing.Sum(body)\n\t_ = canonical"),
    ("replay-not-canonical", PJ, "\t\t\tout, err = hashing.Canonical(replay.Body)", "\t\t\tout, err = replay.Body, nil"),
    ("replay-status-not-stored", PJ, "\t\t\tstatus = replay.Status", "\t\t\tstatus = 200"),
    ("audit-skipped", PJ, "\t\tif err := auditlog.AppendUser(ctx, tx, m.OrganizationID, m.UserID, auditlog.Event{\n\t\t\tAction: \"project.create\", TargetType: \"project\", TargetID: p.ID, RequestID: requestID}); err != nil {\n\t\t\treturn err\n\t\t}\n", "\t\t_ = auditlog.AppendUser\n"),
    ("audit-request-id-missing", PJ, "RequestID: requestID})", "RequestID: \"\"})"),
    ("audit-action-wrong", PJ, "Action: \"project.create\"", "Action: \"project.created\""),
    ("audit-target-wrong", PJ, "TargetID: p.ID, ", "TargetID: m.UserID, "),
    ("response-not-canonical", PJ, "\t\tif out, err = hashing.Canonical(data); err != nil {", "\t\tif out, err = data, error(nil); err != nil {"),
    ("create-status-200", PJ, "\t\tstatus = 201\n", "\t\tstatus = 200\n"),
    ("complete-skipped", PJ, "\t\treturn idempotency.Complete(ctx, tx, k, idempotency.Response{Status: status, Body: out})", "\t\treturn nil"),
    ("list-no-lookahead", PJ, "projects.List(ctx, tx, m.OrganizationID, after, limit+1)", "projects.List(ctx, tx, m.OrganizationID, after, limit)"),
    ("cursor-after-wrong-item", PJ, "\t\t\tlast := rows[limit-1]", "\t\t\tlast := rows[limit]"),
    ("page-not-truncated", PJ, "\t\t\trows = rows[:limit]\n", ""),
    ("created-at-not-utc", PJ, "CreatedAt: p.CreatedAt.UTC().Format(time.RFC3339Nano)", "CreatedAt: p.CreatedAt.Format(time.RFC3339Nano)"),
    ("cursor-time-seconds", PJ, "After: pos.CreatedAt.UTC().Format(time.RFC3339Nano)", "After: pos.CreatedAt.UTC().Format(time.RFC3339)"),
    ("cursor-not-strict", PJ, "base64.RawURLEncoding.Strict().DecodeString(s)", "base64.RawURLEncoding.DecodeString(s)"),
    ("cursor-unknown-members", PJ, "json.Unmarshal(data, &c, json.RejectUnknownMembers(true))", "json.Unmarshal(data, &c)"),
    ("cursor-time-error-ignored", PJ, CURSOR_CHECK, CURSOR_CHECK.replace("err != nil || ", "")),
    ("cursor-version-unchecked", PJ, CURSOR_CHECK, CURSOR_CHECK.replace("c.Version != 1 || ", "")),
    ("cursor-org-unchecked", PJ, CURSOR_CHECK, CURSOR_CHECK.replace("c.Org != org || ", "")),
    ("cursor-list-unchecked", PJ, CURSOR_CHECK, CURSOR_CHECK.replace("c.List != cursorList || ", "")),
    ("cursor-id-unchecked", PJ, CURSOR_CHECK, CURSOR_CHECK.replace(" || !ValidID(c.ID)", "")),
    ("project-id-loose", PJ, "`^prj_[0-7][0-9A-HJKMNP-TV-Z]{25}$`", "`^prj_[0-9A-Z]{26}$`"),
    # --- Project handlers (http.go).
    ("route-role-wrong", PH, "{\"POST\", \"/v1/projects\", \"createProject\", createRoles},", "{\"POST\", \"/v1/projects\", \"createProject\", readRoles},"),
    ("route-path-wrong", PH, "\"/v1/projects/{id}\"", "\"/v1/project/{id}\""),
    ("conflict-unmapped", PH, CONFLICT, "\tif errors.Is(err, idempotency.ErrInProgress) {"),
    # Equivalent: Claim and Complete share one transaction, so a committed
    # key always has its response and ErrInProgress cannot be returned.
    ("in-progress-unmapped", PH, CONFLICT, "\tif errors.Is(err, idempotency.ErrConflict) {", "equivalent"),
    ("key-required-unchecked", PH, "\tif len(values) == 0 {\n\t\treturn \"\", httpapi.Errorf(httpapi.IdempotencyKeyRequired", "\tif false {\n\t\treturn \"\", httpapi.Errorf(httpapi.IdempotencyKeyRequired"),
    ("key-multiple-allowed", PH, KEY, KEY.replace("len(values) > 1 || ", "")),
    ("key-min-off-by-one", PH, KEY, KEY.replace("len(k) < 8", "len(k) < 7")),
    ("key-min-too-high", PH, KEY, KEY.replace("len(k) < 8", "len(k) < 9")),
    ("key-max-off-by-one", PH, KEY, KEY.replace("len(k) > 128", "len(k) > 129")),
    ("key-max-too-low", PH, KEY, KEY.replace("len(k) > 128", "len(k) > 127")),
    ("key-space-allowed", PH, KEY, KEY.replace("c < 0x21", "c < 0x20")),
    ("key-non-ascii-allowed", PH, KEY, KEY.replace(" || c > 0x7e", "")),
    ("key-tilde-refused", PH, KEY, KEY.replace("c > 0x7e", "c > 0x7d")),
    ("limit-default", PH, "\tlimit := 50\n", "\tlimit := 51\n"),
    ("limit-multiple-allowed", PH, LIMIT, LIMIT.replace("len(vs) != 1 || ", "")),
    ("limit-zero-allowed", PH, LIMIT, LIMIT.replace("n < 1", "n < 0")),
    ("limit-max-off-by-one", PH, LIMIT, LIMIT.replace("n > 100", "n > 101")),
    ("limit-max-too-low", PH, LIMIT, LIMIT.replace("n > 100", "n > 99")),
    ("limit-not-canonical", PH, LIMIT, LIMIT.replace(" || strconv.Itoa(n) != vs[0]", "")),
    # Equivalent: Atoi's error result is 0 or a bound (n < 1 or n > 100)
    # and never matches strconv.Itoa(n), so the err test is redundant.
    ("limit-error-ignored", PH, LIMIT, LIMIT.replace("err != nil || ", "(err != nil && false) || "), "equivalent"),
    ("limit-not-applied", PH, "\t\tlimit = n\n", ""),
    ("cursor-empty-allowed", PH, CURSOR_Q, CURSOR_Q.replace(" || vs[0] == \"\"", "")),
    ("cursor-multiple-allowed", PH, CURSOR_Q, CURSOR_Q.replace("len(vs) != 1 || ", "")),
    # Review M1: not equivalent; JSON whitespace pads a valid cursor past 2048.
    ("cursor-length-unbounded", PH, CURSOR_Q, CURSOR_Q.replace(" || len(vs[0]) > 2048", "")),
    ("cursor-not-passed", PH, "\t\tcursor = vs[0]\n", ""),
    ("invalid-cursor-unmapped", PH, "\tif errors.Is(err, ErrInvalidCursor) {", "\tif false {"),
    ("get-id-unchecked", PH, "\tif !ValidID(id) {", "\tif false {"),
    ("not-found-unmapped", PH, "\tif errors.Is(err, ErrNotFound) {", "\tif false {"),
    ("catch-all-missing", API, "\tmux.Handle(\"/\", notFound)\n", "\t_ = notFound\n"),
    # --- Idempotency store.
    ("claim-always-new", I, "\tif tag.RowsAffected() == 1 {\n\t\treturn nil, nil", "\tif tag.RowsAffected() >= 0 {\n\t\treturn nil, nil"),
    ("conflict-ignored", I, "\tcase stored != hash:\n\t\treturn nil, ErrConflict", "\tcase false:\n\t\treturn nil, ErrConflict"),
    # Equivalent: see in-progress-unmapped; an incomplete key is never
    # visible to another transaction.
    ("in-progress-unchecked", I, "\tcase status == nil:\n\t\treturn nil, ErrInProgress", "\tcase false:\n\t\treturn nil, ErrInProgress", "equivalent"),
    ("expiry-ignored", I, "\tif expired {\n", "\tif false {\n"),
    ("ttl-shorter", I, "const TTL = 24 * time.Hour", "const TTL = 23 * time.Hour"),
    ("insert-ttl-wrong", I, "now()+make_interval(secs => $6)) ON CONFLICT DO NOTHING", "now()+make_interval(secs => $6/2)) ON CONFLICT DO NOTHING"),
    ("reclaim-ttl-wrong", I, "created_at=now(), expires_at=now()+make_interval(secs => $6)", "created_at=now(), expires_at=now()+make_interval(secs => $6/2)"),
    ("reclaim-keeps-created-at", I, "completed_at=NULL, created_at=now(), expires_at", "completed_at=NULL, expires_at"),
    ("reclaim-keeps-hash", I, "SET request_hash=$5, response_status=NULL", "SET request_hash=request_hash||left($5::text,0), response_status=NULL"),
    # Review M1: not equivalent; retries racing on an expired key all
    # re-claim it without the lock (TestExpiredKeyRace).
    ("select-not-locked", I, " AND key=$4 FOR UPDATE`", " AND key=$4`"),
    ("complete-status-wrong", I, "k.Org, k.Actor, k.Operation, k.Key, r.Status, string(r.Body))", "k.Org, k.Actor, k.Operation, k.Key, 200, string(r.Body))"),
    # Equivalent: Complete always follows its own Claim in one transaction,
    # so the row is claimed and incomplete; the guard reports misuse.
    ("complete-rows-unchecked", I, "\tif tag.RowsAffected() != 1 {\n\t\treturn errors.New(\"idempotency: completing", "\tif tag.RowsAffected() != 1 && false {\n\t\treturn errors.New(\"idempotency: completing", "equivalent"),
    # --- Project storage.
    ("get-org-filter-dropped", PS, "FROM twin.projects WHERE organization_id=$1 AND id=$2`, org, id)", "FROM twin.projects WHERE $1<>'' AND id=$2`, org, id)"),
    ("list-org-filter-dropped", PS, "FROM twin.projects WHERE organization_id=$1\n\t\t\tORDER BY created_at, id LIMIT $2`", "FROM twin.projects WHERE $1<>''\n\t\t\tORDER BY created_at, id LIMIT $2`"),
    ("list-after-org-filter-dropped", PS, "FROM twin.projects WHERE organization_id=$1\n\t\t\tAND (created_at, id)", "FROM twin.projects WHERE $1<>''\n\t\t\tAND (created_at, id)"),
    ("list-newest-first", PS, "ORDER BY created_at, id LIMIT $2`", "ORDER BY created_at DESC, id DESC LIMIT $2`"),
    ("list-after-newest-first", PS, "ORDER BY created_at, id LIMIT $4`", "ORDER BY created_at DESC, id DESC LIMIT $4`"),
    ("keyset-inclusive", PS, KEYSET, "AND (created_at, id) >= ($2, $3)"),
    ("keyset-time-only", PS, KEYSET, "AND created_at > $2 AND $3::text<>''"),
    ("list-limit-ignored", PS, "ORDER BY created_at, id LIMIT $2`, org, limit)", "ORDER BY created_at, id LIMIT $2::int+100`, org, limit)"),
    # --- Audit append.
    ("audit-actor-type", AL, "VALUES ($1,$2,'user',$3,", "VALUES ($1,$2,'system',$3,"),
    ("audit-request-id-dropped", AL, "e.TargetID, e.RequestID, string(data))", "e.TargetID, nil, string(data))"),
    # --- D-001 IDs.
    ("alphabet-wrong", ID, "\"0123456789ABCDEFGHJKMNPQRSTVWXYZ\"", "\"0123456789ABCDEFGHIJKMNPQRSTVWXY\""),
    ("time-shift-wrong", ID, "ms<<16", "ms<<15"),
    ("entropy-offset", ID, "copy(b[6:], entropy[:])", "copy(b[7:], entropy[:])"),
    ("carry-wrong", ID, "lo = lo>>5 | hi<<59", "lo = lo>>5 | hi<<58"),
    ("no-entropy", ID, "\t_, _ = rand.Read(entropy[:]) // crypto/rand.Read never fails (Go 1.24)\n", "\t_ = rand.Read\n"),
    ("wrong-time", ID, "ULID(time.Now())", "ULID(time.Unix(0, 0))"),
    ("separator-wrong", ID, "prefix + \"_\" + ULID", "prefix + \"-\" + ULID"),
    # --- Listener configuration.
    ("listen-default", CS, "\t\taddr = \":8080\"", "\t\taddr = \":8081\""),
    ("port-negative-allowed", CS, PORT, PORT.replace("n < 0 || ", "")),
    ("port-max-off-by-one", CS, PORT, PORT.replace("n > 65535", "n > 65536")),
    ("port-zero-refused", CS, PORT, PORT.replace("n < 0", "n < 1")),
    ("port-parse-error-ignored", CS, PORT, PORT.replace("perr != nil || ", "(perr != nil && false) || ")),
    # Equivalent: on error net.SplitHostPort returns an empty port, which
    # Atoi refuses.
    ("split-error-ignored", CS, PORT, "\tif (err != nil && false) || perr != nil || n < 0 || n > 65535 {", "equivalent"),
    # --- API command.
    ("runtime-role-unchecked", M, "\terr = tenant.CheckRuntimeRole(checkCtx, pool, tenant.APIRole)", "\terr = nil\n\t_, _ = checkCtx, tenant.CheckRuntimeRole"),
    ("server-config-error-ignored", M, "\tsrvCfg, srvErr := config.LoadServer(getenv)\n", "\tsrvCfg, srvErr := config.LoadServer(getenv)\n\tsrvErr = nil\n"),
    ("shutdown-exit-nonzero", M, "\tlog.Info(\"stopped\")\n\treturn 0", "\tlog.Info(\"stopped\")\n\treturn exitServe"),
    # Review M1: not equivalent; TestServerSettings checks the guard is installed.
    ("guard-not-installed", M, "\tcfg.AfterRelease = tenant.Guard\n", ""),
    # --- Review round 1 fixes.
    ("read-header-timeout-dropped", M, "\t\tReadHeaderTimeout: 10 * time.Second,\n", ""),
    ("read-timeout-dropped", M, "\t\tReadTimeout:       30 * time.Second,\n", ""),
    ("write-timeout-dropped", M, "\t\tWriteTimeout:      60 * time.Second,\n", ""),
    ("idle-timeout-dropped", M, "\t\tIdleTimeout:       120 * time.Second,\n", ""),
    ("header-limit-dropped", M, "\t\tMaxHeaderBytes:    32 << 10,\n", ""),
    ("server-error-log-dropped", M, "\t\tErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),\n", ""),
    ("shutdown-grace-zero", M, "const shutdownGrace = 20 * time.Second", "const shutdownGrace = 0"),
    ("unsafe-role-reported-as-unavailable", M, "\tif errors.Is(err, tenant.ErrUnsafeRole) {", "\tif false {"),
    ("unavailable-reported-as-refusal", M, "\t\tlog.Error(\"database unavailable\", \"error\", err.Error())", "\t\tlog.Error(\"refusing the database login\", \"error\", err.Error())"),
    ("membership-not-locked", TX, "\t\t\tq += ` FOR SHARE`\n", ""),
    ("membership-key-share-only", TX, "q += ` FOR SHARE`", "q += ` FOR KEY SHARE`"),
    ("membership-locked-when-read-only", TX, "\t\tif opts.AccessMode != pgx.ReadOnly {\n\t\t\tq += ` FOR SHARE`", "\t\tif true {\n\t\t\tq += ` FOR SHARE`"),
    ("retryable-alias-wrong", TX, "\tErrRetryable      = postgres.ErrRetryable\n", "\tErrRetryable      = errors.New(\"x\")\n"),
    ("nul-unchecked", R, "\tif path, ok := nulString(data); ok {", "\tif path, ok := nulString(data); ok && false {"),
    ("nul-prefilter-wrong", R, "bytes.Contains(data, []byte(`\\u0000`))", "bytes.Contains(data, []byte(`\\u0001`))"),
    ("nul-in-name-not-parent", R, "\t\t\tp = p.Parent() // a member name\n", ""),
    ("nul-ignored-when-deep", R, "\t\tif tok.Kind() != '\"' || !strings.ContainsRune(tok.String(), 0) {", "\t\tif tok.Kind() != '\"' || !strings.ContainsRune(tok.String(), 0) || dec.StackDepth() > 9 {"),
    ("query-errors-ignored", R, "\tq, err := url.ParseQuery(r.URL.RawQuery)\n\tif err != nil {", "\tq, err := url.ParseQuery(r.URL.RawQuery)\n\tif err != nil && false {"),
    ("list-query-not-strict", PH, "\tq, err := httpapi.Query(r)\n\tif err != nil {\n\t\treturn err\n\t}\n", "\tq := r.URL.Query()\n"),
    ("create-roles-members", PJ, "\tcreateRoles = tenant.Writers\n", "\tcreateRoles = tenant.Members\n"),
    ("service-create-roles-bypassed", PJ, "\t\tif err := m.Require(createRoles...); err != nil {", "\t\tif err := m.Require(tenant.Members...); err != nil {"),
    ("head-served", API, "\t\tif r.Method == http.MethodHead || r.URL.Path != path.Clean(r.URL.Path) {", "\t\tif r.URL.Path != path.Clean(r.URL.Path) {"),
    ("unclean-paths-served", API, "\t\tif r.Method == http.MethodHead || r.URL.Path != path.Clean(r.URL.Path) {", "\t\tif r.Method == http.MethodHead {\n\t\t\t_ = path.Clean"),
    ("keyset-first-page-without-id", PS, "ORDER BY created_at, id LIMIT $2`", "ORDER BY created_at LIMIT $2`"),
    ("keyset-later-pages-without-id", PS, "ORDER BY created_at, id LIMIT $4`", "ORDER BY created_at LIMIT $4`"),
    ("db-handles-rule-dropped", AR, DB_HANDLES_RULE, "", "checker"),
    ("db-handles-through-dropped", AR, "\t\tforbid:  []string{\"./internal/storage/postgres/...\"},\n\t\tthrough: []string{\"./internal/storage/...\", \"./internal/tenant/...\"},", "\t\tforbid:  []string{\"./internal/storage/postgres/...\"},", "checker"),
    # --- Architecture checker: the DB rule's gateways (checker tests).
    ("through-ignored", AC, "\t\tif p == nil || (cur != root && matchAny(r.through, cur)) {", "\t\tif p == nil {", "checker"),
    ("through-tenant-missing", AR, THROUGH, THROUGH.replace(", \"./internal/tenant/...\"}", "}"), "checker"),
    ("through-storage-missing", AR, THROUGH, THROUGH.replace("\"./internal/storage/...\", ", ""), "checker"),
    ("httpapi-not-app-layer", AR, "\t\"./internal/httpapi/...\", // HTTP transport (T-009, ADR-019)\n", "", "checker"),
    ("api-not-app-layer", AR, "\t\"./internal/api/...\",     // route assembly\n", "", "checker"),
    ("dbtest-not-test-infra", AR, "\t\"./internal/storage/dbtest/...\", // provisions", "\t// \"./internal/storage/dbtest/...\", // provisions", "checker"),
]


def main() -> int:
    if CHECK:
        bad = [(n, (REPO / f).read_text().count(o)) for n, f, o, *_ in MUT if (REPO / f).read_text().count(o) != 1]
        for n, c in bad:
            print(f"{n}: pattern found {c} times")
        names = [m[0] for m in MUT]
        dups = sorted({n for n in names if names.count(n) > 1})
        for n in dups:
            print(f"{n}: name used twice")
        print(f"{len(MUT) - len(bad)}/{len(MUT)} patterns occur exactly once")
        return 1 if bad or dups else 0
    if not os.environ.get("GEMINUS_TEST_DATABASE_URL"):
        print("set GEMINUS_TEST_DATABASE_URL to the disposable database of `make db-test`", file=sys.stderr)
        return 2
    env = dict(os.environ, GEMINUS_REQUIRE_DB_TESTS="1")
    results = []
    with tempfile.TemporaryDirectory() as tmp:
        for i, (name, f, old, new, *flags) in enumerate(MUT):
            if ONLY and name not in ONLY:
                continue
            src = (REPO / f).read_text()
            if src.count(old) != 1:
                results.append((name, f"BAD-MUTANT (pattern found {src.count(old)} times)"))
                print("\t".join(results[-1]), flush=True)
                continue
            m = pathlib.Path(tmp) / f"m{i}.go"
            m.write_text(src.replace(old, new))
            ov = pathlib.Path(tmp) / f"m{i}.json"
            ov.write_text(json.dumps({"Replace": {str(REPO / f): str(m)}}))
            pkgs = CHECKER if "checker" in flags else PKGS
            p = subprocess.run(["go", "test", "-p", "1", "-count=1", "-timeout", "600s", "-overlay", str(ov), *pkgs],
                               cwd=REPO, capture_output=True, text=True, env=env)
            o = p.stdout + p.stderr
            if p.returncode == 0:
                status = "SURVIVED"
            elif "build failed" in o or "setup failed" in o or "[build failed]" in o:
                status = "BUILD-FAILED"
            else:
                status = "KILLED"
            first = next((l.strip() for l in o.splitlines() if l.strip().startswith(("--- FAIL", "panic:"))), "")
            if "equivalent" in flags:
                status = "EQUIVALENT" if status == "SURVIVED" else f"UNEXPECTED-{status}"
            results.append((name, f"{status} {first[:100]}"))
            print("\t".join(results[-1]), flush=True)
    killed = sum(1 for r in results if r[1].startswith("KILLED"))
    equivalent = sum(1 for r in results if r[1].startswith("EQUIVALENT"))
    print(f"{killed}/{len(results) - equivalent} killed, {equivalent} equivalent (documented above)")
    return 0 if killed + equivalent == len(results) else 1


if __name__ == "__main__":
    sys.exit(main())
