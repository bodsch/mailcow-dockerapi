// Package config reads the dockerapi's runtime configuration from the
// environment.
//
// The variable names and their defaults mirror the Python implementation
// (REDIS_SLAVEOF_IP, REDIS_SLAVEOF_PORT, REDISPASS, DBROOT) so that an existing
// mailcow deployment keeps working unchanged. Everything main.py re-derived at
// the point of use — the Redis endpoint, the certificate paths — is derived here
// once, at startup.
package config

import (
	"fmt"
	"net"
	"strings"
	"time"
)

// Defaults matching the behaviour of main.py.
const (
	DefaultListen     = ":443"
	DefaultCertFile   = "/app/dockerapi_cert.pem"
	DefaultKeyFile    = "/app/dockerapi_key.pem"
	DefaultDockerHost = "unix:///var/run/docker.sock"
	DefaultChannel    = "MC_CHANNEL"

	// DefaultRedisAddr is the endpoint main.py fell back to. The watchdog uses
	// the "redis" alias of the same container because watchdog.sh did; both
	// names resolve inside the mailcow network, and each service keeps the
	// spelling of the implementation it replaces.
	DefaultRedisAddr = "redis-mailcow:6379"

	// DefaultStatsTimeout bounds the wait for freshly computed statistics.
	// main.py polled for these in an endless loop with no way out.
	DefaultStatsTimeout = 30 * time.Second
)

// DefaultObsListen is where the observability endpoint binds.
//
// It sits outside 9100-9999, the Prometheus project's fully allocated exporter
// registry, and avoids every port mailcow uses internally (443, 8081, 8642,
// 9000-9002, 9900, 10001, 10055, 11332-11334, 20000). The watchdog serves the
// same three endpoints one port below, on 9393.
const DefaultObsListen = ":9394"

// Config is the fully resolved runtime configuration.
type Config struct {
	Server Server
	Docker Docker
	Redis  Redis
	DB     DB
	Stats  Stats
	Log    Log
	Obs    Obs
}

// Server describes the HTTPS endpoint the mailcow UI talks to.
type Server struct {
	Listen   string // DOCKERAPI_LISTEN
	CertFile string // DOCKERAPI_CERT
	KeyFile  string // DOCKERAPI_KEY
}

// Docker describes how the daemon is reached.
type Docker struct {
	Host string // DOCKER_HOST
}

// Redis describes the Redis endpoint. When REDIS_SLAVEOF_IP is set, mailcow runs
// Redis in replication and the local instance is read-only, so every client has
// to talk to the primary.
type Redis struct {
	Addr     string // host:port of the instance to talk to
	Password string // REDISPASS
	DB       int    // REDIS_DB
	Channel  string // REDIS_CHANNEL, the channel mailcow publishes jobs on
}

// DB holds the database credential the system actions need.
type DB struct {
	// Root is the MySQL root password mysql_upgrade and mysql_tzinfo_to_sql
	// require. It is only read by those two actions.
	Root string // DBROOT
}

// Stats configures the metrics cache.
type Stats struct {
	Timeout time.Duration // DOCKERAPI_STATS_TIMEOUT
}

// Log configures the structured logger.
type Log struct {
	Level  string // LOG_LEVEL: debug|info|warn|error
	Format string // LOG_FORMAT: python|json|text
}

// Obs configures the observability endpoint serving /metrics, /healthz and
// /readyz. An empty Listen disables the server entirely.
type Obs struct {
	Listen string // DOCKERAPI_METRICS_LISTEN, e.g. ":9394"
}

// validate reports the first configuration problem that would make the service
// misbehave silently.
func (c *Config) validate() error {
	if c.Server.Listen == "" {
		return fmt.Errorf("DOCKERAPI_LISTEN must not be empty")
	}
	// The service generates the pair when it is missing, so it needs both paths
	// even on a first start.
	if c.Server.CertFile == "" || c.Server.KeyFile == "" {
		return fmt.Errorf("DOCKERAPI_CERT and DOCKERAPI_KEY must both be set")
	}
	if c.Server.CertFile == c.Server.KeyFile {
		return fmt.Errorf("DOCKERAPI_CERT and DOCKERAPI_KEY must not be the same file")
	}
	if scheme, _, ok := strings.Cut(c.Docker.Host, "://"); ok {
		switch scheme {
		case "unix", "tcp", "http", "https", "npipe":
		default:
			return fmt.Errorf("DOCKER_HOST must use unix://, tcp://, http:// or https:// (got %q)", c.Docker.Host)
		}
	} else {
		return fmt.Errorf("DOCKER_HOST needs a scheme such as unix:// (got %q)", c.Docker.Host)
	}
	if c.Redis.Addr == "" {
		return fmt.Errorf("no Redis endpoint resolved")
	}
	if _, _, err := net.SplitHostPort(c.Redis.Addr); err != nil {
		return fmt.Errorf("the Redis endpoint %q is not host:port: %w", c.Redis.Addr, err)
	}
	if c.Redis.Channel == "" {
		return fmt.Errorf("REDIS_CHANNEL must not be empty")
	}
	// A non-positive timeout would abort every stats request before the
	// collector had a chance to answer.
	if c.Stats.Timeout <= 0 {
		return fmt.Errorf("DOCKERAPI_STATS_TIMEOUT must be positive (got %v)", c.Stats.Timeout)
	}
	switch c.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("LOG_LEVEL must be one of debug, info, warn, error (got %q)", c.Log.Level)
	}
	switch c.Log.Format {
	case "python", "json", "text":
	default:
		return fmt.Errorf("LOG_FORMAT must be python, json or text (got %q)", c.Log.Format)
	}
	return nil
}
