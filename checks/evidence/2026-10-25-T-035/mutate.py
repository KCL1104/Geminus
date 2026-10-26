"""Targeted mutants of T-035's audit, privacy-filter, correlation and
telemetry code.

Each mutant replaces exact snippets (each must occur once), runs the named
tests of the named packages, expects a failure, and restores the files. A
mutant that does not compile is reported as BUILD (not counted as killed):
every mutant here must build, so a test, not the compiler, has to catch it.
The "unit" set must be killed by the tests that need no database; the "db"
set by DB-backed tests, so it needs GEMINUS_TEST_DATABASE_URL (a disposable
database prepared by `make db-test`) and is refused without it. "dbbuild"
only checks, without a database, that each DB mutant applies once and
builds (go vet of its packages); its tests are NOT RUN.

Usage: mutate.py <repo root> <log path> [unit|db|dbbuild|check] [name,name...]
"""
import os
import subprocess
import sys

root, log = sys.argv[1], sys.argv[2]
which = sys.argv[3] if len(sys.argv) > 3 else 'unit'

O, H, C = 'internal/observability/redact.go', 'internal/observability/handler.go', 'internal/observability/context.go'
OP = ['./internal/observability/']
A, AP = 'internal/audit/audit.go', ['./internal/audit/']
L, LP = 'internal/storage/auditlog/auditlog.go', ['./internal/storage/auditlog/']
W, WP, WT = 'internal/worker/worker.go', ['./internal/worker/'], 'TestTheAttemptsEvent|TestNewChecksTheConfig'
X, R, E, S = 'internal/httpapi/access.go', 'internal/httpapi/request.go', 'internal/httpapi/errors.go', 'internal/httpapi/scope.go'
XP, XT = ['./internal/httpapi/'], 'TestRecover|TestRequestID|TestScoped|TestSelf|TestWriteError|TestAccessLog|TestAccessLogTelemetry|TestAccessLogKeepsTheWriter|TestAccessLogAndAbort|TestSelfAnnotatesTheUser'
T, TP = 'internal/observability/telemetry/telemetry.go', ['./internal/observability/telemetry/']
G, GP = 'internal/config/telemetry.go', ['./internal/config/']
EX, EXP = 'internal/observability/telemetry/export/export.go', ['./internal/observability/telemetry/export/']
P, PP, PT = 'internal/proposals/generator/generator.go', ['./internal/proposals/generator/'], 'TestAuditMeta|TestTheCallsLine'
N, NP = 'internal/runs/runner/runner.go', ['./internal/runs/runner/']
ALL = 'Test.*'


def m(name, path, old, new, pkgs, run):
    return (name, [(path, old, new)], pkgs, run)


