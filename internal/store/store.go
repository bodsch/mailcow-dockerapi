// Package store is the dockerapi's Redis cache for statistics.
//
// The keys and their expiries are the ones DockerApi.py used: host_stats with ten
// seconds, <container_id>_stats with sixty. The mailcow UI reads them, so the
// names and record shapes are fixed by the consumer rather than by this package.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Store reads and writes cached values. Everything that talks to Redis takes this
// interface so it can be faked in tests.
type Store interface {
	// Get returns a value and whether the key existed.
	Get(ctx context.Context, key string) (json.RawMessage, bool, error)
	// Set stores a value with an expiry.
	Set(ctx context.Context, key string, value json.RawMessage, ttl time.Duration) error
	// Close releases the connection pool.
	Close() error
}

// Redis is the production Store.
type Redis struct {
	client *redis.Client
}

// Options configures the connection.
type Options struct {
	Addr     string
	Password string
	DB       int
}

// New returns a Store backed by a real Redis connection.
func New(opts Options) *Redis {
	return &Redis{client: redis.NewClient(&redis.Options{
		Addr:     opts.Addr,
		Password: opts.Password,
		DB:       opts.DB,
	})}
}

// Client hands out the underlying client, which the PubSub subscriber needs in
// order to subscribe. Nothing else should reach past the interface.
func (r *Redis) Client() *redis.Client {
	return r.client
}

// Ping reports whether the instance answers. Startup waits on this rather than
// letting the first request discover that Redis is not up yet.
func (r *Redis) Ping(ctx context.Context) error {
	return r.client.Ping(ctx).Err()
}

// Get implements Store. A missing key is reported through the boolean rather than
// as an error, because "not computed yet" is the normal state of a cache entry.
func (r *Redis) Get(ctx context.Context, key string) (json.RawMessage, bool, error) {
	val, err := r.client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("GET %s: %w", key, err)
	}

	return json.RawMessage(val), true, nil
}

// Set implements Store.
func (r *Redis) Set(ctx context.Context, key string, value json.RawMessage, ttl time.Duration) error {
	if err := r.client.Set(ctx, key, []byte(value), ttl).Err(); err != nil {
		return fmt.Errorf("SET %s: %w", key, err)
	}
	return nil
}

// Close implements Store.
func (r *Redis) Close() error {
	return r.client.Close()
}
