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
		{"nur id", Target{ContainerID: "abc"}, true},
		{"nur name", Target{ContainerName: "postfix-mailcow"}, true},
		{"beides", Target{ContainerID: "abc", ContainerName: "x"}, true},
		{"nichts", Target{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.t.Valid(); got != tt.want {
				t.Errorf("Valid() = %v, want %v", got, tt.want)
			}
		})
	}
}

// DockerApi.py prüft container_id zuerst; ein zusätzlich gesetzter Name bleibt
// dann unberücksichtigt.
func TestFiltersForPrefersID(t *testing.T) {
	f, err := filtersFor(Target{ContainerID: "abc123", ContainerName: "postfix-mailcow"})
	if err != nil {
		t.Fatalf("filtersFor: %v", err)
	}

	if !f["id"]["abc123"] {
		t.Errorf("id-Filter fehlt: %v", f)
	}
	if _, ok := f["name"]; ok {
		t.Errorf("name-Filter darf nicht gesetzt sein: %v", f)
	}
}

func TestFiltersForName(t *testing.T) {
	f, err := filtersFor(Target{ContainerName: "postfix-mailcow"})
	if err != nil {
		t.Fatalf("filtersFor: %v", err)
	}

	if !f["name"]["postfix-mailcow"] {
		t.Errorf("name-Filter fehlt: %v", f)
	}
}

func TestFiltersForEmptyTarget(t *testing.T) {
	if _, err := filtersFor(Target{}); !errors.Is(err, ErrNoTarget) {
		t.Errorf("err = %v, want ErrNoTarget", err)
	}
}

// readUntilIdle beendet das Sammeln, wenn für die Leerlaufspanne nichts mehr
// eintrifft – der Schreiber hält die Verbindung danach absichtlich offen.
func TestReadUntilIdleStopsAfterSilence(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	go func() {
		server.Write([]byte("erste "))
		time.Sleep(20 * time.Millisecond)
		server.Write([]byte("zweite"))
		// Danach Stille: readUntilIdle muss von selbst zurückkehren.
	}()

	start := time.Now()
	got := readUntilIdle(client, 100*time.Millisecond)
	elapsed := time.Since(start)

	if string(got) != "erste zweite" {
		t.Errorf("gelesen = %q, want %q", got, "erste zweite")
	}
	if elapsed > 2*time.Second {
		t.Errorf("Dauer = %v, zu lang", elapsed)
	}
}

func TestReadUntilIdleReturnsOnClose(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()

	go func() {
		server.Write([]byte("fertig"))
		server.Close()
	}()

	if got := readUntilIdle(client, time.Second); string(got) != "fertig" {
		t.Errorf("gelesen = %q, want %q", got, "fertig")
	}
}

func TestReadUntilIdleHandlesSilentPeer(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	start := time.Now()
	got := readUntilIdle(client, 50*time.Millisecond)

	if len(got) != 0 {
		t.Errorf("gelesen = %q, want leer", got)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Dauer = %v, zu lang", elapsed)
	}
}
