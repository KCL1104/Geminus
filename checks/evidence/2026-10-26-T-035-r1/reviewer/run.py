"""Reviewer mutants for T-035, applied only through `go test -overlay`
(the worktree is never modified). Usage: run.py <worktree> <scratch> <log>"""
import json
import os
import subprocess
import sys

root, scratch, log = sys.argv[1], sys.argv[2], sys.argv[3]
GO = "/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin/go"

O = "internal/observability/redact.go"
H = "internal/observability/handler.go"
A = "internal/audit/audit.go"
X = "internal/httpapi/access.go"
S = "internal/httpapi/scope.go"
W = "internal/worker/worker.go"
T = "internal/observability/telemetry/telemetry.go"
G = "internal/config/telemetry.go"

OP = ["./internal/observability/", "./cmd/api/", "./cmd/worker/"]
AP = ["./internal/audit/", "./internal/storage/auditlog/", "./internal/worker/", "./internal/proposals/generator/"]
XP = ["./internal/httpapi/", "./cmd/api/"]
WP = ["./internal/worker/"]
TP = ["./internal/observability/telemetry/...", "./internal/httpapi/", "./internal/proposals/generator/", "./internal/runs/runner/"]
GP = ["./internal/config/", "./internal/observability/telemetry/export/", "./cmd/api/", "./cmd/worker/"]

mutants = [
    ("access-status-last-write", X, [("\tif w.status == 0 {\n\t\tw.status = code\n\t}\n\tw.ResponseWriter.WriteHeader(code)",
                                       "\tw.status = code\n\tw.ResponseWriter.WriteHeader(code)")], XP),
    ("access-route-before-serve", X, [("\t\trec := &recorder{ResponseWriter: w}\n", "\t\trec := &recorder{ResponseWriter: w}\n\t\tpat := r.Pattern\n"),
                                        ("slog.String(\"route\", route(r.Pattern))", "slog.String(\"route\", route(pat))"),
                                        ("tel.EndRequest(ctx, span, method(r.Method), route(r.Pattern), status, took, id)",
                                         "tel.EndRequest(ctx, span, method(r.Method), route(pat), status, took, id)")], XP),
    ("with-keys-not-tracked", H, [("\t\tkeys[a.Key] = true\n\t\tfiltered[i] = filter(a)", "\t\tfiltered[i] = filter(a)")], OP),
    ("group-under-text-kept", O, [("\t\tcase slog.KindAny:\n\t\t\tif err, ok := v.Any().(error); ok {",
                                     "\t\tcase slog.KindGroup:\n\t\t\treturn v\n\t\tcase slog.KindAny:\n\t\t\tif err, ok := v.Any().(error); ok {")], OP),
    ("gotype-may-be-a-credential", O, [("return goTypeShape.MatchString(s) && !Sensitive(s)", "return goTypeShape.MatchString(s)")], OP),
    ("invalid-utf8-kept", O, [("\ts = strings.ToValidUTF8(s, \"�\")\n", "")], OP),
    ("pointer-bound-is-text", O, [("\t\tmax = MaxPointer\n", "\t\tmax = MaxText\n")], OP),
    ("system-actor-unchecked", A, [("case e.ActorType != User && !ValidActor(e.ActorID):", "case e.ActorType == Worker && !ValidActor(e.ActorID):")], AP),
    ("field-name-from-data-in-error", A, [("\t\tif !keyRe.MatchString(k) {\n\t\t\tname = \"<not a field name>\"\n\t\t}\n", "")], AP),
    ("action-name-from-data-in-error", A, [("\t\tif !actionRe.MatchString(action) {\n\t\t\taction = \"<not an action name>\"\n\t\t}\n", "")], AP),
    ("empty-label-in-list", A, [("if s := r.Index(i).String(); s == \"\" || !isLabel(s) {", "if s := r.Index(i).String(); s != \"\" && !isLabel(s) {")], AP),
    ("count-accepts-float", A, [("\t\tcase reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:\n\t\t\treturn \"\"",
                                   "\t\tcase reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float64:\n\t\t\treturn \"\"")], AP),
    ("endpoint-with-fragment", G, [("u.Fragment == \"\" && !strings.Contains(raw, \"#\") && ", "")], GP),
    ("endpoint-force-query", G, [("\t\t!u.ForceQuery && ", "\t\t")], GP),
    ("endpoint-opaque", G, [(" && u.Opaque == \"\"", "")], GP),
    ("sdk-disabled-case-sensitive", G, [("strings.ToLower(strings.TrimSpace(getenv(\"OTEL_SDK_DISABLED\")))", "strings.TrimSpace(getenv(\"OTEL_SDK_DISABLED\"))")], GP),
    ("protocol-grpc-silently-http", G, [("\tcase \"\", \"http/protobuf\":\n", "\tcase \"\", \"http/protobuf\", \"grpc\":\n")], GP),
    ("retryable-always-true", W, [("\"code\": f.Code, \"retryable\": f.Retryable, \"outcome\": outcome}", "\"code\": f.Code, \"retryable\": true, \"outcome\": outcome}")], WP),
    ("max-attempts-is-attempt", W, [("\"max_attempts\": j.MaxAttempts,", "\"max_attempts\": j.Attempt,")], WP),
    ("lease-lost-outcome-dropped", W, [("\t\thow = \"lease_lost\"\n\t\tlog.Warn(\"the job is no longer this attempt's; its end is not recorded\"",
                                          "\t\tlog.Warn(\"the job is no longer this attempt's; its end is not recorded\"")], WP),
    ("loop-audit-error-ignored", W, [("\t\treturn w.audit(ctx, tx, job, failure, retried)\n", "\t\t_ = w.audit(ctx, tx, job, failure, retried)\n\t\treturn nil\n")], WP),
    ("job-span-error-only-failed", T, [("\tif how != \"completed\" && how != \"cancelled\" {", "\tif how == \"failed\" {")], TP),
    ("metric-route-raw", T, [("\tt.httpDuration.Record(ctx, d.Seconds(), metric.WithAttributes(method(m), rt, status(statusCode)))",
                               "\tt.httpDuration.Record(ctx, d.Seconds(), metric.WithAttributes(method(m), attribute.String(\"http.route\", r), status(statusCode)))")], TP),
    ("scoped-user-is-subject", S, [("slog.String(\"user_id\", user))\n\t\tctx := auth.WithIdentity", "slog.String(\"user_id\", id.Subject))\n\t\tctx := auth.WithIdentity")], XP),
    ("provider-call-tokens-unfiltered-model", T, [("name(\"gen_ai.request.model\", model)", "attribute.String(\"gen_ai.request.model\", model)")], TP),
]

