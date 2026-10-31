package main

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// sonicLine is the one line sonic v1.15.0's compat path (encoding/json, no
// JIT) writes to stderr at init on go1.27 (ADR-031; H-064 accepted it as
// the one exception to D-093 (a) and D-094; scripts/check-architecture's
// TestSonicRunsItsCompatPath pins it for the Eino packages).
const sonicLine = "WARNING: sonic/ast only supports (go1.17~1.26 and amd64 CPU) or (go1.20~1.26 and arm64 CPU), but your environment is not suitable and will fallback to encoding/json"

// The worker links Eino since its analysis handler runs the analyst graph
// (H-059, E2). Built and started with an empty environment, it refuses to
// start, and what it writes to stderr is exactly H-064's exception, once,
// before anything else, and then JSON lines only; nothing on stdout.
func TestTheWorkerWritesOneLineThatIsNotJSON(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "worker")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v: %s", err, out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	run := exec.CommandContext(ctx, bin)
	run.Env = []string{}
	var stdout, stderr bytes.Buffer
	run.Stdout, run.Stderr = &stdout, &stderr
	err := run.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() == 0 {
		t.Fatalf("the worker without configuration ended with %v", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout: %q", stdout.String())
	}
	lines := strings.Split(strings.TrimSuffix(stderr.String(), "\n"), "\n")
	if len(lines) < 2 || lines[0] != sonicLine {
		t.Fatalf("stderr does not start with the sonic line and a log line:\n%s", stderr.String())
	}
	for i, l := range lines[1:] {
		if !jsontext.Value(l).IsValid() || !strings.HasPrefix(l, "{") {
			t.Errorf("line %d is not a JSON object: %q", i+2, l)
		}
	}
}
