package export_test

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/config"
	"github.com/kcl1104/geminus/internal/observability"
	"github.com/kcl1104/geminus/internal/observability/telemetry/export"
)

// helperEnv makes the test binary the process TestTheSDKWritesNothingRawToStderr
// runs, instead of running tests.
const helperEnv = "GEMINUS_EXPORT_STDERR_HELPER"

func TestMain(m *testing.M) {
	if os.Getenv(helperEnv) == "1" {
		os.Exit(stderrHelper())
	}
	os.Exit(m.Run())
}

// stderrHelper sets the SDK up as cmd/api and cmd/worker do, with the
// filtered log on the process's stderr, and stops it. It runs in a fresh
// process, so OpenTelemetry's own logger is still its default (stdr, which
// writes to the process's stderr) when Setup begins.
func stderrHelper() int {
	log := observability.NewLogger(os.Stderr, "helper")
	base := os.Getenv("GEMINUS_EXPORT_STDERR_COLLECTOR")
	shutdown, err := export.Setup(context.Background(), config.Telemetry{TracesEndpoint: base + "/v1/traces", MetricsEndpoint: base + "/v1/metrics"},
		"geminus-test", log)
	if err != nil {
		log.Error("setup failed", "error", err.Error())
		return 3
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		log.Error("shutdown failed", "error", err.Error())
		return 4
	}
	return 0
}

// Review M1: the exporters parse OTEL_EXPORTER_OTLP_HEADERS when they are
// built and complain about a value they cannot parse, with the value. The
// complaint must reach the process's real stderr only as a filtered JSON
// line, never as the SDK's raw text: config.LoadTelemetry refuses such a
// value first, but Setup does not depend on it. Both malformations: a pair
// without "=" (an HTTP header written out, a credential after the colon) and
// a value that is not URL-encoded. The filtered line still says what went
// wrong (review L7).
func TestTheSDKWritesNothingRawToStderr(t *testing.T) {
	c := newCollector(t)
	for name, headers := range map[string]string{
		"a pair without =":    "Authorization: Bearer HDR-SENTINEL-0123456789",
		"a value not escaped": "x-api-key=HDR-SENTINEL-0123%ZZ",
	} {
		cmd := exec.Command(os.Args[0], "-test.run=^$")
		cmd.Env = append(os.Environ(), helperEnv+"=1", "GEMINUS_EXPORT_STDERR_COLLECTOR="+c.srv.URL, "OTEL_EXPORTER_OTLP_HEADERS="+headers)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("%s: %v\n%s%s", name, err, stdout.String(), stderr.String())
		}
		if out := stdout.String() + stderr.String(); strings.Contains(out, "SENTINEL") {
			t.Errorf("%s: the process printed the header:\n%s", name, out)
		}
		diagnosed := false
		for _, l := range strings.Split(strings.TrimSpace(stderr.String()), "\n") {
			if l == "" {
				continue
			}
			var m map[string]any
			if err := json.Unmarshal([]byte(l), &m); err != nil || m["component"] != "helper" {
				t.Errorf("%s: a line outside the filtered log: %q", name, l)
				continue
			}
			if e, _ := m["error"].(string); m["level"] == "ERROR" && strings.HasPrefix(m["msg"].(string), "telemetry: ") && e != "" && e != observability.Redacted {
				diagnosed = true
			}
		}
		if !diagnosed {
			t.Errorf("%s: no filtered line says what the SDK could not parse:\n%s", name, stderr.String())
		}
	}
}
