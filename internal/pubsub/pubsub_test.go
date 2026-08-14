package pubsub

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"bodsch.me/mailcow-dockerapi/internal/actions"
	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
	"bodsch.me/mailcow-dockerapi/internal/dockerclient/dockertest"
	"bodsch.me/mailcow-dockerapi/internal/logging"
	"bodsch.me/mailcow-dockerapi/internal/metrics"
	"github.com/alicebob/miniredis/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/redis/go-redis/v9"
)

const testChannel = "MC_CHANNEL"

func newSubscriber(t *testing.T, fake *dockertest.Fake) (*Subscriber, *strings.Builder) {
	t.Helper()

	sub, logs, _ := newSubscriberWithRegistry(t, fake)
	return sub, logs
}

func newSubscriberWithRegistry(t *testing.T, fake *dockertest.Fake) (*Subscriber, *strings.Builder, *prometheus.Registry) {
	t.Helper()

	var logs strings.Builder
	log := logging.New(&logs, logging.Options{Level: "debug", Format: "text"})
	reg := prometheus.NewRegistry()

	sub := New(Options{
		Channel: testChannel,
		Env:     actions.Env{Docker: fake, Log: log},
		Metrics: metrics.New(reg, "test"),
		Log:     log,
	})

	return sub, &logs, reg
}

// Messages name the container by name, not by id.
func TestHandleContainerPostByName(t *testing.T) {
	fake := dockertest.WithContainers("abc123", "postfix-mailcow")
	sub, _ := newSubscriber(t, fake)

	payload := `{"api_call":"container_post","post_action":"restart","container_name":"postfix-mailcow"}`
	sub.Handle(context.Background(), []byte(payload))

	if len(fake.ListCalls) != 1 {
		t.Fatalf("List calls = %d, want 1", len(fake.ListCalls))
	}

	want := dockerclient.Target{ContainerName: "postfix-mailcow"}
	if fake.ListCalls[0].Target != want {
		t.Errorf("Target = %+v, want %+v", fake.ListCalls[0].Target, want)
	}
	if len(fake.Restarted) != 1 {
		t.Errorf("restarts = %v, want exactly one", fake.Restarted)
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
		t.Fatal("no command was issued")
	}
	if call.Cmd[0] != "/usr/sbin/postqueue" || call.Cmd[1] != "-f" {
		t.Errorf("Cmd = %v", call.Cmd)
	}
}

// Missing fields left the name unbound in main.py:232 and raised a NameError there.
func TestHandleRejectsIncompleteMessages(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		wantLog string
	}{
		{
			name:    "unknown api_call",
			payload: `{"api_call":"something_else","post_action":"stop","container_name":"x"}`,
			wantLog: "Unknown PubSub received",
		},
		{
			name:    "no container_name",
			payload: `{"api_call":"container_post","post_action":"stop"}`,
			wantLog: "missing container_name",
		},
		{
			name:    "no post_action",
			payload: `{"api_call":"container_post","container_name":"x"}`,
			wantLog: "missing container_name",
		},
		{
			name:    "exec without a request",
			payload: `{"api_call":"container_post","post_action":"exec","container_name":"x"}`,
			wantLog: "request missing",
		},
		{
			name:    "exec without cmd",
			payload: `{"api_call":"container_post","post_action":"exec","container_name":"x","request":{"task":"flush"}}`,
			wantLog: "cmd missing",
		},
		{
			name:    "exec without task",
			payload: `{"api_call":"container_post","post_action":"exec","container_name":"x","request":{"cmd":"mailq"}}`,
			wantLog: "task missing",
		},
		{
			name:    "not json",
			payload: `{broken`,
			wantLog: "Unknown PubSub received",
		},
		{
			name:    "unknown action",
			payload: `{"api_call":"container_post","post_action":"does-not-exist","container_name":"x"}`,
			wantLog: "api call not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := dockertest.WithContainers("abc123", "postfix-mailcow")
			sub, logs := newSubscriber(t, fake)

			sub.Handle(context.Background(), []byte(tt.payload))

			if !strings.Contains(logs.String(), tt.wantLog) {
				t.Errorf("the log does not contain %q:\n%s", tt.wantLog, logs.String())
			}
			if len(fake.ExecCalls) != 0 || len(fake.Restarted) != 0 {
				t.Error("an operation was carried out anyway")
			}
		})
	}
}

// Every message is accounted for, so a frontend sending calls this build does not
// implement shows up as a metric rather than only as a log line.
func TestHandleCountsMessages(t *testing.T) {
	fake := dockertest.WithContainers("abc123", "postfix-mailcow")
	sub, _, reg := newSubscriberWithRegistry(t, fake)

	ctx := context.Background()
	sub.Handle(ctx, []byte(`{"api_call":"container_post","post_action":"restart","container_name":"postfix-mailcow"}`))
	sub.Handle(ctx, []byte(`{broken`))
	sub.Handle(ctx, []byte(`{"api_call":"container_post","post_action":"does-not-exist","container_name":"x"}`))

	const want = `
# HELP mailcow_dockerapi_pubsub_messages_total Messages received on the mailcow channel, by outcome.
# TYPE mailcow_dockerapi_pubsub_messages_total counter
mailcow_dockerapi_pubsub_messages_total{result="handled"} 1
mailcow_dockerapi_pubsub_messages_total{result="malformed"} 1
mailcow_dockerapi_pubsub_messages_total{result="unknown"} 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want),
		"mailcow_dockerapi_pubsub_messages_total"); err != nil {
		t.Error(err)
	}
}

// An end-to-end run over a real Redis connection.
func TestRunReceivesPublishedMessage(t *testing.T) {
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	defer func() { _ = client.Close() }()

	fake := dockertest.WithContainers("abc123", "postfix-mailcow")
	log := logging.New(io.Discard, logging.Options{Level: "error", Format: "text"})
	sub := New(Options{
		Client:  client,
		Channel: testChannel,
		Env:     actions.Env{Docker: fake, Log: log},
		Log:     log,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = sub.Run(ctx)
	}()

	// Wait until the subscription is established — messages published before that
	// were lost.
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
	t.Error("the message was not processed")
}

func TestRunStopsOnContextCancel(t *testing.T) {
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	defer func() { _ = client.Close() }()

	log := logging.New(io.Discard, logging.Options{Level: "error", Format: "text"})
	sub := New(Options{
		Client:  client,
		Channel: testChannel,
		Env:     actions.Env{Docker: &dockertest.Fake{}, Log: log},
		Log:     log,
	})

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- sub.Run(ctx) }()

	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Error("expected the cancellation cause as an error")
		}
	case <-time.After(2 * time.Second):
		t.Error("Run did not return")
	}
}
