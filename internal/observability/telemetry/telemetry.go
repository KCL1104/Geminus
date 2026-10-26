// Package telemetry is the API's and the worker's traces and metrics
// (T-035, REQ-020; D-095, ADR-028) through the OpenTelemetry API: the spans
// and instruments the HTTP layer, the job loop and the handlers record.
// Every attribute comes from a closed list and passes the shapes of the
// log filter (observability.Fits, D-094 (f)): a value without its shape is
// recorded as "[redacted]". Metrics carry low-cardinality attributes only
// (method, route, status, job type, outcome, code, provider, model, role),
// never an ID; spans may also carry the request and job IDs a log line
// carries (D-093).
//
// Until Install is given real providers (internal/observability/telemetry/
// export does that, and only when an operator configured an OTLP endpoint)
// the telemetry is the OpenTelemetry no-op: nothing is recorded and nothing
// leaves the process. It never uses OpenTelemetry's global providers, so no
// library can export through them.
package telemetry

import (
	"context"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/kcl1104/geminus/internal/observability"
)

// scope names the instrumentation.
const scope = "github.com/kcl1104/geminus"

// Telemetry is the tracer and the instruments, on one pair of providers.
type Telemetry struct {
	tracer         trace.Tracer
	httpDuration   metric.Float64Histogram
	jobDuration    metric.Float64Histogram
	engineDuration metric.Float64Histogram
	engineEvents   metric.Int64Counter
	aiDuration     metric.Float64Histogram
	aiTokens       metric.Int64Counter
	aiSpend        metric.Int64Counter
	aiRefusals     metric.Int64Counter
	aiWarnings     metric.Int64Counter
	importDuration metric.Float64Histogram
	importRows     metric.Int64Counter
}

// New builds the tracer and the instruments on tp and mp.
func New(tp trace.TracerProvider, mp metric.MeterProvider) (*Telemetry, error) {
	m := mp.Meter(scope)
	t := &Telemetry{tracer: tp.Tracer(scope)}
	var err error
	seconds := func(name, desc string) metric.Float64Histogram {
		if err != nil {
			return nil
		}
		var h metric.Float64Histogram
		h, err = m.Float64Histogram(name, metric.WithUnit("s"), metric.WithDescription(desc))
		return h
	}
	counter := func(name, unit, desc string) metric.Int64Counter {
		if err != nil {
			return nil
		}
		var c metric.Int64Counter
		c, err = m.Int64Counter(name, metric.WithUnit(unit), metric.WithDescription(desc))
		return c
	}
	t.httpDuration = seconds("http.server.request.duration", "Duration of HTTP server requests.")
	t.jobDuration = seconds("geminus.job.attempt.duration", "Duration of job attempts, from the claim to their end.")
	t.engineDuration = seconds("geminus.simulation.engine.duration", "Duration of simulation engine runs.")
	t.engineEvents = counter("geminus.simulation.engine.events", "{event}", "Events the simulation engine processed.")
	t.aiDuration = seconds("gen_ai.client.operation.duration", "Duration of AI provider calls.")
	t.aiTokens = counter("geminus.ai.tokens", "{token}", "Tokens AI provider calls used.")
	t.aiSpend = counter("geminus.ai.spend", "{nanoUSD}", "What settled live AI calls cost, in nano-USD (T-053).")
	t.aiRefusals = counter("geminus.ai.spend.refusals", "{call}", "Live AI calls a spend limit refused before they were made (T-053).")
	t.aiWarnings = counter("geminus.ai.spend.warnings", "{call}",
		"Settled live AI calls whose reservation's bound did not hold: cost above the reservation, or an answer naming no known inference geo (T-053).")
	t.importDuration = seconds("geminus.import.duration", "Duration of import profiling and validation.")
	t.importRows = counter("geminus.import.rows", "{row}", "Rows import profiling and validation read.")
	if err != nil {
		return nil, err
	}
	return t, nil
}

var current atomic.Pointer[Telemetry]

