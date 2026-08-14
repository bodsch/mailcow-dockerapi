package dockerclient

import (
	"errors"
	"net"
	"testing"
	"time"
)

func TestTargetValid(t *testing.T) {
	tests := []struct {
		name string
		t    Target
		want bool
	}{
		{"id only", Target{ContainerID: "abc"}, true},
		{"name only", Target{ContainerName: "postfix-mailcow"}, true},
		{"both", Target{ContainerID: "abc", ContainerName: "x"}, true},
		{"neither", Target{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.t.Valid(); got != tt.want {
				t.Errorf("Valid() = %v, want %v", got, tt.want)
			}
		})
	}
}

// DockerApi.py checks container_id first; a name set alongside it is ignored.
func TestFiltersForPrefersID(t *testing.T) {
	f, err := filtersFor(Target{ContainerID: "abc123", ContainerName: "postfix-mailcow"})
	if err != nil {
		t.Fatalf("filtersFor: %v", err)
	}

	if !f["id"]["abc123"] {
		t.Errorf("the id filter is missing: %v", f)
	}
	if _, ok := f["name"]; ok {
		t.Errorf("the name filter must not be set: %v", f)
	}
}

func TestFiltersForName(t *testing.T) {
	f, err := filtersFor(Target{ContainerName: "postfix-mailcow"})
	if err != nil {
		t.Fatalf("filtersFor: %v", err)
	}

	if !f["name"]["postfix-mailcow"] {
		t.Errorf("the name filter is missing: %v", f)
	}
}

func TestFiltersForEmptyTarget(t *testing.T) {
	if _, err := filtersFor(Target{}); !errors.Is(err, ErrNoTarget) {
		t.Errorf("err = %v, want ErrNoTarget", err)
	}
}

// readUntilIdle stops collecting once nothing arrives for the idle span — the
// writer deliberately keeps the connection open afterwards.
func TestReadUntilIdleStopsAfterSilence(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	go func() {
		_, _ = server.Write([]byte("first "))
		time.Sleep(20 * time.Millisecond)
		_, _ = server.Write([]byte("second"))
		// Silence from here on: readUntilIdle has to return on its own.
	}()

	start := time.Now()
	got := readUntilIdle(client, 100*time.Millisecond)
	elapsed := time.Since(start)

	if string(got) != "first second" {
		t.Errorf("read = %q, want %q", got, "first second")
	}
	if elapsed > 2*time.Second {
		t.Errorf("took %v, too long", elapsed)
	}
}

func TestReadUntilIdleReturnsOnClose(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()

	go func() {
		_, _ = server.Write([]byte("done"))
		_ = server.Close()
	}()

	if got := readUntilIdle(client, time.Second); string(got) != "done" {
		t.Errorf("read = %q, want %q", got, "done")
	}
}

func TestReadUntilIdleHandlesSilentPeer(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	start := time.Now()
	got := readUntilIdle(client, 50*time.Millisecond)

	if len(got) != 0 {
		t.Errorf("read = %q, want empty", got)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %v, too long", elapsed)
	}
}
