// Package dockertest stellt eine Attrappe der Docker-API für Tests bereit.
//
// Sie zeichnet jeden Aufruf mitsamt Argumenten auf. Damit lässt sich prüfen,
// welches Argv eine Action tatsächlich absetzt – der wirksamste Schutz gegen
// Fehler beim Maskieren von Benutzereingaben.
package dockertest

import (
	"context"
	"encoding/json"
	"sync"

	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
)

// ExecCall hält einen aufgezeichneten Exec-Aufruf.
type ExecCall struct {
	ContainerID string
	Cmd         []string
	User        string
}

// InteractiveCall hält einen aufgezeichneten interaktiven Aufruf.
type InteractiveCall struct {
	ContainerID string
	Shell       string
	Command     string
	User        string
}

// ListCall hält eine aufgezeichnete Container-Auswahl.
type ListCall struct {
	Target dockerclient.Target
	All    bool
}

// Fake implementiert dockerclient.API.
//
// Ohne weitere Einstellung liefert List die in Containers hinterlegten
// Einträge und Exec einen Erfolg mit leerer Ausgabe.
type Fake struct {
	mu sync.Mutex

	// Containers ist die Standardantwort von List und ListAll.
	Containers []dockerclient.Container
	// ListErr lässt List und ListAll scheitern.
	ListErr error

	// ExecFn beantwortet Exec-Aufrufe. Ist sie nil, wird ExecResults der
	// Reihe nach abgearbeitet; ist auch die Liste erschöpft, gilt Exit-Code 0
	// mit leerer Ausgabe.
	ExecFn func(id string, opts dockerclient.ExecOptions) (dockerclient.ExecResult, error)
	// ExecResults wird der Reihe nach zurückgegeben.
	ExecResults []dockerclient.ExecResult

	// InteractiveFn beantwortet ExecInteractive-Aufrufe.
	InteractiveFn func(id string, opts dockerclient.InteractiveOptions) (string, error)

	// InspectFn beantwortet InspectRaw-Aufrufe.
	InspectFn func(id string) (json.RawMessage, error)
	// TopFn beantwortet Top-Aufrufe.
	TopFn func(id string) (dockerclient.TopResult, error)
	// StatsFn beantwortet Stats-Aufrufe.
	StatsFn func(id string) (json.RawMessage, error)

	// StartErr, StopErr und RestartErr lassen die jeweilige Operation scheitern.
	StartErr, StopErr, RestartErr error

	// Aufzeichnungen.
	ListCalls        []ListCall
	ListAllCalls     []bool
	ExecCalls        []ExecCall
	InteractiveCalls []InteractiveCall
	InspectCalls     []string
	TopCalls         []string
	StatsCalls       []string
	Started          []string
	Stopped          []string
	Restarted        []string
	Closed           bool

	execIndex int
}

var _ dockerclient.API = (*Fake)(nil)

// WithContainers erzeugt eine Attrappe, die genau einen Container mit der
// angegebenen ID und dem angegebenen Namen liefert.
func WithContainers(id, name string) *Fake {
	return &Fake{
		Containers: []dockerclient.Container{
			{ID: id, Names: []string{"/" + name}, State: "running"},
		},
	}
}

func (f *Fake) List(_ context.Context, t dockerclient.Target, all bool) ([]dockerclient.Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.ListCalls = append(f.ListCalls, ListCall{Target: t, All: all})

	if !t.Valid() {
		return nil, dockerclient.ErrNoTarget
	}
	if f.ListErr != nil {
		return nil, f.ListErr
	}

	return f.Containers, nil
}

func (f *Fake) ListAll(_ context.Context, all bool) ([]dockerclient.Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.ListAllCalls = append(f.ListAllCalls, all)

	if f.ListErr != nil {
		return nil, f.ListErr
	}
	return f.Containers, nil
}

