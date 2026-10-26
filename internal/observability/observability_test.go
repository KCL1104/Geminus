package observability_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/observability"
)

// TEST-027 sentinels: customer content, secrets and AI text that must never
// reach a log line, whichever way a caller hands them over.
var (
	csvRow   = "case_id,event_type,timestamp\nCASE-SENTINEL-7731,FCT_FAIL,2026-10-01T00:00:00Z"
	csvCell  = "CASE-SENTINEL-7731"
	header   = "Lötstelle_SENTINEL" // a customer column name
	bearer   = "Bearer eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJTRU5USU5FTCJ9.U0VOVElORUwtc2lnbmF0dXJl"
	jwt      = "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJTRU5USU5FTCJ9.U0VOVElORUwtc2lnbmF0dXJl"
	apiKey   = "sk-ant-api03-SENTINELsentinel0123456789abcdef"
	awsKey   = "AKIASENTINEL0000ABCD"
	pem      = "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwSENTINEL\n-----END PRIVATE KEY-----"
	dbURL    = "postgres://twin_api:dbpassSENTINEL@db.internal:5432/twin"
	cookie   = "Cookie: __Host-geminus_session=SESSIONSENTINEL"
	password = "DB_PASSWORD=hunter2SENTINEL"
	email    = "sentinel.person@example.com"
	prompt   = "SYSTEM: you are the modeler. PROMPT-SENTINEL"
	thinking = "CHAIN-OF-THOUGHT-SENTINEL: first I will"
	answer   = `{"summary":"PROVIDER-OUTPUT-SENTINEL"}`
	comment  = "Approved by the night shift COMMENT-SENTINEL"
	// A credential header whose value is no key=value secret, and a bare
	// base64 secret (a signature, a key body).
	negotiate = "Authorization: Negotiate TlRMTVNTUAABSENTINEL"
	prefs     = "Cookie: prefs=COOKIE-SENTINEL; theme=dark"
	blob      = "c2VjcmV0X2tleV9TRU5USU5FTF8wMTIzNDU2Nzg5X2FiY2RlZmdoaWprbG1ub3A="
)

// forbidden are the substrings no line may contain.
var forbidden = []string{"SENTINEL", "sentinel.person", "hunter2", "dbpass", "MIIEvQIB", "eyJzdWIi", "Lötstelle", "U0VOVElORUw", "c2VjcmV0X2tl"}

type sink struct{ bytes.Buffer }

func (s *sink) lines(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(s.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("not a JSON line: %q (%v)", l, err)
		}
		out = append(out, m)
	}
	return out
}

func (s *sink) clean(t *testing.T) {
	t.Helper()
	for _, f := range forbidden {
		if strings.Contains(s.String(), f) {
			t.Errorf("the log holds %q:\n%s", f, s.String())
		}
	}
}

func logger(s *sink) *slog.Logger { return observability.NewLogger(s, "test") }

func TestEveryLineCarriesTheContextsIDs(t *testing.T) {
	var s sink
	log := logger(&s)
	req, org, job := ids.New("req"), ids.New("org"), ids.New("job")
	ctx := observability.WithRequestID(context.Background(), req)
	ctx = observability.With(ctx, slog.String("organization_id", org), slog.String("job_id", job), slog.Int("attempt", 2))
	log.InfoContext(ctx, "one")
	log.WarnContext(ctx, "two", "code", "SOME_CODE")
	// A line that names a key itself keeps its own value, once.
	other := ids.New("org")
	log.InfoContext(ctx, "three", "organization_id", other)
	lines := s.lines(t)
	if len(lines) != 3 {
		t.Fatalf("%d lines", len(lines))
	}
	for i, l := range lines {
		if l["request_id"] != req || l["job_id"] != job || l["attempt"] != float64(2) || l["component"] != "test" {
			t.Errorf("line %d: %v", i, l)
		}
	}
	if lines[0]["organization_id"] != org || lines[2]["organization_id"] != other {
		t.Errorf("organization: %v / %v", lines[0], lines[2])
	}
	if n := strings.Count(s.String(), `"organization_id"`); n != 3 {
		t.Errorf("organization_id appears %d times in 3 lines: %s", n, s.String())
	}
	// Without the context, nothing is invented.
	s.Reset()
	log.Info("no context")
	if l := s.lines(t)[0]; l["request_id"] != nil {
		t.Errorf("a request ID without a request: %v", l)
	}
	if observability.RequestID(context.Background()) != "" || observability.RequestID(ctx) != req {
		t.Error("RequestID")
	}
}

