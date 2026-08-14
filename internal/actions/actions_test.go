package actions

import (
	"context"
	"reflect"
	"testing"
	"time"

	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
	"bodsch.me/mailcow-dockerapi/internal/dockerclient/dockertest"
)

const (
	testContainerID   = "abc123"
	testContainerName = "postfix-mailcow"
	testDBRoot        = "geheim"
)

// testTime ist der feste Zeitpunkt, gegen den maildir-Aktionen prüfen.
var testTime = time.Unix(1700000000, 0)

func newFake() *dockertest.Fake {
	return dockertest.WithContainers(testContainerID, testContainerName)
}

func newEnv(fake *dockertest.Fake) Env {
	return Env{
		Docker: fake,
		DBRoot: testDBRoot,
		Now:    func() time.Time { return testTime },
	}
}

func byID() dockerclient.Target {
	return dockerclient.Target{ContainerID: testContainerID}
}

// assertBody vergleicht die Antwort Zeichen für Zeichen – die Einrückung ist
// Teil des Vertrags mit dem mailcow-Frontend.
func assertBody(t *testing.T, got Result, wantType, wantBody string) {
	t.Helper()

	if got.ContentType != wantType {
		t.Errorf("ContentType = %q, want %q", got.ContentType, wantType)
	}
	if string(got.Body) != wantBody {
		t.Errorf("Body =\n%s\n--- want ---\n%s", got.Body, wantBody)
	}
}

// assertExec prüft das abgesetzte Argv und den Benutzer.
func assertExec(t *testing.T, fake *dockertest.Fake, index int, wantCmd []string, wantUser string) {
	t.Helper()

	if len(fake.ExecCalls) <= index {
		t.Fatalf("erwarte mindestens %d Exec-Aufrufe, got %d", index+1, len(fake.ExecCalls))
	}

	call := fake.ExecCalls[index]
	if !reflect.DeepEqual(call.Cmd, wantCmd) {
		t.Errorf("Cmd[%d] =\n%#v\nwant\n%#v", index, call.Cmd, wantCmd)
	}
	if call.User != wantUser {
		t.Errorf("User[%d] = %q, want %q", index, call.User, wantUser)
	}
	if call.ContainerID != testContainerID {
		t.Errorf("ContainerID[%d] = %q, want %q", index, call.ContainerID, testContainerID)
	}
}

const successBody = `{
    "type": "success",
    "msg": "command completed successfully"
}`

// Das Antwortformat muss json.dumps(..., indent=4) entsprechen.
func TestJSONMatchesPythonFormatting(t *testing.T) {
	got := JSON(Message{Type: TypeSuccess, Msg: MsgCommandCompleted})

	assertBody(t, got, ContentTypeJSON, successBody)
}

// Go maskiert <, > und & in JSON standardmäßig, Python nicht. Die Zeichen
// kommen in Mailq-Ausgaben und Adressen laufend vor.
func TestJSONDoesNotEscapeHTML(t *testing.T) {
	got := JSON(Message{Type: TypeDanger, Msg: "<user@beispiel.de> & mehr"})

	want := `{
    "type": "danger",
    "msg": "<user@beispiel.de> & mehr"
}`
	assertBody(t, got, ContentTypeJSON, want)
}

// Der Encoder hängt einen Zeilenumbruch an, json.dumps nicht.
func TestJSONHasNoTrailingNewline(t *testing.T) {
	got := JSON(Message{Type: TypeSuccess, Msg: "x"})

	if len(got.Body) > 0 && got.Body[len(got.Body)-1] == '\n' {
		t.Errorf("Body endet mit Zeilenumbruch: %q", got.Body)
	}
}

// Das Feld text wird auch leer ausgegeben – json.dumps ließ es nie weg.
func TestMessageWithTextKeepsEmptyField(t *testing.T) {
	got := JSON(MessageWithText{Type: TypeInfo, Msg: "fertig", Text: ""})

	want := `{
    "type": "info",
    "msg": "fertig",
    "text": ""
}`
	assertBody(t, got, ContentTypeJSON, want)
}

func TestExecHandlerGeneric(t *testing.T) {
	t.Run("erfolg", func(t *testing.T) {
		got := execHandler(dockerclient.ExecResult{ExitCode: 0, Output: []byte("egal")})
		assertBody(t, got, ContentTypeJSON, successBody)
	})

	t.Run("fehler enthaelt ausgabe", func(t *testing.T) {
		got := execHandler(dockerclient.ExecResult{ExitCode: 1, Output: []byte("kaputt")})

		want := `{
    "type": "danger",
    "msg": "command failed: kaputt"
}`
		assertBody(t, got, ContentTypeJSON, want)
	})
}

func TestText(t *testing.T) {
	got := Text("reiner text")

	assertBody(t, got, ContentTypeText, "reiner text")
}

// Ohne Treffer lieferte das Original einen Rumpf mit "null"; hier ist es eine
// verwertbare Fehlermeldung.
func TestFirstContainerWithoutMatch(t *testing.T) {
	fake := &dockertest.Fake{}

	_, errRes := firstContainer(context.Background(), newEnv(fake), byID(), false)
	if errRes == nil {
		t.Fatal("erwarte Fehlerantwort")
	}

	want := `{
    "type": "danger",
    "msg": "no container found"
}`
	assertBody(t, *errRes, ContentTypeJSON, want)
}

func TestFirstContainerPropagatesListError(t *testing.T) {
	fake := newFake()
	fake.ListErr = errDocker

	_, errRes := firstContainer(context.Background(), newEnv(fake), byID(), false)
	if errRes == nil {
		t.Fatal("erwarte Fehlerantwort")
	}

	want := `{
    "type": "danger",
    "msg": "daemon nicht erreichbar"
}`
	assertBody(t, *errRes, ContentTypeJSON, want)
}

// errDocker steht für einen Fehler des Docker-Daemons.
var errDocker = testError("daemon nicht erreichbar")

type testError string

func (e testError) Error() string { return string(e) }
