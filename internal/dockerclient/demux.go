package dockerclient

import "encoding/binary"

// Stream identifiers in the Docker attach protocol.
const (
	streamStdin  byte = 0
	streamStdout byte = 1
	streamStderr byte = 2

	frameHeaderLen = 8
)

// demux splits a multiplexed Docker attach stream.
//
// Without a TTY the daemon frames every write in eight bytes: one byte of stream
// identifier, three padding bytes, and the payload length as a big-endian uint32.
//
// Unlike stdcopy.StdCopy this tolerates a truncated stream: ExecInteractive reads
// against a deadline and regularly stops in the middle of a frame. Incomplete or
// implausible data is discarded, and everything read up to that point is kept.
func demux(data []byte) (stdout, stderr []byte) {
	for len(data) >= frameHeaderLen {
		kind := data[0]
		if kind != streamStdin && kind != streamStdout && kind != streamStderr {
			// Not a valid header — the remainder cannot be interpreted.
			return stdout, stderr
		}

		// The length stays a uint64 so that clamping it to the remaining bytes
		// needs no narrowing conversion.
		size := uint64(binary.BigEndian.Uint32(data[4:frameHeaderLen]))
		rest := data[frameHeaderLen:]
		if size > uint64(len(rest)) {
			// A clipped frame: take what is there.
			size = uint64(len(rest))
		}

		payload := rest[:size]
		switch kind {
		case streamStderr:
			stderr = append(stderr, payload...)
		default:
			// The server never sends stdin frames; treating them as stdout
			// matches the raw read-along in the Python implementation.
			stdout = append(stdout, payload...)
		}

		data = rest[size:]
	}

	return stdout, stderr
}

// demuxCombined merges stdout and stderr in the order they occurred. That matches
// docker-py's exec_run with demux=False, which exec_run_handler in DockerApi.py
// builds on.
func demuxCombined(data []byte) []byte {
	var out []byte

	for len(data) >= frameHeaderLen {
		kind := data[0]
		if kind != streamStdin && kind != streamStdout && kind != streamStderr {
			return out
		}

		size := uint64(binary.BigEndian.Uint32(data[4:frameHeaderLen]))
		rest := data[frameHeaderLen:]
		if size > uint64(len(rest)) {
			size = uint64(len(rest))
		}

		out = append(out, rest[:size]...)
		data = rest[size:]
	}

	return out
}
