package dockerclient

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// frame builds one Docker attach frame.
func frame(kind byte, payload string) []byte {
	b := make([]byte, frameHeaderLen+len(payload))
	b[0] = kind
	binary.BigEndian.PutUint32(b[4:frameHeaderLen], uint32(len(payload)))
	copy(b[frameHeaderLen:], payload)
	return b
}

func TestDemuxSplitsStreams(t *testing.T) {
	in := bytes.Join([][]byte{
		frame(streamStdout, "hello "),
		frame(streamStderr, "failure"),
		frame(streamStdout, "world"),
	}, nil)

	stdout, stderr := demux(in)

	if string(stdout) != "hello world" {
		t.Errorf("stdout = %q, want %q", stdout, "hello world")
	}
	if string(stderr) != "failure" {
		t.Errorf("stderr = %q, want %q", stderr, "failure")
	}
}

func TestDemuxCombinedKeepsOrder(t *testing.T) {
	in := bytes.Join([][]byte{
		frame(streamStdout, "one "),
		frame(streamStderr, "two "),
		frame(streamStdout, "three"),
	}, nil)

	if got := string(demuxCombined(in)); got != "one two three" {
		t.Errorf("combined = %q, want %q", got, "one two three")
	}
}

// ExecInteractive reads against a deadline and regularly stops in the middle of a
// frame — whatever was read up to that point has to survive.
func TestDemuxToleratesTruncatedPayload(t *testing.T) {
	full := frame(streamStdout, "$2$abcdef")
	truncated := full[:len(full)-3]

	stdout, _ := demux(truncated)

	if string(stdout) != "$2$abc" {
		t.Errorf("stdout = %q, want %q", stdout, "$2$abc")
	}
}

func TestDemuxToleratesTruncatedHeader(t *testing.T) {
	in := append(frame(streamStdout, "complete"), 1, 0, 0)

	stdout, _ := demux(in)

	if string(stdout) != "complete" {
		t.Errorf("stdout = %q, want %q", stdout, "complete")
	}
}

func TestDemuxStopsAtInvalidStreamKind(t *testing.T) {
	in := append(frame(streamStdout, "good"), frame(9, "garbage")...)

	stdout, stderr := demux(in)

	if string(stdout) != "good" {
		t.Errorf("stdout = %q, want %q", stdout, "good")
	}
	if len(stderr) != 0 {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

func TestDemuxEmpty(t *testing.T) {
	stdout, stderr := demux(nil)
	if stdout != nil || stderr != nil {
		t.Errorf("expected nil/nil, got %q/%q", stdout, stderr)
	}

	if out := demuxCombined(nil); out != nil {
		t.Errorf("combined = %q, want nil", out)
	}
}

// A payload length pointing past the end of the buffer must not panic.
func TestDemuxRejectsOversizedLength(t *testing.T) {
	b := make([]byte, frameHeaderLen+4)
	b[0] = streamStdout
	binary.BigEndian.PutUint32(b[4:frameHeaderLen], 0xFFFFFFFF)
	copy(b[frameHeaderLen:], "shrt")

	stdout, _ := demux(b)

	if string(stdout) != "shrt" {
		t.Errorf("stdout = %q, want %q", stdout, "shrt")
	}
}

func FuzzDemux(f *testing.F) {
	f.Add(frame(streamStdout, "hello"))
	f.Add(append(frame(streamStderr, "failure"), 2, 0, 0, 0))
	f.Add([]byte{0, 0, 0, 0, 255, 255, 255, 255})

	f.Fuzz(func(t *testing.T, data []byte) {
		stdout, stderr := demux(data)
		combined := demuxCombined(data)

		// The merged length can never fall below the split ones.
		if len(combined) < len(stdout) {
			t.Fatalf("combined (%d) is shorter than stdout (%d)", len(combined), len(stdout))
		}
		if len(stdout)+len(stderr) != len(combined) {
			t.Fatalf("stdout+stderr = %d, combined = %d", len(stdout)+len(stderr), len(combined))
		}
	})
}
