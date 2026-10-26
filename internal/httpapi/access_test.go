package httpapi

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/kcl1104/geminus/internal/auth"
	"github.com/kcl1104/geminus/internal/observability"
	"github.com/kcl1104/geminus/internal/observability/telemetry"
	"github.com/kcl1104/geminus/internal/tenant"
)

// lines decodes the JSON lines of logs.
func lines(t *testing.T, logs *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("not a JSON line %q: %v", l, err)
		}
		out = append(out, m)
	}
	return out
}

// T-035 (D-093 (c)): one line per request, after it ends, with the route's
// pattern (never the path or query the client sent), the method, status,
// duration and size, and the request's ID, the one the response and the
// error envelope carry. What the request learned on the way (the selected
// organization and the user once authenticated, the error code) is on the
// line too, and on every line the request's handlers log.
func TestAccessLog(t *testing.T) {
	const org = "org_01M4YDQK00TA00000000000000"
	const user = "usr_01M4YDQK00TA00000000000001"
	var logs bytes.Buffer
	log := observability.NewLogger(&logs, "api")
	mux := http.NewServeMux()
	mux.Handle("POST /v1/things/{id}", Handler(log, Scoped(fakeVerifier{id: auth.Identity{Issuer: "https://id", Subject: "s"}}, &fakeResolver{user: user},
		func(w http.ResponseWriter, r *http.Request, s tenant.Scope) error {
			log.WarnContext(r.Context(), "inside the route", "error", `the row "CASE-SENTINEL" is odd`)
			if r.PathValue("id") == "conflict" {
				return Errorf(LifecycleConflict, "not now")
			}
			WriteJSON(w, 201, map[string]string{"ok": "yes"})
			return nil
		})))
	mux.Handle("/", Handler(log, func(http.ResponseWriter, *http.Request) error { return Errorf(NotFound, "no such operation") }))
	h := AccessLog(log, mux)

	do := func(method, target string, authed bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, target, strings.NewReader(`{"name":"CASE-SENTINEL"}`))
		if authed {
			r.Header.Set("Authorization", "Bearer eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJTRU5USU5FTCJ9.U0VOVElORUw")
			r.Header.Set("X-Organization-ID", org)
		}
		r.Header.Set("X-Request-ID", "req_01AAAAAAAAAAAAAAAAAAAAAAAA") // never used
		r.Header.Set("Cookie", "__Host-geminus_session=SENTINEL")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	created := do("POST", "/v1/things/CASE-SENTINEL?cursor=SENTINEL&name=SENTINEL", true) // (a token in the query is refused: auth.BearerToken)
	refused := do("POST", "/v1/things/conflict", true)
	anonymous := do("POST", "/v1/things/x", false)
	unmatched := do("DELETE", "/v1/SENTINEL", false)
	odd := do("PROPFIND", "/v1/things/x", false)

	if strings.Contains(logs.String(), "SENTINEL") || strings.Contains(logs.String(), "access_token") || strings.Contains(logs.String(), "req_01AAAA") {
		t.Errorf("the log holds what the client sent: %s", logs.String())
	}
	var access, inside []map[string]any
	for _, l := range lines(t, &logs) {
		switch l["msg"] {
		case "request":
			access = append(access, l)
		case "inside the route":
			inside = append(inside, l)
		}
	}
	if len(access) != 5 || len(inside) != 2 {
		t.Fatalf("%d access lines, %d route lines: %s", len(access), len(inside), logs.String())
	}
	for i, c := range []struct {
		w             *httptest.ResponseRecorder
		route, method string
		status        int
		code          string
		authed        bool
	}{
		{created, "POST /v1/things/{id}", "POST", 201, "", true},
		{refused, "POST /v1/things/{id}", "POST", 409, "LIFECYCLE_CONFLICT", true},
		{anonymous, "POST /v1/things/{id}", "POST", 401, "UNAUTHENTICATED", false},
		{unmatched, "unmatched", "DELETE", 404, "NOT_FOUND", false},
		{odd, "unmatched", "OTHER", 404, "NOT_FOUND", false},
	} {
		l := access[i]
		id := c.w.Header().Get("X-Request-ID")
		if c.w.Code != c.status || l["route"] != c.route || l["method"] != c.method || l["http_status"] != float64(c.status) ||
			l["request_id"] != id || !strings.HasPrefix(id, "req_") || l["component"] != "api" {
			t.Errorf("request %d: %d %v", i, c.w.Code, l)
		}
		if _, ok := l["duration_ms"].(float64); !ok || l["response_bytes"] != float64(c.w.Body.Len()) {
			t.Errorf("request %d: %v", i, l)
		}
		if c.code == "" && l["error_code"] != nil || c.code != "" && l["error_code"] != c.code {
			t.Errorf("request %d: error code %v", i, l["error_code"])
		}
		if c.status >= 400 {
			if e := checkEnvelope(t, c.w); e.Error.RequestID != id {
				t.Errorf("request %d: envelope %s, header %s", i, e.Error.RequestID, id)
			}
		}
		if c.authed && (l["organization_id"] != org || l["user_id"] != user) || !c.authed && (l["organization_id"] != nil || l["user_id"] != nil) {
			t.Errorf("request %d: scope %v", i, l)
		}
	}
	for i, l := range inside {
		if l["request_id"] != access[i]["request_id"] || l["organization_id"] != org || l["user_id"] != user {
			t.Errorf("route line %d: %v", i, l)
		}
	}
	// A 5xx is a warning; anything else is information.
	logs.Reset()
	failing := AccessLog(log, Handler(log, func(http.ResponseWriter, *http.Request) error { return Errorf(InternalError, "x") }))
	failing.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/x", nil))
	for _, l := range lines(t, &logs) {
		if l["msg"] == "request" && l["level"] != "WARN" {
			t.Errorf("%v", l)
		}
	}
}