unit = [
    # --- the log filter: default deny (D-094 (a)) ---
    m('unregistered-key-kept', O, '\tif !ok {\n\t\treturn slog.String(key, Redacted)\n\t}', '\tif !ok {\n\t\treturn slog.Attr{Key: key, Value: a.Value.Resolve()}\n\t}', OP, ALL),
    m('invalid-key-name-kept', O, '\tif !keyShape.MatchString(key) {\n\t\tkey, ok = InvalidKey, false\n\t}', '\tif !keyShape.MatchString(key) {\n\t\tok = false\n\t}', OP, ALL),
    # --- shapes (D-094 (b)) ---
    m('wrong-shape-kept', O, 'if v.Kind() != slog.KindString || (v.String() != "" && !fits(kind, v.String())) {', 'if v.Kind() != slog.KindString {', OP, ALL),
    m('number-takes-a-string', O, '\t\treturn slog.StringValue(Redacted)\n\tcase KindText, KindStack, KindPointer:', '\t\treturn v\n\tcase KindText, KindStack, KindPointer:', OP, ALL),
    m('name-may-be-a-credential', O, 'return nameShape.MatchString(s) && !Sensitive(s)', 'return nameShape.MatchString(s)', OP, ALL),
    m('id-of-any-shape', O, '`^[a-z]{2,4}_[0-7][0-9A-HJKMNP-TV-Z]{25}$`', '`^[a-z]{2,4}_.{1,64}$`', OP, ALL),
    m('route-with-any-path', O, '/[A-Za-z0-9_./{}$-]{0,200}|unmatched)$`', '/.{0,200}|unmatched)$`', OP, ALL),
    m('other-values-as-text', O, '\t\t\t}\n\t\t}\n\t\treturn slog.StringValue(Redacted)\n\t}\n\t// An empty', '\t\t\t}\n\t\t}\n\t\treturn slog.StringValue(v.String())\n\t}\n\t// An empty', OP, ALL),
    # --- free text (D-094 (c)) ---
    m('text-not-sanitized', O, 'return slog.StringValue(sanitize(v.String(), kind))', 'return v', OP, ALL),
    m('error-not-sanitized', O, 'return slog.StringValue(sanitize(errorString(err), kind))', 'return slog.StringValue(errorString(err))', OP, ALL),
    m('credentials-kept', O, '\tfor _, re := range credentials {\n\t\ts = re.ReplaceAllString(s, Redacted)\n\t}\n', '', OP, ALL),
    m('key-value-secrets-kept', O, '\ts = keyValue.ReplaceAllString(s, "${1}"+Redacted)\n', '', OP, ALL),
    m('credential-headers-kept', O, '\ts = headerLine.ReplaceAllString(s, "${1}${2}"+Redacted)\n', '', OP, ALL),
    m('url-userinfo-kept', O, '\ts = userinfo.ReplaceAllString(s, "${1}"+Redacted+"@")\n', '', OP, ALL),
    m('emails-kept', O, '\ts = email.ReplaceAllString(s, Redacted)\n', '', OP, ALL),
    m('long-base64-kept', O, '\t\tif base64Like(m) {\n\t\t\treturn Redacted\n\t\t}\n\t\treturn m\n', '\t\treturn m\n', OP, ALL),
    m('quoted-values-kept', O, '\ts = quoted.ReplaceAllString(s, `"`+Redacted+`"`)\n', '', OP, ALL),
    m('unterminated-quote-kept', O, '`"(?:[^"\\\\]|\\\\.)*(?:"|\\\\?$)`', '`"(?:[^"\\\\]|\\\\.)*"`', OP, ALL),
    m('bidi-controls-kept', O, 'case unicode.IsControl(r) || unicode.Is(unicode.Cf, r)', 'case unicode.IsControl(r)', OP, ALL),
    m('text-unbounded', O, '\tif utf8.RuneCountInString(s) > max {', '\tif utf8.RuneCountInString(s) > max*100 {', OP, ALL),
    # Expected to survive (equivalent): preCut bounds the patterns' work on
    # text that is cut to at most MaxStack runes afterwards anyway.
    m('no-pre-cut', O, '\tif len(s) > preCut {\n\t\ts = s[:preCut]\n\t}\n', '', OP, ALL),
    m('sensitive-misses-emails', O, '|| userinfo.MatchString(s) || email.MatchString(s) {', '|| userinfo.MatchString(s) {', OP, ALL),
    # --- panics and broken values (D-094 (d), (e)) ---
    m('panic-value-logged', O, 'return fmt.Sprintf("panic with a value of type %T", v)', 'return fmt.Sprintf("panic: %v", v)', OP + XP, ALL),
    m('runtime-error-unsanitized', O, 'return "panic: " + Text(errorString(re))', 'return "panic: " + errorString(re)', OP + XP, ALL),
    m('panicking-error-crashes', O, 'func errorString(err error) (s string) {\n\tdefer func() {\n\t\tif recover() != nil {\n\t\t\ts = Redacted\n\t\t}\n\t}()\n',
      'func errorString(err error) (s string) {\n', OP, ALL),
    # --- the handler and the context (D-093 (b)) ---
    m('message-not-sanitized', H, 'out := slog.NewRecord(r.Time, r.Level, Text(r.Message), r.PC)', 'out := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)', OP, ALL),
    m('context-attrs-unfiltered', H, '\t\tnamed[a.Key] = true\n\t\tout.AddAttrs(filter(a))\n', '\t\tnamed[a.Key] = true\n\t\tout.AddAttrs(a)\n', OP, ALL),
    m('with-attrs-unfiltered', H, 'filtered[i] = filter(a)', 'filtered[i] = a', OP, ALL),
    m('no-context-correlation', H, 'for _, a := range correlation(ctx) {', 'for _, a := range correlation(nil) {', OP, ALL),
    m('context-repeats-the-records-keys', H, 'if named[a.Key] || h.keys[a.Key] {', 'if h.keys[a.Key] {', OP, ALL),
    m('group-name-unchecked', H, '\tif !keyShape.MatchString(name) {\n\t\tname = InvalidKey\n\t}\n', '', OP, ALL),
    m('with-shares-its-backing-array', C, '\tnext := make([]slog.Attr, 0, len(prev)+len(attrs))\n\tnext = append(append(next, prev...), attrs...)\n',
      '\tnext := append(prev, attrs...)\n', OP, ALL),
    m('annotations-repeat-a-key', C, '\t\tif i := slices.IndexFunc(a.attrs, func(x slog.Attr) bool { return x.Key == attr.Key }); i >= 0 {\n\t\t\ta.attrs[i] = attr\n\t\t\tcontinue\n\t\t}\n',
      '', OP, ALL),
    m('annotations-not-copied', C, '\treturn slices.Clone(a.attrs)', '\treturn a.attrs', OP, ALL),
    # --- the audit catalogue (D-091 (c)) ---
    m('unknown-action-accepted', A, '\ts, ok := catalogue[e.Action]\n', '\ts, ok := catalogue[e.Action]\n\tif !ok {\n\t\treturn nil\n\t}\n', AP, ALL),
    m('actor-type-unchecked', A, '\tcase e.ActorType != s.actor:', '\tcase false:', AP, ALL),
    m('user-actor-unchecked', A, '\tcase e.ActorType == User && !validID("usr", e.ActorID):', '\tcase false:', AP, ALL),
    m('worker-actor-unchecked', A, '\tcase e.ActorType != User && !ValidActor(e.ActorID):', '\tcase false:', AP, ALL),
    m('target-type-unchecked', A, '\tcase !slices.Contains(s.targets, e.TargetType):', '\tcase false:', AP, ALL),
    m('target-id-of-any-kind', A, '\tcase !validID(targets[e.TargetType], e.TargetID):', '\tcase !serverID.MatchString(e.TargetID):', AP, ALL),
    m('request-id-unchecked', A, '\tcase !validID("req", e.RequestID):', '\tcase false:', AP, ALL),
    m('metadata-unbounded', A, '\tcase len(e.Metadata) > MaxFields:', '\tcase false:', AP, ALL),
    m('unknown-field-accepted', A, '\t\tif !ok {\n\t\t\treturn bad(name, "not a field of this action")\n\t\t}', '\t\tif !ok {\n\t\t\tcontinue\n\t\t}', AP, ALL),
    m('refusal-names-the-value', A, '\t\tif reason := f.check(v); reason != "" {\n\t\t\treturn bad(name, reason)\n',
      '\t\tif reason := f.check(v); reason != "" {\n\t\t\treturn bad(name, reason+": "+fmt.Sprint(v))\n', AP, ALL),
    m('negative-count', A, '\t\t\tif r.Int() >= 0 {', '\t\t\tif true {', AP, ALL),
    m('any-hash', A, '\t\tif s == "" || hashRe.MatchString(s) {', '\t\tif true {', AP, ALL),
    m('any-enum-value', A, '\t\tif slices.Contains(f.values, s) {', '\t\tif true {', AP, ALL),
    m('id-of-another-kind', A, '\t\tif validID(f.prefix, s) {', '\t\tif serverID.MatchString(s) {', AP, ALL),
    m('label-may-be-a-credential', A, '&& !observability.Sensitive(s)', '&& !observability.Sensitive("")', AP, ALL),
    m('label-overlong', A, '`^[A-Za-z0-9][A-Za-z0-9._:/+-]{0,127}$`', '`^[A-Za-z0-9][A-Za-z0-9._:/+-]{0,511}$`', AP, ALL),
    m('labels-unbounded', A, '\t\tif r.Len() > maxLabels {', '\t\tif false {', AP, ALL),
    m('label-keeps-odd-values', A, 'func Label(s string) string {\n\tif isLabel(s) {', 'func Label(s string) string {\n\tif true {', AP + PP, ALL),
    # --- auditlog (the one writer) ---
    m('append-skips-the-check', L, '\t\treturn err\n\t}\n\tdata, err := json.Marshal', '\t\t_ = err\n\t}\n\tdata, err := json.Marshal', LP, ALL),
    m('system-event-as-worker', L, 'return appendEvent(ctx, tx, org, audit.System, actor, e)', 'return appendEvent(ctx, tx, org, audit.Worker, actor, e)', LP, ALL),
    # --- the worker's audit and correlation (D-091 (b), D-092, D-093 (b)) ---
    m('worker-id-unchecked', W, '\tcase !audit.ValidActor(cfg.ID):', '\tcase cfg.ID == "" || len(cfg.ID) > 200 || !audit.ValidActor("w"):', WP, WT),
    m('outcome-inverted', W, '\tif requeued {\n\t\toutcome = "requeued"\n\t}\n\tmeta := map', '\tif !requeued {\n\t\toutcome = "requeued"\n\t}\n\tmeta := map', WP, WT),
    m('uncorrelated-not-marked', W, '\t\treq = ids.New("req")\n\t\tmeta["uncorrelated"] = true\n', '\t\treq = ids.New("req")\n', WP, WT),
    m('request-not-linked', W, '\treq, ok := requestID(j)\n\toutcome := "failed"\n', '\t_, ok := requestID(j)\n\treq := ids.New("req")\n\toutcome := "failed"\n', WP, WT),
    m('import-audited-as-analysis', W, 'case r.ImportID != "":\n\t\treturn "import", r.ImportID', 'case r.ImportID != "":\n\t\treturn "ai_analysis", r.ImportID', WP, WT),
    m('failure-message-audited', W, '"code": f.Code, "retryable": f.Retryable, "outcome": outcome}', '"code": f.Code, "retryable": f.Retryable, "outcome": outcome, "message": f.Message}', WP, WT),
    m('job-lines-without-request', W, '\tif req, ok := requestID(j); ok {\n\t\tout = append(out, "request_id", req)\n\t}\n', '', WP, WT),
    m('any-payload-request-id', W, 'if json.Unmarshal(j.Payload, &p) != nil || !reqIDs.MatchString(p.RequestID) {', 'if json.Unmarshal(j.Payload, &p) != nil || p.RequestID == "" {', WP, WT),
    # --- the access log and the request layer (D-093 (c)) ---
    m('route-is-the-path', X, 'slog.String("route", route(r.Pattern))', 'slog.String("route", r.URL.Path)', XP, XT),
    m('no-annotations', X, 'observability.Track(observability.WithRequestID(r.Context(), id))', 'observability.WithRequestID(r.Context(), id)', XP, XT),
    m('5xx-not-warn', X, '\t\t\tif status >= 500 {\n\t\t\t\tlevel = slog.LevelWarn', '\t\t\tif status > 500 {\n\t\t\t\tlevel = slog.LevelWarn', XP, XT),
    m('any-method-logged', X, '\t\treturn m\n\t}\n\treturn "OTHER"\n', '\t\treturn m\n\t}\n\treturn m\n', XP, XT),
    m('unrouted-pattern-kept', X, 'ok && method(m) == m && strings.HasPrefix(rest, "/")', 'ok || m != "" || rest != ""', XP, XT),
    m('status-zero-logged', X, '\t\t\tif status == 0 {\n\t\t\t\tstatus = http.StatusOK\n\t\t\t}\n', '', XP, XT),
    m('writer-not-unwrapped', X, 'func (w *recorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }', 'func (w *recorder) Unwrap() http.ResponseWriter { return nil }', XP, XT),
    m('client-request-id-trusted', R, 'id := observability.RequestID(r.Context()) // only server code sets it', 'id := r.Header.Get("X-Request-ID") // only server code sets it', XP, XT),
    m('handler-ignores-the-access-id', R, 'id := observability.RequestID(r.Context()) // only server code sets it', 'id := "" // only server code sets it', XP, XT),
    m('api-panic-value-logged', R, '"panic", observability.PanicText(v),', '"panic", fmt.Sprint(v),', XP, XT),
    m('error-code-not-annotated', E, '\tobservability.Annotate(r.Context(), slog.String("error_code", string(e.Code)))\n', '\t_ = observability.Annotate\n', XP, XT),
    m('organization-not-annotated', S, 'observability.Annotate(r.Context(), slog.String("organization_id", values[0]), slog.String("user_id", user))',
      'observability.Annotate(r.Context(), slog.String("user_id", user))', XP, XT),
    m('self-user-not-annotated', S, '\t\tobservability.Annotate(r.Context(), slog.String("user_id", user))\n\t\treturn next(w, r.WithContext(auth.WithIdentity(r.Context(), id)), user)',
      '\t\treturn next(w, r.WithContext(auth.WithIdentity(r.Context(), id)), user)', XP, XT),
    # --- telemetry (D-095 (c)) ---
    m('metric-with-a-request-id', T, 'metric.WithAttributes(method(m), rt, status(statusCode)))', 'metric.WithAttributes(method(m), rt, status(statusCode), id("geminus.request_id", requestID)))', TP, ALL),
    m('attributes-unfiltered', T, '\tif !observability.Fits(kind, value) {', '\tif false {', TP, ALL),
    m('job-span-not-a-root', T, 'trace.WithSpanKind(trace.SpanKindConsumer), trace.WithNewRoot(),', 'trace.WithSpanKind(trace.SpanKindConsumer),', TP, ALL),
    m('request-span-not-a-root', T, 'trace.WithSpanKind(trace.SpanKindServer), trace.WithNewRoot(),', 'trace.WithSpanKind(trace.SpanKindServer),', TP + XP, ALL),
    m('failed-job-span-ok', T, '\tif how != "completed" && how != "cancelled" {', '\tif false {', TP, ALL),
    m('tokens-of-one-type', T, 'attribute.String("gen_ai.token.type", kind)', 'attribute.String("gen_ai.token.type", kind[:0]+"input")', TP, ALL),
    m('engine-span-never-failed', T, 't.after(ctx, "simulation engine", start, how == "failed",', 't.after(ctx, "simulation engine", start, false,', TP, ALL),
    # --- the telemetry configuration and export (D-095 (b)) ---
    m('a-default-endpoint', G, '\t\tif base == "" {\n\t\t\treturn ""\n\t\t}', '\t\tif base == "" {\n\t\t\tbase = "http://localhost:4318"\n\t\t}', GP, 'TestLoadTelemetry'),
    m('sdk-disabled-ignored', G, '\tif disabled {\n\t\treturn Telemetry{}, nil\n\t}', '\tif disabled && false {\n\t\treturn Telemetry{}, nil\n\t}', GP, 'TestLoadTelemetry'),
    m('endpoint-with-credentials', G, 'u.User == nil && ', '', GP, 'TestLoadTelemetry'),
    m('endpoint-with-a-query', G, 'u.RawQuery == "" &&', '', GP, 'TestLoadTelemetry'),
    m('exporter-none-ignored', G, '\t\tcase "none":\n\t\t\treturn false', '\t\tcase "none":\n\t\t\treturn true', GP, 'TestLoadTelemetry'),
    m('grpc-accepted', G, '\tcase "", "http/protobuf":', '\tcase "", "http/protobuf", "grpc":', GP, 'TestLoadTelemetry'),
    m('installed-when-off', EX, '\tif !cfg.Enabled() {', '\tif false {', EXP, ALL),
    m('sdk-errors-to-stderr', EX, '\totel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {\n\t\tlog.Warn("telemetry export failed", "error", err.Error())\n\t}))\n', '', EXP, ALL),
    m('service-name-from-the-environment', EX, 'attribute.String("service.name", service)', 'attribute.String("service.namespace", service)', EXP, ALL),
    # --- the provider call (D-091 (d), D-095 (a)) ---
    m('provider-request-id-unfiltered', P, '"provider_request_id": audit.Label(m.ProviderRequestID)', '"provider_request_id": m.ProviderRequestID', PP, PT),
    m('provider-model-unfiltered', P, '"model": audit.Label(m.Model)', '"model": m.Model', PP, PT),
    m('call-code-lost', P, 'code = "AI_" + strings.ToUpper(string(c))', 'code = "ERROR"', PP, PT),
    m('call-line-with-the-error', P, '"provider_request_id", m.ProviderRequestID, "code", code)', '"provider_request_id", m.ProviderRequestID, "code", code, "error", fmt.Sprint(err))', PP, PT),
    # --- the engine's outcome (D-095 (a)) ---
    m('cancellation-as-failure', N, '\tcase errors.Is(cause, errCancelRequested) && attempt.Err() == nil:\n\t\treturn "cancelled"', '\tcase false:\n\t\treturn "cancelled"', NP, 'TestEngineOutcome'),
]

