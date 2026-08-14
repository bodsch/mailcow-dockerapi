package config

import (
	"testing"
	"time"
)

// clearEnv isoliert den Test von der Entwicklungsumgebung – auf einem
// Rechner mit Docker Desktop ist DOCKER_HOST typischerweise gesetzt.
func clearEnv(t *testing.T) {
	t.Helper()

	for _, k := range []string{
		"DOCKERAPI_LISTEN", "DOCKERAPI_CERT", "DOCKERAPI_KEY", "DOCKER_HOST",
		"REDIS_SLAVEOF_IP", "REDIS_SLAVEOF_PORT", "REDISPASS", "REDIS_CHANNEL",
		"REDIS_DB", "DBROOT", "DOCKERAPI_STATS_TIMEOUT",
	} {
		t.Setenv(k, "")
	}
}

func TestLoadDefaults(t *testing.T) {
	clearEnv(t)

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if c.RedisAddr != DefaultRedisAddr {
		t.Errorf("RedisAddr = %q, want %q", c.RedisAddr, DefaultRedisAddr)
	}
	if c.ListenAddr != DefaultListenAddr {
		t.Errorf("ListenAddr = %q, want %q", c.ListenAddr, DefaultListenAddr)
	}
	if c.RedisChannel != DefaultChannel {
		t.Errorf("RedisChannel = %q, want %q", c.RedisChannel, DefaultChannel)
	}
	if c.DockerHost != DefaultDockerHost {
		t.Errorf("DockerHost = %q, want %q", c.DockerHost, DefaultDockerHost)
	}
	if c.StatsTimeout != DefaultStatsTimeout {
		t.Errorf("StatsTimeout = %v, want %v", c.StatsTimeout, DefaultStatsTimeout)
	}
}

func TestLoadRedisSlaveof(t *testing.T) {
	tests := []struct {
		name string
		ip   string
		port string
		want string
	}{
		{"ip und port", "10.0.0.5", "6380", "10.0.0.5:6380"},
		{"ip ohne port", "10.0.0.5", "", "10.0.0.5:6379"},
		{"leere ip faellt auf default", "", "6380", DefaultRedisAddr},
		{"ipv6 wird geklammert", "fd00::1", "6379", "[fd00::1]:6379"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("REDIS_SLAVEOF_IP", tt.ip)
			t.Setenv("REDIS_SLAVEOF_PORT", tt.port)

			c, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if c.RedisAddr != tt.want {
				t.Errorf("RedisAddr = %q, want %q", c.RedisAddr, tt.want)
			}
		})
	}
}

func TestLoadPassthrough(t *testing.T) {
	clearEnv(t)
	t.Setenv("REDISPASS", "geheim")
	t.Setenv("DBROOT", "wurzel")
	t.Setenv("REDIS_DB", "3")
	t.Setenv("DOCKERAPI_STATS_TIMEOUT", "5s")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if c.RedisPassword != "geheim" {
		t.Errorf("RedisPassword = %q", c.RedisPassword)
	}
	if c.DBRoot != "wurzel" {
		t.Errorf("DBRoot = %q", c.DBRoot)
	}
	if c.RedisDB != 3 {
		t.Errorf("RedisDB = %d, want 3", c.RedisDB)
	}
	if c.StatsTimeout != 5*time.Second {
		t.Errorf("StatsTimeout = %v, want 5s", c.StatsTimeout)
	}
}

func TestLoadInvalidValues(t *testing.T) {
	t.Run("REDIS_DB", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("REDIS_DB", "keine-zahl")
		if _, err := Load(); err == nil {
			t.Fatal("erwarte Fehler bei ungueltigem REDIS_DB")
		}
	})

	t.Run("STATS_TIMEOUT", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("DOCKERAPI_STATS_TIMEOUT", "bald")
		if _, err := Load(); err == nil {
			t.Fatal("erwarte Fehler bei ungueltigem DOCKERAPI_STATS_TIMEOUT")
		}
	})
}
