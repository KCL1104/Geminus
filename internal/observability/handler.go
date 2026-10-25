// Package observability is the process's structured logging (T-035,
// REQ-020; D-093, D-094): JSON lines on which every attribute passes the
// privacy filter, and every line logged with a request's or a job's
// context carries that context's correlation IDs.
//
// The filter is a registry of log keys, each with the shape its value must
// have (an ID, a code, a lowercase word, a number...). A key the registry
// does not list, or a value without its key's shape, is logged as
// "[redacted]": default deny, so raw customer CSV content, uploaded field
// values, names, comments, prompts, provider answers and chain-of-thought,
// which have no registered key, never reach a log line however a caller
// passes them. The server's own free text (errors, panics, stacks) is kept
// but sanitized: credentials, a URL's user information, credential headers,
// e-mail addresses and every double-quoted value are removed, and it is
// bounded.
//
// It depends on the standard library only, so any application package can
// log through it; the generic kernel never does (scripts/check-architecture).
package observability

import (
	"context"
	"io"
	"log/slog"
)

// Handler is a slog.Handler that filters every attribute and adds the
// context's correlation attributes.
type Handler struct {
	inner slog.Handler
	// keys are the top-level keys WithAttrs gave this handler: a context
	// attribute of the same key is not added again.
	keys map[string]bool
}

// NewHandler wraps inner, which formats and writes the lines.
func NewHandler(inner slog.Handler) *Handler { return &Handler{inner: inner} }

// NewLogger is the API's and the worker's logger: JSON lines on w at INFO
// and above, filtered, each with component.
func NewLogger(w io.Writer, component string) *slog.Logger {
	return slog.New(NewHandler(slog.NewJSONHandler(w, nil))).With("component", component)
}

func (h *Handler) Enabled(ctx context.Context, l slog.Level) bool { return h.inner.Enabled(ctx, l) }

// Handle writes r with its attributes filtered, after the context's
// correlation attributes that neither r nor WithAttrs names.
func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	out := slog.NewRecord(r.Time, r.Level, Text(r.Message), r.PC)
	named := make(map[string]bool, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		named[a.Key] = true
		return true
	})
	for _, a := range correlation(ctx) {
		if named[a.Key] || h.keys[a.Key] {
			continue
		}
		named[a.Key] = true
		out.AddAttrs(filter(a))
	}
	r.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(filter(a))
		return true
	})
	return h.inner.Handle(ctx, out)
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	keys := make(map[string]bool, len(h.keys)+len(attrs))
	for k := range h.keys {
		keys[k] = true
	}
	filtered := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		keys[a.Key] = true
		filtered[i] = filter(a)
	}
	return &Handler{inner: h.inner.WithAttrs(filtered), keys: keys}
}

// WithGroup nests the attributes that follow, the context's included,
// under name; the group's own attributes are filtered like any other.
func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	if !keyShape.MatchString(name) {
		name = InvalidKey
	}
	return &Handler{inner: h.inner.WithGroup(name), keys: map[string]bool{}}
}
