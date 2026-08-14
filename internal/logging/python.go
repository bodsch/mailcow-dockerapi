package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
)

// PythonHandler reproduces the output format of the Python implementation:
// "%(levelname)s:     %(message)s", with the slog attributes appended as
// key=value pairs.
//
// It exists so that LOG_FORMAT=python keeps the service a drop-in replacement for
// operators whose log processing matches on that shape.
type PythonHandler struct {
	mu    *sync.Mutex
	w     io.Writer
	level slog.Level
	attrs []slog.Attr
	group string
}

// NewPythonHandler returns a handler writing records at or above level to w.
func NewPythonHandler(w io.Writer, level slog.Level) *PythonHandler {
	return &PythonHandler{mu: &sync.Mutex{}, w: w, level: level}
}

func (h *PythonHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level
}

func (h *PythonHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder

	b.WriteString(levelName(r.Level))
	b.WriteString(":     ")
	b.WriteString(r.Message)

	for _, a := range h.attrs {
		appendAttr(&b, h.group, a)
	}
	r.Attrs(func(a slog.Attr) bool {
		appendAttr(&b, h.group, a)
		return true
	})
	b.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()

	_, err := io.WriteString(h.w, b.String())
	return err
}

func (h *PythonHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}

	clone := *h
	clone.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	return &clone
}

func (h *PythonHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}

	clone := *h
	if h.group == "" {
		clone.group = name
	} else {
		clone.group = h.group + "." + name
	}
	return &clone
}

func appendAttr(b *strings.Builder, group string, a slog.Attr) {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return
	}

	if a.Value.Kind() == slog.KindGroup {
		next := a.Key
		if group != "" && next != "" {
			next = group + "." + next
		} else if next == "" {
			next = group
		}
		for _, sub := range a.Value.Group() {
			appendAttr(b, next, sub)
		}
		return
	}

	key := a.Key
	if group != "" {
		key = group + "." + key
	}

	fmt.Fprintf(b, " %s=%v", key, a.Value.Any())
}

// levelName maps slog levels onto the names of the Python logging module.
func levelName(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "ERROR"
	case l >= slog.LevelWarn:
		return "WARNING"
	case l >= slog.LevelInfo:
		return "INFO"
	default:
		return "DEBUG"
	}
}