# Killed only with a database.
API = ['./internal/api/']
db = [
    m('db-failure-not-audited', W, '\t\treturn w.audit(ctx, tx, job, failure, retried)\n', '\t\treturn nil\n', WP, 'TestFailedAttemptsAreAudited|TestAnAuditFailureCommitsNothing'),
    m('db-reap-not-audited', W, '\t\t\t\tif err := w.audit(ctx, tx, r.Job, jobs.ReapFailure, r.Requeued); err != nil {\n\t\t\t\t\treturn err\n\t\t\t\t}\n', '', WP,
      'TestTheReaperAudits|TestAnAuditFailureCommitsNothing'),
    m('db-handler-lines-uncorrelated', W, '\thctx, stop := context.WithCancel(observability.With(sctx,\n\t\tattrs(append([]any{"organization_id", job.OrganizationID}, correlation(job)...))...))',
      '\thctx, stop := context.WithCancel(observability.With(sctx))', WP, 'TestTheHandlersLinesCarryTheJob'),
    m('db-reaped-requeued-inverted', 'internal/storage/jobs/jobs.go', 'return Reaped{Job: j, Requeued: j.Status == Queued}, err', 'return Reaped{Job: j, Requeued: j.Status != Queued}, err',
      ['./internal/storage/jobs/', './internal/worker/'], 'TestReapJobsReturnsWhatItTook|TestTheReaperAudits'),
    m('db-reaped-in-reverse', 'internal/storage/jobs/jobs.go', '\tslices.SortFunc(reaped, func(a, b Reaped) int { return strings.Compare(a.Job.ID, b.Job.ID) })\n',
      '\tslices.SortFunc(reaped, func(a, b Reaped) int { return strings.Compare(b.Job.ID, a.Job.ID) })\n', ['./internal/storage/jobs/'], 'TestReapJobsReturnsWhatItTook'),
    ('db-project-not-audited', [('internal/project/project.go', 'if err := auditlog.AppendUser(ctx, tx, m.OrganizationID, m.UserID, auditlog.Event{\n\t\t\tAction: "project.create"',
                                 'if err := func(...any) error { return nil }(ctx, tx, m.OrganizationID, m.UserID, auditlog.Event{\n\t\t\tAction: "project.create"')],
     API, 'TestAnUnwritableAuditEventCommitsNothing'),
    ('db-run-not-audited', [('internal/runs/runs.go', 'if err := auditlog.AppendUser(ctx, tx, m.OrganizationID, m.UserID, auditlog.Event{\n\t\t\tAction: "simulation.create"',
                             'if err := func(...any) error { return nil }(ctx, tx, m.OrganizationID, m.UserID, auditlog.Event{\n\t\t\tAction: "simulation.create"')],
     API, 'TestAnUnwritableAuditEventCommitsNothing|TestRunClosedLoop'),
    m('db-provision-as-worker', 'internal/storage/provisioning/provisioning.go', 'return auditlog.AppendSystem(ctx, tx, orgID, Actor,', 'return auditlog.AppendWorker(ctx, tx, orgID, Actor,',
      ['./internal/storage/provisioning/'], 'TestProvision'),
    m('db-engine-line-missing', N, 'h.cfg.Log.InfoContext(ctx, "simulation engine ran",', 'h.cfg.Log.DebugContext(ctx, "simulation engine ran",', API, 'TestRunClosedLoop'),
    m('db-profile-line-missing', 'internal/import/profiler/profiler.go', 'h.cfg.Log.InfoContext(ctx, "import profiled",', 'h.cfg.Log.DebugContext(ctx, "import profiled",', API, 'TestImportMappingClosedLoop'),
    m('db-validation-line-missing', 'internal/import/validator/validator.go', 'h.cfg.Log.InfoContext(ctx, "import validated",', 'h.cfg.Log.DebugContext(ctx, "import validated",', API, 'TestImportMappingClosedLoop'),
    m('db-no-access-log', 'cmd/api/main.go', 'srv := newServer(httpapi.AccessLog(log, handler), log)', 'srv := newServer(httpapi.AccessLog(slog.New(slog.DiscardHandler), handler), log)',
      ['./cmd/api/'], 'TestRunServesAndStops'),
]


