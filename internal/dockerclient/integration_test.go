//go:build integration

// These tests talk to a real Docker daemon and therefore only run with
// -tags=integration. They cover the part that cannot sensibly be faked: the
// interplay with the engine API, splitting the multiplexed exec stream, and the
// interactive shell from DockerApi.py:580.
//
//	go test -tags=integration ./internal/dockerclient/
package dockerclient

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// testImage is deliberately small and ships a shell.
const testImage = "alpine:3.23"

func newTestClient(t *testing.T) API {
	t.Helper()

	cli, err := New("")
	if err != nil {
		t.Skipf("no Docker daemon reachable: %v", err)
	}
	t.Cleanup(func() { cli.Close() })

	return cli
}

// startContainer creates a running container and cleans it up again.
func startContainer(t *testing.T) (id, name string) {
	t.Helper()

	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("the docker command is not available")
	}

	if out, err := exec.Command("docker", "image", "inspect", testImage).CombinedOutput(); err != nil {
		t.Logf("pulling the image: %s", out)
		if out, err := exec.Command("docker", "pull", testImage).CombinedOutput(); err != nil {
			t.Skipf("the image is not available: %s", out)
		}
	}

	name = "dockerapi-test-" + strings.ReplaceAll(t.Name(), "/", "-")
	_ = exec.Command("docker", "rm", "-f", name).Run()

	out, err := exec.Command("docker", "run", "-d", "--name", name,
		testImage, "sleep", "300").CombinedOutput()
	if err != nil {
		t.Fatalf("starting the container: %s", out)
	}

	id = strings.TrimSpace(string(out))

	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", name).Run()
	})

	return id, name
}

func TestIntegrationListByID(t *testing.T) {
	cli := newTestClient(t)
	id, _ := startContainer(t)

	list, err := cli.List(context.Background(), Target{ContainerID: id}, false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("matches = %d, want 1", len(list))
	}
	if list[0].ID != id {
		t.Errorf("ID = %q, want %q", list[0].ID, id)
	}
}

func TestIntegrationListByName(t *testing.T) {
	cli := newTestClient(t)
	id, name := startContainer(t)

	list, err := cli.List(context.Background(), Target{ContainerName: name}, false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ID != id {
		t.Fatalf("matches = %+v, want exactly %s", list, id)
	}
}

// Passing the raw bytes through has to preserve the complete inspect output.
func TestIntegrationInspectRawKeepsAllFields(t *testing.T) {
	cli := newTestClient(t)
	id, _ := startContainer(t)

	raw, err := cli.InspectRaw(context.Background(), id)
	if err != nil {
		t.Fatalf("InspectRaw: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("reading the inspect output: %v", err)
	}

	for _, field := range []string{"Id", "State", "Config", "HostConfig", "NetworkSettings"} {
		if _, ok := parsed[field]; !ok {
			t.Errorf("the field %s is missing from the inspect output", field)
		}
	}
}

// Exec has to return stdout and stderr merged — exec_run_handler builds on that.
func TestIntegrationExecCombinesStreams(t *testing.T) {
	cli := newTestClient(t)
	id, _ := startContainer(t)

	res, err := cli.Exec(context.Background(), id, ExecOptions{
		Cmd: []string{"/bin/sh", "-c", "echo out; echo failure >&2"},
	})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}

	if res.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", res.ExitCode)
	}

	out := string(res.Output)
	if !strings.Contains(out, "out") {
		t.Errorf("stdout is missing: %q", out)
	}
	if !strings.Contains(out, "failure") {
		t.Errorf("stderr is missing: %q", out)
	}
}

func TestIntegrationExecReportsExitCode(t *testing.T) {
	cli := newTestClient(t)
	id, _ := startContainer(t)

	res, err := cli.Exec(context.Background(), id, ExecOptions{
		Cmd: []string{"/bin/sh", "-c", "exit 42"},
	})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}

	if res.ExitCode != 42 {
		t.Errorf("ExitCode = %d, want 42", res.ExitCode)
	}
}