func init() {
	t, err := New(tracenoop.NewTracerProvider(), metricnoop.NewMeterProvider())
	if err != nil {
		panic(err) // the no-op providers never fail
	}
	current.Store(t)
}

// Install makes t the process's telemetry and returns the one before.
func Install(t *Telemetry) *Telemetry { return current.Swap(t) }

// Current is the process's telemetry: the no-op until Install.
func Current() *Telemetry { return current.Load() }

// str is key with value when value has kind's shape, [redacted] otherwise.
func str(key string, kind observability.Kind, value string) attribute.KeyValue {
	if !observability.Fits(kind, value) {
		value = observability.Redacted
	}
	return attribute.String(key, value)
}

func method(m string) attribute.KeyValue {
	return str("http.request.method", observability.KindMethod, m)
}
func route(r string) attribute.KeyValue     { return str("http.route", observability.KindRoute, r) }
func jobType(j string) attribute.KeyValue   { return str("geminus.job.type", observability.KindToken, j) }
func outcome(o string) attribute.KeyValue   { return str("geminus.outcome", observability.KindToken, o) }
func code(c string) attribute.KeyValue      { return str("geminus.code", observability.KindCode, c) }
func id(key, v string) attribute.KeyValue   { return str(key, observability.KindID, v) }
func status(s int) attribute.KeyValue       { return attribute.Int("http.response.status_code", s) }
func name(key, v string) attribute.KeyValue { return str(key, observability.KindName, v) }

// StartRequest starts the server span of an HTTP request. It is a new
// root: a client's trace context is never read (D-095 (c)).
func (t *Telemetry) StartRequest(ctx context.Context, m string) (context.Context, trace.Span) {
	mt := method(m)
	return t.tracer.Start(ctx, "HTTP "+mt.Value.AsString(), trace.WithSpanKind(trace.SpanKindServer), trace.WithNewRoot(),
		trace.WithAttributes(mt))
}

// EndRequest names span by the route that served the request, records its
// outcome and ends it, and records the request's duration.
func (t *Telemetry) EndRequest(ctx context.Context, span trace.Span, m, r string, statusCode int, d time.Duration, requestID string) {
	rt := route(r)
	span.SetName(rt.Value.AsString())
	span.SetAttributes(rt, status(statusCode), id("geminus.request_id", requestID))
	if statusCode >= 500 {
		span.SetStatus(codes.Error, "")
	}
	span.End()
	t.httpDuration.Record(ctx, d.Seconds(), metric.WithAttributes(method(m), rt, status(statusCode)))
}

// StartJob starts the span of one job attempt, with its type, job ID,
// attempt and the request that queued it.
func (t *Telemetry) StartJob(ctx context.Context, typ, jobID string, attempt int, requestID string) (context.Context, trace.Span) {
	jt := jobType(typ)
	return t.tracer.Start(ctx, "job "+jt.Value.AsString(), trace.WithSpanKind(trace.SpanKindConsumer), trace.WithNewRoot(),
		trace.WithAttributes(jt, id("geminus.job_id", jobID), attribute.Int("geminus.job.attempt", attempt), id("geminus.request_id", requestID)))
}

// EndJob ends a job attempt's span with how it ended (completed,
// cancelled, requeued, failed, lease_lost, not_recorded) and its failure
// code, and records the attempt's duration.
func (t *Telemetry) EndJob(span trace.Span, typ, how, failure string, d time.Duration) {
	o, c := outcome(how), code(failure)
	span.SetAttributes(o, c)
	if how != "completed" && how != "cancelled" {
		span.SetStatus(codes.Error, "")
	}
	span.End()
	t.jobDuration.Record(context.Background(), d.Seconds(), metric.WithAttributes(jobType(typ), o, c))
}

// after records a span of work that started at start and ended now, as a
// child of ctx's span: the caller need not carry a span through the work.
func (t *Telemetry) after(ctx context.Context, spanName string, start time.Time, failed bool, attrs ...attribute.KeyValue) {
	_, span := t.tracer.Start(ctx, spanName, trace.WithTimestamp(start), trace.WithAttributes(attrs...))
	if failed {
		span.SetStatus(codes.Error, "")
	}
	span.End()
}

