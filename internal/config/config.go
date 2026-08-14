// Package config liest die Laufzeitkonfiguration aus der Umgebung.
//
// Die Variablennamen entsprechen denen der Python-Implementierung
// (REDIS_SLAVEOF_IP, REDIS_SLAVEOF_PORT, REDISPASS, DBROOT), damit ein
// bestehendes mailcow-Deployment unverändert weiterläuft.
package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"time"
)

// Defaults, die dem Verhalten von main.py entsprechen.
const (
	DefaultListenAddr = ":443"
	DefaultCertFile   = "/app/dockerapi_cert.pem"
	DefaultKeyFile    = "/app/dockerapi_key.pem"
	DefaultDockerHost = "unix:///var/run/docker.sock"
	DefaultRedisAddr  = "redis-mailcow:6379"
	DefaultChannel    = "MC_CHANNEL"

	// StatsTimeout begrenzt das Warten auf frisch berechnete Statistiken.
	// main.py pollt hier in einer Endlosschleife ohne Abbruchbedingung.
	DefaultStatsTimeout = 30 * time.Second
)

// Config bündelt alle Laufzeiteinstellungen.
type Config struct {
	ListenAddr string
	CertFile   string
	KeyFile    string

	DockerHost string

	RedisAddr     string
	RedisPassword string
	RedisDB       int
	RedisChannel  string

	// DBRoot ist das MySQL-root-Passwort, das die system-Actions
	// mysql_upgrade und mysql_tzinfo_to_sql benötigen.
	DBRoot string

	StatsTimeout time.Duration
}

// Load baut die Konfiguration aus der Umgebung.
//
// Anders als main.py:36 wirft ein fehlendes REDIS_SLAVEOF_IP keinen Fehler,
// sondern führt zum Default redis-mailcow:6379.
func Load() (Config, error) {
	c := Config{
		ListenAddr:    env("DOCKERAPI_LISTEN", DefaultListenAddr),
		CertFile:      env("DOCKERAPI_CERT", DefaultCertFile),
		KeyFile:       env("DOCKERAPI_KEY", DefaultKeyFile),
		DockerHost:    env("DOCKER_HOST", DefaultDockerHost),
		RedisPassword: os.Getenv("REDISPASS"),
		RedisChannel:  env("REDIS_CHANNEL", DefaultChannel),
		DBRoot:        os.Getenv("DBROOT"),
		StatsTimeout:  DefaultStatsTimeout,
	}

	c.RedisAddr = redisAddr()

	if raw := os.Getenv("REDIS_DB"); raw != "" {
		db, err := strconv.Atoi(raw)
		if err != nil {
			return Config{}, fmt.Errorf("REDIS_DB: %w", err)
		}
		c.RedisDB = db
	}

	if raw := os.Getenv("DOCKERAPI_STATS_TIMEOUT"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return Config{}, fmt.Errorf("DOCKERAPI_STATS_TIMEOUT: %w", err)
		}
		c.StatsTimeout = d
	}

	return c, nil
}

// redisAddr bildet die Logik aus main.py:36-39 nach: ist REDIS_SLAVEOF_IP
// gesetzt, gilt dieser Endpunkt, sonst der mailcow-interne Default.
func redisAddr() string {
	ip := os.Getenv("REDIS_SLAVEOF_IP")
	if ip == "" {
		return DefaultRedisAddr
	}

	port := os.Getenv("REDIS_SLAVEOF_PORT")
	if port == "" {
		port = "6379"
	}

	return net.JoinHostPort(ip, port)
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