func (f *Fake) InspectRaw(_ context.Context, id string) (json.RawMessage, error) {
	f.mu.Lock()
	fn := f.InspectFn
	f.InspectCalls = append(f.InspectCalls, id)
	f.mu.Unlock()

	if fn != nil {
		return fn(id)
	}
	return json.RawMessage(`{"Id":"` + id + `"}`), nil
}

func (f *Fake) Start(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.Started = append(f.Started, id)
	return f.StartErr
}

func (f *Fake) Stop(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.Stopped = append(f.Stopped, id)
	return f.StopErr
}

func (f *Fake) Restart(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.Restarted = append(f.Restarted, id)
	return f.RestartErr
}

func (f *Fake) Top(_ context.Context, id string) (dockerclient.TopResult, error) {
	f.mu.Lock()
	fn := f.TopFn
	f.TopCalls = append(f.TopCalls, id)
	f.mu.Unlock()

	if fn != nil {
		return fn(id)
	}
	return dockerclient.TopResult{
		Titles:    []string{"PID", "USER", "COMMAND"},
		Processes: [][]string{{"1", "root", "/sbin/init"}},
	}, nil
}

func (f *Fake) Stats(_ context.Context, id string) (json.RawMessage, error) {
	f.mu.Lock()
	fn := f.StatsFn
	f.StatsCalls = append(f.StatsCalls, id)
	f.mu.Unlock()

	if fn != nil {
		return fn(id)
	}
	return json.RawMessage(`{"id":"` + id + `"}`), nil
}

func (f *Fake) Exec(_ context.Context, id string, opts dockerclient.ExecOptions) (dockerclient.ExecResult, error) {
	f.mu.Lock()
	f.ExecCalls = append(f.ExecCalls, ExecCall{ContainerID: id, Cmd: opts.Cmd, User: opts.User})
	fn := f.ExecFn
	idx := f.execIndex
	f.execIndex++
	var queued *dockerclient.ExecResult
	if fn == nil && idx < len(f.ExecResults) {
		queued = &f.ExecResults[idx]
	}
	f.mu.Unlock()

	if fn != nil {
		return fn(id, opts)
	}
	if queued != nil {
		return *queued, nil
	}
	return dockerclient.ExecResult{}, nil
}

func (f *Fake) ExecInteractive(_ context.Context, id string, opts dockerclient.InteractiveOptions) (string, error) {
	f.mu.Lock()
	f.InteractiveCalls = append(f.InteractiveCalls, InteractiveCall{
		ContainerID: id,
		Shell:       opts.Shell,
		Command:     opts.Command,
		User:        opts.User,
	})
	fn := f.InteractiveFn
	f.mu.Unlock()

	if fn != nil {
		return fn(id, opts)
	}
	return "", nil
}

func (f *Fake) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.Closed = true
	return nil
}

// LastExec liefert den zuletzt aufgezeichneten Exec-Aufruf.
func (f *Fake) LastExec() (ExecCall, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if len(f.ExecCalls) == 0 {
		return ExecCall{}, false
	}
	return f.ExecCalls[len(f.ExecCalls)-1], true
}

// Die folgenden Methoden geben Kopien der Aufzeichnungen heraus.
//
// Wird die Attrappe aus mehreren Goroutinen bedient – etwa beim PubSub-Empfang
// oder bei nebenläufigen Anfragen – ist der direkte Feldzugriff ein Wettlauf.
// Für solche Tests sind diese Methoden gedacht.

// StoppedIDs liefert die gestoppten Container.
func (f *Fake) StoppedIDs() []string { return f.snapshot(func() []string { return f.Stopped }) }

// StartedIDs liefert die gestarteten Container.
func (f *Fake) StartedIDs() []string { return f.snapshot(func() []string { return f.Started }) }

// RestartedIDs liefert die neu gestarteten Container.
func (f *Fake) RestartedIDs() []string { return f.snapshot(func() []string { return f.Restarted }) }

// ExecCallCount liefert die Anzahl der Exec-Aufrufe.
func (f *Fake) ExecCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.ExecCalls)
}

func (f *Fake) snapshot(get func() []string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), get()...)
}
