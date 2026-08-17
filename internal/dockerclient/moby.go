package dockerclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// mobyAPI implements API on top of the official engine client.
type mobyAPI struct {
	cli *client.Client
}

// New connects to the Docker daemon at host.
//
// version='auto' from main.py:42 has no counterpart any more: the client
// negotiates the API version by default.
func New(host string) (API, error) {
	var opts []client.Opt
	if host != "" {
		opts = append(opts, client.WithHost(host))
	}

	cli, err := client.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("building the docker client: %w", err)
	}

	return &mobyAPI{cli: cli}, nil
}

func (m *mobyAPI) Close() error {
	return m.cli.Close()
}

// filtersFor mirrors the selection in DockerApi.py: container_id wins, otherwise
// container_name.
func filtersFor(t Target) (client.Filters, error) {
	f := client.Filters{}

	switch {
	case t.ContainerID != "":
		return f.Add("id", t.ContainerID), nil
	case t.ContainerName != "":
		return f.Add("name", t.ContainerName), nil
	default:
		return nil, ErrNoTarget
	}
}

func (m *mobyAPI) List(ctx context.Context, t Target, all bool) ([]Container, error) {
	f, err := filtersFor(t)
	if err != nil {
		return nil, err
	}

	res, err := m.cli.ContainerList(ctx, client.ContainerListOptions{All: all, Filters: f})
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}

	return toContainers(res), nil
}

func (m *mobyAPI) ListAll(ctx context.Context, all bool) ([]Container, error) {
	res, err := m.cli.ContainerList(ctx, client.ContainerListOptions{All: all})
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}

	return toContainers(res), nil
}

func toContainers(res client.ContainerListResult) []Container {
	out := make([]Container, 0, len(res.Items))
	for _, item := range res.Items {
		out = append(out, Container{
			ID:        item.ID,
			Names:     item.Names,
			State:     string(item.State),
			Labels:    item.Labels,
			Endpoints: toEndpoints(item.NetworkSettings),
		})
	}
	return out
}

// toEndpoints collects the container's network attachments. A container that is
// on no network of its own — one sharing the host's namespace, say — has none,
// and the daemon may leave the whole section out.
func toEndpoints(ns *container.NetworkSettingsSummary) []Endpoint {
	if ns == nil {
		return nil
	}

	out := make([]Endpoint, 0, len(ns.Networks))
	for name, settings := range ns.Networks {
		if settings == nil {
			continue
		}

		ep := Endpoint{Network: name}
		// A container that is attached but not yet running has an endpoint
		// without an address; netip.Addr renders that as "invalid IP".
		if settings.IPAddress.IsValid() {
			ep.IPs = append(ep.IPs, settings.IPAddress.String())
		}
		if settings.GlobalIPv6Address.IsValid() {
			ep.IPs = append(ep.IPs, settings.GlobalIPv6Address.String())
		}
		out = append(out, ep)
	}

	// Map iteration is randomised and these end up in log lines, so the order is
	// fixed here rather than differing from one call to the next.
	slices.SortFunc(out, func(a, b Endpoint) int { return strings.Compare(a.Network, b.Network) })

	return out
}

func (m *mobyAPI) InspectRaw(ctx context.Context, id string) (json.RawMessage, error) {
	res, err := m.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return nil, fmt.Errorf("inspecting container %s: %w", id, err)
	}

	return res.Raw, nil
}

func (m *mobyAPI) Start(ctx context.Context, id string) error {
	if _, err := m.cli.ContainerStart(ctx, id, client.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("starting container %s: %w", id, err)
	}
	return nil
}

func (m *mobyAPI) Stop(ctx context.Context, id string) error {
	if _, err := m.cli.ContainerStop(ctx, id, client.ContainerStopOptions{}); err != nil {
		return fmt.Errorf("stopping container %s: %w", id, err)
	}
	return nil
}

func (m *mobyAPI) Restart(ctx context.Context, id string) error {
	if _, err := m.cli.ContainerRestart(ctx, id, client.ContainerRestartOptions{}); err != nil {
		return fmt.Errorf("restarting container %s: %w", id, err)
	}
	return nil
}

