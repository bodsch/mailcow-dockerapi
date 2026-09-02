package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bodsch.me/mailcow-dockerapi/internal/dockerclient/dockertest"
)

// countingReader reports how much of the body the handler actually consumed.
// A fixed-size string could not tell the difference between a limit that works
// and one that was raised, because the handler would read all of it either way.
type countingReader struct {
	remaining int
	read      int
}

func (r *countingReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, io.EOF
	}
	n := len(p)
	if n > r.remaining {
		n = r.remaining
	}
	for i := range p[:n] {
		p[i] = 'A'
	}
	r.remaining -= n
	r.read += n
	return n, nil
}

// TestOversizedBodyIsNotReadWhole is the promise on maxRequestBody: "the service
// runs privileged and should not be knocked over by an oversized body".
//
// Nothing checked it. The container this serves sits on the mailcow network with
// the Docker socket mounted, so the body of a POST is the cheapest thing an
// unhappy client — or a loop in one — can make arbitrarily large. Without the
// limit the handler reads all of it into memory before deciding it is not valid
// JSON, and enough concurrent requests take the process with the Docker socket
// down.
func TestOversizedBodyIsNotReadWhole(t *testing.T) {
	srv := newServer(&dockertest.Fake{}, &fakeStats{})

	// Four times the limit, streamed rather than allocated up front.
	body := &countingReader{remaining: 4 * maxRequestBody}

	req := httptest.NewRequest(http.MethodPost, "/containers/abc123/stop", body)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 — errors travel in the body here", rec.Code)
	}
	// A little slack for the read buffer's last chunk; what must not happen is
	// the whole 16 MiB arriving.
	if body.read > maxRequestBody+64<<10 {
		t.Errorf("the handler read %d bytes of a %d byte body, want it to stop near %d",
			body.read, 4*maxRequestBody, maxRequestBody)
	}
}

// The other half: a body inside the limit still reaches the action intact, or the
// limit would be protecting the service by breaking it. exec is the only action
// that reads the body, so it is the one that can tell.
func TestBodyWithinTheLimitReachesTheAction(t *testing.T) {
	srv := newServer(&dockertest.Fake{}, &fakeStats{})

	// A megabyte of padding in a field nothing reads, so the document stays
	// valid JSON and stays well under the limit.
	padding := strings.Repeat("x", 1<<20)
	body := `{"cmd":"system","task":"df","dir":"/","note":"` + padding + `"}`

	res := do(t, srv, http.MethodPost, "/containers/abc123/exec", body)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Code)
	}
	// Each of these means a field was not there to read, which is what a
	// truncated body looks like from inside: cmd and task are read while
	// resolving the action, dir by the action itself. Their absence is the proof
	// that all three survived the megabyte after them.
	for _, missing := range []string{msgCmdMissing, msgTaskMissing, "dir is missing"} {
		if strings.Contains(res.Body, missing) {
			t.Errorf("a 1 MiB body lost a field below the limit (%q): %s", missing, res.Body)
		}
	}
}
