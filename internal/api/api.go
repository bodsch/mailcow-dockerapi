// Package api stellt die HTTP-Oberfläche bereit, die main.py mit FastAPI
// bediente.
//
// Die Routen, das Antwortformat und der immer gleiche Statuscode 200 bleiben
// unverändert: das mailcow-Frontend wertet ausschließlich das Feld "type" im
// Rumpf aus.
package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"bodsch.me/mailcow-dockerapi/internal/actions"
	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
)

// StatsProvider liefert zwischengespeicherte Messwerte.
type StatsProvider interface {
	// HostStats liefert die Kennzahlen des Wirtssystems.
	HostStats(ctx context.Context) (json.RawMessage, error)
	// ContainerStats liefert den Ringpuffer der letzten Messungen.
	ContainerStats(ctx context.Context, containerID string) (json.RawMessage, error)
}

// Server bündelt die Abhängigkeiten der Handler.
type Server struct {
	Docker dockerclient.API
	Stats  StatsProvider
	Env    actions.Env
	Logger *slog.Logger
}

// New baut den Server samt Router.
func New(docker dockerclient.API, stats StatsProvider, env actions.Env, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}

	return &Server{Docker: docker, Stats: stats, Env: env, Logger: logger}
}

// Handler liefert den Router mit allen Routen aus main.py.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Das genauere Muster gewinnt: /containers/json wird nicht von
	// /containers/{container_id}/json verdeckt.
	mux.HandleFunc("GET /host/stats", s.handleHostStats)
	mux.HandleFunc("GET /containers/json", s.handleListContainers)
	mux.HandleFunc("GET /containers/{container_id}/json", s.handleGetContainer)
	mux.HandleFunc("POST /containers/{container_id}/{post_action}", s.handleContainerPost)

	// Der Pfad steht bewusst im Singular – so lautet er in main.py:178.
	mux.HandleFunc("POST /container/{container_id}/stats/update", s.handleContainerStats)

	return mux
}

// write gibt ein Ergebnis aus. Der Statuscode ist stets 200; Fehler werden
// über das Feld "type" im Rumpf gemeldet.
func (s *Server) write(w http.ResponseWriter, res actions.Result) {
	w.Header().Set("Content-Type", contentTypeHeader(res.ContentType))
	w.WriteHeader(http.StatusOK)

	if _, err := w.Write(res.Body); err != nil {
		s.Logger.Error("response write failed", "error", err)
	}
}

// contentTypeHeader ergänzt die Zeichensatzangabe, die FastAPI mitschickte.
func contentTypeHeader(ct string) string {
	switch ct {
	case actions.ContentTypeText:
		return "text/plain; charset=utf-8"
	default:
		return "application/json"
	}
}
