package telemetry_test

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/observability"
	"github.com/kcl1104/geminus/internal/observability/telemetry"
)

// recorded is what the in-memory SDK saw.
type recorded struct {
	spans  *tracetest.SpanRecorder
	reader *sdkmetric.ManualReader
}

func install(t *testing.T) recorded {
	t.Helper()
	r := recorded{spans: tracetest.NewSpanRecorder(), reader: sdkmetric.NewManualReader()}
	tel, err := telemetry.New(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(r.spans)), sdkmetric.NewMeterProvider(sdkmetric.WithReader(r.reader)))
	if err != nil {
		t.Fatal(err)
	}
	prev := telemetry.Install(tel)
	t.Cleanup(func() { telemetry.Install(prev) })
	return r
}

// point is one metric data point: its metric and its attributes.
type point struct {
	metric string
	attrs  map[string]string
	value  float64
}

func (r recorded) points(t *testing.T) []point {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := r.reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	var out []point
	add := func(name string, set attribute.Set, v float64) {
		m := map[string]string{}
		for _, kv := range set.ToSlice() {
			m[string(kv.Key)] = kv.Value.Emit()
		}
		out = append(out, point{name, m, v})
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch d := m.Data.(type) {
			case metricdata.Histogram[float64]:
				for _, p := range d.DataPoints {
					add(m.Name, p.Attributes, p.Sum)
				}
			case metricdata.Sum[int64]:
				for _, p := range d.DataPoints {
					add(m.Name, p.Attributes, float64(p.Value))
				}
			default:
				t.Fatalf("%s: unexpected data %T", m.Name, d)
			}
		}
	}
	return out
}

// dump is everything recorded, as text, for the sentinel checks.
func (r recorded) dump(t *testing.T) string {
	var b strings.Builder
	for _, s := range r.spans.Ended() {
		fmt.Fprintf(&b, "span %q", s.Name())
		for _, kv := range s.Attributes() {
			fmt.Fprintf(&b, " %s=%q", kv.Key, kv.Value.Emit())
		}
		for _, e := range s.Events() {
			fmt.Fprintf(&b, " event %q", e.Name)
		}
		fmt.Fprintf(&b, " status %q\n", s.Status().Description)
	}
	for _, p := range r.points(t) {
		fmt.Fprintf(&b, "metric %s %v\n", p.metric, p.attrs)
	}
	return b.String()
}

// metricKeys are the only attributes a metric may carry (D-095 (c)): low
// cardinality, never an ID.
var metricKeys = map[string]bool{"http.request.method": true, "http.route": true, "http.response.status_code": true,
	"geminus.job.type": true, "geminus.outcome": true, "geminus.code": true, "gen_ai.system": true, "gen_ai.request.model": true,
	"geminus.ai.role": true, "gen_ai.token.type": true, "geminus.import.stage": true,
	"geminus.ai.limit": true} // T-053: which spend limit refused a call

func checkMetrics(t *testing.T, ps []point) {
	t.Helper()
	for _, p := range ps {
		for k := range p.attrs {
			if !metricKeys[k] {
				t.Errorf("%s carries %s", p.metric, k)
			}
		}
	}
}

func find(ps []point, metric string) []point {
	var out []point
	for _, p := range ps {
		if p.metric == metric {
			out = append(out, p)
		}
	}
	return out
}

// Until providers are installed, telemetry records nothing: the spans are
// not recording and have no trace ID.
func TestNothingIsRecordedByDefault(t *testing.T) {
	tel := telemetry.Current()
	ctx, span := tel.StartRequest(context.Background(), "GET")
	tel.EndRequest(ctx, span, "GET", "GET /v1/projects", 200, time.Millisecond, ids.New("req"))
	if span.IsRecording() || span.SpanContext().IsValid() {
		t.Error("the default telemetry records")
	}
	_, job := tel.StartJob(context.Background(), "simulation", ids.New("job"), 1, ids.New("req"))
	if job.IsRecording() {
		t.Error("the default job span records")
	}
}

