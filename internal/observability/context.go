package observability

import (
	"context"
	"log/slog"
	"slices"
	"sync"
)

type ctxKey int

const (
	requestIDKey ctxKey = iota
	attrsKey
	annotationsKey
)

// WithRequestID returns ctx carrying id, the server-issued ID of the request
// (httpapi) or of the request that queued a job (the worker): every line
// logged with ctx carries it as request_id.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestID returns the request ID ctx carries ("" when none).
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// With returns ctx carrying attrs as well (after those it already
// carries): correlation IDs such as a job's organization, job ID, type and
// attempt. Every line logged with ctx gets them unless the line, or the
// logger's With, names the key itself. The parent's attributes are copied,
// never shared.
func With(ctx context.Context, attrs ...slog.Attr) context.Context {
	if len(attrs) == 0 {
		return ctx
	}
	prev := Attrs(ctx)
	next := make([]slog.Attr, 0, len(prev)+len(attrs))
	next = append(append(next, prev...), attrs...)
	return context.WithValue(ctx, attrsKey, next)
}

// Attrs returns the attributes With gave ctx, in order (nil when none).
func Attrs(ctx context.Context) []slog.Attr {
	a, _ := ctx.Value(attrsKey).([]slog.Attr)
	return a
}

// annotations is what a request learns while it runs. The access log reads
// it after the handler returned; the handler's own goroutines may add to it
// meanwhile, hence the lock.
type annotations struct {
	mu    sync.Mutex
	attrs []slog.Attr
}

// Track returns ctx with an empty set of annotations: Annotate adds to it,
// through any context derived from ctx, and every later line logged with
// such a context carries them. The HTTP access log tracks each request.
func Track(ctx context.Context) context.Context {
	return context.WithValue(ctx, annotationsKey, &annotations{})
}

// Annotate records attrs on the request ctx belongs to (see Track): a later
// value of a key replaces the earlier one. It does nothing when ctx is not
// tracked.
func Annotate(ctx context.Context, attrs ...slog.Attr) {
	a, _ := ctx.Value(annotationsKey).(*annotations)
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, attr := range attrs {
		if i := slices.IndexFunc(a.attrs, func(x slog.Attr) bool { return x.Key == attr.Key }); i >= 0 {
			a.attrs[i] = attr
			continue
		}
		a.attrs = append(a.attrs, attr)
	}
}

// Annotations returns a copy of what Annotate recorded on ctx's request
// (nil when ctx is not tracked).
func Annotations(ctx context.Context) []slog.Attr {
	a, _ := ctx.Value(annotationsKey).(*annotations)
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.attrs)
}

// correlation is what ctx adds to a line: its request ID, its With
// attributes, then its request's annotations.
func correlation(ctx context.Context) []slog.Attr {
	if ctx == nil {
		return nil
	}
	var out []slog.Attr
	if id := RequestID(ctx); id != "" {
		out = append(out, slog.String("request_id", id))
	}
	out = append(out, Attrs(ctx)...)
	return append(out, Annotations(ctx)...)
}