// With copies: a derived context's attributes never leak into its parent
// or a sibling.
func TestWithDoesNotShare(t *testing.T) {
	base := observability.With(context.Background(), slog.String("job_id", ids.New("job")))
	a := observability.With(base, slog.String("run_id", ids.New("sim")))
	b := observability.With(base, slog.String("import_id", ids.New("imp")))
	if len(observability.Attrs(base)) != 1 || len(observability.Attrs(a)) != 2 || len(observability.Attrs(b)) != 2 ||
		observability.Attrs(b)[1].Key != "import_id" {
		t.Errorf("%v %v %v", observability.Attrs(base), observability.Attrs(a), observability.Attrs(b))
	}
	// Siblings of a context built in steps (where an appended slice would
	// have room to spare) keep their own last attribute.
	three := observability.With(context.Background(), slog.Int("attempt", 1), slog.Int("rows", 2), slog.Int("columns", 3))
	four := observability.With(three, slog.Int("events", 4))
	x := observability.With(four, slog.String("code", "FIRST"))
	y := observability.With(four, slog.String("code", "SECOND"))
	if got := observability.Attrs(x); len(got) != 5 || got[4].Value.String() != "FIRST" {
		t.Errorf("a sibling overwrote it: %v (and %v)", got, observability.Attrs(y))
	}
}

// What a request learns as it runs (the organization and user once
// authenticated, the error code) is on every later line of that request.
func TestAnnotations(t *testing.T) {
	var s sink
	log := logger(&s)
	ctx := observability.Track(observability.WithRequestID(context.Background(), ids.New("req")))
	log.InfoContext(ctx, "before")
	org, user := ids.New("org"), ids.New("usr")
	observability.Annotate(ctx, slog.String("organization_id", org), slog.String("user_id", user))
	derived := context.WithValue(ctx, struct{}{}, 1) // a handler's own derived context still sees them
	log.InfoContext(derived, "after")
	observability.Annotate(ctx, slog.String("organization_id", ids.New("org"))) // the last value counts
	lines := s.lines(t)
	if lines[0]["organization_id"] != nil || lines[1]["organization_id"] != org || lines[1]["user_id"] != user {
		t.Errorf("%v", lines)
	}
	if got := observability.Annotations(ctx); len(got) != 2 || got[0].Value.String() == org {
		t.Errorf("annotations: %v", got)
	}
	// What Annotations returned is a copy: a later annotation does not
	// change it.
	before := observability.Annotations(ctx)
	observability.Annotate(ctx, slog.String("user_id", ids.New("usr")))
	if before[1].Key != "user_id" || before[1].Value.String() != user {
		t.Errorf("an earlier result changed: %v", before)
	}
	// Annotating a context that does not track is a no-op.
	observability.Annotate(context.Background(), slog.String("user_id", user))
	if observability.Annotations(context.Background()) != nil {
		t.Error("annotated an untracked context")
	}
}