func TestRequests(t *testing.T) {
	r := install(t)
	tel := telemetry.Current()
	req := ids.New("req")
	ctx, span := tel.StartRequest(context.Background(), "POST")
	tel.EndRequest(ctx, span, "POST", "POST /v1/models/{id}/versions", 201, 12*time.Millisecond, req)
	ctx, span = tel.StartRequest(context.Background(), "GET")
	tel.EndRequest(ctx, span, "GET", "unmatched", 503, time.Millisecond, ids.New("req"))
	spans := r.spans.Ended()
	if len(spans) != 2 || spans[0].Name() != "POST /v1/models/{id}/versions" || spans[1].Name() != "unmatched" {
		t.Fatalf("%v", r.dump(t))
	}
	attrs := map[string]string{}
	for _, kv := range spans[0].Attributes() {
		attrs[string(kv.Key)] = kv.Value.Emit()
	}
	if attrs["http.request.method"] != "POST" || attrs["http.route"] != "POST /v1/models/{id}/versions" ||
		attrs["http.response.status_code"] != "201" || attrs["geminus.request_id"] != req || spans[0].SpanKind().String() != "server" {
		t.Errorf("%v", attrs)
	}
	if spans[0].Status().Code.String() != "Unset" || spans[1].Status().Code.String() != "Error" {
		t.Errorf("status %v %v", spans[0].Status(), spans[1].Status())
	}
	ps := r.points(t)
	checkMetrics(t, ps)
	if got := find(ps, "http.server.request.duration"); len(got) != 2 || got[0].attrs["http.route"] == "" {
		t.Errorf("%v", ps)
	}
}

func TestJobs(t *testing.T) {
	r := install(t)
	tel := telemetry.Current()
	job, req := ids.New("job"), ids.New("req")
	ctx, span := tel.StartJob(context.Background(), "simulation", job, 2, req)
	if !span.IsRecording() {
		t.Fatal("not recording")
	}
	tel.Engine(ctx, time.Now().Add(-time.Second), "completed", 12345)
	tel.EndJob(span, "simulation", "completed", "", time.Second)
	_, span = tel.StartJob(context.Background(), "model_proposal", ids.New("job"), 1, "")
	tel.EndJob(span, "model_proposal", "failed", "AI_RATE_LIMITED", 2*time.Second)
	spans := r.spans.Ended()
	if len(spans) != 3 {
		t.Fatalf("%s", r.dump(t))
	}
	engine, sim := spans[0], spans[1]
	if engine.Name() != "simulation engine" || sim.Name() != "job simulation" || engine.Parent().SpanID() != sim.SpanContext().SpanID() ||
		engine.EndTime().Sub(engine.StartTime()) < 900*time.Millisecond {
		t.Errorf("%s", r.dump(t))
	}
	if spans[2].Status().Code.String() != "Error" {
		t.Errorf("a failed job's status %v", spans[2].Status())
	}
	ps := r.points(t)
	checkMetrics(t, ps)
	if got := find(ps, "geminus.job.attempt.duration"); len(got) != 2 {
		t.Errorf("%v", ps)
	}
	if got := find(ps, "geminus.simulation.engine.events"); len(got) != 1 || got[0].value != 12345 || got[0].attrs["geminus.outcome"] != "completed" {
		t.Errorf("%v", got)
	}
}

