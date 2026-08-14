// Package logging stellt einen slog.Handler bereit, der das Ausgabeformat
// der Python-Implementierung reproduziert: "%(levelname)s:     %(message)s".
//
// Das Format wird beibehalten, weil mailcow-Betreiber ihre Log-Auswertung
// darauf ausgerichtet haben.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
)

// Handler schreibt Records im Format "LEVEL:     message key=value".
type Handler struct {
	mu    *sync.Mutex
	w     io.Writer
	level slog.Level
	attrs []slog.Attr
	group string
}

// NewHandler erzeugt einen Handler, der ab level nach w schreibt.
func NewHandler(w io.Writer, level slog.Level) *Handler {
	return &Handler{mu: &sync.Mutex{}, w: w, level: level}
}

// New liefert einen Logger im Original-Format.
func New(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(NewHandler(w, level))
}

func (h *Handler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level
}

func (h *Handler) Handle(_ context.Context, r slog.Record) error {
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

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}

	clone := *h
	clone.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	return &clone
}

func (h *Handler) WithGroup(name string) slog.Handler {
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

// levelName bildet slog-Level auf die Namen des Python-logging-Moduls ab.
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
