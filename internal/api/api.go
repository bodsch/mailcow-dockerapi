// Package api provides the HTTP surface main.py served with FastAPI.
//
// The routes, the response format and the invariable status code 200 stay
// unchanged: the mailcow frontend evaluates nothing but the "type" field in the
// body.
package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"bodsch.me/mailcow-dockerapi/internal/actions"
	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
	"bodsch.me/mailcow-dockerapi/internal/metrics"
)

// StatsProvider supplies cached measurements.
type StatsProvider interface {
	// HostStats returns the host's figures.
	HostStats(ctx context.Context) (json.RawMessage, error)
	// ContainerStats returns the ring buffer of the most recent samples.
	ContainerStats(ctx context.Context, containerID string) (json.RawMessage, error)
}

// Options configures the server.
type Options struct {
	Docker  dockerclient.API
	Stats   StatsProvider
	Env     actions.Env
	Metrics *metrics.Metrics
	Log     *slog.Logger
}

// Server bundles the handlers' dependencies.
type Server struct {
	docker  dockerclient.API
	stats   StatsProvider
	env     actions.Env
	metrics *metrics.Metrics
	log     *slog.Logger
}

// New builds the server and its router.
func New(opts Options) *Server {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}

	return &Server{
		docker:  opts.Docker,
		stats:   opts.Stats,
		env:     opts.Env,
		metrics: opts.Metrics,
		log:     log.With("component", "api"),
	}
}

// Handler returns the router with every route from main.py.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// The more specific pattern wins: /containers/json is not shadowed by
	// /containers/{container_id}/json.
	s.handle(mux, "GET /host/stats", s.handleHostStats)
	s.handle(mux, "GET /containers/json", s.handleListContainers)
	s.handle(mux, "GET /containers/{container_id}/json", s.handleGetContainer)
	s.handle(mux, "POST /containers/{container_id}/{post_action}", s.handleContainerPost)

	// The path is deliberately singular — that is how it reads in main.py:178.
	s.handle(mux, "POST /container/{container_id}/stats/update", s.handleContainerStats)

	return mux
}

// handle registers a route and records its request count and latency. The pattern
// is used as the metric label, so container ids never become label values.
func (s *Server) handle(mux *http.ServeMux, pattern string, h http.HandlerFunc) {
	mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		defer func() { s.metrics.ObserveHTTP(pattern, time.Since(start).Seconds()) }()

		h(w, r)
	})
}

// write emits a result. The status code is always 200; errors are reported in the
// body's "type" field.
func (s *Server) write(w http.ResponseWriter, res actions.Result) {
	w.Header().Set("Content-Type", contentTypeHeader(res.ContentType))
	w.WriteHeader(http.StatusOK)

	if _, err := w.Write(res.Body); err != nil {
		s.log.Error("cannot write the response", "err", err)
	}
}

// contentTypeHeader adds the charset FastAPI used to send along.
func contentTypeHeader(ct string) string {
	switch ct {
	case actions.ContentTypeText:
		return "text/plain; charset=utf-8"
	default:
		return "application/json"
	}
}
