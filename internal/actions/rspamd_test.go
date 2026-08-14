package actions

import (
	"context"
	"strings"
	"testing"

	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
)

const testHash = "$2$abcdefghijklmnopqrstuvwxyz123456$789"

// rspamdSuccess reproduces the complete sequence: rspamadm returns the hash, and
// reading the file back confirms it.
func rspamdSuccess() func(string, dockerclient.InteractiveOptions) (string, error) {
	return func(_ string, opts dockerclient.InteractiveOptions) (string, error) {
		if strings.Contains(opts.Command, "rspamadm pw") {
			return "\n" + testHash + "\n", nil
		}
		return `enable_password = "` + testHash + `";` + "\n", nil
	}
}

func TestRspamdWorkerPassword(t *testing.T) {
	fake := newFake()
	fake.InteractiveFn = rspamdSuccess()
	req := Request{"raw": "newPassword"}

	got := RspamdWorkerPassword(context.Background(), newEnv(fake), req, byID())

	assertBody(t, got, ContentTypeJSON, successBody)

	if len(fake.InteractiveCalls) != 2 {
		t.Fatalf("interactive calls = %d, want 2", len(fake.InteractiveCalls))
	}

	gen := fake.InteractiveCalls[0]
	if gen.Command != "/usr/bin/rspamadm pw -e -p 'newPassword' 2> /dev/null" {
		t.Errorf("generation = %q", gen.Command)
	}
	if gen.User != "_rspamd" {
		t.Errorf("User = %q, want _rspamd", gen.User)
	}

	write := fake.InteractiveCalls[1]
	wantWrite := `/bin/echo 'enable_password = "` + testHash + `";' > ` +
		rspamdPasswordFile + " && cat " + rspamdPasswordFile
	if write.Command != wantWrite {
		t.Errorf("write command =\n%q\nwant\n%q", write.Command, wantWrite)
	}

	// Only the restart makes the password take effect.
	if len(fake.Restarted) != 1 || fake.Restarted[0] != testContainerID {
		t.Errorf("restarts = %v, want [%s]", fake.Restarted, testContainerID)
	}
}

// A password containing quotes must not escape the command.
func TestRspamdWorkerPasswordQuotesRaw(t *testing.T) {
	fake := newFake()
	fake.InteractiveFn = rspamdSuccess()
	req := Request{"raw": "pass'; touch /tmp/x; '"}

	RspamdWorkerPassword(context.Background(), newEnv(fake), req, byID())

	gen := fake.InteractiveCalls[0]
	want := `/usr/bin/rspamadm pw -e -p 'pass'\''; touch /tmp/x; '\''' 2> /dev/null`
	if gen.Command != want {
		t.Errorf("generation =\n%q\nwant\n%q", gen.Command, want)
	}
}

// When the file read back does not confirm the hash, the change counts as failed
// and the container is left alone.
func TestRspamdWorkerPasswordFailsWhenFileDoesNotConfirm(t *testing.T) {
	fake := newFake()
	fake.InteractiveFn = func(_ string, opts dockerclient.InteractiveOptions) (string, error) {
		if strings.Contains(opts.Command, "rspamadm pw") {
			return testHash + "\n", nil
		}
		return "something else entirely\n", nil
	}
	req := Request{"raw": "newPassword"}

	got := RspamdWorkerPassword(context.Background(), newEnv(fake), req, byID())

	want := `{
    "type": "danger",
    "msg": "command did not complete"
}`
	assertBody(t, got, ContentTypeJSON, want)

	if len(fake.Restarted) != 0 {
		t.Errorf("the container was restarted anyway: %v", fake.Restarted)
	}
}

func TestRspamdWorkerPasswordWithoutHashInOutput(t *testing.T) {
	fake := newFake()
	fake.InteractiveFn = func(string, dockerclient.InteractiveOptions) (string, error) {
		return "rspamadm: unknown command\n", nil
	}
	req := Request{"raw": "newPassword"}

	got := RspamdWorkerPassword(context.Background(), newEnv(fake), req, byID())

	want := `{
    "type": "danger",
    "msg": "command did not complete"
}`
	assertBody(t, got, ContentTypeJSON, want)
}

// With $2$ at the end of a line and no hash after it, Python failed on the access
// to group(0).
func TestRspamdWorkerPasswordHandlesBarePrefix(t *testing.T) {
	fake := newFake()
	fake.InteractiveFn = func(string, dockerclient.InteractiveOptions) (string, error) {
		return "$2$\n", nil
	}
	req := Request{"raw": "newPassword"}

	got := RspamdWorkerPassword(context.Background(), newEnv(fake), req, byID())

	want := `{
    "type": "danger",
    "msg": "command did not complete"
}`
	assertBody(t, got, ContentTypeJSON, want)
}

func TestRspamdWorkerPasswordRequiresRaw(t *testing.T) {
	fake := newFake()

	got := RspamdWorkerPassword(context.Background(), newEnv(fake), Request{}, byID())

	want := `{
    "type": "danger",
    "msg": "raw is missing"
}`
	assertBody(t, got, ContentTypeJSON, want)
}

// Before being written, the hash is stripped of everything that is not part of it.
func TestRspamdSanitizesHash(t *testing.T) {
	fake := newFake()
	fake.InteractiveFn = func(_ string, opts dockerclient.InteractiveOptions) (string, error) {
		if strings.Contains(opts.Command, "rspamadm pw") {
			// With newlines and control characters, as they can arrive over the
			// multiplexed stream.
			return "Password: \r\n" + testHash + " \r\n", nil
		}
		return `enable_password = "` + testHash + `";`, nil
	}
	req := Request{"raw": "x"}

	got := RspamdWorkerPassword(context.Background(), newEnv(fake), req, byID())

	assertBody(t, got, ContentTypeJSON, successBody)

	write := fake.InteractiveCalls[1]
	if !strings.Contains(write.Command, `"`+testHash+`"`) {
		t.Errorf("the write command does not carry the hash cleanly: %q", write.Command)
	}
}