// T-035 (D-095): with telemetry installed, each request is a server span
// named by its route and a point of http.server.request.duration; neither
// holds the path, the query, the body or a header the client sent.
func TestAccessLogTelemetry(t *testing.T) {
	spans, reader := tracetest.NewSpanRecorder(), sdkmetric.NewManualReader()
	tel, err := telemetry.New(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans)), sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	if err != nil {
		t.Fatal(err)
	}
	defer telemetry.Install(telemetry.Install(tel))
	log := observability.NewLogger(io.Discard, "api")
	mux := http.NewServeMux()
	mux.Handle("GET /v1/things/{id}", Handler(log, func(w http.ResponseWriter, r *http.Request) error {
		WriteJSON(w, 200, map[string]string{"id": r.PathValue("id")})
		return nil
	}))
	mux.Handle("/", Handler(log, func(http.ResponseWriter, *http.Request) error { return Errorf(InternalError, "x") }))
	h := AccessLog(log, mux)
	for _, target := range []string{"/v1/things/CASE-SENTINEL?q=SENTINEL", "/v1/SENTINEL"} {
		r := httptest.NewRequest("GET", target, nil)
		r.Header.Set("Authorization", "Bearer SENTINEL-token-0123456789")
		r.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01") // never read
		h.ServeHTTP(httptest.NewRecorder(), r)
	}
	ended := spans.Ended()
	if len(ended) != 2 || ended[0].Name() != "GET /v1/things/{id}" || ended[1].Name() != "unmatched" ||
		ended[1].Status().Code.String() != "Error" || ended[0].Parent().IsValid() ||
		ended[0].SpanContext().TraceID().String() == "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("%v", ended)
	}
	var dump strings.Builder
	for _, s := range ended {
		dump.WriteString(s.Name())
		for _, kv := range s.Attributes() {
			dump.WriteString(" " + string(kv.Key) + "=" + kv.Value.Emit())
		}
	}
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	points := 0
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if h, ok := m.Data.(metricdata.Histogram[float64]); ok && m.Name == "http.server.request.duration" {
				for _, p := range h.DataPoints {
					points++
					for _, kv := range p.Attributes.ToSlice() {
						dump.WriteString(" " + string(kv.Key) + "=" + kv.Value.Emit())
						if strings.HasSuffix(string(kv.Key), "request_id") {
							t.Errorf("a metric carries %s", kv.Key)
						}
					}
				}
			}
		}
	}
	if points != 2 || strings.Contains(dump.String(), "SENTINEL") || !strings.Contains(dump.String(), "geminus.request_id=req_") {
		t.Errorf("%d points: %s", points, dump.String())
	}
}

// The response writer AccessLog wraps still reaches http.ResponseController
// (the progress stream flushes and sets write deadlines through it), and a
// handler that writes without WriteHeader is a 200.
func TestAccessLogKeepsTheWriter(t *testing.T) {
	var logs bytes.Buffer
	log := observability.NewLogger(&logs, "api")
	h := AccessLog(log, Handler(log, func(w http.ResponseWriter, r *http.Request) error {
		rc := http.NewResponseController(w)
		if _, err := w.Write([]byte("data: 1\n\n")); err != nil {
			return err
		}
		return rc.Flush()
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/stream", nil))
	if !w.Flushed || w.Code != 200 {
		t.Errorf("flushed %v, %d", w.Flushed, w.Code)
	}
	if l := lines(t, &logs)[0]; l["http_status"] != float64(200) || l["response_bytes"] != float64(9) {
		t.Errorf("%v", l)
	}
}

// A panic AccessLog sees (http.ErrAbortHandler, which Handler passes on)
// still logs its line and keeps its meaning.
func TestAccessLogAndAbort(t *testing.T) {
	var logs bytes.Buffer
	log := observability.NewLogger(&logs, "api")
	h := AccessLog(log, Handler(log, func(http.ResponseWriter, *http.Request) error { panic(http.ErrAbortHandler) }))
	func() {
		defer func() {
			if recover() != http.ErrAbortHandler {
				t.Error("ErrAbortHandler was swallowed")
			}
		}()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/x", nil))
	}()
	if !strings.Contains(logs.String(), `"msg":"request"`) {
		t.Errorf("no access line: %s", logs.String())
	}
}
