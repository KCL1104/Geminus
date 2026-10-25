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
)

// forbidden are the substrings no line may contain.
var forbidden = []string{"SENTINEL", "sentinel.person", "hunter2", "dbpass", "MIIEvQIB", "eyJzdWIi", "Lötstelle", "U0VOVElORUw"}

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
		"operation_id", jwt, "result_digest", csvCell, "method", "SENTINEL METHOD")
	l := s.lines(t)[0]
	for _, k := range []string{"organization_id", "run_id", "code", "status", "attempt", "route", "job_type", "model", "worker_id",
		"operation_id", "result_digest", "method"} {
		if l[k] != observability.Redacted {
			t.Errorf("%s = %v", k, l[k])
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
	} {
		log.Error("failed", "error", text)
	}
	out := s.String()
	for _, l := range s.lines(t) {
		e, _ := l["error"].(string)
		if strings.ContainsAny(e, "\n\r\t\u202e\u202c") {
			t.Errorf("a control or format character survived: %q", e)
		}
	}
	s.clean(t)
	if !strings.Contains(out, `cell \"[redacted]\" is invalid`) || !strings.Contains(out, "(SQLSTATE 22P02)") ||
		!strings.Contains(out, "postgres://[redacted]@db.internal:5432/twin") {
		t.Errorf("the context around a redaction is lost: %s", out)
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
