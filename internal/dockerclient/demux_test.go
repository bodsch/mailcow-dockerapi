package dockerclient

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// frame baut einen Docker-Attach-Frame.
func frame(kind byte, payload string) []byte {
	b := make([]byte, frameHeaderLen+len(payload))
	b[0] = kind
	binary.BigEndian.PutUint32(b[4:frameHeaderLen], uint32(len(payload)))
	copy(b[frameHeaderLen:], payload)
	return b
}

func TestDemuxSplitsStreams(t *testing.T) {
	in := bytes.Join([][]byte{
		frame(streamStdout, "hallo "),
		frame(streamStderr, "fehler"),
		frame(streamStdout, "welt"),
	}, nil)

	stdout, stderr := demux(in)

	if string(stdout) != "hallo welt" {
		t.Errorf("stdout = %q, want %q", stdout, "hallo welt")
	}
	if string(stderr) != "fehler" {
		t.Errorf("stderr = %q, want %q", stderr, "fehler")
	}
}

func TestDemuxCombinedKeepsOrder(t *testing.T) {
	in := bytes.Join([][]byte{
		frame(streamStdout, "eins "),
		frame(streamStderr, "zwei "),
		frame(streamStdout, "drei"),
	}, nil)

	if got := string(demuxCombined(in)); got != "eins zwei drei" {
		t.Errorf("combined = %q, want %q", got, "eins zwei drei")
	}
}

// ExecInteractive liest gegen eine Zeitschranke und endet regelmäßig mitten
// im Frame – der bis dahin gelesene Teil muss erhalten bleiben.
func TestDemuxToleratesTruncatedPayload(t *testing.T) {
	full := frame(streamStdout, "$2$abcdef")
	truncated := full[:len(full)-3]

	stdout, _ := demux(truncated)

	if string(stdout) != "$2$abc" {
		t.Errorf("stdout = %q, want %q", stdout, "$2$abc")
	}
}

func TestDemuxToleratesTruncatedHeader(t *testing.T) {
	in := append(frame(streamStdout, "vollstaendig"), 1, 0, 0)

	stdout, _ := demux(in)

	if string(stdout) != "vollstaendig" {
		t.Errorf("stdout = %q, want %q", stdout, "vollstaendig")
	}
}

func TestDemuxStopsAtInvalidStreamKind(t *testing.T) {
	in := append(frame(streamStdout, "gut"), frame(9, "muell")...)

	stdout, stderr := demux(in)

	if string(stdout) != "gut" {
		t.Errorf("stdout = %q, want %q", stdout, "gut")
	}
	if len(stderr) != 0 {
		t.Errorf("stderr = %q, want leer", stderr)
	}
}

func TestDemuxEmpty(t *testing.T) {
	stdout, stderr := demux(nil)
	if stdout != nil || stderr != nil {
		t.Errorf("erwarte nil/nil, got %q/%q", stdout, stderr)
	}

	if out := demuxCombined(nil); out != nil {
		t.Errorf("combined = %q, want nil", out)
	}
}

// Ein Nutzlastfeld, das über das Pufferende hinausweist, darf keinen Panic
// auslösen.
func TestDemuxRejectsOversizedLength(t *testing.T) {
	b := make([]byte, frameHeaderLen+4)
	b[0] = streamStdout
	binary.BigEndian.PutUint32(b[4:frameHeaderLen], 0xFFFFFFFF)
	copy(b[frameHeaderLen:], "kurz")

	stdout, _ := demux(b)

	if string(stdout) != "kurz" {
		t.Errorf("stdout = %q, want %q", stdout, "kurz")
	}
}

func FuzzDemux(f *testing.F) {
	f.Add(frame(streamStdout, "hallo"))
	f.Add(append(frame(streamStderr, "fehler"), 2, 0, 0, 0))
	f.Add([]byte{0, 0, 0, 0, 255, 255, 255, 255})

	f.Fuzz(func(t *testing.T, data []byte) {
		stdout, stderr := demux(data)
		combined := demuxCombined(data)

		// Die zusammengeführte Länge darf die getrennten nie unterschreiten.
		if len(combined) < len(stdout) {
			t.Fatalf("combined (%d) kuerzer als stdout (%d)", len(combined), len(stdout))
		}
		if len(stdout)+len(stderr) != len(combined) {
			t.Fatalf("stdout+stderr = %d, combined = %d", len(stdout)+len(stderr), len(combined))
		}
	})
}
