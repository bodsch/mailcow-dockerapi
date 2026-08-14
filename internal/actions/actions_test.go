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
	testDBRoot        = "secret"
)

// testTime is the fixed instant the maildir actions are checked against.
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

// assertBody compares the response byte for byte — the indentation is part of the
// contract with the mailcow frontend.
func assertBody(t *testing.T, got Result, wantType, wantBody string) {
	t.Helper()

	if got.ContentType != wantType {
		t.Errorf("ContentType = %q, want %q", got.ContentType, wantType)
	}
	if string(got.Body) != wantBody {
		t.Errorf("body =\n%s\n--- want ---\n%s", got.Body, wantBody)
	}
}

// assertExec checks the issued argv and the user.
func assertExec(t *testing.T, fake *dockertest.Fake, index int, wantCmd []string, wantUser string) {
	t.Helper()

	if len(fake.ExecCalls) <= index {
		t.Fatalf("expected at least %d exec calls, got %d", index+1, len(fake.ExecCalls))
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

// The response format has to match json.dumps(..., indent=4).
func TestJSONMatchesPythonFormatting(t *testing.T) {
	got := JSON(Message{Type: TypeSuccess, Msg: MsgCommandCompleted})

	assertBody(t, got, ContentTypeJSON, successBody)
}

// Go escapes <, > and & in JSON by default, Python does not. Those characters turn
// up in mailq output and in addresses constantly.
func TestJSONDoesNotEscapeHTML(t *testing.T) {
	got := JSON(Message{Type: TypeDanger, Msg: "<user@example.org> & more"})

	want := `{
    "type": "danger",
    "msg": "<user@example.org> & more"
}`
	assertBody(t, got, ContentTypeJSON, want)
}

// The encoder appends a newline, json.dumps does not.
func TestJSONHasNoTrailingNewline(t *testing.T) {
	got := JSON(Message{Type: TypeSuccess, Msg: "x"})

	if len(got.Body) > 0 && got.Body[len(got.Body)-1] == '\n' {
		t.Errorf("the body ends in a newline: %q", got.Body)
	}
}

// The text field is emitted even when empty — json.dumps never dropped it.
func TestMessageWithTextKeepsEmptyField(t *testing.T) {
	got := JSON(MessageWithText{Type: TypeInfo, Msg: "done", Text: ""})

	want := `{
    "type": "info",
    "msg": "done",
    "text": ""
}`
	assertBody(t, got, ContentTypeJSON, want)
}

func TestExecHandlerGeneric(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		got := execHandler(dockerclient.ExecResult{ExitCode: 0, Output: []byte("whatever")})
		assertBody(t, got, ContentTypeJSON, successBody)
	})

	t.Run("a failure carries the output", func(t *testing.T) {
		got := execHandler(dockerclient.ExecResult{ExitCode: 1, Output: []byte("broken")})

		want := `{
    "type": "danger",
    "msg": "command failed: broken"
}`
		assertBody(t, got, ContentTypeJSON, want)
	})
}

func TestText(t *testing.T) {
	got := Text("plain text")

	assertBody(t, got, ContentTypeText, "plain text")
}

// Without a match the original produced a body of "null"; here it is a usable error
// message.
func TestFirstContainerWithoutMatch(t *testing.T) {
	fake := &dockertest.Fake{}

	_, errRes := firstContainer(context.Background(), newEnv(fake), byID(), false)
	if errRes == nil {
		t.Fatal("expected an error response")
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
		t.Fatal("expected an error response")
	}

	want := `{
    "type": "danger",
    "msg": "the daemon is unreachable"
}`
	assertBody(t, *errRes, ContentTypeJSON, want)
}

// errDocker stands for a failure of the Docker daemon.
var errDocker = testError("the daemon is unreachable")

type testError string

func (e testError) Error() string { return string(e) }
