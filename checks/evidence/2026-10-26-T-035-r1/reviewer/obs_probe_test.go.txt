package observability_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/observability"
)

type lv struct{ v slog.Value }

func (l lv) LogValue() slog.Value { return l.v }

type strErr string

func (e strErr) Error() string { return string(e) }

// TestReviewProbes prints, for each probe, whether SENTINEL reached the line.
func TestReviewProbes(t *testing.T) {
	const s = "PROBESECRET"
	type probe struct {
		name string
		log  func(l *slog.Logger)
	}
	long := strings.Repeat("a ", 40000)
	probes := []probe{
		{"unquoted value in error", func(l *slog.Logger) { l.Error("m", "error", fmt.Errorf("column %s unknown", s)) }},
		{"single-quoted value in error", func(l *slog.Logger) { l.Error("m", "error", fmt.Errorf("value '%s' invalid", s)) }},
		{"curly-quoted value in error", func(l *slog.Logger) { l.Error("m", "error", fmt.Errorf("value “%s” invalid", s)) }},
		{"backtick value in error", func(l *slog.Logger) { l.Error("m", "error", fmt.Errorf("value `%s` invalid", s)) }},
		{"double-quoted value in error", func(l *slog.Logger) { l.Error("m", "error", fmt.Errorf("value %q invalid", s)) }},
		{"pg doubled quote", func(l *slog.Logger) { l.Error("m", "error", errors.New(`column "a""`+s+`" does not exist`)) }},
		{"message itself", func(l *slog.Logger) { l.Info("row " + s) }},
		{"KindName key with data", func(l *slog.Logger) { l.Info("m", "model", s) }},
		{"KindToken key with data", func(l *slog.Logger) { l.Info("m", "status", strings.ToLower(s)) }},
		{"unregistered key", func(l *slog.Logger) { l.Info("m", "csv", s) }},
		{"group name from data", func(l *slog.Logger) { l.WithGroup(strings.ToLower(s)).Info("m", "status", "ok") }},
		{"inline empty group", func(l *slog.Logger) { l.Info("m", slog.Group("", slog.String("prompt", s))) }},
		{"group under error key", func(l *slog.Logger) { l.Info("m", slog.Group("error", slog.String("error", s))) }},
		{"With registered key bad shape", func(l *slog.Logger) { l.With("request_id", s).Info("m") }},
		{"With unregistered key", func(l *slog.Logger) { l.With("prompt", s).Info("m") }},
		{"WithGroup then With", func(l *slog.Logger) { l.WithGroup("g").With("prompt", s).Info("m") }},
		{"nested LogValuer under error", func(l *slog.Logger) {
			l.Info("m", "error", lv{slog.AnyValue(lv{slog.StringValue("x " + s)})})
		}},
		{"nested LogValuer to group", func(l *slog.Logger) {
			l.Info("m", "error", lv{slog.GroupValue(slog.String("error", s))})
		}},
		{"error struct under number key", func(l *slog.Logger) { l.Info("m", "rows", strErr(s)) }},
		{"map under error", func(l *slog.Logger) { l.Info("m", "error", map[string]string{"a": s}) }},
		{"bytes under error", func(l *slog.Logger) { l.Info("m", "error", []byte(s)) }},
		{"JSESSIONID cookie in text", func(l *slog.Logger) {
			l.Error("m", "error", errors.New("JSESSIONID=ABCDEF0123456789"+s))
		}},
		{"oauth code in url", func(l *slog.Logger) { l.Error("m", "error", errors.New("callback https://x/cb?code="+s)) }},
		{"aws presigned signature", func(l *slog.Logger) {
			l.Error("m", "error", errors.New("GET https://b.s3.amazonaws.com/k?X-Amz-Signature=0123456789abcdef0123456789abcdef"+strings.ToLower(s)))
		}},
		{"zero-width split key", func(l *slog.Logger) { l.Error("m", "error", errors.New("sk-ant-​api03-"+s+"abcdefgh")) }},
		{"line separator U+2028", func(l *slog.Logger) { l.Error("m", "error", errors.New("a b "+s)) }},
		{"secret after 70KB", func(l *slog.Logger) { l.Error("m", "error", errors.New(long+s)) }},
		{"stack with value", func(l *slog.Logger) { l.Error("m", "stack", "goroutine 1\n\tmain.f("+s+")") }},
		{"pointer with column name", func(l *slog.Logger) { l.Error("m", "path", "/mappings/"+s) }},
		{"addr with data", func(l *slog.Logger) { l.Error("m", "addr", s) }},
		{"context attr unregistered", func(l *slog.Logger) {
			l.InfoContext(observability.With(context.Background(), slog.String("prompt", s)), "m")
		}},
		{"annotation unregistered", func(l *slog.Logger) {
			ctx := observability.Track(context.Background())
			observability.Annotate(ctx, slog.String("prompt", s))
			l.InfoContext(ctx, "m")
		}},
		{"request id from ctx not ID", func(l *slog.Logger) {
			l.InfoContext(observability.WithRequestID(context.Background(), s), "m")
		}},
		{"panic text of runtime-like custom", func(l *slog.Logger) {
			l.Error("m", "panic", observability.PanicText(fakeRuntime(s)))
		}},
		{"panic string", func(l *slog.Logger) { l.Error("m", "panic", observability.PanicText("boom "+s)) }},
		{"email-ish in KindName", func(l *slog.Logger) { l.Info("m", "provider", "a+b."+s) }},
		{"type key with data", func(l *slog.Logger) { l.Info("m", "type", "*main."+s) }},
	}
	for _, p := range probes {
		var b bytes.Buffer
		l := observability.NewLogger(&b, "probe")
		p.log(l)
		leaked := strings.Contains(strings.ToUpper(b.String()), s)
		t.Logf("PROBE %-36s leaked=%v line=%s", p.name, leaked, trunc(b.String()))
	}
}

type fakeRuntime string

func (f fakeRuntime) Error() string { return "custom " + string(f) }
func (f fakeRuntime) RuntimeError() {}

func trunc(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 260 {
		return s[:200] + " ... " + s[len(s)-50:]
	}
	return s
}
