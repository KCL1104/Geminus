package export

import (
	"bytes"
	"encoding/json/v2"
	"errors"
	"strings"
	"testing"

	"github.com/go-logr/logr"

	"github.com/kcl1104/geminus/internal/observability"
)

// Review L7: what OpenTelemetry's own logger says reaches the filtered log
// in a form an operator can read: its Error lines with their error as the
// free-text "error" (logr names it "err", which the filter does not know),
// its Warn (logr V(1)) as WARN; its Info and Debug (V(4), V(8)) are not
// logged. The raw values the SDK adds (input, value, key) stay redacted.
func TestDiagnostics(t *testing.T) {
	var b bytes.Buffer
	l := logr.FromSlogHandler(diagnostics{observability.NewLogger(&b, "test").Handler()})
	l.Error(errors.New(`parse "Bearer DIAG-SENTINEL" failed`), "parse headers", "input", "Authorization: Bearer DIAG-SENTINEL")
	l.V(1).Info("dropping data", "dropped", 3)
	l.V(4).Info("exporter started")
	l.V(8).Info("a debug detail")
	l.WithValues("err", errors.New(`the value "DIAG-SENTINEL"`)).Error(nil, "with values")
	var got []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("%q: %v", line, err)
		}
		got = append(got, m)
	}
	if len(got) != 3 {
		t.Fatalf("%d lines: %s", len(got), b.String())
	}
	if got[0]["level"] != "ERROR" || got[0]["msg"] != "telemetry: parse headers" || got[0]["error"] != `parse "[redacted]" failed` ||
		got[0]["input"] != observability.Redacted || got[0]["component"] != "test" {
		t.Errorf("%v", got[0])
	}
	if got[1]["level"] != "WARN" || got[1]["msg"] != "telemetry: dropping data" || got[1]["dropped"] != observability.Redacted {
		t.Errorf("%v", got[1])
	}
	if got[2]["msg"] != "telemetry: with values" || got[2]["error"] != `the value "[redacted]"` {
		t.Errorf("%v", got[2])
	}
	if strings.Contains(b.String(), "SENTINEL") {
		t.Errorf("%s", b.String())
	}
}
