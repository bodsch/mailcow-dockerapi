// Package pubsub receives jobs over the Redis channel MC_CHANNEL.
//
// mailcow sends container operations that need no answer through it. The messages
// name the container by name rather than by id.
package pubsub

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"bodsch.me/mailcow-dockerapi/internal/actions"
	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
	"bodsch.me/mailcow-dockerapi/internal/metrics"
	"github.com/redis/go-redis/v9"
)

// APICallContainerPost is the only job type main.py knew.
const APICallContainerPost = "container_post"

// Message is one message on MC_CHANNEL.
type Message struct {
	APICall       string          `json:"api_call"`
	PostAction    string          `json:"post_action"`
	ContainerName string          `json:"container_name"`
	Request       json.RawMessage `json:"request"`
}

// Options configures the subscriber.
type Options struct {
	Client  *redis.Client
	Channel string
	Env     actions.Env
	Metrics *metrics.Metrics
	Log     *slog.Logger
}

// Subscriber processes incoming messages.
type Subscriber struct {
	client  *redis.Client
	channel string
	env     actions.Env
	metrics *metrics.Metrics
	log     *slog.Logger
}

// New builds a subscriber.
func New(opts Options) *Subscriber {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}

	return &Subscriber{
		client:  opts.Client,
		channel: opts.Channel,
		env:     opts.Env,
		metrics: opts.Metrics,
		log:     log.With("component", "pubsub"),
	}
}

// Run receives messages until the context ends.
//
// go-redis catches connection drops itself and re-establishes the subscription;
// the loop keeps running.
func (s *Subscriber) Run(ctx context.Context) error {
	sub := s.client.Subscribe(ctx, s.channel)
	defer func() { _ = sub.Close() }()

	s.log.Info("Subscribe to redis channel", "channel", s.channel)

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

// Handle evaluates a single message.
//
// There is no result to report back; failures are logged.
func (s *Subscriber) Handle(ctx context.Context, payload []byte) {
	s.log.Info("PubSub Received", "payload", string(payload))

	var msg Message
	if err := json.Unmarshal(payload, &msg); err != nil {
		s.log.Error("Unknown PubSub received", "payload", string(payload), "err", err)
		s.metrics.ObservePubSub(metrics.PubSubMalformed)
		return
	}

	if msg.APICall != APICallContainerPost {
		s.log.Error("Unknown PubSub received", "payload", string(payload))
		s.metrics.ObservePubSub(metrics.PubSubUnknown)
		return
	}

	if msg.PostAction == "" || msg.ContainerName == "" {
		s.log.Error("api call: missing container_name, post_action or request")
		s.metrics.ObservePubSub(metrics.PubSubMalformed)
		s.metrics.ObserveRejected(metrics.ReasonNoTarget, metrics.SourcePubSub)
		return
	}

	req := actions.ParseRequest(msg.Request)

	name, ok := s.resolveName(msg, req)
	if !ok {
		s.metrics.ObservePubSub(metrics.PubSubMalformed)
		s.metrics.ObserveRejected(metrics.ReasonMalformed, metrics.SourcePubSub)
		return
	}

	fn, ok := actions.Lookup(name)
	if !ok {
		s.log.Error("api call not found", "method", name, "container_name", msg.ContainerName)
		s.metrics.ObservePubSub(metrics.PubSubUnknown)
		s.metrics.ObserveRejected(metrics.ReasonUnknownCall, metrics.SourcePubSub)
		return
	}

	s.log.Info("api call", "method", name, "container_name", msg.ContainerName)

	start := time.Now()
	res := fn(ctx, s.env, req, dockerclient.Target{ContainerName: msg.ContainerName})

	s.metrics.ObserveAction(name, metrics.SourcePubSub, time.Since(start).Seconds())
	s.metrics.ObservePubSub(metrics.PubSubHandled)

	s.log.Debug("api call finished", "method", name, "response", string(res.Body))
}

// resolveName builds the action's name.
//
// When post_action was "exec" and cmd or task were missing, the name stayed unbound
// in main.py:232 and the access failed with a NameError.
func (s *Subscriber) resolveName(msg Message, req actions.Request) (string, bool) {
	if msg.PostAction != "exec" {
		return actions.MethodName(msg.PostAction, "", ""), true
	}

	if len(msg.Request) == 0 {
		s.log.Error("api call: request missing")
		return "", false
	}

	cmd, ok := req.String("cmd")
	if !ok {
		s.log.Error("api call: cmd missing")
		return "", false
	}

	task, ok := req.String("task")
	if !ok {
		s.log.Error("api call: task missing")
		return "", false
	}

	return actions.MethodName(msg.PostAction, cmd, task), true
}