func TestSafeValuesAreKept(t *testing.T) {
	var s sink
	log := logger(&s)
	org, run := ids.New("org"), ids.New("sim")
	log.Info("kept", "organization_id", org, "run_id", run, "code", "LEASE_EXPIRED", "status", "completed",
		"attempt", 3, "duration_ms", int64(12), "retried", true, "job_type", "import_profile", "route", "POST /v1/models/{id}/simulations",
		"method", "POST", "operation_id", "publishVersion", "type", "*fs.PathError", "result_digest", "sha256:"+strings.Repeat("ab", 32),
		"worker_id", "worker-0.pod_a:1", "model", "claude-opus-5-5", "error", "jobs: lease lost", "path", "/nodes/2/service_time")
	l := s.lines(t)[0]
	for k, want := range map[string]any{"organization_id": org, "run_id": run, "code": "LEASE_EXPIRED", "status": "completed",
		"attempt": float64(3), "duration_ms": float64(12), "retried": true, "job_type": "import_profile",
		"route": "POST /v1/models/{id}/simulations", "method": "POST", "operation_id": "publishVersion", "type": "*fs.PathError",
		"worker_id": "worker-0.pod_a:1", "model": "claude-opus-5-5", "error": "jobs: lease lost", "path": "/nodes/2/service_time"} {
		if l[k] != want {
			t.Errorf("%s = %v, want %v", k, l[k], want)
		}
	}
}

// A registered key whose value does not have the key's shape is redacted:
// the shape is what makes the value safe, not the key.
func TestValuesOfTheWrongShapeAreRedacted(t *testing.T) {
	var s sink
	log := logger(&s)
	log.Info("shapes", "organization_id", csvCell, "run_id", email, "code", "free text "+csvCell, "status", header,
		"attempt", "3; "+csvCell, "route", "/v1/"+csvCell, "job_type", apiKey, "model", apiKey, "worker_id", bearer,
		"operation_id", jwt, "result_digest", csvCell, "method", "SENTINEL METHOD", "type", apiKey)
	l := s.lines(t)[0]
	for _, k := range []string{"organization_id", "run_id", "code", "status", "attempt", "route", "job_type", "model", "worker_id",
		"operation_id", "result_digest", "method", "type"} {
		if l[k] != observability.Redacted {
			t.Errorf("%s = %v", k, l[k])
		}
	}
	s.clean(t)
	// A route pattern is a method and a pattern of path characters: a path
	// with a query or a space is not one.
	s.Reset()
	log.Info("routes", "route", "GET /v1/projects?cursor=CURSOR-SENTINEL")
	log.Info("routes", "route", "GET /v1/models/"+csvCell+" x")
	for _, l := range s.lines(t) {
		if l["route"] != observability.Redacted {
			t.Errorf("route = %v", l["route"])
		}
	}
	s.clean(t)
}

// Default deny: a key the registry does not list is redacted, whatever it
// holds, so a new log field is a reviewed change to the registry.
func TestUnregisteredKeysAreRedacted(t *testing.T) {
	var s sink
	log := logger(&s)
	log.Info("unregistered", "prompt", prompt, "thinking", thinking, "completion", answer, "row", csvRow, "cell", csvCell,
		"column", header, "password", "hunter2", "authorization", bearer, "cookie", cookie, "api_key", apiKey, "comment", comment,
		"name", "Line SENTINEL", "count", 3, csvCell, "value", "Bad Key!", 1)
	l := s.lines(t)[0]
	for _, k := range []string{"prompt", "thinking", "completion", "row", "cell", "column", "password", "authorization", "cookie",
		"api_key", "comment", "name", "count"} {
		if l[k] != observability.Redacted {
			t.Errorf("%s = %v", k, l[k])
		}
	}
	// A key that is not a plain name is not echoed either.
	if l[observability.InvalidKey] != observability.Redacted {
		t.Errorf("an invalid key: %v", l)
	}
	s.clean(t)
}

