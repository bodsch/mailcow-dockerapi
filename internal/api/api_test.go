package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bodsch.me/mailcow-dockerapi/internal/actions"
	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
	"bodsch.me/mailcow-dockerapi/internal/dockerclient/dockertest"
	"bodsch.me/mailcow-dockerapi/internal/logging"
	"bodsch.me/mailcow-dockerapi/internal/metrics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

const testContainerID = "abc123"

// fakeStats returns canned measurements.
type fakeStats struct {
	host          json.RawMessage
	container     json.RawMessage
	hostErr       error
	containerErr  error
	containerSeen []string
}

func (f *fakeStats) HostStats(context.Context) (json.RawMessage, error) {
	return f.host, f.hostErr
}

func (f *fakeStats) ContainerStats(_ context.Context, id string) (json.RawMessage, error) {
	f.containerSeen = append(f.containerSeen, id)
	return f.container, f.containerErr
}

func newServer(fake *dockertest.Fake, stats *fakeStats) *Server {
	srv, _ := newServerWithRegistry(fake, stats)
	return srv
}

// newServerWithRegistry also hands back the registry, for the tests that assert on
// the instrumentation.
func newServerWithRegistry(fake *dockertest.Fake, stats *fakeStats) (*Server, *prometheus.Registry) {
	log := logging.New(io.Discard, logging.Options{Level: "error", Format: "text"})
	reg := prometheus.NewRegistry()

	srv := New(Options{
		Docker:  fake,
		Stats:   stats,
		Env:     actions.Env{Docker: fake, Log: log},
		Metrics: metrics.New(reg, "test"),
		Log:     log,
	})

	return srv, reg
}

// response is what a test needs from a served request. Returning this rather than
// the *http.Response keeps the body's lifetime inside the helper.
type response struct {
	Code        int
	ContentType string
	Body        string
}

// do runs one request against the router.
func do(t *testing.T, srv *Server, method, path, body string) response {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	req := httptest.NewRequest(method, path, reader)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()

	out, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading the response: %v", err)
	}

	return response{
		Code:        res.StatusCode,
		ContentType: res.Header.Get("Content-Type"),
		Body:        string(out),
	}
}

func assertJSONResponse(t *testing.T, res response, want string) {
	t.Helper()

	// The status code is always 200; errors live in the "type" field.
	if res.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", res.Code)
	}
	if res.ContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", res.ContentType)
	}
	if res.Body != want {
		t.Errorf("body =\n%s\n--- want ---\n%s", res.Body, want)
	}
}

// /containers/json must not be shadowed by /containers/{container_id}/json —
// "json" is a valid value for container_id.
func TestRoutingDistinguishesListFromInspect(t *testing.T) {
	fake := dockertest.WithContainers(testContainerID, "postfix-mailcow")
	srv := newServer(fake, &fakeStats{})

	body := do(t, srv, http.MethodGet, "/containers/json", "").Body

	if !strings.Contains(body, testContainerID) {
		t.Errorf("expected the list, got %s", body)
	}
	if len(fake.ListAllCalls) != 1 {
		t.Errorf("ListAll calls = %d, want 1", len(fake.ListAllCalls))
	}
}

func TestGetContainer(t *testing.T) {
	fake := dockertest.WithContainers(testContainerID, "postfix-mailcow")
	fake.InspectFn = func(id string) (json.RawMessage, error) {
		return json.RawMessage(`{"Id":"` + id + `","State":{"Running":true}}`), nil
	}
	srv := newServer(fake, &fakeStats{})

	res := do(t, srv, http.MethodGet, "/containers/"+testContainerID+"/json", "")

	want := `{
    "Id": "abc123",
    "State": {
        "Running": true
    }
}`
	assertJSONResponse(t, res, want)
}

// Only running containers are searched — ListAll is called with all=false.
func TestGetContainerSearchesRunningOnly(t *testing.T) {
	fake := dockertest.WithContainers(testContainerID, "postfix-mailcow")
	srv := newServer(fake, &fakeStats{})

	do(t, srv, http.MethodGet, "/containers/"+testContainerID+"/json", "")

	if len(fake.ListAllCalls) != 1 || fake.ListAllCalls[0] {
		t.Errorf("ListAll calls = %v, want [false]", fake.ListAllCalls)
	}
}

