package config

import (
	"strings"
	"testing"
	"time"
)

// lookupFrom turns a map into a Lookup, so the tests never touch the process
// environment — on a developer machine DOCKER_HOST is typically set.
func lookupFrom(m map[string]string) Lookup {
	return func(key string) (string, bool) {
		v, ok := m[key]
		return v, ok
	}
}

func loadWith(t *testing.T, overrides map[string]string) *Config {
	t.Helper()

	cfg, err := Load(lookupFrom(overrides))
	if err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}
	return cfg
}

func TestLoadDefaults(t *testing.T) {
	cfg := loadWith(t, nil)

	if got, want := cfg.Redis.Addr, DefaultRedisAddr; got != want {
		t.Errorf("Redis.Addr = %q, want %q", got, want)
	}
	if got, want := cfg.Redis.Channel, DefaultChannel; got != want {
		t.Errorf("Redis.Channel = %q, want %q", got, want)
	}
	if got, want := cfg.Redis.DB, 0; got != want {
		t.Errorf("Redis.DB = %d, want %d", got, want)
	}
	if got, want := cfg.Server.Listen, DefaultListen; got != want {
		t.Errorf("Server.Listen = %q, want %q", got, want)
	}
	if got, want := cfg.Server.CertFile, DefaultCertFile; got != want {
		t.Errorf("Server.CertFile = %q, want %q", got, want)
	}
	if got, want := cfg.Docker.Host, DefaultDockerHost; got != want {
		t.Errorf("Docker.Host = %q, want %q", got, want)
	}
	if got, want := cfg.Stats.Timeout, DefaultStatsTimeout; got != want {
		t.Errorf("Stats.Timeout = %v, want %v", got, want)
	}
	if got, want := cfg.Obs.Listen, DefaultObsListen; got != want {
		t.Errorf("Obs.Listen = %q, want %q", got, want)
	}
	if got, want := cfg.Log.Level, "info"; got != want {
		t.Errorf("Log.Level = %q, want %q", got, want)
	}
	// The Python implementation's format stays the default so existing log
	// processing keeps working.
	if got, want := cfg.Log.Format, "python"; got != want {
		t.Errorf("Log.Format = %q, want %q", got, want)
	}
}

