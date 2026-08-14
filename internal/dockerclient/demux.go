package dockerclient

import "encoding/binary"

// Stream-Kennungen im Docker-Attach-Protokoll.
const (
	streamStdin  byte = 0
	streamStdout byte = 1
	streamStderr byte = 2

	frameHeaderLen = 8
)

// demux zerlegt einen gemultiplexten Docker-Attach-Strom.
//
// Ohne TTY rahmt der Daemon jede Ausgabe mit acht Byte ein: ein Byte
// Stromkennung, drei Füllbytes und die Nutzlastlänge als uint32 (big endian).
//
// Anders als stdcopy.StdCopy verträgt die Funktion einen abgeschnittenen
// Strom: ExecInteractive liest gegen eine Zeitschranke und endet regelmäßig
// mitten in einem Frame. Unvollständige oder unplausible Daten werden
// verworfen, das bis dahin Gelesene bleibt erhalten.
func demux(data []byte) (stdout, stderr []byte) {
	for len(data) >= frameHeaderLen {
		kind := data[0]
		if kind != streamStdin && kind != streamStdout && kind != streamStderr {
			// Kein gültiger Header – der Rest ist nicht interpretierbar.
			return stdout, stderr
		}

		size := binary.BigEndian.Uint32(data[4:frameHeaderLen])
		rest := data[frameHeaderLen:]
		if uint64(size) > uint64(len(rest)) {
			// Angeschnittener Frame: übernehmen, was vorliegt.
			size = uint32(len(rest))
		}

		payload := rest[:size]
		switch kind {
		case streamStderr:
			stderr = append(stderr, payload...)
		default:
			// stdin-Frames treten serverseitig nicht auf; sie würden wie
			// stdout behandelt, was dem rohen Mitlesen in Python entspricht.
			stdout = append(stdout, payload...)
		}

		data = rest[size:]
	}

	return stdout, stderr
}

// demuxCombined führt stdout und stderr in der Reihenfolge ihres Auftretens
// zusammen. Das entspricht docker-py exec_run mit demux=False, worauf
// exec_run_handler in DockerApi.py aufbaut.
func demuxCombined(data []byte) []byte {
	var out []byte

	for len(data) >= frameHeaderLen {
		kind := data[0]
		if kind != streamStdin && kind != streamStdout && kind != streamStderr {
			return out
		}

		size := binary.BigEndian.Uint32(data[4:frameHeaderLen])
		rest := data[frameHeaderLen:]
		if uint64(size) > uint64(len(rest)) {
			size = uint32(len(rest))
		}

		out = append(out, rest[:size]...)
		data = rest[size:]
	}

	return out
}