func TestGetContainerNotFound(t *testing.T) {
	fake := &dockertest.Fake{}
	srv := newServer(fake, &fakeStats{})

	res := do(t, srv, http.MethodGet, "/containers/deadbeef/json", "")

	want := `{
    "type": "danger",
    "msg": "no container found"
}`
	assertJSONResponse(t, res, want)
}

// The id has to match in full; a prefix is not enough.
func TestGetContainerRequiresExactID(t *testing.T) {
	fake := dockertest.WithContainers("abc123456789", "postfix-mailcow")
	srv := newServer(fake, &fakeStats{})

	body := do(t, srv, http.MethodGet, "/containers/abc123/json", "").Body

	if !strings.Contains(body, "no container found") {
		t.Errorf("expected no container found, got %s", body)
	}
}

func TestGetContainerRejectsInvalidID(t *testing.T) {
	fake := dockertest.WithContainers(testContainerID, "postfix-mailcow")
	srv := newServer(fake, &fakeStats{})

	for _, id := range []string{"abc-123", "abc_123", "abc.123", "abc%20123"} {
		t.Run(id, func(t *testing.T) {
			body := do(t, srv, http.MethodGet, "/containers/"+id+"/json", "").Body

			if !strings.Contains(body, msgInvalidID) {
				t.Errorf("expected %q, got %s", msgInvalidID, body)
			}
		})
	}
}

// Path segments containing .. never reach the handler: the router normalises the
// path and redirects.
func TestPathTraversalIsNormalizedByRouter(t *testing.T) {
	fake := dockertest.WithContainers(testContainerID, "postfix-mailcow")
	srv := newServer(fake, &fakeStats{})

	req := httptest.NewRequest(http.MethodGet, "/containers/../etc/json", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusTemporaryRedirect && rec.Code != http.StatusMovedPermanently {
		t.Errorf("status = %d, expected a redirect", rec.Code)
	}
	if len(fake.ListAllCalls) != 0 {
		t.Error("the handler was reached despite the traversal attempt")
	}
}

func TestListContainersHonoursAllParameter(t *testing.T) {
	tests := []struct {
		query string
		want  bool
	}{
		{"", false},
		{"?all=true", true},
		{"?all=True", true},
		{"?all=1", true},
		{"?all=yes", true},
		{"?all=false", false},
		{"?all=0", false},
		{"?all=nonsense", false},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			fake := dockertest.WithContainers(testContainerID, "postfix-mailcow")
			srv := newServer(fake, &fakeStats{})

			do(t, srv, http.MethodGet, "/containers/json"+tt.query, "")

			if len(fake.ListAllCalls) != 1 {
				t.Fatalf("ListAll calls = %d, want 1", len(fake.ListAllCalls))
			}
			if fake.ListAllCalls[0] != tt.want {
				t.Errorf("all = %v, want %v", fake.ListAllCalls[0], tt.want)
			}
		})
	}
}

func TestListContainersReportsError(t *testing.T) {
	fake := &dockertest.Fake{ListErr: errors.New("the daemon is gone")}
	srv := newServer(fake, &fakeStats{})

	res := do(t, srv, http.MethodGet, "/containers/json", "")

	want := `{
    "type": "danger",
    "msg": "the daemon is gone"
}`
	assertJSONResponse(t, res, want)
}

func TestContainerPostSimpleAction(t *testing.T) {
	fake := dockertest.WithContainers(testContainerID, "postfix-mailcow")
	srv := newServer(fake, &fakeStats{})

	res := do(t, srv, http.MethodPost, "/containers/"+testContainerID+"/restart", "")

	want := `{
    "type": "success",
    "msg": "command completed successfully"
}`
	assertJSONResponse(t, res, want)

	if len(fake.Restarted) != 1 {
		t.Errorf("restarts = %v, want exactly one", fake.Restarted)
	}
}

func TestContainerPostExecResolvesCmdAndTask(t *testing.T) {
	fake := dockertest.WithContainers(testContainerID, "postfix-mailcow")
	srv := newServer(fake, &fakeStats{})

	body := `{"cmd":"mailq","task":"flush"}`
	res := do(t, srv, http.MethodPost, "/containers/"+testContainerID+"/exec", body)

	want := `{
    "type": "success",
    "msg": "command completed successfully"
}`
	assertJSONResponse(t, res, want)

	call, ok := fake.LastExec()
	if !ok {
		t.Fatal("no command was issued")
	}
	if call.Cmd[0] != "/usr/sbin/postqueue" || call.Cmd[1] != "-f" {
		t.Errorf("Cmd = %v", call.Cmd)
	}
}

