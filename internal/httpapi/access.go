package httpapi

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/observability"
	"github.com/kcl1104/geminus/internal/observability/telemetry"
)

// AccessLog is the API's outermost layer (cmd/api, T-035, D-093 (c)): it
// gives the request its server-issued ID (Handler then uses it; a client's
// X-Request-ID is never read), lets the request record what it learns
// (observability.Track: the organization and user once Scoped resolved
// them, the error code WriteError wrote), and after the request ends logs
// one line: the route's pattern (never the path or query the client sent),
// the method, the status, the duration, the response's size and those
// annotations. Requests that fail with 5xx are WARN, others INFO. The
// request is also a server span and a point of http.server.request.duration
// (telemetry, D-095; the no-op unless an OTLP endpoint is configured).
func AccessLog(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := ids.New("req")
		tel := telemetry.Current()
		ctx, span := tel.StartRequest(observability.Track(observability.WithRequestID(r.Context(), id)), method(r.Method))
		r = r.WithContext(ctx)
		rec := &recorder{ResponseWriter: w}
		defer func() {
			// After the handler, even one that aborted with
			// http.ErrAbortHandler (the panic goes on after the line).
			status := rec.status
			if status == 0 {
				status = http.StatusOK
			}
			level := slog.LevelInfo
			if status >= 500 {
				level = slog.LevelWarn
			}
			took := time.Since(start)
			log.LogAttrs(ctx, level, "request", slog.String("method", method(r.Method)), slog.String("route", route(r.Pattern)),
				slog.Int("http_status", status), slog.Int64("duration_ms", took.Milliseconds()),
				slog.Int64("response_bytes", rec.bytes))
			tel.EndRequest(ctx, span, method(r.Method), route(r.Pattern), status, took, id)
		}()
		next.ServeHTTP(rec, r)
	})
}

// method is r.Method when it is a standard one, OTHER otherwise (a client
// can send any token).
func method(m string) string {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions,
		http.MethodConnect, http.MethodTrace:
		return m
	}
	return "OTHER"
}

// route is the ServeMux pattern that served the request, or "unmatched"
// for the catch-all and anything a mux did not route: the pattern names
// the operation without the IDs or values in the path.
func route(pattern string) string {
	if m, rest, ok := strings.Cut(pattern, " "); ok && method(m) == m && strings.HasPrefix(rest, "/") {
		return pattern
	}
	return "unmatched"
}

// recorder notes the response's status and size. Unwrap lets
// http.ResponseController reach the server's writer (deadlines, flushing).
type recorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *recorder) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *recorder) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += int64(n)
	return n, err
}

func (w *recorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// FlushError flushes the server's writer (http.ResponseController prefers
// it to Unwrap).
func (w *recorder) FlushError() error {
	return http.NewResponseController(w.ResponseWriter).Flush()
}
