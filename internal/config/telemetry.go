package config

import (
	"errors"
	"net"
	"net/url"
	"strings"
	"unicode"
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
//     exporter is built in);
//   - OTEL_EXPORTER_OTLP_HEADERS, _TRACES_HEADERS and _METRICS_HEADERS,
//     which the exporters read themselves: they are only checked here
//     (validHeaders), so that a value the exporters could not parse, and
//     would complain about with the value, stops the process instead.
//
// An endpoint is an absolute https URL with a host, or an http URL whose
// host is this machine (localhost, 127.0.0.0/8, ::1), without user
// information, query or fragment: credentials for the collector go in the
// headers, never in clear text across a network, and the headers' values
// reach no log line (the SDK's diagnostics go through the filtered log,
// internal/observability/telemetry/export). All problems are reported
// together, never with their values.
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
	for _, name := range []string{"OTEL_EXPORTER_OTLP_HEADERS", "OTEL_EXPORTER_OTLP_TRACES_HEADERS", "OTEL_EXPORTER_OTLP_METRICS_HEADERS"} {
		if !validHeaders(getenv(name)) {
			errs = append(errs, &Error{name, CodeInvalid, "must be comma-separated name=value pairs: names of HTTP token characters, values URL-encoded"})
		}
	}
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
	return err == nil && (u.Scheme == "https" || u.Scheme == "http" && loopback(u.Hostname())) && u.Host != "" && u.User == nil &&
		u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && !strings.Contains(raw, "#") && u.Opaque == ""
}

// loopback reports whether host is this machine: localhost, 127.0.0.0/8 or
// ::1. Only there may a collector be plain http (review L6): anywhere else
// the headers (the collector's credentials) would cross a network in clear
// text.
func loopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// validHeaders reports whether v is what the OTLP exporters parse
// OTEL_EXPORTER_OTLP_*HEADERS as without complaint (go.opentelemetry.io/
// otel's envconfig, v1.47.0): after trimming, empty, or comma-separated
// name=value pairs whose trimmed name is one or more HTTP token characters
// and whose value URL-decodes (review M1). The exporters would log a pair
// they cannot parse, with its value: such a value stops the process here
// instead, and the error does not repeat it.
func validHeaders(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return true
	}
	for _, pair := range strings.Split(v, ",") {
		name, value, ok := strings.Cut(pair, "=")
		if !ok || !token(strings.TrimSpace(name)) {
			return false
		}
		if _, err := url.PathUnescape(value); err != nil {
			return false
		}
	}
	return true
}

// token reports whether s is an HTTP token (RFC 9110 §5.6.2), as the
// exporters check a header name.
func token(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c > unicode.MaxASCII || !(unicode.IsLetter(c) || unicode.IsDigit(c) || strings.ContainsRune("!#$%&'*+-.^_`|~", c)) {
			return false
		}
	}
	return true
}
