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
// e-mail addresses and every quoted value are removed, and it is bounded.
//
// It depends on the standard library only, so any application package can
// log through it; the generic kernel never does (scripts/check-architecture).
package observability

import (
	"context"
	"io"
	"log/slog"
	"slices"
)

// Handler is a slog.Handler that filters every attribute and adds the
// context's correlation attributes, at the top level of the line whatever
// groups the logger has (review I7).
type Handler struct {
	// inner formats and writes the lines, with the logger's top-level
	// attributes (those given before any group).
	inner slog.Handler
	// keys are the top-level keys WithAttrs gave this handler: a context
	// attribute of the same key is not added again.
	keys map[string]bool
	// steps are the logger's groups and the attributes given inside them,
	// in order; Handle applies them after the correlation, so that stays
	// outside every group.
	steps []step
}

// step is a WithGroup name, or the (filtered) attributes of a WithAttrs
// inside a group.
type step struct {
	group string
	attrs []slog.Attr
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
// correlation attributes that neither WithAttrs nor (outside a group) r
// names.
func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	out := slog.NewRecord(r.Time, r.Level, Text(r.Message), r.PC)
	named := make(map[string]bool, r.NumAttrs())
	if len(h.steps) == 0 {
		// The record's attributes are at the top level too: a key it names
		// is its own.
		r.Attrs(func(a slog.Attr) bool {
			named[a.Key] = true
			return true
		})
	}
	var top []slog.Attr
	for _, a := range correlation(ctx) {
		if named[a.Key] || h.keys[a.Key] {
			continue
		}
		named[a.Key] = true
		top = append(top, filter(a))
	}
	inner := h.inner
	if len(h.steps) == 0 {
		out.AddAttrs(top...)
	} else {
		// The record's attributes belong inside the groups, the
		// correlation outside them: the groups are opened after it.
		if len(top) > 0 {
			inner = inner.WithAttrs(top)
		}
		for _, s := range h.steps {
			if s.group != "" {
				inner = inner.WithGroup(s.group)
			} else {
				inner = inner.WithAttrs(s.attrs)
			}
		}
	}
	r.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(filter(a))
		return true
	})
	return inner.Handle(ctx, out)
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	filtered := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		filtered[i] = filter(a)
	}
	if len(h.steps) > 0 {
		return &Handler{inner: h.inner, keys: h.keys, steps: append(slices.Clip(h.steps), step{attrs: filtered})}
	}
	keys := make(map[string]bool, len(h.keys)+len(attrs))
	for k := range h.keys {
		keys[k] = true
	}
	for _, a := range attrs {
		keys[a.Key] = true
	}
	return &Handler{inner: h.inner.WithAttrs(filtered), keys: keys}
}

// WithGroup nests the attributes that follow under name (never the
// context's correlation); the group's own attributes are filtered like any
// other.
func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	if !keyShape.MatchString(name) {
		name = InvalidKey
	}
	return &Handler{inner: h.inner, keys: h.keys, steps: append(slices.Clip(h.steps), step{group: name})}
}
