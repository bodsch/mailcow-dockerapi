package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// Das Format muss exakt dem Python-Formatter entsprechen:
// "%(levelname)s:     %(message)s"
func TestFormatMatchesPython(t *testing.T) {
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
			name: "attribute werden angehaengt",
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
			name: "gruppen werden mit punkt verbunden",
			log: func(l *slog.Logger) {
				l.WithGroup("docker").Info("exec", "user", "vmail")
			},
			want: "INFO:     exec docker.user=vmail\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := New(&buf, slog.LevelInfo)

			tt.log(logger)

			if got := buf.String(); got != tt.want {
				t.Errorf("Ausgabe = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, slog.LevelInfo)

	logger.Debug("nicht sichtbar")
	if buf.Len() != 0 {
		t.Errorf("Debug wurde ausgegeben: %q", buf.String())
	}

	logger.Info("sichtbar")
	if !strings.Contains(buf.String(), "sichtbar") {
		t.Errorf("Info fehlt: %q", buf.String())
	}
}

func TestConcurrentWritesAreSerialized(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, slog.LevelInfo)

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
		t.Fatalf("Zeilen = %d, want 50", len(lines))
	}
	for _, l := range lines {
		if l != "INFO:     parallel" {
			t.Fatalf("verstuemmelte Zeile: %q", l)
		}
	}
}
