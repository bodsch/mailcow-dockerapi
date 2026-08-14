package actions

import (
	"context"
	"strings"
	"testing"

	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
)

const testHash = "$2$abcdefghijklmnopqrstuvwxyz123456$789"

// rspamdSuccess bildet den vollständigen Ablauf nach: rspamadm liefert den
// Hash, das Zurücklesen der Datei bestätigt ihn.
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
	req := Request{"raw": "neuesPasswort"}

	got := RspamdWorkerPassword(context.Background(), newEnv(fake), req, byID())

	assertBody(t, got, ContentTypeJSON, successBody)

	if len(fake.InteractiveCalls) != 2 {
		t.Fatalf("interaktive Aufrufe = %d, want 2", len(fake.InteractiveCalls))
	}

	gen := fake.InteractiveCalls[0]
	if gen.Command != "/usr/bin/rspamadm pw -e -p 'neuesPasswort' 2> /dev/null" {
		t.Errorf("Erzeugung = %q", gen.Command)
	}
	if gen.User != "_rspamd" {
		t.Errorf("User = %q, want _rspamd", gen.User)
	}

	write := fake.InteractiveCalls[1]
	wantWrite := `/bin/echo 'enable_password = "` + testHash + `";' > ` +
		rspamdPasswordFile + " && cat " + rspamdPasswordFile
	if write.Command != wantWrite {
		t.Errorf("Schreibkommando =\n%q\nwant\n%q", write.Command, wantWrite)
	}

	// Erst der Neustart macht das Passwort wirksam.
	if len(fake.Restarted) != 1 || fake.Restarted[0] != testContainerID {
		t.Errorf("Neustarts = %v, want [%s]", fake.Restarted, testContainerID)
	}
}

// Ein Passwort mit Anführungszeichen darf das Kommando nicht verlassen.
func TestRspamdWorkerPasswordQuotesRaw(t *testing.T) {
	fake := newFake()
	fake.InteractiveFn = rspamdSuccess()
	req := Request{"raw": "pass'; touch /tmp/x; '"}

	RspamdWorkerPassword(context.Background(), newEnv(fake), req, byID())

	gen := fake.InteractiveCalls[0]
	want := `/usr/bin/rspamadm pw -e -p 'pass'\''; touch /tmp/x; '\''' 2> /dev/null`
	if gen.Command != want {
		t.Errorf("Erzeugung =\n%q\nwant\n%q", gen.Command, want)
	}
}

// Bestätigt die zurückgelesene Datei den Hash nicht, gilt der Wechsel als
// gescheitert und der Container bleibt unangetastet.
func TestRspamdWorkerPasswordFailsWhenFileDoesNotConfirm(t *testing.T) {
	fake := newFake()
	fake.InteractiveFn = func(_ string, opts dockerclient.InteractiveOptions) (string, error) {
		if strings.Contains(opts.Command, "rspamadm pw") {
			return testHash + "\n", nil
		}
		return "etwas ganz anderes\n", nil
	}
	req := Request{"raw": "neuesPasswort"}

	got := RspamdWorkerPassword(context.Background(), newEnv(fake), req, byID())

	want := `{
    "type": "danger",
    "msg": "command did not complete"
}`
	assertBody(t, got, ContentTypeJSON, want)

	if len(fake.Restarted) != 0 {
		t.Errorf("Container wurde neu gestartet: %v", fake.Restarted)
	}
}

func TestRspamdWorkerPasswordWithoutHashInOutput(t *testing.T) {
	fake := newFake()
	fake.InteractiveFn = func(string, dockerclient.InteractiveOptions) (string, error) {
		return "rspamadm: unbekannter Befehl\n", nil
	}
	req := Request{"raw": "neuesPasswort"}

	got := RspamdWorkerPassword(context.Background(), newEnv(fake), req, byID())

	want := `{
    "type": "danger",
    "msg": "command did not complete"
}`
	assertBody(t, got, ContentTypeJSON, want)
}

// Steht $2$ ohne folgenden Hash am Zeilenende, scheiterte Python am Zugriff
// auf group(0).
func TestRspamdWorkerPasswordHandlesBarePrefix(t *testing.T) {
	fake := newFake()
	fake.InteractiveFn = func(string, dockerclient.InteractiveOptions) (string, error) {
		return "$2$\n", nil
	}
	req := Request{"raw": "neuesPasswort"}

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

// Der Hash wird vor dem Schreiben von allem befreit, was nicht dazugehört.
func TestRspamdSanitizesHash(t *testing.T) {
	fake := newFake()
	fake.InteractiveFn = func(_ string, opts dockerclient.InteractiveOptions) (string, error) {
		if strings.Contains(opts.Command, "rspamadm pw") {
			// Mit Zeilenumbrüchen und Steuerzeichen, wie sie über den
			// gemultiplexten Strom hereinkommen können.
			return "Passwort: \r\n" + testHash + " \r\n", nil
		}
		return `enable_password = "` + testHash + `";`, nil
	}
	req := Request{"raw": "x"}

	got := RspamdWorkerPassword(context.Background(), newEnv(fake), req, byID())

	assertBody(t, got, ContentTypeJSON, successBody)

	write := fake.InteractiveCalls[1]
	if !strings.Contains(write.Command, `"`+testHash+`"`) {
		t.Errorf("Schreibkommando enthaelt den Hash nicht sauber: %q", write.Command)
	}
}
