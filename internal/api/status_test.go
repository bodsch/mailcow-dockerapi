package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"bodsch.me/mailcow-dockerapi/internal/dockerclient/dockertest"
)

// TestEveryHandlerAnswers200 is the promise the frontend is built on: the status
// code carries no information, the body's "type" field does. A handler that
// started reporting 4xx or 5xx would make a client that only reads the body miss
// the error, or one that only reads the code report a failure that did not
// happen.
func TestEveryHandlerAnswers200(t *testing.T) {
	// Deliberately the unhappy paths: an unknown container, a missing body, a
	// stats backend that fails. Every one of them is an error, and every one of
	// them has to arrive as 200.
	errStats := errors.New("the stats backend is unreachable")
	srv := newServer(&dockertest.Fake{}, &fakeStats{
		hostErr:      errStats,
		containerErr: errStats,
	})

	requests := []struct {
		method, path, body string
	}{
		{http.MethodGet, "/host/stats", ""},
		{http.MethodGet, "/containers/json", ""},
		{http.MethodGet, "/containers/deadbeef/json", ""},
		{http.MethodPost, "/containers/deadbeef/stop", ""},
		{http.MethodPost, "/containers/deadbeef/exec", ""},
		{http.MethodPost, "/containers/deadbeef/exec", `{"cmd":"system"}`},
		{http.MethodPost, "/containers/deadbeef/nonsense", ""},
		{http.MethodPost, "/containers/not-alnum/stop", ""},
		{http.MethodPost, "/container/deadbeef/stats/update", ""},
		{http.MethodPost, "/container/not-alnum/stats/update", ""},
	}

	for _, rq := range requests {
		t.Run(rq.method+" "+rq.path, func(t *testing.T) {
			res := do(t, srv, rq.method, rq.path, rq.body)

			if res.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 — the frontend evaluates the body, not the code", res.Code)
			}
			// And the body has to be the shape it evaluates.
			if res.ContentType == "application/json" && !json.Valid([]byte(res.Body)) {
				t.Errorf("a 200 carried a body that is not JSON: %q", res.Body)
			}
		})
	}
}

// TestTheRouterAnswersOutsideThatPromise records where "always 200" stops, which
// neither README nor DEVIATIONS said.
//
// An unknown path or a wrong method is answered by net/http's mux, not by a
// handler, so it comes back 4xx with a plain-text body. FastAPI answered those
// with JSON (`{"detail":"Not Found"}`), so a client that parses every body is
// looking at the one real difference. It is left as it is — the codes are correct
// HTTP and no mailcow client calls these paths — but it is written down here so
// the next person does not read "always 200" as covering them.
func TestTheRouterAnswersOutsideThatPromise(t *testing.T) {
	srv := newServer(&dockertest.Fake{}, &fakeStats{})

	tests := []struct {
		name, method, path string
		want               int
	}{
		{"unknown path", http.MethodGet, "/unknown", http.StatusNotFound},
		{"root", http.MethodGet, "/", http.StatusNotFound},
		{"wrong method on a GET route", http.MethodPost, "/containers/json", http.StatusMethodNotAllowed},
		{"wrong method on a POST route", http.MethodGet, "/containers/abc123/stop", http.StatusMethodNotAllowed},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := do(t, srv, tc.method, tc.path, "")
			if res.Code != tc.want {
				t.Errorf("status = %d, want %d", res.Code, tc.want)
			}
		})
	}
}
