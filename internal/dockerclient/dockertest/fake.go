// Package dockertest provides a fake Docker API for tests.
//
// It records every call together with its arguments, which is how a test can
// assert what argv an action actually issues — the most effective guard against
// mistakes in quoting user input.
//
// It lives in its own package so the production binary never links it.
package dockertest

import (
	"context"
	"encoding/json"
	"sync"

	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
)

// ExecCall holds one recorded exec call.
type ExecCall struct {
	ContainerID string
	Cmd         []string
	User        string
}

// InteractiveCall holds one recorded interactive call.
type InteractiveCall struct {
	ContainerID string
	Shell       string
	Command     string
	User        string
}

// ListCall holds one recorded container selection.
type ListCall struct {
	Target dockerclient.Target
	All    bool
}

// Fake implements dockerclient.API.
//
// Without further setup List returns whatever is in Containers and Exec reports
// success with empty output.
type Fake struct {
	mu sync.Mutex

	// Containers is the default answer of List and ListAll.
	Containers []dockerclient.Container
	// ListErr makes List and ListAll fail.
	ListErr error

	// ExecFn answers exec calls. When it is nil, ExecResults is consumed in
	// order; once that list is exhausted, exit code 0 with empty output applies.
	ExecFn func(id string, opts dockerclient.ExecOptions) (dockerclient.ExecResult, error)
	// ExecResults is returned one entry at a time.
	ExecResults []dockerclient.ExecResult

	// InteractiveFn answers ExecInteractive calls.
	InteractiveFn func(id string, opts dockerclient.InteractiveOptions) (string, error)

	// InspectFn answers InspectRaw calls.
	InspectFn func(id string) (json.RawMessage, error)
	// TopFn answers Top calls.
	TopFn func(id string) (dockerclient.TopResult, error)
	// StatsFn answers Stats calls.
	StatsFn func(id string) (json.RawMessage, error)

	// StartErr, StopErr and RestartErr make the respective operation fail.
	StartErr, StopErr, RestartErr error

	// Recordings.
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

// WithContainers builds a fake that returns exactly one container with the given
// id and name.
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

// LastExec returns the most recently recorded exec call.
func (f *Fake) LastExec() (ExecCall, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if len(f.ExecCalls) == 0 {
		return ExecCall{}, false
	}
	return f.ExecCalls[len(f.ExecCalls)-1], true
}

// The methods below hand out copies of the recordings.
//
// When the fake is driven from several goroutines — during PubSub delivery, say,
// or with concurrent requests — reading the fields directly is a race. These
// methods exist for those tests.

// StoppedIDs returns the stopped containers.
func (f *Fake) StoppedIDs() []string { return f.snapshot(func() []string { return f.Stopped }) }

// StartedIDs returns the started containers.
func (f *Fake) StartedIDs() []string { return f.snapshot(func() []string { return f.Started }) }

// RestartedIDs returns the restarted containers.
func (f *Fake) RestartedIDs() []string { return f.snapshot(func() []string { return f.Restarted }) }

// ExecCallCount returns the number of exec calls.
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
