package pubsub

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"bodsch.me/mailcow-dockerapi/internal/actions"
	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
	"bodsch.me/mailcow-dockerapi/internal/dockerclient/dockertest"
	"bodsch.me/mailcow-dockerapi/internal/logging"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

const testChannel = "MC_CHANNEL"

func newSubscriber(t *testing.T, fake *dockertest.Fake) (*Subscriber, *strings.Builder) {
	t.Helper()

	var logs strings.Builder
	logger := logging.New(io.MultiWriter(&logs, io.Discard), slog.LevelDebug)

	return New(nil, testChannel, actions.Env{Docker: fake, Logger: logger}, logger), &logs
}

// Nachrichten benennen den Container über seinen Namen, nicht über die Kennung.
func TestHandleContainerPostByName(t *testing.T) {
	fake := dockertest.WithContainers("abc123", "postfix-mailcow")
	sub, _ := newSubscriber(t, fake)

	payload := `{"api_call":"container_post","post_action":"restart","container_name":"postfix-mailcow"}`
	sub.Handle(context.Background(), []byte(payload))

	if len(fake.ListCalls) != 1 {
		t.Fatalf("List-Aufrufe = %d, want 1", len(fake.ListCalls))
	}

	want := dockerclient.Target{ContainerName: "postfix-mailcow"}
	if fake.ListCalls[0].Target != want {
		t.Errorf("Target = %+v, want %+v", fake.ListCalls[0].Target, want)
	}
	if len(fake.Restarted) != 1 {
		t.Errorf("Neustarts = %v, want einen", fake.Restarted)
	}
}

func TestHandleExecResolvesCmdAndTask(t *testing.T) {
	fake := dockertest.WithContainers("abc123", "postfix-mailcow")
	sub, _ := newSubscriber(t, fake)

	payload := `{"api_call":"container_post","post_action":"exec","container_name":"postfix-mailcow",` +
		`"request":{"cmd":"mailq","task":"flush"}}`
	sub.Handle(context.Background(), []byte(payload))

	call, ok := fake.LastExec()
	if !ok {
		t.Fatal("kein Kommando abgesetzt")
	}
	if call.Cmd[0] != "/usr/sbin/postqueue" || call.Cmd[1] != "-f" {
		t.Errorf("Cmd = %v", call.Cmd)
	}
}

// Fehlende Felder ließen den Namen in main.py:232 unbelegt und lösten dort
// einen NameError aus.
func TestHandleRejectsIncompleteMessages(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		wantLog string
	}{
		{
			name:    "unbekannter api_call",
			payload: `{"api_call":"etwas_anderes","post_action":"stop","container_name":"x"}`,
			wantLog: "Unknown PubSub received",
		},
		{
			name:    "ohne container_name",
			payload: `{"api_call":"container_post","post_action":"stop"}`,
			wantLog: "missing container_name",
		},
		{
			name:    "ohne post_action",
			payload: `{"api_call":"container_post","container_name":"x"}`,
			wantLog: "missing container_name",
		},
		{
			name:    "exec ohne request",
			payload: `{"api_call":"container_post","post_action":"exec","container_name":"x"}`,
			wantLog: "request missing",
		},
		{
			name:    "exec ohne cmd",
			payload: `{"api_call":"container_post","post_action":"exec","container_name":"x","request":{"task":"flush"}}`,
			wantLog: "cmd missing",
		},
		{
			name:    "exec ohne task",
			payload: `{"api_call":"container_post","post_action":"exec","container_name":"x","request":{"cmd":"mailq"}}`,
			wantLog: "task missing",
		},
		{
			name:    "kein json",
			payload: `{kaputt`,
			wantLog: "Unknown PubSub received",
		},
		{
			name:    "unbekannte action",
			payload: `{"api_call":"container_post","post_action":"gibtsnicht","container_name":"x"}`,
			wantLog: "api call not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := dockertest.WithContainers("abc123", "postfix-mailcow")
			sub, logs := newSubscriber(t, fake)

			sub.Handle(context.Background(), []byte(tt.payload))

			if !strings.Contains(logs.String(), tt.wantLog) {
				t.Errorf("Protokoll enthaelt %q nicht:\n%s", tt.wantLog, logs.String())
			}
			if len(fake.ExecCalls) != 0 || len(fake.Restarted) != 0 {
				t.Error("es wurde eine Operation ausgefuehrt")
			}
		})
	}
}

// Ein Ende-zu-Ende-Durchlauf über eine echte Redis-Verbindung.
func TestRunReceivesPublishedMessage(t *testing.T) {
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	defer client.Close()

	fake := dockertest.WithContainers("abc123", "postfix-mailcow")
	logger := logging.New(io.Discard, slog.LevelError)
	sub := New(client, testChannel, actions.Env{Docker: fake, Logger: logger}, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		sub.Run(ctx)
	}()

	// Abwarten, bis die Anmeldung am Kanal steht – vorher veröffentlichte
	// Nachrichten gingen verloren.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(srv.PubSubChannels("")) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	payload := `{"api_call":"container_post","post_action":"stop","container_name":"postfix-mailcow"}`
	srv.Publish(testChannel, payload)

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(fake.StoppedIDs()) > 0 {
			cancel()
			<-done
			return
		}
		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	<-done
	t.Error("die Nachricht wurde nicht verarbeitet")
}

func TestRunStopsOnContextCancel(t *testing.T) {
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	defer client.Close()

	logger := logging.New(io.Discard, slog.LevelError)
	sub := New(client, testChannel, actions.Env{Docker: &dockertest.Fake{}, Logger: logger}, logger)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- sub.Run(ctx) }()

	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Error("erwarte den Abbruchgrund als Fehler")
		}
	case <-time.After(2 * time.Second):
		t.Error("Run ist nicht zurueckgekehrt")
	}
}