// D-095 (c): a request and a job attempt are each a new root, whatever span
// the context already carries (a client's or a library's); the work
// recorded afterwards under them is an Error span when it failed.
func TestRootsAndFailedWork(t *testing.T) {
	r := install(t)
	tel := telemetry.Current()
	foreign := trace.TraceID{0x4b, 0xf9}
	inherited := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: foreign, SpanID: trace.SpanID{0x01}, TraceFlags: trace.FlagsSampled, Remote: true}))
	ctx, span := tel.StartRequest(inherited, "GET")
	tel.EndRequest(ctx, span, "GET", "GET /v1/projects", 200, time.Millisecond, ids.New("req"))
	jctx, job := tel.StartJob(inherited, "simulation", ids.New("job"), 1, "")
	tel.Engine(jctx, time.Now().Add(-time.Millisecond), "failed", 7)
	tel.ProviderCall(jctx, time.Now(), "fake", "fake", "modeler", "AI_TIMEOUT", 0, 0)
	tel.Import(jctx, time.Now(), "profile", "failed", 0)
	tel.EndJob(job, "simulation", "failed", "ENGINE_FAILED", time.Second)
	spans := r.spans.Ended()
	if len(spans) != 5 {
		t.Fatalf("%s", r.dump(t))
	}
	for _, s := range spans {
		switch s.Name() {
		case "GET /v1/projects", "job simulation":
			if s.Parent().IsValid() || s.SpanContext().TraceID() == foreign {
				t.Errorf("%s is not a new root: parent %v", s.Name(), s.Parent())
			}
		default:
			if s.Status().Code != codes.Error || s.Parent().SpanID() != job.SpanContext().SpanID() {
				t.Errorf("%s: %v, parent %v", s.Name(), s.Status(), s.Parent())
			}
		}
	}
}

// Review L5: an attempt's span is an Error unless the attempt completed or
// was cancelled: a retry, a lost lease and an end the worker could not
// record are failures of that attempt too; the outcome says which.
func TestJobOutcomes(t *testing.T) {
	r := install(t)
	tel := telemetry.Current()
	want := map[string]string{"completed": "Unset", "cancelled": "Unset", "failed": "Error", "requeued": "Error", "lease_lost": "Error",
		"not_recorded": "Error"}
	for how := range want {
		_, span := tel.StartJob(context.Background(), "simulation", ids.New("job"), 1, "")
		tel.EndJob(span, "simulation", how, "", time.Millisecond)
	}
	spans := r.spans.Ended()
	if len(spans) != len(want) {
		t.Fatalf("%s", r.dump(t))
	}
	for _, s := range spans {
		var how string
		for _, kv := range s.Attributes() {
			if kv.Key == "geminus.outcome" {
				how = kv.Value.Emit()
			}
		}
		if code, ok := want[how]; !ok || s.Status().Code.String() != code {
			t.Errorf("outcome %q: status %v", how, s.Status())
		}
	}
	got := map[string]bool{}
	for _, p := range find(r.points(t), "geminus.job.attempt.duration") {
		got[p.attrs["geminus.outcome"]] = true
	}
	if len(got) != len(want) {
		t.Errorf("duration points by outcome: %v", got)
	}
}

func TestProviderCallsAndImports(t *testing.T) {
	r := install(t)
	tel := telemetry.Current()
	tel.ProviderCall(context.Background(), time.Now().Add(-3*time.Second), "anthropic", "claude-opus-5-5", "modeler", "OK", 1200, 300)
	tel.ProviderCall(context.Background(), time.Now(), "fake", "fake", "scenario", "AI_TIMEOUT", 0, 0)
	tel.Import(context.Background(), time.Now().Add(-time.Second), "profile", "profiled", 1000)
	ps := r.points(t)
	checkMetrics(t, ps)
	tokens := find(ps, "geminus.ai.tokens")
	if len(tokens) != 4 { // two calls, input and output each
		t.Fatalf("%v", tokens)
	}
	for _, p := range tokens {
		want := map[string]float64{"input": 1200, "output": 300}[p.attrs["gen_ai.token.type"]]
		if p.attrs["gen_ai.system"] == "fake" {
			want = 0
		}
		if p.value != want {
			t.Errorf("%v", p)
		}
	}
	if got := find(ps, "gen_ai.client.operation.duration"); len(got) != 2 {
		t.Errorf("%v", got)
	}
	if got := find(ps, "geminus.import.rows"); len(got) != 1 || got[0].value != 1000 || got[0].attrs["geminus.import.stage"] != "profile" {
		t.Errorf("%v", got)
	}
	if names := r.dump(t); !strings.Contains(names, `span "ai provider call"`) || !strings.Contains(names, `span "import profile"`) {
		t.Errorf("%s", names)
	}
}

