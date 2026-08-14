package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"bodsch.me/mailcow-dockerapi/internal/actions"
	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
	"bodsch.me/mailcow-dockerapi/internal/metrics"
)

// maxRequestBody bounds a request body. FastAPI had no limit; the service runs
// privileged and should not be knocked over by an oversized body.
const maxRequestBody = 4 << 20 // 4 MiB

// Error messages from main.py.
const (
	msgInvalidID     = "no or invalid id defined"
	msgNoContainer   = "no container found"
	msgInvalidAction = "invalid container id or missing action"
	msgCmdMissing    = "cmd is missing"
	msgTaskMissing   = "task is missing"
)

// isAlnum reproduces str.isalnum() from main.py:87.
//
// Python evaluates that method Unicode-aware; the narrower ASCII reading applies
// here. Docker ids are hexadecimal, so the restriction changes nothing for valid
// input and rejects exotic values earlier.
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

// handleGetContainer serves GET /containers/{container_id}/json.
func (s *Server) handleGetContainer(w http.ResponseWriter, r *http.Request) {
	containerID := r.PathValue("container_id")
	if !isAlnum(containerID) {
		s.metrics.ObserveRejected(metrics.ReasonNoTarget, metrics.SourceHTTP)
		s.write(w, actions.Danger(msgInvalidID))
		return
	}

	// As in the original, the list of running containers is searched and the id
	// compared in full — a direct inspect would also find stopped containers.
	list, err := s.docker.ListAll(r.Context(), false)
	if err != nil {
		s.write(w, actions.Danger(err.Error()))
		return
	}

	for _, c := range list {
		if c.ID != containerID {
			continue
		}

		raw, err := s.docker.InspectRaw(r.Context(), c.ID)
		if err != nil {
			s.write(w, actions.Danger(err.Error()))
			return
		}

		s.write(w, actions.JSON(json.RawMessage(raw)))
		return
	}

	s.write(w, actions.Danger(msgNoContainer))
}

// handleListContainers serves GET /containers/json.
//
// The response is an object mapping every container id onto its inspect output.
func (s *Server) handleListContainers(w http.ResponseWriter, r *http.Request) {
	all := parseBool(r.URL.Query().Get("all"))

	list, err := s.docker.ListAll(r.Context(), all)
	if err != nil {
		s.write(w, actions.Danger(err.Error()))
		return
	}

	containers := make(map[string]json.RawMessage, len(list))
	for _, c := range list {
		raw, err := s.docker.InspectRaw(r.Context(), c.ID)
		if err != nil {
			s.write(w, actions.Danger(err.Error()))
			return
		}
		containers[c.ID] = raw
	}

	s.write(w, actions.JSON(containers))
}

// parseBool reads the query parameter the way FastAPI did for a bool argument.
func parseBool(v string) bool {
	switch strings.ToLower(v) {
	case "1", "true", "on", "yes", "y", "t":
		return true
	default:
		return false
	}
}

// handleContainerPost serves POST /containers/{container_id}/{post_action} and
// resolves the matching action.
func (s *Server) handleContainerPost(w http.ResponseWriter, r *http.Request) {
	containerID := r.PathValue("container_id")
	postAction := r.PathValue("post_action")

	if !isAlnum(containerID) || postAction == "" {
		s.metrics.ObserveRejected(metrics.ReasonNoTarget, metrics.SourceHTTP)
		s.write(w, actions.Danger(msgInvalidAction))
		return
	}

	// A missing or invalid body yields an empty request — main.py:133 swallowed
	// the error in the same way.
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBody))
	if err != nil {
		body = nil
	}
	req := actions.ParseRequest(body)

	name, errRes := s.resolveName(postAction, req)
	if errRes != nil {
		s.metrics.ObserveRejected(metrics.ReasonMalformed, metrics.SourceHTTP)
		s.write(w, *errRes)
		return
	}

	fn, ok := actions.Lookup(name)
	if !ok {
		s.log.Error("unknown api call", "method", name, "container_id", containerID)
		s.metrics.ObserveRejected(metrics.ReasonUnknownCall, metrics.SourceHTTP)
		s.write(w, actions.Danger(actions.MsgUnknownAPICall))
		return
	}

	s.log.Info("api call", "method", name, "container_id", containerID)

	start := time.Now()
	res := fn(r.Context(), s.env, req, dockerclient.Target{ContainerID: containerID})
	s.metrics.ObserveAction(name, metrics.SourceHTTP, time.Since(start).Seconds())

	s.write(w, res)
}

// resolveName builds the action's name from the post action and — for exec — from
// the body's cmd and task fields.
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
