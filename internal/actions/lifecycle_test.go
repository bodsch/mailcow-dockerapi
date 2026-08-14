package actions

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
	"bodsch.me/mailcow-dockerapi/internal/dockerclient/dockertest"
)

// Die Lifecycle-Actions wirken auf alle Treffer, nicht nur auf den ersten.
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
					{ID: "eins"}, {ID: "zwei"}, {ID: "drei"},
				},
			}

			got := tt.action(context.Background(), newEnv(fake), Request{},
				dockerclient.Target{ContainerName: "mailcow"})

			assertBody(t, got, ContentTypeJSON, successBody)

			want := []string{"eins", "zwei", "drei"}
			if applied := tt.applied(fake); !reflect.DeepEqual(applied, want) {
				t.Errorf("bearbeitet = %v, want %v", applied, want)
			}
		})
	}
}

// Anders als die exec-Actions beziehen sie gestoppte Container ein.
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
				t.Fatalf("List-Aufrufe = %d, want 1", len(fake.ListCalls))
			}
			if !fake.ListCalls[0].All {
				t.Error("all = false, want true")
			}
		})
	}
}

// Findet die Auswahl nichts, meldet das Original dennoch Erfolg – mailcow
// stoppt darüber auch bereits gestoppte Container.
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
    "msg": "daemon nicht erreichbar"
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

// Die Messwerte werden unverändert durchgereicht, damit keine Felder verloren
// gehen.
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
