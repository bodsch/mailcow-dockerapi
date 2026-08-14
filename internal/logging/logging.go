// Package logging builds the service's structured logger.
//
// The factory below is shared with mailcow-watchdog; only the "python" format is
// specific to this service. Keep both copies in sync — see CONVENTIONS.md.
package logging

import (
	"io"
	"log/slog"
)

// Options selects the level and the output format. It mirrors config.Log field
// for field, so main can convert one into the other — logging deliberately does
// not import the configuration package.
type Options struct {
	// Level is debug, info, warn or error. Anything else means info.
	Level string
	// Format is python, json or text. Anything else means python, which is the
	// format the replaced implementation wrote.
	Format string
}

// New returns a logger writing to w.
func New(w io.Writer, opts Options) *slog.Logger {
	level := Level(opts.Level)
	handlerOpts := &slog.HandlerOptions{Level: level}

	switch opts.Format {
	case "json":
		return slog.New(slog.NewJSONHandler(w, handlerOpts))
	case "text":
		return slog.New(slog.NewTextHandler(w, handlerOpts))
	default:
		// The Python implementation's "LEVEL:     message" stays the default,
		// because operators have built their log processing around it.
		return slog.New(NewPythonHandler(w, level))
	}
}

// Level maps a configured level name onto its slog level. An unknown name is
// info, so a typo makes the service chattier rather than silent.
func Level(name string) slog.Level {
	switch name {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
