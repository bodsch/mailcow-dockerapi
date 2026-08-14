// Package pubsub empfängt Aufträge über den Redis-Kanal MC_CHANNEL.
//
// mailcow schickt darüber Container-Operationen, die keinen Rückkanal
// brauchen. Die Nachrichten benennen den Container über seinen Namen, nicht
// über die Kennung.
package pubsub

import (
	"context"
	"encoding/json"
	"log/slog"

	"bodsch.me/mailcow-dockerapi/internal/actions"
	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
	"github.com/redis/go-redis/v9"
)

// APICallContainerPost ist der einzige Auftragstyp, den main.py kannte.
const APICallContainerPost = "container_post"

// Message ist eine Nachricht auf MC_CHANNEL.
type Message struct {
	APICall       string          `json:"api_call"`
	PostAction    string          `json:"post_action"`
	ContainerName string          `json:"container_name"`
	Request       json.RawMessage `json:"request"`
}

// Subscriber verarbeitet eingehende Nachrichten.
type Subscriber struct {
	client  *redis.Client
	channel string
	env     actions.Env
	logger  *slog.Logger
}

// New baut einen Subscriber.
func New(client *redis.Client, channel string, env actions.Env, logger *slog.Logger) *Subscriber {
	if logger == nil {
		logger = slog.Default()
	}

	return &Subscriber{client: client, channel: channel, env: env, logger: logger}
}

// Run empfängt Nachrichten, bis der Kontext endet.
//
// Verbindungsabbrüche fängt go-redis selbst ab und stellt die Verbindung
// wieder her; die Schleife läuft weiter.
func (s *Subscriber) Run(ctx context.Context) error {
	sub := s.client.Subscribe(ctx, s.channel)
	defer sub.Close()

	s.logger.Info("Subscribe to redis channel", "channel", s.channel)

	ch := sub.Channel()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-ch:
			if !ok {
				return nil
			}
			s.Handle(ctx, []byte(msg.Payload))
		}
	}
}

// Handle wertet eine einzelne Nachricht aus.
//
// Ein Ergebnis gibt es nicht zurückzumelden; Fehler werden protokolliert.
func (s *Subscriber) Handle(ctx context.Context, payload []byte) {
	s.logger.Info("PubSub Received", "payload", string(payload))

	var msg Message
	if err := json.Unmarshal(payload, &msg); err != nil {
		s.logger.Error("Unknown PubSub received", "payload", string(payload), "error", err)
		return
	}

	if msg.APICall != APICallContainerPost {
		s.logger.Error("Unknown PubSub received", "payload", string(payload))
		return
	}

	if msg.PostAction == "" || msg.ContainerName == "" {
		s.logger.Error("api call: missing container_name, post_action or request")
		return
	}

	req := actions.ParseRequest(msg.Request)

	name, ok := s.resolveName(msg, req)
	if !ok {
		return
	}

	fn, ok := actions.Lookup(name)
	if !ok {
		s.logger.Error("api call not found", "method", name, "container_name", msg.ContainerName)
		return
	}

	s.logger.Info("api call", "method", name, "container_name", msg.ContainerName)

	res := fn(ctx, s.env, req, dockerclient.Target{ContainerName: msg.ContainerName})
	s.logger.Debug("api call finished", "method", name, "response", string(res.Body))
}

// resolveName bildet den Namen der Action.
//
// Fehlten bei post_action "exec" die Felder cmd oder task, blieb der Name in
// main.py:232 unbelegt und der Zugriff schlug mit einem NameError fehl.
func (s *Subscriber) resolveName(msg Message, req actions.Request) (string, bool) {
	if msg.PostAction != "exec" {
		return actions.MethodName(msg.PostAction, "", ""), true
	}

	if len(msg.Request) == 0 {
		s.logger.Error("api call: request missing")
		return "", false
	}

	cmd, ok := req.String("cmd")
	if !ok {
		s.logger.Error("api call: cmd missing")
		return "", false
	}

	task, ok := req.String("task")
	if !ok {
		s.logger.Error("api call: task missing")
		return "", false
	}

	return actions.MethodName(msg.PostAction, cmd, task), true
}