// Free text (errors, panics, addresses, messages) keeps what helps an
// operator and loses credentials, personal data and quoted values. A value
// an error must name is quoted (%q, as PostgreSQL quotes it), so the
// filter can drop it (D-094 (c)); unquoted prose cannot be told from the
// error's own words, which is why handlers keep values out of errors.
func TestTextIsSanitized(t *testing.T) {
	var s sink
	log := logger(&s)
	for _, text := range []string{
		bearer, "auth failed: " + jwt, "key " + apiKey + " refused", "aws " + awsKey, pem, "connect " + dbURL,
		"request headers: " + cookie, "env " + password, "mail " + email, "token=" + "abcdefSENTINEL01",
		fmt.Sprintf("row %q is invalid", csvRow), fmt.Sprintf("cell %q is invalid", csvCell), fmt.Sprintf("column %q unknown", header),
		`ERROR: invalid input syntax for type integer: "CASE-SENTINEL-7731" (SQLSTATE 22P02)`,
		"bidi \u202eevil\u202c text\r\nnext line\ttab",
		"line\u2028separator\u2029paragraph",
		"request headers: " + negotiate, "request headers: " + prefs, "signature " + blob + " rejected",
		// A quote that is never closed (a value with a quote in it, text
		// cut short) hides everything after it.
		`cell "CASE-SENTINEL-7731 is invalid`, `value "a\"b CASE-SENTINEL\`,
		// Review L2: single quotes that open and close a span, backticks
		// and typographic quotes mark a value as well, closed or not.
		"value 'QUOTE-SENTINEL' is invalid", "value `QUOTE-SENTINEL` is invalid", "value \u201cQUOTE-SENTINEL\u201d is invalid",
		"value \u2018QUOTE-SENTINEL\u2019 is invalid", "invalid character 'Q' then 'QUOTE-SENTINEL'", "unclosed 'QUOTE-SENTINEL and more",
		"unclosed `QUOTE-SENTINEL", "unclosed \u201cQUOTE-SENTINEL", "it's 'don't QUOTE-SENTINEL' inside",
		// A zero-width character does not split a credential; session
		// cookies and URL signatures are credentials (review probes).
		"key sk-ant-\u200bapi03-ZW-SENTINEL-abcdefgh", "JSESSIONID=ABCDEF0123456789SENTINEL",
		"GET https://b.s3.amazonaws.com/k?X-Amz-Signature=0123456789abcdefSENTINEL", "https://a.blob.core.windows.net/c?sv=2024&sig=SIGSENTINEL0123",
	} {
		log.Error("failed", "error", text)
	}
	out := s.String()
	for _, l := range s.lines(t) {
		e, _ := l["error"].(string)
		if strings.ContainsAny(e, "\n\r\t\u202e\u202c\u2028\u2029\u200b") {
			t.Errorf("a control or format character survived: %q", e)
		}
	}
	s.clean(t)
	if !strings.Contains(out, `cell \"[redacted]\" is invalid`) || !strings.Contains(out, "(SQLSTATE 22P02)") ||
		!strings.Contains(out, "postgres://[redacted]@db.internal:5432/twin") || !strings.Contains(out, "request headers: Authorization: [redacted]") ||
		!strings.Contains(out, "signature [redacted] rejected") || !strings.Contains(out, "value '[redacted]' is invalid") ||
		!strings.Contains(out, "value `[redacted]` is invalid") || !strings.Contains(out, "value \u201c[redacted]\u201d is invalid") {
		t.Errorf("the context around a redaction is lost: %s", out)
	}
}

