package config

import (
	"errors"
	"net/url"
	"strings"
)

// Telemetry is where the process sends traces and metrics (T-035, D-095
// (b), ADR-028): nowhere, unless an operator names an OTLP/HTTP endpoint.
// There is no default endpoint, not even localhost, so a deployment that
// sets nothing sends nothing.
type Telemetry struct {
	// TracesEndpoint and MetricsEndpoint are the full OTLP/HTTP URLs of
	// each signal ("" when the signal is off).
	TracesEndpoint, MetricsEndpoint string
}

// Enabled reports whether any signal is exported.
func (t Telemetry) Enabled() bool { return t.TracesEndpoint != "" || t.MetricsEndpoint != "" }

// LoadTelemetry reads the OpenTelemetry SDK's variables, with their usual
// meaning:
//   - OTEL_EXPORTER_OTLP_ENDPOINT, a base URL to which each signal's path
//     (v1/traces, v1/metrics) is appended;
//   - OTEL_EXPORTER_OTLP_TRACES_ENDPOINT and _METRICS_ENDPOINT, a signal's
//     full URL, which wins over the base;
//   - OTEL_SDK_DISABLED=true, which turns everything off;
//   - OTEL_TRACES_EXPORTER and OTEL_METRICS_EXPORTER, "otlp" (or unset) or
//     "none";
//   - OTEL_EXPORTER_OTLP_PROTOCOL, which may only be http/protobuf (no gRPC
//     exporter is built in).
//
// An endpoint is an absolute http or https URL with a host and without
// user information, query or fragment: credentials for the collector go
// in OTEL_EXPORTER_OTLP_HEADERS, which the exporter reads itself and no
// log line shows. All problems are reported together, never with their
// values.
func LoadTelemetry(getenv func(string) string) (Telemetry, error) {
	var errs []error
	disabled := false
	switch v := strings.ToLower(strings.TrimSpace(getenv("OTEL_SDK_DISABLED"))); v {
	case "", "false":
	case "true":
		disabled = true
	default:
		errs = append(errs, &Error{"OTEL_SDK_DISABLED", CodeInvalid, "must be true or false"})
	}
	switch p := getenv("OTEL_EXPORTER_OTLP_PROTOCOL"); p {
	case "", "http/protobuf":
	default:
		errs = append(errs, &Error{"OTEL_EXPORTER_OTLP_PROTOCOL", CodeNotImplemented, "only http/protobuf is built in"})
	}
	on := func(name string) bool {
		switch v := getenv(name); v {
		case "", "otlp":
			return true
		case "none":
			return false
		}
		errs = append(errs, &Error{name, CodeNotImplemented, "only otlp or none is built in"})
		return false
	}
	tracesOn, metricsOn := on("OTEL_TRACES_EXPORTER"), on("OTEL_METRICS_EXPORTER")
	endpoint := func(name string) string {
		raw := getenv(name)
		if raw == "" {
			return ""
		}
		if !validEndpoint(raw) {
			errs = append(errs, &Error{name, CodeInvalid,
				"must be an absolute http or https URL with a host, without user information, query or fragment"})
			return ""
		}
		return raw
	}
	base := endpoint("OTEL_EXPORTER_OTLP_ENDPOINT")
	traces, metrics := endpoint("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"), endpoint("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT")
	if len(errs) > 0 {
		return Telemetry{}, errors.Join(errs...)
	}
	if disabled {
		return Telemetry{}, nil
	}
	join := func(signal string) string {
		if base == "" {
			return ""
		}
		return strings.TrimSuffix(base, "/") + "/v1/" + signal
	}
	var t Telemetry
	if tracesOn {
		t.TracesEndpoint = traces
		if t.TracesEndpoint == "" {
			t.TracesEndpoint = join("traces")
		}
	}
	if metricsOn {
		t.MetricsEndpoint = metrics
		if t.MetricsEndpoint == "" {
			t.MetricsEndpoint = join("metrics")
		}
	}
	return t, nil
}

func validEndpoint(raw string) bool {
	if len(raw) > 2048 {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil && u.RawQuery == "" &&
		!u.ForceQuery && u.Fragment == "" && !strings.Contains(raw, "#") && u.Opaque == ""
}
