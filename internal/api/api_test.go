package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bodsch.me/mailcow-dockerapi/internal/actions"
	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
	"bodsch.me/mailcow-dockerapi/internal/dockerclient/dockertest"
	"bodsch.me/mailcow-dockerapi/internal/logging"
)

const testContainerID = "abc123"

// fakeStats liefert vorgegebene Messwerte.
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
	logger := logging.New(io.Discard, slog.LevelError)

	return New(fake, stats, actions.Env{Docker: fake, Logger: logger}, logger)
}

// do führt eine Anfrage gegen den Router aus.
func do(t *testing.T, srv *Server, method, path, body string) (*http.Response, string) {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	req := httptest.NewRequest(method, path, reader)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	res := rec.Result()
	t.Cleanup(func() { res.Body.Close() })

	out, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("Antwort lesen: %v", err)
	}

	return res, string(out)
}

func assertJSONResponse(t *testing.T, res *http.Response, body, want string) {
	t.Helper()

	// Der Statuscode ist immer 200; Fehler stehen im Feld "type".
	if res.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want 200", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if body != want {
		t.Errorf("Body =\n%s\n--- want ---\n%s", body, want)
	}
}

// /containers/json darf nicht von /containers/{container_id}/json verdeckt
// werden – "json" ist ein gültiger Wert für container_id.
func TestRoutingDistinguishesListFromInspect(t *testing.T) {
	fake := dockertest.WithContainers(testContainerID, "postfix-mailcow")
	srv := newServer(fake, &fakeStats{})

	_, body := do(t, srv, http.MethodGet, "/containers/json", "")

	if !strings.Contains(body, testContainerID) {
		t.Errorf("Liste erwartet, got %s", body)
	}
	if len(fake.ListAllCalls) != 1 {
		t.Errorf("ListAll-Aufrufe = %d, want 1", len(fake.ListAllCalls))
	}
}

func TestGetContainer(t *testing.T) {
	fake := dockertest.WithContainers(testContainerID, "postfix-mailcow")
	fake.InspectFn = func(id string) (json.RawMessage, error) {
		return json.RawMessage(`{"Id":"` + id + `","State":{"Running":true}}`), nil
	}
	srv := newServer(fake, &fakeStats{})

	res, body := do(t, srv, http.MethodGet, "/containers/"+testContainerID+"/json", "")

	want := `{
    "Id": "abc123",
    "State": {
        "Running": true
    }
}`
	assertJSONResponse(t, res, body, want)
}

// Nur laufende Container werden durchsucht – ListAll wird mit all=false
// aufgerufen.
func TestGetContainerSearchesRunningOnly(t *testing.T) {
	fake := dockertest.WithContainers(testContainerID, "postfix-mailcow")
	srv := newServer(fake, &fakeStats{})

	do(t, srv, http.MethodGet, "/containers/"+testContainerID+"/json", "")

	if len(fake.ListAllCalls) != 1 || fake.ListAllCalls[0] {
		t.Errorf("ListAll-Aufrufe = %v, want [false]", fake.ListAllCalls)
	}
}

func TestGetContainerNotFound(t *testing.T) {
	fake := &dockertest.Fake{}
	srv := newServer(fake, &fakeStats{})

	res, body := do(t, srv, http.MethodGet, "/containers/deadbeef/json", "")

	want := `{
    "type": "danger",
    "msg": "no container found"
}`
	assertJSONResponse(t, res, body, want)
}

// Die Kennung muss vollständig übereinstimmen; ein Präfix genügt nicht.
func TestGetContainerRequiresExactID(t *testing.T) {
	fake := dockertest.WithContainers("abc123456789", "postfix-mailcow")
	srv := newServer(fake, &fakeStats{})

	_, body := do(t, srv, http.MethodGet, "/containers/abc123/json", "")

	if !strings.Contains(body, "no container found") {
		t.Errorf("erwarte no container found, got %s", body)
	}
}

func TestGetContainerRejectsInvalidID(t *testing.T) {
	fake := dockertest.WithContainers(testContainerID, "postfix-mailcow")
	srv := newServer(fake, &fakeStats{})

	for _, id := range []string{"abc-123", "abc_123", "abc.123", "abc%20123"} {
		t.Run(id, func(t *testing.T) {
			_, body := do(t, srv, http.MethodGet, "/containers/"+id+"/json", "")

			if !strings.Contains(body, msgInvalidID) {
				t.Errorf("erwarte %q, got %s", msgInvalidID, body)
			}
		})
	}
}