def apply(edits):
    saved = {}
    for path, old, new in edits:
        full = root + '/' + path
        src = saved.get(full) or open(full).read()
        saved.setdefault(full, src)
        cur = open(full).read()
        if cur.count(old) != 1:
            for f, s in saved.items():
                open(f, 'w').write(s)
            return None, f'SNIPPET NOT FOUND ONCE in {path} ({cur.count(old)})'
        open(full, 'w').write(cur.replace(old, new))
    return saved, None


def restore(saved):
    for f, s in saved.items():
        open(f, 'w').write(s)


if which == 'check':
    # Only that every snippet applies once (nothing is built or run).
    bad = 0
    for name, edits, pkgs, run in unit + db:
        saved, err = apply(edits)
        if err:
            bad += 1
            print(f'{name}: {err}')
        else:
            restore(saved)
    print(f'{len(unit)} unit and {len(db)} db mutants; {bad} do not apply')
    sys.exit(1 if bad else 0)

if which == 'dbbuild':
    out = open(log, 'w')
    for name, edits, pkgs, run in db:
        saved, err = apply(edits)
        if err:
            out.write(f'{name}: {err}\n')
            continue
        try:
            vet = subprocess.run(['go', 'vet', *pkgs], cwd=root, capture_output=True, text=True, timeout=900)
        finally:
            restore(saved)
        out.write(f'{name}: {"BUILDS" if vet.returncode == 0 else "DOES NOT BUILD"}; tests NOT RUN ({run}) {vet.stderr.strip()[:200]}\n')
    out.close()
    print(open(log).read())
    sys.exit(0)

