//go:build integration

// Diese Tests sprechen einen echten Docker-Daemon an und laufen deshalb nur
// mit -tags=integration. Sie decken den Teil ab, der sich nicht sinnvoll
// nachbilden lässt: das Zusammenspiel mit der Engine-API, das Zerlegen des
// gemultiplexten Exec-Stroms und die interaktive Shell aus DockerApi.py:580.
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

// testImage ist bewusst klein und bringt eine Shell mit.
const testImage = "alpine:3.23"

func newTestClient(t *testing.T) API {
	t.Helper()

	cli, err := New("")
	if err != nil {
		t.Skipf("kein Docker-Daemon erreichbar: %v", err)
	}
	t.Cleanup(func() { cli.Close() })

	return cli
}

// startContainer legt einen laufenden Container an und räumt ihn wieder ab.
func startContainer(t *testing.T) (id, name string) {
	t.Helper()

	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker-Kommando nicht verfuegbar")
	}

	if out, err := exec.Command("docker", "image", "inspect", testImage).CombinedOutput(); err != nil {
		t.Logf("Abbild wird geholt: %s", out)
		if out, err := exec.Command("docker", "pull", testImage).CombinedOutput(); err != nil {
			t.Skipf("Abbild nicht verfuegbar: %s", out)
		}
	}

	name = "dockerapi-test-" + strings.ReplaceAll(t.Name(), "/", "-")
	_ = exec.Command("docker", "rm", "-f", name).Run()

	out, err := exec.Command("docker", "run", "-d", "--name", name,
		testImage, "sleep", "300").CombinedOutput()
	if err != nil {
		t.Fatalf("Container starten: %s", out)
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
		t.Fatalf("Treffer = %d, want 1", len(list))
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
		t.Fatalf("Treffer = %+v, want genau %s", list, id)
	}
}

// Der Rohdurchgriff muss die vollständige Inspect-Ausgabe liefern.
func TestIntegrationInspectRawKeepsAllFields(t *testing.T) {
	cli := newTestClient(t)
	id, _ := startContainer(t)

	raw, err := cli.InspectRaw(context.Background(), id)
	if err != nil {
		t.Fatalf("InspectRaw: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("Inspect-Ausgabe lesen: %v", err)
	}

	for _, field := range []string{"Id", "State", "Config", "HostConfig", "NetworkSettings"} {
		if _, ok := parsed[field]; !ok {
			t.Errorf("Feld %s fehlt in der Inspect-Ausgabe", field)
		}
	}
}

// Exec muss stdout und stderr zusammengeführt liefern – darauf baut
// exec_run_handler auf.
func TestIntegrationExecCombinesStreams(t *testing.T) {
	cli := newTestClient(t)
	id, _ := startContainer(t)

	res, err := cli.Exec(context.Background(), id, ExecOptions{
		Cmd: []string{"/bin/sh", "-c", "echo raus; echo fehler >&2"},
	})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}

	if res.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", res.ExitCode)
	}

	out := string(res.Output)
	if !strings.Contains(out, "raus") {
		t.Errorf("stdout fehlt: %q", out)
	}
	if !strings.Contains(out, "fehler") {
		t.Errorf("stderr fehlt: %q", out)
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

// Argumente dürfen von der Shell nicht ausgewertet werden, wenn kein
// Interpreter im Argv steht.
func TestIntegrationExecDoesNotInvokeShell(t *testing.T) {
	cli := newTestClient(t)
	id, _ := startContainer(t)

	res, err := cli.Exec(context.Background(), id, ExecOptions{
		Cmd: []string{"/bin/echo", "$(id -u)", "; echo geleakt"},
	})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}

	out := strings.TrimSpace(string(res.Output))
	if out != "$(id -u) ; echo geleakt" {
		t.Errorf("Ausgabe = %q, das Argument wurde ausgewertet", out)
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
		t.Errorf("Benutzer = %q, want nobody", got)
	}
}

// Der Pfad, den der Rspamd-Passwortwechsel nutzt.
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
		t.Errorf("Ausgabe = %q, erwarte den Hash", out)
	}
}

// Zwei aufeinanderfolgende Kommandos über dieselbe Shell – so schreibt und
// liest der Rspamd-Wechsel die Override-Datei.
func TestIntegrationExecInteractiveWriteAndReadBack(t *testing.T) {
	cli := newTestClient(t)
	id, _ := startContainer(t)
	ctx := context.Background()

	if _, err := cli.ExecInteractive(ctx, id, InteractiveOptions{
		Shell:   "/bin/sh",
		Command: "/bin/echo 'enable_password = \"$2$test\";' > /tmp/pw.inc && cat /tmp/pw.inc",
		Timeout: time.Second,
	}); err != nil {
		t.Fatalf("ExecInteractive (schreiben): %v", err)
	}

	out, err := cli.ExecInteractive(ctx, id, InteractiveOptions{
		Shell:   "/bin/sh",
		Command: "cat /tmp/pw.inc",
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("ExecInteractive (lesen): %v", err)
	}

	if !strings.Contains(out, `enable_password = "$2$test";`) {
		t.Errorf("Ausgabe = %q", out)
	}
}

// Die Messwerte müssen precpu_stats enthalten, sonst kann die Oberfläche die
// CPU-Auslastung nicht berechnen.
func TestIntegrationStatsIncludePreviousSample(t *testing.T) {
	cli := newTestClient(t)
	id, _ := startContainer(t)

	raw, err := cli.Stats(context.Background(), id)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("Messwerte lesen: %v (Rohdaten: %s)", err, raw)
	}

	precpu, ok := parsed["precpu_stats"].(map[string]any)
	if !ok {
		t.Fatalf("precpu_stats fehlt: %s", raw)
	}

	usage, ok := precpu["cpu_usage"].(map[string]any)
	if !ok {
		t.Fatalf("precpu_stats.cpu_usage fehlt: %s", raw)
	}
	if total, _ := usage["total_usage"].(float64); total == 0 {
		t.Errorf("precpu_stats.cpu_usage.total_usage ist 0 – die Vormessung fehlt")
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
		t.Error("Titles ist leer")
	}
	if len(top.Processes) == 0 {
		t.Error("Processes ist leer")
	}
}

func TestIntegrationLifecycle(t *testing.T) {
	cli := newTestClient(t)
	id, _ := startContainer(t)
	ctx := context.Background()

	if err := cli.Stop(ctx, id); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Gestoppte Container erscheinen nur mit all=true.
	running, err := cli.List(ctx, Target{ContainerID: id}, false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(running) != 0 {
		t.Errorf("der gestoppte Container erscheint in der Liste der laufenden")
	}

	stopped, err := cli.List(ctx, Target{ContainerID: id}, true)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(stopped) != 1 {
		t.Fatalf("Treffer mit all=true = %d, want 1", len(stopped))
	}

	if err := cli.Start(ctx, id); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := cli.Restart(ctx, id); err != nil {
		t.Fatalf("Restart: %v", err)
	}
}
