package dockerclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/moby/moby/client"
)

// mobyAPI setzt API auf dem offiziellen Engine-Client um.
type mobyAPI struct {
	cli *client.Client
}

// New verbindet sich mit dem Docker-Daemon unter host.
//
// Die Versionsaushandlung entspricht version='auto' aus main.py:42.
func New(host string) (API, error) {
	opts := []client.Opt{client.WithAPIVersionNegotiation()}
	if host != "" {
		opts = append(opts, client.WithHost(host))
	}

	cli, err := client.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}

	return &mobyAPI{cli: cli}, nil
}

func (m *mobyAPI) Close() error {
	return m.cli.Close()
}

// filters bildet die Auswahl aus DockerApi.py nach: container_id gewinnt,
// sonst container_name.
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
		return nil, err
	}

	return toContainers(res), nil
}

func (m *mobyAPI) ListAll(ctx context.Context, all bool) ([]Container, error) {
	res, err := m.cli.ContainerList(ctx, client.ContainerListOptions{All: all})
	if err != nil {
		return nil, err
	}

	return toContainers(res), nil
}

func toContainers(res client.ContainerListResult) []Container {
	out := make([]Container, 0, len(res.Items))
	for _, item := range res.Items {
		out = append(out, Container{ID: item.ID, Names: item.Names, State: string(item.State)})
	}
	return out
}

func (m *mobyAPI) InspectRaw(ctx context.Context, id string) (json.RawMessage, error) {
	res, err := m.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return nil, err
	}

	return res.Raw, nil
}

func (m *mobyAPI) Start(ctx context.Context, id string) error {
	_, err := m.cli.ContainerStart(ctx, id, client.ContainerStartOptions{})
	return err
}

func (m *mobyAPI) Stop(ctx context.Context, id string) error {
	_, err := m.cli.ContainerStop(ctx, id, client.ContainerStopOptions{})
	return err
}

func (m *mobyAPI) Restart(ctx context.Context, id string) error {
	_, err := m.cli.ContainerRestart(ctx, id, client.ContainerRestartOptions{})
	return err
}

func (m *mobyAPI) Top(ctx context.Context, id string) (TopResult, error) {
	res, err := m.cli.ContainerTop(ctx, id, client.ContainerTopOptions{})
	if err != nil {
		return TopResult{}, err
	}

	return TopResult{Titles: res.Titles, Processes: res.Processes}, nil
}

// Stats holt ein einzelnes Sample.
//
// IncludePreviousSample entspricht dem Verhalten von aiodocker
// stats(stream=False): der Daemon nimmt zwei Messungen im Abstand einer
// Sekunde, sodass precpu_stats gefüllt ist. Ohne dieses Feld kann die
// mailcow-Oberfläche die CPU-Auslastung nicht berechnen.
func (m *mobyAPI) Stats(ctx context.Context, id string) (json.RawMessage, error) {
	res, err := m.cli.ContainerStats(ctx, id, client.ContainerStatsOptions{
		Stream:                false,
		IncludePreviousSample: true,
	})
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	// Der Strom enthält bei Stream=false genau einen Datensatz; ein
	// abschließender Zeilenumbruch würde ungültiges JSON ergeben.
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
		return ExecResult{}, fmt.Errorf("exec create: %w", err)
	}

	attached, err := m.cli.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		return ExecResult{}, fmt.Errorf("exec attach: %w", err)
	}
	defer attached.Close()

	raw, err := io.ReadAll(attached.Reader)
	if err != nil {
		return ExecResult{}, fmt.Errorf("exec read: %w", err)
	}

	inspect, err := m.cli.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
	if err != nil {
		return ExecResult{}, fmt.Errorf("exec inspect: %w", err)
	}

	return ExecResult{ExitCode: inspect.ExitCode, Output: demuxCombined(raw)}, nil
}

// ExecInteractive öffnet eine Shell mit angehängtem stdin, schreibt das
// Kommando hinein und sammelt die Ausgabe, bis für opts.Timeout nichts mehr
// nachkommt.
//
// Das bildet exec_cmd_container aus DockerApi.py:580 nach. Der Umweg über eine
// interaktive Shell ist dort nötig, weil `rspamadm pw` bei einem regulären
// exec kein Ergebnis liefert.
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
		return "", fmt.Errorf("exec create: %w", err)
	}

	attached, err := m.cli.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		return "", fmt.Errorf("exec attach: %w", err)
	}
	defer attached.Close()

	cmd := opts.Command
	if !strings.HasSuffix(cmd, "\n") {
		cmd += "\n"
	}
	if _, err := attached.Conn.Write([]byte(cmd)); err != nil {
		return "", fmt.Errorf("exec write: %w", err)
	}

	raw := readUntilIdle(attached.Conn, timeout)

	stdout, _ := demux(raw)
	return string(stdout), nil
}

// readUntilIdle sammelt Daten, bis für idle nichts mehr eintrifft oder die
// doppelte Spanne insgesamt verstrichen ist. Das entspricht der Heuristik von
// recv_socket_data in DockerApi.py:581.
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
			// Nach Nutzdaten beginnt die Leerlaufspanne neu.
			deadline = time.Now().Add(2 * idle)
		}
		if err != nil {
			// Zeitschranke oder Verbindungsende – in beiden Fällen ist
			// eingesammelt, was erreichbar war.
			return out
		}
	}
}
