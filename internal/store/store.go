// Package store kapselt den Redis-Zwischenspeicher für Messwerte.
//
// Die Schlüssel und Verfallszeiten entsprechen DockerApi.py: host_stats mit
// zehn Sekunden, <container_id>_stats mit sechzig.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// Store liest und schreibt zwischengespeicherte Werte.
type Store interface {
	// Get liefert den Wert und ob er vorhanden war.
	Get(ctx context.Context, key string) (json.RawMessage, bool, error)
	// Set legt den Wert mit einer Verfallszeit ab.
	Set(ctx context.Context, key string, value json.RawMessage, ttl time.Duration) error
	Close() error
}

// Redis setzt Store auf go-redis um.
type Redis struct {
	client *redis.Client
}

// Options beschreibt die Verbindung.
type Options struct {
	Addr     string
	Password string
	DB       int
}

// NewRedis baut den Redis-gestützten Speicher.
func NewRedis(opts Options) *Redis {
	return &Redis{client: redis.NewClient(&redis.Options{
		Addr:     opts.Addr,
		Password: opts.Password,
		DB:       opts.DB,
	})}
}

// Client gibt den zugrunde liegenden Client heraus – der PubSub-Empfänger
// benötigt ihn.
func (r *Redis) Client() *redis.Client {
	return r.client
}

func (r *Redis) Get(ctx context.Context, key string) (json.RawMessage, bool, error) {
	val, err := r.client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	return json.RawMessage(val), true, nil
}

func (r *Redis) Set(ctx context.Context, key string, value json.RawMessage, ttl time.Duration) error {
	return r.client.Set(ctx, key, []byte(value), ttl).Err()
}

func (r *Redis) Close() error {
	return r.client.Close()
}
