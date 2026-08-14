package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"bodsch.me/mailcow-dockerapi/internal/actions"
	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
)

// maxRequestBody begrenzt den Rumpf einer Anfrage. FastAPI kannte keine
// Grenze; der Dienst läuft privilegiert und sollte sich nicht über einen
// übergroßen Rumpf lahmlegen lassen.
const maxRequestBody = 4 << 20 // 4 MiB

// Fehlermeldungen aus main.py.
const (
	msgInvalidID     = "no or invalid id defined"
	msgNoContainer   = "no container found"
	msgInvalidAction = "invalid container id or missing action"
	msgCmdMissing    = "cmd is missing"
	msgTaskMissing   = "task is missing"
)

// isAlnum bildet str.isalnum() aus main.py:87 nach.
//
// Python wertet die Methode Unicode-bewusst aus; hier gilt die engere
// ASCII-Fassung. Docker-Kennungen sind hexadezimal, die Einschränkung ändert
// für gültige Eingaben nichts und weist exotische Werte früher ab.
func isAlnum(s string) bool {
	if s == "" {
		return false
	}

	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		default:
			return false
		}
	}

	return true
}

// handleGetContainer bedient GET /containers/{container_id}/json.
func (s *Server) handleGetContainer(w http.ResponseWriter, r *http.Request) {
	containerID := r.PathValue("container_id")
	if !isAlnum(containerID) {
		s.write(w, actions.Danger(msgInvalidID))
		return
	}

	// Wie im Original wird die Liste der laufenden Container durchsucht und
	// die Kennung vollständig verglichen – ein direktes Inspect würde auch
	// gestoppte Container finden.
	list, err := s.Docker.ListAll(r.Context(), false)
	if err != nil {
		s.write(w, actions.Danger(err.Error()))
		return
	}

	for _, c := range list {
		if c.ID != containerID {
			continue
		}

		raw, err := s.Docker.InspectRaw(r.Context(), c.ID)
		if err != nil {
			s.write(w, actions.Danger(err.Error()))
			return
		}

		s.write(w, actions.JSON(json.RawMessage(raw)))
		return
	}

	s.write(w, actions.Danger(msgNoContainer))
}

// handleListContainers bedient GET /containers/json.
//
// Die Antwort ist ein Objekt, das jede Container-Kennung auf ihre
// Inspect-Ausgabe abbildet.
func (s *Server) handleListContainers(w http.ResponseWriter, r *http.Request) {
	all := parseBool(r.URL.Query().Get("all"))

	list, err := s.Docker.ListAll(r.Context(), all)
	if err != nil {
		s.write(w, actions.Danger(err.Error()))
		return
	}

	containers := make(map[string]json.RawMessage, len(list))
	for _, c := range list {
		raw, err := s.Docker.InspectRaw(r.Context(), c.ID)
		if err != nil {
			s.write(w, actions.Danger(err.Error()))
			return
		}
		containers[c.ID] = raw
	}

	s.write(w, actions.JSON(containers))
}

// parseBool wertet den Abfrageparameter so aus, wie FastAPI es für ein
// bool-Argument tat.
func parseBool(v string) bool {
	switch strings.ToLower(v) {
	case "1", "true", "on", "yes", "y", "t":
		return true
	default:
		return false
	}
}

// handleContainerPost bedient POST /containers/{container_id}/{post_action}
// und löst die passende Action auf.
func (s *Server) handleContainerPost(w http.ResponseWriter, r *http.Request) {
	containerID := r.PathValue("container_id")
	postAction := r.PathValue("post_action")

	if !isAlnum(containerID) || postAction == "" {
		s.write(w, actions.Danger(msgInvalidAction))
		return
	}

	// Ein fehlender oder ungültiger Rumpf ergibt eine leere Anfrage –
	// main.py:133 fing den Fehler ebenso ab.
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBody))
	if err != nil {
		body = nil
	}
	req := actions.ParseRequest(body)

	name, errRes := s.resolveName(postAction, req)
	if errRes != nil {
		s.write(w, *errRes)
		return
	}

	fn, ok := actions.Lookup(name)
	if !ok {
		s.Logger.Error("unknown api call", "method", name, "container_id", containerID)
		s.write(w, actions.Danger(actions.MsgUnknownAPICall))
		return
	}

	s.Logger.Info("api call", "method", name, "container_id", containerID)

	res := fn(r.Context(), s.Env, req, dockerclient.Target{ContainerID: containerID})
	s.write(w, res)
}

// resolveName bildet den Namen der Action aus der Aktion und – bei exec –
// aus den Feldern cmd und task des Rumpfes.
func (s *Server) resolveName(postAction string, req actions.Request) (string, *actions.Result) {
	if postAction != "exec" {
		return actions.MethodName(postAction, "", ""), nil
	}

	cmd, ok := req.String("cmd")
	if !ok {
		res := actions.Danger(msgCmdMissing)
		return "", &res
	}

	task, ok := req.String("task")
	if !ok {
		res := actions.Danger(msgTaskMissing)
		return "", &res
	}

	return actions.MethodName(postAction, cmd, task), nil
}