// T-053: what settled live AI calls cost, and the calls a spend limit
// refused, by model, role, outcome and limit; never an ID.
func TestSpend(t *testing.T) {
	r := install(t)
	tel := telemetry.Current()
	tel.Spend(context.Background(), "claude-opus-5-5", "modeler", "usage", 84_000_000)
	tel.Spend(context.Background(), "claude-opus-5-5", "modeler", "usage", 16_000_000)
	tel.Spend(context.Background(), "claude-opus-5-5", "scenario", "unknown", 5)
	tel.Spend(context.Background(), "claude-opus-5-5", "scenario", "none", -3) // never negative
	tel.SpendRefused(context.Background(), "claude-opus-5-5", "modeler", "organization_day")
	tel.SpendRefused(context.Background(), "aip_01M4YDQK00TA00000000000001", "modeler", "org_01M4YDQK00TA00000000000000")
	ps := r.points(t)
	checkMetrics(t, ps)
	got := map[string]float64{}
	for _, p := range find(ps, "geminus.ai.spend") {
		got[p.attrs["geminus.ai.role"]+"/"+p.attrs["geminus.outcome"]] = p.value
	}
	if want := map[string]float64{"modeler/usage": 100_000_000, "scenario/unknown": 5, "scenario/none": 0}; !maps.Equal(got, want) {
		t.Errorf("spend %v, want %v", got, want)
	}
	refused := find(ps, "geminus.ai.spend.refusals")
	if len(refused) != 2 {
		t.Fatalf("%v", refused)
	}
	for _, p := range refused {
		if p.attrs["geminus.ai.limit"] == "organization_day" && p.value != 1 {
			t.Errorf("%v", p)
		}
	}
	if out := r.dump(t); strings.Contains(out, "org_01M4") || strings.Contains(out, "aip_01M4") {
		t.Errorf("an ID is recorded:\n%s", out)
	}
}

// TEST-027 for traces and metrics: what a caller hands over is recorded
// only when it has its attribute's shape (D-094 (f)); a customer value, a
// credential or free text becomes [redacted], and a metric never carries
// an ID, so its cardinality stays bounded.
func TestAttributesAreFiltered(t *testing.T) {
	r := install(t)
	tel := telemetry.Current()
	const row = "CASE-SENTINEL-7731,FCT_FAIL"
	ctx, span := tel.StartRequest(context.Background(), "BREW")
	tel.EndRequest(ctx, span, "BREW", "/v1/things/"+row, 200, time.Millisecond, "req_"+row)
	_, job := tel.StartJob(context.Background(), "job "+row, "job_"+row, 1, "sentinel@example.com")
	tel.EndJob(job, "job "+row, "free text "+row, "not a code "+row, time.Millisecond)
	tel.ProviderCall(context.Background(), time.Now(), "sk-ant-api03-SENTINELsentinel0123456789", "model "+row, "Bearer eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJTRU5USU5FTCJ9.U0VOVElORUw", row, 1, 1)
	tel.Import(context.Background(), time.Now(), row, row, 1)
	out := r.dump(t)
	for _, f := range []string{"SENTINEL", "sentinel", "FCT_FAIL", "eyJ", "BREW"} {
		if strings.Contains(out, f) {
			t.Errorf("recorded %q:\n%s", f, out)
		}
	}
	if !strings.Contains(out, observability.Redacted) {
		t.Errorf("nothing was redacted:\n%s", out)
	}
	checkMetrics(t, r.points(t))
}
