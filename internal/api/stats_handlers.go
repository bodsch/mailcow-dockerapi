package api

import (
	"encoding/json"
	"net/http"

	"bodsch.me/mailcow-dockerapi/internal/actions"
)

// handleHostStats bedient GET /host/stats.
//
// main.py stieß bei Bedarf eine Aktualisierung an und wartete danach in einer
// Endlosschleife darauf, dass der Schlüssel in Redis auftaucht. Blieb er aus –
// etwa weil Redis nicht erreichbar war – kehrte die Anfrage nie zurück. Der
// StatsProvider bricht stattdessen nach einer Frist ab.
func (s *Server) handleHostStats(w http.ResponseWriter, r *http.Request) {
	raw, err := s.Stats.HostStats(r.Context())
	if err != nil {
		s.Logger.Error("host stats failed", "error", err)
		s.write(w, actions.Danger(err.Error()))
		return
	}

	s.write(w, actions.JSON(json.RawMessage(raw)))
}

// handleContainerStats bedient POST /container/{container_id}/stats/update.
func (s *Server) handleContainerStats(w http.ResponseWriter, r *http.Request) {
	containerID := r.PathValue("container_id")

	// Die Prüfung fehlte im Handler des Originals; sie steckte in
	// get_container_stats, das bei ungültiger Kennung nichts nach Redis
	// schrieb – die wartende Anfrage lief daraufhin in eine Endlosschleife.
	if !isAlnum(containerID) {
		s.write(w, actions.Danger(msgInvalidID))
		return
	}

	raw, err := s.Stats.ContainerStats(r.Context(), containerID)
	if err != nil {
		s.Logger.Error("container stats failed", "container_id", containerID, "error", err)
		s.write(w, actions.Danger(err.Error()))
		return
	}

	s.write(w, actions.JSON(json.RawMessage(raw)))
}