func TestLoadRedisSlaveof(t *testing.T) {
	tests := []struct {
		name string
		ip   string
		port string
		want string
	}{
		{"ip and port", "10.0.0.5", "6380", "10.0.0.5:6380"},
		{"ip without a port", "10.0.0.5", "", "10.0.0.5:6379"},
		{"an empty ip falls back to the default", "", "6380", DefaultRedisAddr},
		{"an IPv6 address is bracketed", "fd00::1", "6379", "[fd00::1]:6379"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := loadWith(t, map[string]string{
				"REDIS_SLAVEOF_IP":   tt.ip,
				"REDIS_SLAVEOF_PORT": tt.port,
			})

			if got := cfg.Redis.Addr; got != tt.want {
				t.Errorf("Redis.Addr = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLoadPassthrough(t *testing.T) {
	cfg := loadWith(t, map[string]string{
		"REDISPASS":               "secret",
		"DBROOT":                  "root-secret",
		"REDIS_DB":                "3",
		"REDIS_CHANNEL":           "OTHER_CHANNEL",
		"DOCKERAPI_STATS_TIMEOUT": "5s",
		"DOCKERAPI_LISTEN":        ":8443",
		"DOCKER_HOST":             "tcp://docker.example.org:2376",
		"LOG_LEVEL":               "DEBUG",
		"LOG_FORMAT":              "JSON",
	})

	if got, want := cfg.Redis.Password, "secret"; got != want {
		t.Errorf("Redis.Password = %q, want %q", got, want)
	}
	if got, want := cfg.DB.Root, "root-secret"; got != want {
		t.Errorf("DB.Root = %q, want %q", got, want)
	}
	if got, want := cfg.Redis.DB, 3; got != want {
		t.Errorf("Redis.DB = %d, want %d", got, want)
	}
	if got, want := cfg.Redis.Channel, "OTHER_CHANNEL"; got != want {
		t.Errorf("Redis.Channel = %q, want %q", got, want)
	}
	if got, want := cfg.Stats.Timeout, 5*time.Second; got != want {
		t.Errorf("Stats.Timeout = %v, want %v", got, want)
	}
	if got, want := cfg.Server.Listen, ":8443"; got != want {
		t.Errorf("Server.Listen = %q, want %q", got, want)
	}
	if got, want := cfg.Docker.Host, "tcp://docker.example.org:2376"; got != want {
		t.Errorf("Docker.Host = %q, want %q", got, want)
	}
	// Level and format are case-insensitive.
	if got, want := cfg.Log.Level, "debug"; got != want {
		t.Errorf("Log.Level = %q, want %q", got, want)
	}
	if got, want := cfg.Log.Format, "json"; got != want {
		t.Errorf("Log.Format = %q, want %q", got, want)
	}
}

func TestLoadDevModeImpliesDebug(t *testing.T) {
	cfg := loadWith(t, map[string]string{"DEV_MODE": "y"})
	if got, want := cfg.Log.Level, "debug"; got != want {
		t.Errorf("Log.Level = %q, want %q", got, want)
	}

	// An explicit level wins over DEV_MODE.
	cfg = loadWith(t, map[string]string{"DEV_MODE": "y", "LOG_LEVEL": "warn"})
	if got, want := cfg.Log.Level, "warn"; got != want {
		t.Errorf("Log.Level = %q, want %q", got, want)
	}
}

func TestLoadRejects(t *testing.T) {
	tests := []struct {
		name  string
		env   map[string]string
		wants string
	}{
		{"REDIS_DB is not a number", map[string]string{"REDIS_DB": "not-a-number"}, "REDIS_DB"},
		{"REDIS_DB is negative", map[string]string{"REDIS_DB": "-1"}, "REDIS_DB"},
		{"the slaveof port is not a number", map[string]string{
			"REDIS_SLAVEOF_IP":   "10.0.0.5",
			"REDIS_SLAVEOF_PORT": "soon",
		}, "REDIS_SLAVEOF_PORT"},
		{"the stats timeout is not a duration", map[string]string{"DOCKERAPI_STATS_TIMEOUT": "soon"}, "DOCKERAPI_STATS_TIMEOUT"},
		{"the stats timeout is zero", map[string]string{"DOCKERAPI_STATS_TIMEOUT": "0s"}, "DOCKERAPI_STATS_TIMEOUT"},
		{"DOCKER_HOST has no scheme", map[string]string{"DOCKER_HOST": "/var/run/docker.sock"}, "DOCKER_HOST"},
		{"DOCKER_HOST has an unknown scheme", map[string]string{"DOCKER_HOST": "ftp://example.org"}, "DOCKER_HOST"},
		{"the certificate and the key are the same file", map[string]string{
			"DOCKERAPI_CERT": "/app/both.pem",
			"DOCKERAPI_KEY":  "/app/both.pem",
		}, "must not be the same file"},
		{"the log level is unknown", map[string]string{"LOG_LEVEL": "chatty"}, "LOG_LEVEL"},
		{"the log format is unknown", map[string]string{"LOG_FORMAT": "yaml"}, "LOG_FORMAT"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(lookupFrom(tt.env))
			if err == nil {
				t.Fatalf("Load: expected an error mentioning %q", tt.wants)
			}
			if !strings.Contains(err.Error(), tt.wants) {
				t.Errorf("Load: error %q does not mention %q", err, tt.wants)
			}
		})
	}
}

// A value that is only whitespace counts as unset, so mailcow.conf entries left
// blank by hand fall back to the default instead of producing an empty setting.
func TestLoadTreatsBlankValuesAsUnset(t *testing.T) {
	cfg := loadWith(t, map[string]string{
		"DOCKERAPI_LISTEN": "  ",
		"REDIS_CHANNEL":    " ",
	})

	if got, want := cfg.Server.Listen, DefaultListen; got != want {
		t.Errorf("Server.Listen = %q, want %q", got, want)
	}
	if got, want := cfg.Redis.Channel, DefaultChannel; got != want {
		t.Errorf("Redis.Channel = %q, want %q", got, want)
	}
}

// validate also guards the invariants Load cannot produce on its own, so a future
// change to Load cannot start a service that talks to nothing.
func TestValidateGuardsEmptyValues(t *testing.T) {
	valid := func() *Config {
		return &Config{
			Server: Server{Listen: DefaultListen, CertFile: DefaultCertFile, KeyFile: DefaultKeyFile},
			Docker: Docker{Host: DefaultDockerHost},
			Redis:  Redis{Addr: DefaultRedisAddr, Channel: DefaultChannel},
			Stats:  Stats{Timeout: DefaultStatsTimeout},
			Log:    Log{Level: "info", Format: "python"},
		}
	}
	if err := valid().validate(); err != nil {
		t.Fatalf("the reference configuration should be valid: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Config)
		wants  string
	}{
		{"no listen address", func(c *Config) { c.Server.Listen = "" }, "DOCKERAPI_LISTEN"},
		{"no channel", func(c *Config) { c.Redis.Channel = "" }, "REDIS_CHANNEL"},
		{"no Redis endpoint", func(c *Config) { c.Redis.Addr = "" }, "Redis endpoint"},
		{"a Redis endpoint without a port", func(c *Config) { c.Redis.Addr = "redis-mailcow" }, "host:port"},
		{"no key file", func(c *Config) { c.Server.KeyFile = "" }, "DOCKERAPI_KEY"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid()
			tt.mutate(cfg)

			err := cfg.validate()
			if err == nil {
				t.Fatalf("validate: expected an error mentioning %q", tt.wants)
			}
			if !strings.Contains(err.Error(), tt.wants) {
				t.Errorf("validate: error %q does not mention %q", err, tt.wants)
			}
		})
	}
}

// The accumulator reports every malformed variable at once, so a misconfigured
// deployment does not have to be fixed one restart at a time.
func TestLoadReportsEveryBadVariable(t *testing.T) {
	_, err := Load(lookupFrom(map[string]string{
		"REDIS_DB":                "nope",
		"DOCKERAPI_STATS_TIMEOUT": "soon",
	}))
	if err == nil {
		t.Fatal("Load: expected an error")
	}
	for _, want := range []string{"REDIS_DB", "DOCKERAPI_STATS_TIMEOUT"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}
