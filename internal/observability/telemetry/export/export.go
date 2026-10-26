// Package export installs the OpenTelemetry SDK for the process (T-035,
// D-095 (b), ADR-028) when, and only when, the configuration names an
// OTLP/HTTP endpoint (config.LoadTelemetry): spans through a batch
// processor and metrics through a periodic reader, each to its own
// endpoint, under a resource that names only the service (plus the other
// attributes the operator sets in OTEL_RESOURCE_ATTRIBUTES: the SDK merges
// them in, and service.name stays the service's, whatever OTEL_SERVICE_NAME
// or OTEL_RESOURCE_ATTRIBUTES say). Without
// an endpoint it does nothing: no exporter exists and no data leaves the
// process. The SDK's own errors and log lines go through the process's
// filtered logger. Only cmd/api and cmd/worker link it (architecture rule
// otel-sdk-only-in-export).
package export

import (
	"context"
	"errors"
	"log/slog"

	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/kcl1104/geminus/internal/config"
	"github.com/kcl1104/geminus/internal/observability/telemetry"
)

// Setup installs the SDK for service ("geminus-api", "geminus-worker") on
// cfg's endpoints and returns the function that flushes what is buffered,
// stops the SDK and puts the no-op telemetry back. When cfg exports
// nothing, it installs nothing and the function does nothing.
func Setup(ctx context.Context, cfg config.Telemetry, service string, log *slog.Logger) (shutdown func(context.Context) error, err error) {
	if !cfg.Enabled() {
		return func(context.Context) error { return nil }, nil
	}
	// The SDK reports export failures and its own diagnostics through
	// OpenTelemetry's global handler and logger, from the moment the
	// exporters are built (they parse OTEL_EXPORTER_OTLP_* then and complain
	// about a value they cannot parse, with the value): both are installed
	// first, so everything goes through the filtered log (D-094), never to
	// stderr through OpenTelemetry's default logger (review M1).
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		log.Warn("telemetry export failed", "error", err.Error())
	}))
	otel.SetLogger(logr.FromSlogHandler(diagnostics{log.Handler()}))
	res := resource.NewSchemaless(attribute.String("service.name", service))
	var tp trace.TracerProvider = tracenoop.NewTracerProvider()
	var mp metric.MeterProvider = metricnoop.NewMeterProvider()
	var stops []func(context.Context) error
	stop := func(ctx context.Context) error {
		var errs []error
		for _, s := range stops {
			errs = append(errs, s(ctx))
		}
		return errors.Join(errs...)
	}
	if cfg.TracesEndpoint != "" {
		exp, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(cfg.TracesEndpoint))
		if err != nil {
			return nil, err
		}
		sdk := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp), sdktrace.WithResource(res))
		tp, stops = sdk, append(stops, sdk.Shutdown)
	}
	if cfg.MetricsEndpoint != "" {
		exp, err := otlpmetrichttp.New(ctx, otlpmetrichttp.WithEndpointURL(cfg.MetricsEndpoint))
		if err != nil {
			return nil, errors.Join(err, stop(ctx))
		}
		sdk := sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp)), sdkmetric.WithResource(res))
		mp, stops = sdk, append(stops, sdk.Shutdown)
	}
	tel, err := telemetry.New(tp, mp)
	if err != nil {
		return nil, errors.Join(err, stop(ctx))
	}
	prev := telemetry.Install(tel)
	return func(ctx context.Context) error {
		telemetry.Install(prev)
		return stop(ctx)
	}, nil
}

// diagnostics is the slog handler OpenTelemetry's own logger writes to,
// through logr (review L7): the SDK's Error lines keep their error, which
// logr names "err", as "error", the free-text key the filter sanitizes, so
// an operator sees what went wrong; its Warn (logr V(1), slog level -1) is
// a WARN line; its Info and Debug (V(4), V(8)) are not logged. Messages
// start with "telemetry: ". Every other key the SDK adds (input, value,
// key: the raw values it could not parse) is unregistered, so the filter
// redacts it.
type diagnostics struct{ inner slog.Handler }

// level is the slog level a logr level of the SDK is logged at, and false
// for the levels that are not logged.
func level(l slog.Level) (slog.Level, bool) {
	switch {
	case l >= slog.LevelInfo:
		return l, true
	case l == -1:
		return slog.LevelWarn, true
	}
	return l, false
}

func (d diagnostics) Enabled(ctx context.Context, l slog.Level) bool {
	l, ok := level(l)
	return ok && d.inner.Enabled(ctx, l)
}

func (d diagnostics) Handle(ctx context.Context, r slog.Record) error {
	l, ok := level(r.Level)
	if !ok {
		return nil
	}
	out := slog.NewRecord(r.Time, l, "telemetry: "+r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(renamed(a))
		return true
	})
	return d.inner.Handle(ctx, out)
}

func (d diagnostics) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		out[i] = renamed(a)
	}
	return diagnostics{d.inner.WithAttrs(out)}
}

func (d diagnostics) WithGroup(name string) slog.Handler { return diagnostics{d.inner.WithGroup(name)} }

// renamed is a, with logr's "err" as "error".
func renamed(a slog.Attr) slog.Attr {
	if a.Key == "err" {
		a.Key = "error"
	}
	return a
}
