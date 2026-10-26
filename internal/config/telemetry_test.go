package config

import (
	"slices"
	"strings"
	"testing"
)

// T-035 (D-095 (b), ADR-028): telemetry is off unless an operator names an
// OTLP/HTTP endpoint; the OpenTelemetry variables mean what they mean
// elsewhere, and a value that is not one is refused without being echoed.
func TestLoadTelemetry(t *testing.T) {
	for name, c := range map[string]struct {
		env             map[string]string
		traces, metrics string
	}{
		"nothing set": {nil, "", ""},
		"a base endpoint": {map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector:4318"},
			"http://collector:4318/v1/traces", "http://collector:4318/v1/metrics"},
		"a base endpoint with a path and a slash": {map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "https://otel.example/ingest/"},
			"https://otel.example/ingest/v1/traces", "https://otel.example/ingest/v1/metrics"},
		"signal endpoints, used as they are": {map[string]string{"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "https://t.example/x",
			"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "https://m.example/y"}, "https://t.example/x", "https://m.example/y"},
		"a signal endpoint wins over the base": {map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://c:4318",
			"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "http://t:4318/v1/traces"}, "http://t:4318/v1/traces", "http://c:4318/v1/metrics"},
		"the SDK disabled": {map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://c:4318", "OTEL_SDK_DISABLED": "TRUE"}, "", ""},
		"the SDK not disabled": {map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://c:4318", "OTEL_SDK_DISABLED": "false"},
			"http://c:4318/v1/traces", "http://c:4318/v1/metrics"},
		"traces off": {map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://c:4318", "OTEL_TRACES_EXPORTER": "none"}, "", "http://c:4318/v1/metrics"},
		"metrics off": {map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://c:4318", "OTEL_METRICS_EXPORTER": "none",
			"OTEL_TRACES_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf"}, "http://c:4318/v1/traces", ""},
		// An exporter named without an endpoint still sends nothing: there
		// is no default endpoint (not even localhost).
		"an exporter without an endpoint": {map[string]string{"OTEL_TRACES_EXPORTER": "otlp"}, "", ""},
	} {
		got, err := LoadTelemetry(env(c.env))
		if err != nil || got.TracesEndpoint != c.traces || got.MetricsEndpoint != c.metrics || got.Enabled() != (c.traces != "" || c.metrics != "") {
			t.Errorf("%s: %+v %v", name, got, err)
		}
	}
	const secret = "pw-SENTINEL"
	for name, c := range map[string]struct {
		env  map[string]string
		vars []string
	}{
		"not a URL":           {map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "collector:4318"}, []string{"OTEL_EXPORTER_OTLP_ENDPOINT:invalid"}},
		"another scheme":      {map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "grpc://c:4317"}, []string{"OTEL_EXPORTER_OTLP_ENDPOINT:invalid"}},
		"no host":             {map[string]string{"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "http:///v1/traces"}, []string{"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT:invalid"}},
		"credentials in it":   {map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "https://u:" + secret + "@c"}, []string{"OTEL_EXPORTER_OTLP_ENDPOINT:invalid"}},
		"a query":             {map[string]string{"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "https://c/v1/metrics?api_key=" + secret}, []string{"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT:invalid"}},
		"a fragment":          {map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "https://c/#" + secret}, []string{"OTEL_EXPORTER_OTLP_ENDPOINT:invalid"}},
		"gRPC":                {map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://c:4317", "OTEL_EXPORTER_OTLP_PROTOCOL": "grpc"}, []string{"OTEL_EXPORTER_OTLP_PROTOCOL:not_implemented"}},
		"another exporter":    {map[string]string{"OTEL_TRACES_EXPORTER": "zipkin", "OTEL_METRICS_EXPORTER": "prometheus"}, []string{"OTEL_TRACES_EXPORTER:not_implemented", "OTEL_METRICS_EXPORTER:not_implemented"}},
		"an odd disable flag": {map[string]string{"OTEL_SDK_DISABLED": "yes"}, []string{"OTEL_SDK_DISABLED:invalid"}},
	} {
		_, err := LoadTelemetry(env(c.env))
		if err == nil || !slices.Equal(codes(t, err), c.vars) {
			t.Errorf("%s: %v", name, err)
		}
		if err != nil && strings.Contains(err.Error(), "SENTINEL") {
			t.Errorf("%s: the error echoes the value: %v", name, err)
		}
	}
}
