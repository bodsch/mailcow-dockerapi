package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// python is the format the service defaults to.
func python(w *bytes.Buffer) *slog.Logger {
	return New(w, Options{Level: "info", Format: "python"})
}

// The format has to match the Python formatter exactly:
// "%(levelname)s:     %(message)s"
func TestPythonFormatMatchesTheOriginal(t *testing.T) {
	tests := []struct {
		name string
		log  func(l *slog.Logger)
		want string
	}{
		{
			name: "info",
			log:  func(l *slog.Logger) { l.Info("Init APP") },
			want: "INFO:     Init APP\n",
		},
		{
			name: "error",
			log:  func(l *slog.Logger) { l.Error("container_post: boom") },
			want: "ERROR:     container_post: boom\n",
		},
		{
			name: "warning",
			log:  func(l *slog.Logger) { l.Warn("fts_rescan error") },
			want: "WARNING:     fts_rescan error\n",
		},
		{
			name: "attributes are appended",
			log: func(l *slog.Logger) {
				l.Info("api call", "method", "container_post__stop", "container_id", "abc")
			},
			want: "INFO:     api call method=container_post__stop container_id=abc\n",
		},
		{
			name: "with attrs",
			log:  func(l *slog.Logger) { l.With("component", "pubsub").Info("subscribed") },
			want: "INFO:     subscribed component=pubsub\n",
		},
		{
			name: "groups are joined with a dot",
			log: func(l *slog.Logger) {
				l.WithGroup("docker").Info("exec", "user", "vmail")
			},
			want: "INFO:     exec docker.user=vmail\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			tt.log(python(&buf))

			if got := buf.String(); got != tt.want {
				t.Errorf("output = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	logger := python(&buf)

	logger.Debug("not visible")
	if buf.Len() != 0 {
		t.Errorf("debug was written: %q", buf.String())
	}

	logger.Info("visible")
	if !strings.Contains(buf.String(), "visible") {
		t.Errorf("info is missing: %q", buf.String())
	}
}

func TestConcurrentWritesAreSerialized(t *testing.T) {
	var buf bytes.Buffer
	logger := python(&buf)

	done := make(chan struct{})
	for i := 0; i < 50; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			logger.Info("parallel")
		}()
	}
	for i := 0; i < 50; i++ {
		<-done
	}

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) != 50 {
		t.Fatalf("lines = %d, want 50", len(lines))
	}
	for _, l := range lines {
		if l != "INFO:     parallel" {
			t.Fatalf("garbled line: %q", l)
		}
	}
}

// The other two formats are the standard library's, so the tests only assert that
// the factory picks the right one and honours the level.
func TestFormatSelection(t *testing.T) {
	tests := []struct {
		format string
		wants  string
	}{
		{"json", `"msg":"hello"`},
		{"text", `msg=hello`},
		{"python", "INFO:     hello"},
		{"", "INFO:     hello"},
		{"nonsense", "INFO:     hello"},
	}

	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			var buf bytes.Buffer
			New(&buf, Options{Level: "info", Format: tt.format}).Info("hello")

			if !strings.Contains(buf.String(), tt.wants) {
				t.Errorf("output %q does not contain %q", buf.String(), tt.wants)
			}
		})
	}
}

func TestLevelNames(t *testing.T) {
	tests := []struct {
		name string
		want slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"error", slog.LevelError},
		{"", slog.LevelInfo},
		{"chatty", slog.LevelInfo},
	}

	for _, tt := range tests {
		if got := Level(tt.name); got != tt.want {
			t.Errorf("Level(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// A debug level has to reach the handler, not just the logger.
func TestDebugLevelIsHonoured(t *testing.T) {
	var buf bytes.Buffer
	New(&buf, Options{Level: "debug", Format: "python"}).Debug("chatty")

	if got, want := buf.String(), "DEBUG:     chatty\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}
