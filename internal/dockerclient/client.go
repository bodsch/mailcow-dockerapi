// Package dockerclient kapselt den Zugriff auf den Docker-Daemon hinter einem
// schmalen Interface.
//
// Die Python-Implementierung hielt zwei Clients parallel (docker synchron für
// die Actions, aiodocker asynchron für Stats und Inspect). Hier genügt einer.
// Das Interface umfasst ausschließlich die Operationen, die DockerApi.py
// tatsächlich verwendet, und macht die Actions damit ohne laufenden Daemon
// testbar.
package dockerclient

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// ErrNoTarget signalisiert einen Aufruf ohne Container-ID und ohne Namen.
//
// DockerApi.py lässt die Variable filters in diesem Fall undefiniert und läuft
// in einen NameError; hier ist es ein regulärer Fehler.
var ErrNoTarget = errors.New("weder container_id noch container_name angegeben")

// Target wählt die Container aus, auf die eine Action wirkt.
//
// Die Auswahl folgt DockerApi.py: eine gesetzte ID hat Vorrang vor dem Namen.
// Der Name wird – wie bei Docker üblich – als Teilstring-Muster ausgewertet,
// kann also mehrere Container treffen.
type Target struct {
	ContainerID   string
	ContainerName string
}

// Valid meldet, ob mindestens ein Auswahlkriterium gesetzt ist.
func (t Target) Valid() bool {
	return t.ContainerID != "" || t.ContainerName != ""
}

// Container ist die Teilmenge der Container-Zusammenfassung, die die Actions
// benötigen.
type Container struct {
	ID    string
	Names []string
	State string
}

// ExecOptions beschreibt ein einzelnes `docker exec`.
type ExecOptions struct {
	// Cmd ist das vollständige Argv. Enthält es eine Shell wie
	// {"/bin/bash", "-c", "..."}, ist das eine bewusste Entscheidung der
	// jeweiligen Action.
	Cmd []string
	// User entspricht dem user-Argument von docker-py exec_run.
	User string
}

// ExecResult hält Exit-Code und die zusammengeführte Ausgabe.
//
// docker-py liefert bei exec_run mit demux=False stdout und stderr in einem
// Strom; dieses Verhalten wird beibehalten, weil exec_run_handler in
// DockerApi.py darauf aufbaut.
type ExecResult struct {
	ExitCode int
	Output   []byte
}

// InteractiveOptions beschreibt den Sonderfall aus DockerApi.py:580 –
// eine Shell wird mit angehängtem stdin geöffnet, das Kommando hineingeschrieben
// und die Ausgabe mit einer Zeitheuristik eingesammelt. Genutzt wird das
// ausschließlich vom Rspamd-Passwortwechsel.
type InteractiveOptions struct {
	// Shell ist der zu startende Interpreter; leer bedeutet /bin/bash.
	Shell string
	// Command wird in stdin geschrieben; ein fehlender Zeilenumbruch wird ergänzt.
	Command string
	User    string
	// Timeout ist die Leerlaufspanne, nach der das Einsammeln endet.
	// Null bedeutet DefaultInteractiveTimeout.
	Timeout time.Duration
}

// DefaultInteractiveTimeout entspricht dem timeout=2 aus DockerApi.py:580.
const DefaultInteractiveTimeout = 2 * time.Second

// DefaultShell entspricht shell_cmd="/bin/bash" aus DockerApi.py:580.
const DefaultShell = "/bin/bash"

// TopResult entspricht der Antwort von GET /containers/{id}/top.
type TopResult struct {
	Titles    []string   `json:"Titles"`
	Processes [][]string `json:"Processes"`
}

// API ist der vom Rest des Dienstes benötigte Ausschnitt der Docker-API.
type API interface {
	// List liefert die Container, auf die t passt. all schließt gestoppte
	// Container ein – die Lifecycle-Actions setzen es, die exec-Actions nicht.
	List(ctx context.Context, t Target, all bool) ([]Container, error)

	// ListAll liefert alle Container ohne Filter (Route /containers/json).
	ListAll(ctx context.Context, all bool) ([]Container, error)

	// InspectRaw liefert die unveränderte Antwort von
	// GET /containers/{id}/json. Der Rohdurchgriff bewahrt Felder, die ein
	// Go-Struct verlieren würde.
	InspectRaw(ctx context.Context, id string) (json.RawMessage, error)

	Start(ctx context.Context, id string) error
	Stop(ctx context.Context, id string) error
	Restart(ctx context.Context, id string) error

	Top(ctx context.Context, id string) (TopResult, error)

	// Stats liefert ein einzelnes Sample von GET /containers/{id}/stats
	// einschließlich precpu_stats, damit die Oberfläche die CPU-Last
	// berechnen kann.
	Stats(ctx context.Context, id string) (json.RawMessage, error)

	Exec(ctx context.Context, id string, opts ExecOptions) (ExecResult, error)
	ExecInteractive(ctx context.Context, id string, opts InteractiveOptions) (string, error)

	Close() error
}