// Pfadanteile mit .. erreichen den Handler gar nicht erst: der Router
// normalisiert den Pfad und leitet um.
func TestPathTraversalIsNormalizedByRouter(t *testing.T) {
	fake := dockertest.WithContainers(testContainerID, "postfix-mailcow")
	srv := newServer(fake, &fakeStats{})

	req := httptest.NewRequest(http.MethodGet, "/containers/../etc/json", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusTemporaryRedirect && rec.Code != http.StatusMovedPermanently {
		t.Errorf("Status = %d, erwarte eine Umleitung", rec.Code)
	}
	if len(fake.ListAllCalls) != 0 {
		t.Error("der Handler wurde trotz Traversal-Versuch erreicht")
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
		{"?all=unsinn", false},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			fake := dockertest.WithContainers(testContainerID, "postfix-mailcow")
			srv := newServer(fake, &fakeStats{})

			do(t, srv, http.MethodGet, "/containers/json"+tt.query, "")

			if len(fake.ListAllCalls) != 1 {
				t.Fatalf("ListAll-Aufrufe = %d, want 1", len(fake.ListAllCalls))
			}
			if fake.ListAllCalls[0] != tt.want {
				t.Errorf("all = %v, want %v", fake.ListAllCalls[0], tt.want)
			}
		})
	}
}

func TestListContainersReportsError(t *testing.T) {
	fake := &dockertest.Fake{ListErr: errors.New("daemon weg")}
	srv := newServer(fake, &fakeStats{})

	res, body := do(t, srv, http.MethodGet, "/containers/json", "")

	want := `{
    "type": "danger",
    "msg": "daemon weg"
}`
	assertJSONResponse(t, res, body, want)
}

func TestContainerPostSimpleAction(t *testing.T) {
	fake := dockertest.WithContainers(testContainerID, "postfix-mailcow")
	srv := newServer(fake, &fakeStats{})

	res, body := do(t, srv, http.MethodPost, "/containers/"+testContainerID+"/restart", "")

	want := `{
    "type": "success",
    "msg": "command completed successfully"
}`
	assertJSONResponse(t, res, body, want)

	if len(fake.Restarted) != 1 {
		t.Errorf("Neustarts = %v, want einen", fake.Restarted)
	}
}

func TestContainerPostExecResolvesCmdAndTask(t *testing.T) {
	fake := dockertest.WithContainers(testContainerID, "postfix-mailcow")
	srv := newServer(fake, &fakeStats{})

	body := `{"cmd":"mailq","task":"flush"}`
	res, out := do(t, srv, http.MethodPost, "/containers/"+testContainerID+"/exec", body)

	want := `{
    "type": "success",
    "msg": "command completed successfully"
}`
	assertJSONResponse(t, res, out, want)

	call, ok := fake.LastExec()
	if !ok {
		t.Fatal("kein Kommando abgesetzt")
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
		{"ohne rumpf", "", msgCmdMissing},
		{"leerer rumpf", "{}", msgCmdMissing},
		{"ohne task", `{"cmd":"mailq"}`, msgTaskMissing},
		{"ungueltiges json", `{kaputt`, msgCmdMissing},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := dockertest.WithContainers(testContainerID, "postfix-mailcow")
			srv := newServer(fake, &fakeStats{})

			_, body := do(t, srv, http.MethodPost, "/containers/"+testContainerID+"/exec", tt.body)

			if !strings.Contains(body, tt.want) {
				t.Errorf("erwarte %q, got %s", tt.want, body)
			}
		})
	}
}

func TestContainerPostUnknownAction(t *testing.T) {
	fake := dockertest.WithContainers(testContainerID, "postfix-mailcow")
	srv := newServer(fake, &fakeStats{})

	res, body := do(t, srv, http.MethodPost, "/containers/"+testContainerID+"/gibtsnicht", "")

	want := `{
    "type": "danger",
    "msg": "container_post - unknown api call"
}`
	assertJSONResponse(t, res, body, want)
}

func TestContainerPostUnknownExecTask(t *testing.T) {
	fake := dockertest.WithContainers(testContainerID, "postfix-mailcow")
	srv := newServer(fake, &fakeStats{})

	body := `{"cmd":"mailq","task":"gibtsnicht"}`
	_, out := do(t, srv, http.MethodPost, "/containers/"+testContainerID+"/exec", body)

	if !strings.Contains(out, actions.MsgUnknownAPICall) {
		t.Errorf("erwarte %q, got %s", actions.MsgUnknownAPICall, out)
	}
}

