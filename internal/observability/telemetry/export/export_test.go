package export_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/config"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/observability"
	"github.com/kcl1104/geminus/internal/observability/telemetry"
	"github.com/kcl1104/geminus/internal/observability/telemetry/export"
)

// collector is an OTLP/HTTP receiver on the loopback interface: what the
// SDK sends, by path.
type collector struct {
	mu   sync.Mutex
	got  map[string][]byte
	srv  *httptest.Server
	hits int
}

func newCollector(t *testing.T) *collector {
	c := &collector{got: map[string][]byte{}}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body io.Reader = r.Body
		if r.Header.Get("Content-Encoding") == "gzip" {
			z, err := gzip.NewReader(r.Body)
			if err != nil {
				t.Error(err)
				return
			}
			body = z
		}
		data, _ := io.ReadAll(body)
		c.mu.Lock()
		c.got[r.URL.Path] = append(c.got[r.URL.Path], data...)
		c.hits++
		c.mu.Unlock()
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(200)
	}))
	t.Cleanup(c.srv.Close)
	return c
}

func (c *collector) seen() (map[string][]byte, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string][]byte{}
	for k, v := range c.got {
		out[k] = bytes.Clone(v)
	}
	return out, c.hits
}

func record(t *testing.T) {
	t.Helper()
	tel := telemetry.Current()
	ctx, span := tel.StartRequest(context.Background(), "POST")
	tel.EndRequest(ctx, span, "POST", "POST /v1/things/{id}", 201, 5*time.Millisecond, ids.New("req"))
	_, job := tel.StartJob(context.Background(), "simulation", ids.New("job"), 1, "")
	tel.EndJob(job, "simulation", "failed", "CASE-SENTINEL is not a code", time.Second)
}

// D-095 (b): without an endpoint nothing is installed, so the process
// records and sends nothing; the same with OTEL_SDK_DISABLED.
func TestOffByDefault(t *testing.T) {
	c := newCollector(t)
	before := telemetry.Current()
	for name, env := range map[string]map[string]string{
		"nothing set":      nil,
		"the SDK disabled": {"OTEL_EXPORTER_OTLP_ENDPOINT": c.srv.URL, "OTEL_SDK_DISABLED": "true"},
		"both signals off": {"OTEL_EXPORTER_OTLP_ENDPOINT": c.srv.URL, "OTEL_TRACES_EXPORTER": "none", "OTEL_METRICS_EXPORTER": "none"},
	} {
		cfg, err := config.LoadTelemetry(func(k string) string { return env[k] })
		if err != nil {
			t.Fatal(err)
		}
		shutdown, err := export.Setup(context.Background(), cfg, "geminus-test", observability.NewLogger(io.Discard, "test"))
		if err != nil {
			t.Fatal(err)
		}
		if telemetry.Current() != before {
			t.Errorf("%s: telemetry was installed", name)
		}
		record(t)
		if err := shutdown(context.Background()); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, hits := c.seen(); hits != 0 {
		t.Errorf("%d requests reached the collector", hits)
	}
}

// With an endpoint, spans and metrics reach it (and only it) when the
// process stops, under the service's name (an operator's OTEL_SERVICE_NAME
// does not replace it), with the filtered attributes.
func TestExportWhenConfigured(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "OPERATOR-SENTINEL")
	c := newCollector(t)
	cfg, err := config.LoadTelemetry(func(k string) string {
		return map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": c.srv.URL}[k]
	})
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	before := telemetry.Current()
	shutdown, err := export.Setup(context.Background(), cfg, "geminus-test", observability.NewLogger(&logs, "test"))
	if err != nil {
		t.Fatal(err)
	}
	if telemetry.Current() == before {
		t.Fatal("nothing was installed")
	}
	record(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if telemetry.Current() != before {
		t.Error("shutdown did not put the previous telemetry back")
	}
	got, _ := c.seen()
	traces, metrics := got["/v1/traces"], got["/v1/metrics"]
	if len(traces) == 0 || len(metrics) == 0 {
		t.Fatalf("paths received: %v", keys(got))
	}
	for name, data := range map[string][]byte{"traces": traces, "metrics": metrics} {
		if !bytes.Contains(data, []byte("geminus-test")) {
			t.Errorf("%s: no service name", name)
		}
		if bytes.Contains(data, []byte("SENTINEL")) {
			t.Errorf("%s: an unfiltered attribute was exported", name)
		}
	}
	if !bytes.Contains(traces, []byte("POST /v1/things/{id}")) || !bytes.Contains(metrics, []byte("http.server.request.duration")) {
		t.Error("the recorded data is missing")
	}
	if strings.Contains(logs.String(), "telemetry export failed") {
		t.Errorf("export failed: %s", logs.String())
	}
}

// An export failure is a WARN line through the filtered log, not a line of
// the SDK's own on stderr.
func TestExportFailuresAreLogged(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(400) }))
	defer dead.Close()
	var logs syncBuffer
	shutdown, err := export.Setup(context.Background(), config.Telemetry{TracesEndpoint: dead.URL + "/v1/traces"}, "geminus-test",
		observability.NewLogger(&logs, "test"))
	if err != nil {
		t.Fatal(err)
	}
	record(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = shutdown(ctx) // the failed export may also be returned here
	out := logs.String()
	if !strings.Contains(out, `"msg":"telemetry export failed"`) || !strings.Contains(out, `"level":"WARN"`) {
		t.Errorf("the failed export is not a filtered WARN line: %q", out)
	}
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if !strings.Contains(l, `"component":"test"`) {
			t.Errorf("a line bypassed the filtered log: %s", l)
		}
	}
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func keys(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