mutants = {'unit': unit, 'db': db}[which]
if len(sys.argv) > 4:  # only the named mutants (a rerun of some)
    only = set(sys.argv[4].split(','))
    mutants = [x for x in mutants if x[0] in only]
if which == 'db' and not os.environ.get('GEMINUS_TEST_DATABASE_URL'):
    sys.exit('db mutants need GEMINUS_TEST_DATABASE_URL (the DB-backed tests skip without it, so every mutant would survive)')

out = open(log, 'w')
killed = survived = build = 0
for name, edits, pkgs, run in mutants:
    saved, err = apply(edits)
    if err:
        out.write(f'{name}: {err}\n')
        survived += 1
        continue
    env = dict(os.environ)
    if which == 'db':
        env['GEMINUS_REQUIRE_DB_TESTS'] = '1'
    try:
        vet = subprocess.run(['go', 'vet', *pkgs], cwd=root, capture_output=True, text=True, timeout=900, env=env)
        r = None
        if vet.returncode == 0:
            r = subprocess.run(['go', 'test', '-count=1', '-run', '^(' + run + ')$', *pkgs], cwd=root, capture_output=True, text=True, timeout=1800, env=env)
    finally:
        restore(saved)
    text = (r.stdout + r.stderr) if r else vet.stderr
    if r is None or '[build failed]' in text or 'setup failed' in text:
        build += 1
        out.write(f'{name}: BUILD ({run}) {" | ".join(text.splitlines()[:2])}\n')
        continue
    status = 'KILLED' if r.returncode != 0 else 'SURVIVED'
    if r.returncode != 0:
        killed += 1
    else:
        survived += 1
    first = [l.strip()[:160] for l in text.splitlines() if l.lstrip().startswith('--- FAIL') or l.startswith('panic:')][:3]
    out.write(f'{name}: {status} ({run}) {" | ".join(first)}\n')
    out.flush()
out.write(f'TOTAL {len(mutants)} ({which}): {killed} killed, {survived} survived, {build} did not build\n')
out.close()
print(open(log).read())