func TestContainerPostRejectsInvalidID(t *testing.T) {
	fake := dockertest.WithContainers(testContainerID, "postfix-mailcow")
	srv := newServer(fake, &fakeStats{})

	_, body := do(t, srv, http.MethodPost, "/containers/abc-123/restart", "")

	if !strings.Contains(body, msgInvalidAction) {
		t.Errorf("erwarte %q, got %s", msgInvalidAction, body)
	}
}

// Die Action erhält die Kennung als Ziel, nicht den Namen.
func TestContainerPostPassesIDAsTarget(t *testing.T) {
	fake := dockertest.WithContainers(testContainerID, "postfix-mailcow")
	srv := newServer(fake, &fakeStats{})

	do(t, srv, http.MethodPost, "/containers/"+testContainerID+"/stop", "")

	if len(fake.ListCalls) != 1 {
		t.Fatalf("List-Aufrufe = %d, want 1", len(fake.ListCalls))
	}

	want := dockerclient.Target{ContainerID: testContainerID}
	if fake.ListCalls[0].Target != want {
		t.Errorf("Target = %+v, want %+v", fake.ListCalls[0].Target, want)
	}
}

// Eine Textantwort trägt den passenden Content-Type.
func TestContainerPostTextResponse(t *testing.T) {
	fake := dockertest.WithContainers(testContainerID, "postfix-mailcow")
	fake.ExecResults = []dockerclient.ExecResult{
		{ExitCode: 0, Output: []byte(`[{"queue_name":"active"}]`)},
	}
	srv := newServer(fake, &fakeStats{})

	body := `{"cmd":"mailq","task":"list"}`
	res, out := do(t, srv, http.MethodPost, "/containers/"+testContainerID+"/exec", body)

	if ct := res.Header.Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if out != `[{"queue_name":"active"}]` {
		t.Errorf("Body = %s", out)
	}
}

func TestHostStats(t *testing.T) {
	stats := &fakeStats{host: json.RawMessage(`{"cpu":{"cores":8,"usage":12.5}}`)}
	srv := newServer(&dockertest.Fake{}, stats)

	res, body := do(t, srv, http.MethodGet, "/host/stats", "")

	want := `{
    "cpu": {
        "cores": 8,
        "usage": 12.5
    }
}`
	assertJSONResponse(t, res, body, want)
}

func TestHostStatsError(t *testing.T) {
	stats := &fakeStats{hostErr: errors.New("timeout beim Sammeln")}
	srv := newServer(&dockertest.Fake{}, stats)

	res, body := do(t, srv, http.MethodGet, "/host/stats", "")

	want := `{
    "type": "danger",
    "msg": "timeout beim Sammeln"
}`
	assertJSONResponse(t, res, body, want)
}

func TestContainerStatsUpdate(t *testing.T) {
	stats := &fakeStats{container: json.RawMessage(`[{"read":"2026-08-14T10:00:00Z"}]`)}
	srv := newServer(&dockertest.Fake{}, stats)

	res, body := do(t, srv, http.MethodPost, "/container/"+testContainerID+"/stats/update", "")

	want := `[
    {
        "read": "2026-08-14T10:00:00Z"
    }
]`
	assertJSONResponse(t, res, body, want)

	if len(stats.containerSeen) != 1 || stats.containerSeen[0] != testContainerID {
		t.Errorf("angefragte Kennungen = %v", stats.containerSeen)
	}
}

// Bei ungültiger Kennung wartete das Original endlos auf einen Redis-Schlüssel,
// der nie geschrieben wurde.
func TestContainerStatsRejectsInvalidID(t *testing.T) {
	stats := &fakeStats{}
	srv := newServer(&dockertest.Fake{}, stats)

	res, body := do(t, srv, http.MethodPost, "/container/abc-123/stats/update", "")

	want := `{
    "type": "danger",
    "msg": "no or invalid id defined"
}`
	assertJSONResponse(t, res, body, want)

	if len(stats.containerSeen) != 0 {
		t.Errorf("Statistiken wurden angefordert: %v", stats.containerSeen)
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

// Methoden, die eine Route nicht kennt, dürfen nicht durchschlagen.
func TestMethodMismatch(t *testing.T) {
	srv := newServer(&dockertest.Fake{}, &fakeStats{})

	req := httptest.NewRequest(http.MethodPost, "/host/stats", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want 405", rec.Code)
	}
}