os.makedirs(os.path.join(scratch, "files"), exist_ok=True)
out = open(log, "w")
killed = survived = build = 0
for name, path, edits, pkgs in mutants:
    src = open(os.path.join(root, path), encoding="utf-8").read()
    ok = True
    for old, new in edits:
        if src.count(old) != 1:
            out.write(f"{name}: NOT APPLIED ({src.count(old)} matches of {old[:60]!r})\n")
            ok = False
            break
        src = src.replace(old, new)
    if not ok:
        continue
    mf = os.path.join(scratch, "files", name + "-" + os.path.basename(path))
    open(mf, "w", encoding="utf-8").write(src)
    ov = os.path.join(scratch, "files", name + ".json")
    json.dump({"Replace": {os.path.join(root, path): mf}}, open(ov, "w"))
    vet = subprocess.run([GO, "vet", "-overlay", ov] + pkgs, cwd=root, capture_output=True, text=True)
    if vet.returncode != 0:
        build += 1
        out.write(f"{name}: BUILD {vet.stderr.strip().splitlines()[-1] if vet.stderr.strip() else ''}\n")
        out.flush()
        continue
    r = subprocess.run([GO, "test", "-overlay", ov, "-count=1"] + pkgs, cwd=root, capture_output=True, text=True)
    fails = [l.strip() for l in r.stdout.splitlines() if l.startswith("--- FAIL") or l.startswith("FAIL\t")]
    if r.returncode != 0:
        killed += 1
        out.write(f"{name}: KILLED {' | '.join(fails[:4])}\n")
    else:
        survived += 1
        out.write(f"{name}: SURVIVED\n")
    out.flush()
out.write(f"TOTAL {len(mutants)}: {killed} killed, {survived} survived, {build} build\n")
out.close()
