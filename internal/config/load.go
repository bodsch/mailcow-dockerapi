package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// Lookup resolves an environment variable. os.LookupEnv satisfies it; tests
// substitute a map so that Load can be exercised without touching the process
// environment.
//
// The Lookup type and the env helper at the bottom of this file are shared with
// mailcow-watchdog. Keep both copies in sync — see CONVENTIONS.md.
type Lookup func(key string) (string, bool)

// EnvLookup reads from the process environment.
func EnvLookup(key string) (string, bool) { return os.LookupEnv(key) }

// Load builds a Config from the environment and validates it.
func Load(look Lookup) (*Config, error) {
	if look == nil {
		look = EnvLookup
	}
	e := env{look: look}

	cfg := &Config{
		Server: Server{
			Listen:   e.str("DOCKERAPI_LISTEN", DefaultListen),
			CertFile: e.str("DOCKERAPI_CERT", DefaultCertFile),
			KeyFile:  e.str("DOCKERAPI_KEY", DefaultKeyFile),
		},

		Docker: Docker{
			Host: e.str("DOCKER_HOST", DefaultDockerHost),
		},

		DB: DB{
			Root: e.str("DBROOT", ""),
		},

		Stats: Stats{
			Timeout: e.duration("DOCKERAPI_STATS_TIMEOUT", DefaultStatsTimeout),
		},

		Obs: Obs{
			Listen: e.str("DOCKERAPI_METRICS_LISTEN", DefaultObsListen),
		},
	}

	cfg.Redis = loadRedis(&e)
	cfg.Log = loadLog(&e)

	if err := e.err(); err != nil {
		return nil, err
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// loadRedis reproduces the logic of main.py:36-39: when REDIS_SLAVEOF_IP is set
// that endpoint applies, otherwise the mailcow-internal default.
//
// Unlike main.py, a missing REDIS_SLAVEOF_IP is not an error. The port is read as
// a number rather than pasted in as text, so a typo fails at startup instead of
// as a connection error later.
func loadRedis(e *env) Redis {
	addr := DefaultRedisAddr
	if ip := e.str("REDIS_SLAVEOF_IP", ""); ip != "" {
		port := e.int("REDIS_SLAVEOF_PORT", 6379)
		addr = net.JoinHostPort(ip, strconv.Itoa(port))
	}
	return Redis{
		Addr:     addr,
		Password: e.str("REDISPASS", ""),
		DB:       e.int("REDIS_DB", 0),
		Channel:  e.str("REDIS_CHANNEL", DefaultChannel),
	}
}

// loadLog resolves the log level and format.
//
// The format defaults to the Python implementation's "LEVEL:     message",
// because operators have built their log processing around it; json and text are
// available for anyone who would rather have structured output. DEV_MODE turns on
// debug logging, as it turned on `set -x` in the shell parts of mailcow. An
// explicit LOG_LEVEL still wins.
func loadLog(e *env) Log {
	level := "info"
	if !isNo(e.str("DEV_MODE", "n")) {
		level = "debug"
	}

	return Log{
		Level:  strings.ToLower(e.str("LOG_LEVEL", level)),
		Format: strings.ToLower(e.str("LOG_FORMAT", "python")),
	}
}

// isNo reports whether v is mailcow's negative spelling.
func isNo(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "n", "no":
		return true
	}
	return false
}

// env accumulates parse errors so that Load can report every malformed variable
// at once instead of failing on the first one.
type env struct {
	look Lookup
	errs []string
}

func (e *env) str(key, def string) string {
	v, ok := e.look(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	return strings.TrimSpace(v)
}

func (e *env) int(key string, def int) int {
	raw, ok := e.look(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		e.errs = append(e.errs, fmt.Sprintf("%s must be an integer (got %q)", key, raw))
		return def
	}
	if n < 0 {
		e.errs = append(e.errs, fmt.Sprintf("%s must not be negative (got %d)", key, n))
		return def
	}
	return n
}

func (e *env) duration(key string, def time.Duration) time.Duration {
	raw, ok := e.look(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return def
	}
	d, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		e.errs = append(e.errs, fmt.Sprintf("%s must be a duration such as 30s (got %q)", key, raw))
		return def
	}
	return d
}

func (e *env) err() error {
	if len(e.errs) == 0 {
		return nil
	}
	return fmt.Errorf("invalid configuration: %s", strings.Join(e.errs, "; "))
}