// Review L2: an apostrophe inside a word is not a quote, so contractions
// and possessives stay readable; a span that a quote opens and closes is a
// value. What the rule costs: a span in backticks is hidden even when it
// holds only names (pgx names the user and database it connects as that
// way); the address and the error after it stay.
func TestQuotesKeepWords(t *testing.T) {
	for in, want := range map[string]string{
		"the worker's lease expired; don't retry":                                    "the worker's lease expired; don't retry",
		"the workers' leases expired, so the job's attempts end":                     "the workers' leases expired, so the job's attempts end",
		"it can't be the run's last attempt (it's the 2nd)":                          "it can't be the run's last attempt (it's the 2nd)",
		"invalid character 'x' looking for beginning of value":                       "invalid character '[redacted]' looking for beginning of value",
		"failed to connect to `user=twin_worker database=twin`: dial error":          "failed to connect to `[redacted]`: dial error",
		"jobs: the lease of 'job_01M4YDQK00ADN261CME3WV5HZ6' was lost, so it's over": "jobs: the lease of '[redacted]' was lost, so it's over",
	} {
		if got := observability.Text(in); got != want {
			t.Errorf("Text(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTextIsBounded(t *testing.T) {
	var s sink
	log := logger(&s)
	log.Error("long", "error", strings.Repeat("x", 5000)+" "+apiKey, "stack", strings.Repeat("frame\n\t", 4000))
	l := s.lines(t)[0]
	if n := len([]rune(l["error"].(string))); n > observability.MaxText+1 {
		t.Errorf("error of %d runes", n)
	}
	// A JSON Pointer is cut at its own, shorter bound (review L4).
	s.Reset()
	log.Error("pointer", "path", "/"+strings.Repeat("p", 2*observability.MaxPointer))
	if n := len([]rune(s.lines(t)[0]["path"].(string))); n != observability.MaxPointer+1 {
		t.Errorf("a pointer of %d runes", n)
	}
	if n := len([]rune(l["stack"].(string))); n > observability.MaxStack+1 || !strings.Contains(l["stack"].(string), "\n\t") {
		t.Errorf("stack of %d runes, lines kept: %v", n, strings.Contains(l["stack"].(string), "\n"))
	}
	s.clean(t)
}

type brokenError struct{}

func (*brokenError) Error() string { panic("broken") }

type secretValue struct{ Row string }

type valuer struct{}

func (valuer) LogValue() slog.Value { return slog.StringValue(csvRow) }

type panicky struct{}

func (panicky) LogValue() slog.Value { panic(csvRow) }

type grouper struct{}

func (grouper) LogValue() slog.Value { return slog.GroupValue(slog.String("stack", csvRow)) }

// Values that are not text are logged only when they are numbers, times or
// errors (as sanitized text); anything else is redacted, and an error
// whose method panics does not crash the logger.
func TestOtherValues(t *testing.T) {
	var s sink
	log := logger(&s)
	log.Error("values", "error", fmt.Errorf("profile: %w", fmt.Errorf("cell %q", csvCell)),
		"panic", &brokenError{}, "stack", secretValue{Row: csvRow}, "path", []string{csvCell}, "code", valuer{},
		"addr", map[string]string{"k": csvCell}, slog.Group("request", slog.String("body", answer)))
	l := s.lines(t)[0]
	if l["error"] != `profile: cell "[redacted]"` {
		t.Errorf("error = %v", l["error"])
	}
	for _, k := range []string{"panic", "stack", "path", "code", "addr", "request"} {
		if l[k] != observability.Redacted {
			t.Errorf("%s = %v", k, l[k])
		}
	}
	s.clean(t)
	// A LogValuer that panics (slog recovers it as an error value) is
	// Redacted under a shaped key and the runtime's own words under a text
	// key: never the panic's value.
	s.Reset()
	log.Error("valuers", "code", panicky{}, "error", panicky{})
	l = s.lines(t)[0]
	if e, _ := l["error"].(string); l["code"] != observability.Redacted || !strings.HasPrefix(e, "LogValue panicked") {
		t.Errorf("%v", l)
	}
	s.clean(t)
	// A group under a free-text key, given or resolved from a LogValuer, is
	// Redacted, not a nested object of unfiltered text (review L1).
	s.Reset()
	log.Error("groups", slog.Group("error", slog.String("error", csvCell)), "stack", grouper{}, "panic", grouper{})
	l = s.lines(t)[0]
	for _, k := range []string{"error", "stack", "panic"} {
		if l[k] != observability.Redacted {
			t.Errorf("%s = %v", k, l[k])
		}
	}
	s.clean(t)
}

type fakeRuntimeError struct{ msg string }

func (e fakeRuntimeError) Error() string { return e.msg }
func (fakeRuntimeError) RuntimeError()   {}

// D-094 (d): a panic's value is never logged, only its type; the message
// of an error the Go runtime raised (a type of package runtime) is kept,
// sanitized like any text. A type of another package is logged by its type
// only, even when it has a RuntimeError method (review L3).
func TestPanicText(t *testing.T) {
	if got := observability.PanicText("row " + csvRow); got != "panic with a value of type string" {
		t.Errorf("%q", got)
	}
	if got := observability.PanicText(fmt.Errorf("cell %s", csvCell)); got != "panic with a value of type *errors.errorString" {
		t.Errorf("%q", got)
	}
	var m map[string]int
	func() {
		defer func() {
			if got := observability.PanicText(recover()); got != "panic: assignment to entry in nil map" {
				t.Errorf("%q", got)
			}
		}()
		m["x"] = 1
	}()
	func() {
		defer func() {
			if got := observability.PanicText(recover()); !strings.HasPrefix(got, "panic: runtime error: index out of range [") {
				t.Errorf("%q", got)
			}
		}()
		var xs []int
		_ = xs[len(m)+3]
	}()
	got := observability.PanicText(fakeRuntimeError{fmt.Sprintf("index %s out of range; key %s", csvCell, apiKey)})
	if got != "panic with a value of type observability_test.fakeRuntimeError" {
		t.Errorf("%q", got)
	}
	if got := observability.PanicText(&fakeRuntimeError{"x"}); got != "panic with a value of type *observability_test.fakeRuntimeError" {
		t.Errorf("%q", got)
	}
	func() {
		defer func() {
			if got := observability.PanicText(recover()); got != "panic: interface conversion: interface {} is string, not int" {
				t.Errorf("a failed type assertion (*runtime.TypeAssertionError): %q", got)
			}
		}()
		var x any = csvCell
		_ = x.(int)
	}()
	if got := observability.PanicText(nil); got != "panic with a value of type <nil>" {
		t.Errorf("%q", got)
	}
}

// Attributes given to With are filtered as well, and a context's key that
// With already set is not added twice.
func TestWithAttributesAreFiltered(t *testing.T) {
	var s sink
	org := ids.New("org")
	log := logger(&s).With("organization_id", org, "prompt", prompt, "error", "key "+apiKey)
	ctx := observability.With(context.Background(), slog.String("organization_id", ids.New("org")))
	log.InfoContext(ctx, "with")
	l := s.lines(t)[0]
	if l["organization_id"] != org || l["prompt"] != observability.Redacted || strings.Contains(l["error"].(string), "sk-ant") {
		t.Errorf("%v", l)
	}
	if n := strings.Count(s.String(), `"organization_id"`); n != 1 {
		t.Errorf("organization_id %d times: %s", n, s.String())
	}
	// Inside a group the context's IDs are still added and filtered.
	s.Reset()
	logger(&s).WithGroup("g").InfoContext(observability.WithRequestID(context.Background(), ids.New("req")), "grouped", "prompt", prompt)
	s.clean(t)
	if !strings.Contains(s.String(), `"request_id"`) {
		t.Errorf("grouped: %s", s.String())
	}
	// A group's name built from data is not echoed.
	s.Reset()
	logger(&s).WithGroup(csvCell).Info("grouped", "code", "SOME_CODE")
	s.clean(t)
	if !strings.Contains(s.String(), `"`+observability.InvalidKey+`":{"code":"SOME_CODE"}`) {
		t.Errorf("grouped: %s", s.String())
	}
}

// Review I7: a logger's groups hold its own attributes, never the
// correlation: request_id, job_id and the other context IDs stay at the top
// level of the line, once, whatever groups the logger has.
func TestCorrelationStaysAtTheTop(t *testing.T) {
	var s sink
	req, job, org := ids.New("req"), ids.New("job"), ids.New("org")
	ctx := observability.With(observability.WithRequestID(context.Background(), req), slog.String("job_id", job),
		slog.String("organization_id", ids.New("org")))
	logger(&s).With("organization_id", org).WithGroup("g").With("code", "WITH_CODE").WithGroup("h").InfoContext(ctx, "grouped",
		"status", "ok", "prompt", prompt)
	l := s.lines(t)[0]
	g, _ := l["g"].(map[string]any)
	h, _ := g["h"].(map[string]any)
	if l["request_id"] != req || l["job_id"] != job || l["organization_id"] != org || g["code"] != "WITH_CODE" || h["status"] != "ok" ||
		h["prompt"] != observability.Redacted || g["request_id"] != nil || h["request_id"] != nil || h["job_id"] != nil {
		t.Errorf("%s", s.String())
	}
	for _, k := range []string{`"request_id"`, `"job_id"`, `"organization_id"`} {
		if n := strings.Count(s.String(), k); n != 1 {
			t.Errorf("%s %d times: %s", k, n, s.String())
		}
	}
	s.clean(t)
	// A key of the record inside a group is the group's: it does not take
	// the context's value off the top level.
	s.Reset()
	other := ids.New("job")
	logger(&s).WithGroup("g").InfoContext(ctx, "grouped", "job_id", other)
	if l := s.lines(t)[0]; l["job_id"] != job || l["g"].(map[string]any)["job_id"] != other {
		t.Errorf("%s", s.String())
	}
	// Without a group the line is as before: the record's own key wins.
	s.Reset()
	logger(&s).InfoContext(ctx, "flat", "status", "ok", "job_id", other)
	if l := s.lines(t)[0]; l["request_id"] != req || l["status"] != "ok" || l["job_id"] != other || strings.Count(s.String(), `"job_id"`) != 1 {
		t.Errorf("%v", l)
	}
}

func TestTheMessageIsSanitized(t *testing.T) {
	var s sink
	logger(&s).Error("http: TLS handshake error: " + bearer + " " + fmt.Sprintf("%q", csvCell))
	s.clean(t)
}

// Context IDs whose shape is wrong are redacted too: a context never
// carries a value a log line would refuse.
func TestContextValuesAreFiltered(t *testing.T) {
	var s sink
	ctx := observability.WithRequestID(context.Background(), "req_"+csvCell)
	ctx = observability.With(ctx, slog.String("prompt", prompt), slog.String("job_id", csvCell))
	logger(&s).InfoContext(ctx, "ctx")
	l := s.lines(t)[0]
	if l["request_id"] != observability.Redacted || l["prompt"] != observability.Redacted || l["job_id"] != observability.Redacted {
		t.Errorf("%v", l)
	}
	s.clean(t)
}

func TestSensitive(t *testing.T) {
	for _, s := range []string{bearer, jwt, apiKey, awsKey, pem, dbURL, cookie, password, email, "ghp_" + strings.Repeat("a", 30)} {
		if !observability.Sensitive(s) {
			t.Errorf("%q not sensitive", s)
		}
	}
	for _, s := range []string{"org-a", "claude-opus-5-5", "des-go-0.4.0", "proposal-output.schema.json", "fake", "modeler-v1",
		"req_011CUcvD2y5nA6z5tYt7aCb1", "default_delivery_pipeline", "worker-0.pod_a:1"} {
		if observability.Sensitive(s) {
			t.Errorf("%q sensitive", s)
		}
	}
}

func TestTextHelper(t *testing.T) {
	got := observability.Text(fmt.Sprintf("x %q y\n%s", csvCell, apiKey))
	if strings.Contains(got, "SENTINEL") || strings.Contains(got, "\n") || !strings.HasPrefix(got, `x "[redacted]" y`) {
		t.Errorf("%q", got)
	}
	if got := observability.Text("a\xffb"); got != "a�b" {
		t.Errorf("invalid UTF-8: %q", got)
	}
	if observability.Text("") != "" {
		t.Error("empty text")
	}
	if got := observability.ErrorText(errors.Join(errors.New("a"), fmt.Errorf("b %q", csvCell))); got != `a b "[redacted]"` {
		t.Errorf("ErrorText: %q", got)
	}
	if got := observability.ErrorText(&brokenError{}); got != observability.Redacted {
		t.Errorf("a broken error: %q", got)
	}
	if observability.ErrorText(nil) != "" {
		t.Error("nil error")
	}
}
