// Package dockerclient wraps access to the Docker daemon behind a narrow
// interface.
//
// The Python implementation kept two clients side by side (docker synchronously
// for the actions, aiodocker asynchronously for stats and inspect). One is enough
// here. The interface covers only the operations DockerApi.py actually used, which
// is what makes the actions testable without a running daemon.
package dockerclient

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// ErrNoTarget signals a call with neither a container id nor a name.
//
// DockerApi.py leaves its filters variable undefined in that case and runs into a
// NameError; here it is a regular error.
var ErrNoTarget = errors.New("neither container_id nor container_name was given")

// Target selects the containers an action operates on.
//
// The selection follows DockerApi.py: an id takes precedence over a name. The name
// is evaluated as a substring pattern, as usual for Docker, so it can match several
// containers.
type Target struct {
	ContainerID   string
	ContainerName string
}

// Valid reports whether at least one selection criterion is set.
func (t Target) Valid() bool {
	return t.ContainerID != "" || t.ContainerName != ""
}

// Container is the subset of the container summary the actions need.
type Container struct {
	ID    string
	Names []string
	State string

	// Labels are the container's labels. Compose puts the service name into
	// com.docker.compose.service, which is how internal/peers names a peer that
	// mailcow started.
	Labels map[string]string

	// Endpoints are the networks the container is attached to.
	Endpoints []Endpoint
}

// Endpoint is a container's attachment to one Docker network.
//
// GET /containers/json reports these along with everything else, so collecting
// them costs no additional call. internal/peers turns them into the mapping from
// a remote address to the container behind it.
type Endpoint struct {
	// Network is the network's name, such as mailcowdockerized_mailcow-network.
	Network string
	// IPs are the container's addresses on that network, IPv4 before IPv6.
	IPs []string
}

// ExecOptions describes a single `docker exec`.
type ExecOptions struct {
	// Cmd is the complete argv. Where it contains a shell such as
	// {"/bin/bash", "-c", "..."}, that is a deliberate decision of the action in
	// question.
	Cmd []string
	// User matches the user argument of docker-py's exec_run.
	User string
}

// ExecResult holds the exit code and the merged output.
//
// docker-py's exec_run with demux=False returns stdout and stderr in one stream;
// that behaviour is kept, because exec_run_handler in DockerApi.py builds on it.
type ExecResult struct {
	ExitCode int
	Output   []byte
}

// InteractiveOptions describes the special case from DockerApi.py:580 — a shell is
// opened with stdin attached, the command is written into it and the output is
// collected with a timing heuristic. Only the rspamd password change uses this.
type InteractiveOptions struct {
	// Shell is the interpreter to start; empty means /bin/bash.
	Shell string
	// Command is written to stdin; a missing newline is appended.
	Command string
	User    string
	// Timeout is the idle span after which collecting ends. Zero means
	// DefaultInteractiveTimeout.
	Timeout time.Duration
}

// DefaultInteractiveTimeout matches timeout=2 from DockerApi.py:580.
const DefaultInteractiveTimeout = 2 * time.Second

// DefaultShell matches shell_cmd="/bin/bash" from DockerApi.py:580.
const DefaultShell = "/bin/bash"

// TopResult matches the response of GET /containers/{id}/top.
type TopResult struct {
	Titles    []string   `json:"Titles"`
	Processes [][]string `json:"Processes"`
}

// API is the slice of the Docker API the rest of the service needs.
type API interface {
	// List returns the containers matching t. all includes stopped containers —
	// the lifecycle actions set it, the exec actions do not.
	List(ctx context.Context, t Target, all bool) ([]Container, error)

	// ListAll returns every container, unfiltered (route /containers/json).
	ListAll(ctx context.Context, all bool) ([]Container, error)

	// InspectRaw returns the unmodified response of GET /containers/{id}/json.
	// Passing the raw bytes through preserves fields a Go struct would drop.
	InspectRaw(ctx context.Context, id string) (json.RawMessage, error)

	Start(ctx context.Context, id string) error
	Stop(ctx context.Context, id string) error
	Restart(ctx context.Context, id string) error

	Top(ctx context.Context, id string) (TopResult, error)

	// Stats returns a single sample of GET /containers/{id}/stats including
	// precpu_stats, so the UI can compute CPU load.
	Stats(ctx context.Context, id string) (json.RawMessage, error)

	Exec(ctx context.Context, id string, opts ExecOptions) (ExecResult, error)
	ExecInteractive(ctx context.Context, id string, opts InteractiveOptions) (string, error)

	Close() error
}