func TestContainerPostExecRequiresCmdAndTask(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"no body", "", msgCmdMissing},
		{"empty body", "{}", msgCmdMissing},
		{"no task", `{"cmd":"mailq"}`, msgTaskMissing},
		{"invalid json", `{broken`, msgCmdMissing},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := dockertest.WithContainers(testContainerID, "postfix-mailcow")
			srv := newServer(fake, &fakeStats{})

			body := do(t, srv, http.MethodPost, "/containers/"+testContainerID+"/exec", tt.body).Body

			if !strings.Contains(body, tt.want) {
				t.Errorf("expected %q, got %s", tt.want, body)
			}
		})
	}
}

func TestContainerPostUnknownAction(t *testing.T) {
	fake := dockertest.WithContainers(testContainerID, "postfix-mailcow")
	srv := newServer(fake, &fakeStats{})

	res := do(t, srv, http.MethodPost, "/containers/"+testContainerID+"/does-not-exist", "")

	want := `{
    "type": "danger",
    "msg": "container_post - unknown api call"
}`
	assertJSONResponse(t, res, want)
}

func TestContainerPostUnknownExecTask(t *testing.T) {
	fake := dockertest.WithContainers(testContainerID, "postfix-mailcow")
	srv := newServer(fake, &fakeStats{})

	body := `{"cmd":"mailq","task":"does-not-exist"}`
	out := do(t, srv, http.MethodPost, "/containers/"+testContainerID+"/exec", body).Body

	if !strings.Contains(out, actions.MsgUnknownAPICall) {
		t.Errorf("expected %q, got %s", actions.MsgUnknownAPICall, out)
	}
}

func TestContainerPostRejectsInvalidID(t *testing.T) {
	fake := dockertest.WithContainers(testContainerID, "postfix-mailcow")
	srv := newServer(fake, &fakeStats{})

	body := do(t, srv, http.MethodPost, "/containers/abc-123/restart", "").Body

	if !strings.Contains(body, msgInvalidAction) {
		t.Errorf("expected %q, got %s", msgInvalidAction, body)
	}
}

// The action receives the id as its target, not the name.
func TestContainerPostPassesIDAsTarget(t *testing.T) {
	fake := dockertest.WithContainers(testContainerID, "postfix-mailcow")
	srv := newServer(fake, &fakeStats{})

	do(t, srv, http.MethodPost, "/containers/"+testContainerID+"/stop", "")

	if len(fake.ListCalls) != 1 {
		t.Fatalf("List calls = %d, want 1", len(fake.ListCalls))
	}

	want := dockerclient.Target{ContainerID: testContainerID}
	if fake.ListCalls[0].Target != want {
		t.Errorf("Target = %+v, want %+v", fake.ListCalls[0].Target, want)
	}
}

// A text response carries the matching content type.
func TestContainerPostTextResponse(t *testing.T) {
	fake := dockertest.WithContainers(testContainerID, "postfix-mailcow")
	fake.ExecResults = []dockerclient.ExecResult{
		{ExitCode: 0, Output: []byte(`[{"queue_name":"active"}]`)},
	}
	srv := newServer(fake, &fakeStats{})

	body := `{"cmd":"mailq","task":"list"}`
	res := do(t, srv, http.MethodPost, "/containers/"+testContainerID+"/exec", body)

	if res.ContentType != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q", res.ContentType)
	}
	if res.Body != `[{"queue_name":"active"}]` {
		t.Errorf("body = %s", res.Body)
	}
}

func TestHostStats(t *testing.T) {
	stats := &fakeStats{host: json.RawMessage(`{"cpu":{"cores":8,"usage":12.5}}`)}
	srv := newServer(&dockertest.Fake{}, stats)

	res := do(t, srv, http.MethodGet, "/host/stats", "")

	want := `{
    "cpu": {
        "cores": 8,
        "usage": 12.5
    }
}`
	assertJSONResponse(t, res, want)
}