func (m *mobyAPI) Top(ctx context.Context, id string) (TopResult, error) {
	res, err := m.cli.ContainerTop(ctx, id, client.ContainerTopOptions{})
	if err != nil {
		return TopResult{}, fmt.Errorf("reading the process list of container %s: %w", id, err)
	}

	return TopResult{Titles: res.Titles, Processes: res.Processes}, nil
}

// Stats fetches a single sample.
//
// IncludePreviousSample matches the behaviour of aiodocker's stats(stream=False):
// the daemon takes two measurements one second apart so that precpu_stats is
// populated. Without that field the mailcow UI cannot compute CPU load.
func (m *mobyAPI) Stats(ctx context.Context, id string) (json.RawMessage, error) {
	res, err := m.cli.ContainerStats(ctx, id, client.ContainerStatsOptions{
		Stream:                false,
		IncludePreviousSample: true,
	})
	if err != nil {
		return nil, fmt.Errorf("reading the statistics of container %s: %w", id, err)
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("reading the statistics stream of container %s: %w", id, err)
	}

	// With Stream=false the stream holds exactly one record; a trailing newline
	// would make it invalid JSON.
	return json.RawMessage(strings.TrimSpace(string(raw))), nil
}

func (m *mobyAPI) Exec(ctx context.Context, id string, opts ExecOptions) (ExecResult, error) {
	created, err := m.cli.ExecCreate(ctx, id, client.ExecCreateOptions{
		Cmd:          opts.Cmd,
		User:         opts.User,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return ExecResult{}, fmt.Errorf("creating the exec: %w", err)
	}

	attached, err := m.cli.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		return ExecResult{}, fmt.Errorf("attaching to the exec: %w", err)
	}
	defer attached.Close()

	raw, err := io.ReadAll(attached.Reader)
	if err != nil {
		return ExecResult{}, fmt.Errorf("reading the exec output: %w", err)
	}

	inspect, err := m.cli.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
	if err != nil {
		return ExecResult{}, fmt.Errorf("inspecting the exec: %w", err)
	}

	return ExecResult{ExitCode: inspect.ExitCode, Output: demuxCombined(raw)}, nil
}

// ExecInteractive opens a shell with stdin attached, writes the command into it
// and collects the output until nothing arrives for opts.Timeout.
//
// This reproduces exec_cmd_container from DockerApi.py:580. The detour through an
// interactive shell is needed there because `rspamadm pw` produces no result under
// a regular exec.
func (m *mobyAPI) ExecInteractive(ctx context.Context, id string, opts InteractiveOptions) (string, error) {
	shell := opts.Shell
	if shell == "" {
		shell = DefaultShell
	}

	timeout := opts.Timeout
	if timeout == 0 {
		timeout = DefaultInteractiveTimeout
	}

	created, err := m.cli.ExecCreate(ctx, id, client.ExecCreateOptions{
		Cmd:          []string{shell},
		User:         opts.User,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return "", fmt.Errorf("creating the exec: %w", err)
	}

	attached, err := m.cli.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		return "", fmt.Errorf("attaching to the exec: %w", err)
	}
	defer attached.Close()

	cmd := opts.Command
	if !strings.HasSuffix(cmd, "\n") {
		cmd += "\n"
	}
	if _, err := attached.Conn.Write([]byte(cmd)); err != nil {
		return "", fmt.Errorf("writing to the exec: %w", err)
	}

	raw := readUntilIdle(attached.Conn, timeout)

	stdout, _ := demux(raw)
	return string(stdout), nil
}

// readUntilIdle collects data until nothing arrives for idle, or until twice that
// span has passed in total. It matches the heuristic of recv_socket_data in
// DockerApi.py:581.
func readUntilIdle(conn net.Conn, idle time.Duration) []byte {
	var (
		out      []byte
		deadline = time.Now().Add(2 * idle)
		buf      = make([]byte, 8192)
	)

	for {
		next := time.Now().Add(idle)
		if next.After(deadline) {
			next = deadline
		}
		if !time.Now().Before(deadline) {
			return out
		}

		if err := conn.SetReadDeadline(next); err != nil {
			return out
		}

		n, err := conn.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
			// Payload restarts the idle window.
			deadline = time.Now().Add(2 * idle)
		}
		if err != nil {
			// A deadline or the end of the connection — either way, everything
			// reachable has been collected.
			return out
		}
	}
}