// Engine records a simulation engine run that started at start: its span,
// duration and events, by outcome (completed, cancelled, stopped, failed).
func (t *Telemetry) Engine(ctx context.Context, start time.Time, how string, events int64) {
	o := outcome(how)
	t.after(ctx, "simulation engine", start, how == "failed", o, attribute.Int64("geminus.simulation.events", events))
	t.engineDuration.Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(o))
	t.engineEvents.Add(ctx, events, metric.WithAttributes(o))
}

// ProviderCall records an AI provider call that started at start: its
// span, duration and tokens, by provider, model, role and outcome code
// (OK, or the job failure code). Never the prompt or the answer.
func (t *Telemetry) ProviderCall(ctx context.Context, start time.Time, provider, model, role, outcome string, input, output int64) {
	attrs := []attribute.KeyValue{name("gen_ai.system", provider), name("gen_ai.request.model", model),
		str("geminus.ai.role", observability.KindToken, role), code(outcome)}
	t.after(ctx, "ai provider call", start, outcome != "OK", append(attrs, attribute.Int64("gen_ai.usage.input_tokens", input),
		attribute.Int64("gen_ai.usage.output_tokens", output))...)
	t.aiDuration.Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(attrs...))
	for kind, n := range map[string]int64{"input": input, "output": output} {
		t.aiTokens.Add(ctx, n, metric.WithAttributes(append(attrs[:3:3], attribute.String("gen_ai.token.type", kind))...))
	}
}

// modelName is the model as a metric attribute: a name, never something
// shaped like an ID (a metric carries none).
func modelName(m string) attribute.KeyValue {
	if observability.Fits(observability.KindID, m) {
		m = observability.Redacted
	}
	return name("gen_ai.request.model", m)
}

// Spend records what a settled live AI call cost, by model, role and how
// the amount was found (usage, none, unknown); never an ID.
func (t *Telemetry) Spend(ctx context.Context, model, role, how string, nanoUSD int64) {
	t.aiSpend.Add(ctx, max(nanoUSD, 0), metric.WithAttributes(modelName(model),
		str("geminus.ai.role", observability.KindToken, role), outcome(how)))
}

// SpendRefused records a live AI call a spend limit refused, by model,
// role and the limit (organization_day, day, month).
func (t *Telemetry) SpendRefused(ctx context.Context, model, role, limit string) {
	t.aiRefusals.Add(ctx, 1, metric.WithAttributes(modelName(model),
		str("geminus.ai.role", observability.KindToken, role), str("geminus.ai.limit", observability.KindToken, limit)))
}

// The warnings of a settled live AI call (SpendWarning; T-053 review round
// 1, M2, M3, L1).
const (
	// SpendOverReservation: the call cost more than its reservation held;
	// it was recorded at what it cost.
	SpendOverReservation = "over_reservation"
	// SpendGeoUnknown: the answer named no inference geo this build
	// prices; it was priced at the dearest.
	SpendGeoUnknown = "inference_geo_unknown"
)

// SpendWarning records a settled live AI call whose reservation's bound
// did not hold, by model, role and which warning (SpendOverReservation,
// SpendGeoUnknown).
func (t *Telemetry) SpendWarning(ctx context.Context, model, role, warning string) {
	t.aiWarnings.Add(ctx, 1, metric.WithAttributes(modelName(model),
		str("geminus.ai.role", observability.KindToken, role), str("geminus.ai.warning", observability.KindToken, warning)))
}

// Import records an import stage (profile, validate) that started at
// start: its span, duration and rows, by outcome. Never a column name or
// value.
func (t *Telemetry) Import(ctx context.Context, start time.Time, stage, how string, rows int) {
	st, o := str("geminus.import.stage", observability.KindToken, stage), outcome(how)
	t.after(ctx, "import "+st.Value.AsString(), start, how == "failed", st, o, attribute.Int("geminus.import.rows", rows))
	t.importDuration.Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(st, o))
	t.importRows.Add(ctx, int64(rows), metric.WithAttributes(st, o))
}