func TestHostStatsError(t *testing.T) {
	stats := &fakeStats{hostErr: errors.New("timeout waiting for stats")}
	srv := newServer(&dockertest.Fake{}, stats)

	res := do(t, srv, http.MethodGet, "/host/stats", "")

	want := `{
    "type": "danger",
    "msg": "timeout waiting for stats"
}`
	assertJSONResponse(t, res, want)
}

func TestContainerStatsUpdate(t *testing.T) {
	stats := &fakeStats{container: json.RawMessage(`[{"read":"2026-08-14T10:00:00Z"}]`)}
	srv := newServer(&dockertest.Fake{}, stats)

	res := do(t, srv, http.MethodPost, "/container/"+testContainerID+"/stats/update", "")

	want := `[
    {
        "read": "2026-08-14T10:00:00Z"
    }
]`
	assertJSONResponse(t, res, want)

	if len(stats.containerSeen) != 1 || stats.containerSeen[0] != testContainerID {
		t.Errorf("requested ids = %v", stats.containerSeen)
	}
}

// For an invalid id the original waited forever on a Redis key that was never
// written.
func TestContainerStatsRejectsInvalidID(t *testing.T) {
	stats := &fakeStats{}
	srv := newServer(&dockertest.Fake{}, stats)

	res := do(t, srv, http.MethodPost, "/container/abc-123/stats/update", "")

	want := `{
    "type": "danger",
    "msg": "no or invalid id defined"
}`
	assertJSONResponse(t, res, want)

	if len(stats.containerSeen) != 0 {
		t.Errorf("statistics were requested anyway: %v", stats.containerSeen)
	}
}

func TestIsAlnum(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"abc123", true},
		{"ABC", true},
		{"0", true},
		{"", false},
		{"abc-123", false},
		{"abc_123", false},
		{"abc 123", false},
		{"abc.123", false},
		{"../etc/passwd", false},
		{"müller", false},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := isAlnum(tt.in); got != tt.want {
				t.Errorf("isAlnum(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// Methods a route does not know must not fall through.
func TestMethodMismatch(t *testing.T) {
	srv := newServer(&dockertest.Fake{}, &fakeStats{})

	req := httptest.NewRequest(http.MethodPost, "/host/stats", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

// A frontend asking for a call this build does not implement is the signal that
// the two versions have drifted apart, so it gets its own counter.
func TestUnknownCallIsCounted(t *testing.T) {
	fake := dockertest.WithContainers(testContainerID, "postfix-mailcow")
	srv, reg := newServerWithRegistry(fake, &fakeStats{})

	do(t, srv, http.MethodPost, "/containers/"+testContainerID+"/does-not-exist", "")

	const want = `
# HELP mailcow_dockerapi_actions_rejected_total Requests that never reached an action, by reason. A rising unknown_call means the frontend is asking for calls this build does not implement.
# TYPE mailcow_dockerapi_actions_rejected_total counter
mailcow_dockerapi_actions_rejected_total{reason="unknown_call",source="http"} 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want),
		"mailcow_dockerapi_actions_rejected_total"); err != nil {
		t.Error(err)
	}
}

// Executed actions are counted per registry name, and the route label is the
// pattern rather than the path — otherwise every container id would become its own
// time series.
func TestActionsAndRoutesAreCounted(t *testing.T) {
	fake := dockertest.WithContainers(testContainerID, "postfix-mailcow")
	srv, reg := newServerWithRegistry(fake, &fakeStats{})

	do(t, srv, http.MethodPost, "/containers/"+testContainerID+"/restart", "")

	const wantActions = `
# HELP mailcow_dockerapi_actions_total Container actions executed, by registry name and the channel they arrived through.
# TYPE mailcow_dockerapi_actions_total counter
mailcow_dockerapi_actions_total{action="container_post__restart",source="http"} 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(wantActions),
		"mailcow_dockerapi_actions_total"); err != nil {
		t.Error(err)
	}

	const wantRequests = `
# HELP mailcow_dockerapi_http_requests_total HTTP requests by route pattern.
# TYPE mailcow_dockerapi_http_requests_total counter
mailcow_dockerapi_http_requests_total{route="POST /containers/{container_id}/{post_action}"} 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(wantRequests),
		"mailcow_dockerapi_http_requests_total"); err != nil {
		t.Error(err)
	}
}