// Arguments must not be evaluated by a shell when no interpreter is in the argv.
func TestIntegrationExecDoesNotInvokeShell(t *testing.T) {
	cli := newTestClient(t)
	id, _ := startContainer(t)

	res, err := cli.Exec(context.Background(), id, ExecOptions{
		Cmd: []string{"/bin/echo", "$(id -u)", "; echo leaked"},
	})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}

	out := strings.TrimSpace(string(res.Output))
	if out != "$(id -u) ; echo leaked" {
		t.Errorf("output = %q, the argument was evaluated", out)
	}
}

func TestIntegrationExecAsUser(t *testing.T) {
	cli := newTestClient(t)
	id, _ := startContainer(t)

	res, err := cli.Exec(context.Background(), id, ExecOptions{
		Cmd:  []string{"/usr/bin/id", "-un"},
		User: "nobody",
	})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}

	if got := strings.TrimSpace(string(res.Output)); got != "nobody" {
		t.Errorf("user = %q, want nobody", got)
	}
}

// The path the rspamd password change uses.
func TestIntegrationExecInteractive(t *testing.T) {
	cli := newTestClient(t)
	id, _ := startContainer(t)

	out, err := cli.ExecInteractive(context.Background(), id, InteractiveOptions{
		Shell:   "/bin/sh",
		Command: "echo '$2$abcdef'",
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("ExecInteractive: %v", err)
	}

	if !strings.Contains(out, "$2$abcdef") {
		t.Errorf("output = %q, expected the hash", out)
	}
}

// Two consecutive commands over the same shell — that is how the rspamd change
// writes and reads back the override file.
func TestIntegrationExecInteractiveWriteAndReadBack(t *testing.T) {
	cli := newTestClient(t)
	id, _ := startContainer(t)
	ctx := context.Background()

	if _, err := cli.ExecInteractive(ctx, id, InteractiveOptions{
		Shell:   "/bin/sh",
		Command: "/bin/echo 'enable_password = \"$2$test\";' > /tmp/pw.inc && cat /tmp/pw.inc",
		Timeout: time.Second,
	}); err != nil {
		t.Fatalf("ExecInteractive (write): %v", err)
	}

	out, err := cli.ExecInteractive(ctx, id, InteractiveOptions{
		Shell:   "/bin/sh",
		Command: "cat /tmp/pw.inc",
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("ExecInteractive (read): %v", err)
	}

	if !strings.Contains(out, `enable_password = "$2$test";`) {
		t.Errorf("output = %q", out)
	}
}

// The sample has to include precpu_stats, or the UI cannot compute CPU load.
func TestIntegrationStatsIncludePreviousSample(t *testing.T) {
	cli := newTestClient(t)
	id, _ := startContainer(t)

	raw, err := cli.Stats(context.Background(), id)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("reading the sample: %v (raw: %s)", err, raw)
	}

	precpu, ok := parsed["precpu_stats"].(map[string]any)
	if !ok {
		t.Fatalf("precpu_stats is missing: %s", raw)
	}

	usage, ok := precpu["cpu_usage"].(map[string]any)
	if !ok {
		t.Fatalf("precpu_stats.cpu_usage is missing: %s", raw)
	}
	if total, _ := usage["total_usage"].(float64); total == 0 {
		t.Errorf("precpu_stats.cpu_usage.total_usage is 0 — the previous sample is missing")
	}
}

func TestIntegrationTop(t *testing.T) {
	cli := newTestClient(t)
	id, _ := startContainer(t)

	top, err := cli.Top(context.Background(), id)
	if err != nil {
		t.Fatalf("Top: %v", err)
	}

	if len(top.Titles) == 0 {
		t.Error("Titles is empty")
	}
	if len(top.Processes) == 0 {
		t.Error("Processes is empty")
	}
}

func TestIntegrationLifecycle(t *testing.T) {
	cli := newTestClient(t)
	id, _ := startContainer(t)
	ctx := context.Background()

	if err := cli.Stop(ctx, id); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Stopped containers only show up with all=true.
	running, err := cli.List(ctx, Target{ContainerID: id}, false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(running) != 0 {
		t.Errorf("the stopped container shows up in the list of running ones")
	}

	stopped, err := cli.List(ctx, Target{ContainerID: id}, true)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(stopped) != 1 {
		t.Fatalf("matches with all=true = %d, want 1", len(stopped))
	}

	if err := cli.Start(ctx, id); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := cli.Restart(ctx, id); err != nil {
		t.Fatalf("Restart: %v", err)
	}
}
