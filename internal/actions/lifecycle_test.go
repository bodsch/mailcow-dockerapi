package actions

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
	"bodsch.me/mailcow-dockerapi/internal/dockerclient/dockertest"
)

// The lifecycle actions apply to every match, not only to the first.
func TestLifecycleAppliesToAllMatches(t *testing.T) {
	tests := []struct {
		name    string
		action  Func
		applied func(*dockertest.Fake) []string
	}{
		{"stop", Stop, func(f *dockertest.Fake) []string { return f.Stopped }},
		{"start", Start, func(f *dockertest.Fake) []string { return f.Started }},
		{"restart", Restart, func(f *dockertest.Fake) []string { return f.Restarted }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &dockertest.Fake{
				Containers: []dockerclient.Container{
					{ID: "one"}, {ID: "two"}, {ID: "three"},
				},
			}

			got := tt.action(context.Background(), newEnv(fake), Request{},
				dockerclient.Target{ContainerName: "mailcow"})

			assertBody(t, got, ContentTypeJSON, successBody)

			want := []string{"one", "two", "three"}
			if applied := tt.applied(fake); !reflect.DeepEqual(applied, want) {
				t.Errorf("processed = %v, want %v", applied, want)
			}
		})
	}
}

// Unlike the exec actions, these include stopped containers.
func TestLifecycleListsAllContainers(t *testing.T) {
	tests := []struct {
		name   string
		action Func
	}{
		{"stop", Stop},
		{"start", Start},
		{"restart", Restart},
		{"top", Top},
		{"stats", Stats},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFake()

			tt.action(context.Background(), newEnv(fake), Request{}, byID())

			if len(fake.ListCalls) != 1 {
				t.Fatalf("List calls = %d, want 1", len(fake.ListCalls))
			}
			if !fake.ListCalls[0].All {
				t.Error("all = false, want true")
			}
		})
	}
}

// When the selection finds nothing the original still reports success — mailcow
// uses it to stop containers that are already stopped.
func TestLifecycleSucceedsWithoutMatches(t *testing.T) {
	fake := &dockertest.Fake{}

	got := Stop(context.Background(), newEnv(fake), Request{}, byID())

	assertBody(t, got, ContentTypeJSON, successBody)
}

func TestLifecyclePropagatesError(t *testing.T) {
	fake := newFake()
	fake.StopErr = errDocker

	got := Stop(context.Background(), newEnv(fake), Request{}, byID())

	want := `{
    "type": "danger",
    "msg": "the daemon is unreachable"
}`
	assertBody(t, got, ContentTypeJSON, want)
}

func TestTop(t *testing.T) {
	fake := newFake()
	fake.TopFn = func(string) (dockerclient.TopResult, error) {
		return dockerclient.TopResult{
			Titles:    []string{"PID", "CMD"},
			Processes: [][]string{{"1", "/sbin/init"}},
		}, nil
	}

	got := Top(context.Background(), newEnv(fake), Request{}, byID())

	want := `{
    "type": "success",
    "msg": {
        "Titles": [
            "PID",
            "CMD"
        ],
        "Processes": [
            [
                "1",
                "/sbin/init"
            ]
        ]
    }
}`
	assertBody(t, got, ContentTypeJSON, want)
}

func TestTopWithoutMatch(t *testing.T) {
	fake := &dockertest.Fake{}

	got := Top(context.Background(), newEnv(fake), Request{}, byID())

	want := `{
    "type": "danger",
    "msg": "no container found"
}`
	assertBody(t, got, ContentTypeJSON, want)
}

// The measurements are passed through unchanged, so no fields are lost.
func TestStatsPassesRawPayloadThrough(t *testing.T) {
	fake := newFake()
	fake.StatsFn = func(string) (json.RawMessage, error) {
		return json.RawMessage(`{"cpu_stats":{"online_cpus":4}}`), nil
	}

	got := Stats(context.Background(), newEnv(fake), Request{}, byID())

	want := `{
    "type": "success",
    "msg": {
        "cpu_stats": {
            "online_cpus": 4
        }
    }
}`
	assertBody(t, got, ContentTypeJSON, want)
}
