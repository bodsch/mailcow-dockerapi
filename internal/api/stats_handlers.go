package api

import (
	"encoding/json"
	"net/http"

	"bodsch.me/mailcow-dockerapi/internal/actions"
	"bodsch.me/mailcow-dockerapi/internal/metrics"
)

// handleHostStats serves GET /host/stats.
//
// main.py triggered a refresh when needed and then waited in an endless loop for
// the key to appear in Redis. When it never did — because Redis was unreachable,
// say — the request never returned. The StatsProvider gives up after a deadline
// instead.
func (s *Server) handleHostStats(w http.ResponseWriter, r *http.Request) {
	raw, err := s.stats.HostStats(r.Context())
	if err != nil {
		s.log.Error("cannot serve the host statistics", "err", err)
		s.metrics.ObserveStats("host", "timeout")
		s.write(w, actions.Danger(err.Error()))
		return
	}

	s.metrics.ObserveStats("host", "hit")
	s.write(w, actions.JSON(json.RawMessage(raw)))
}

// handleContainerStats serves POST /container/{container_id}/stats/update.
func (s *Server) handleContainerStats(w http.ResponseWriter, r *http.Request) {
	containerID := r.PathValue("container_id")

	// The original's handler had no such check; it lived in get_container_stats,
	// which wrote nothing to Redis for an invalid id — leaving the waiting request
	// in an endless loop.
	if !isAlnum(containerID) {
		s.metrics.ObserveRejected(metrics.ReasonNoTarget, metrics.SourceHTTP)
		s.write(w, actions.Danger(msgInvalidID))
		return
	}

	raw, err := s.stats.ContainerStats(r.Context(), containerID)
	if err != nil {
		s.log.Error("cannot serve the container statistics", "container_id", containerID, "err", err)
		s.metrics.ObserveStats("container", "timeout")
		s.write(w, actions.Danger(err.Error()))
		return
	}

	s.metrics.ObserveStats("container", "hit")
	s.write(w, actions.JSON(json.RawMessage(raw)))
}
