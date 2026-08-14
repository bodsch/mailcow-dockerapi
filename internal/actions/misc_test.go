package actions

import (
	"context"
	"testing"

	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
)

func TestReloadActions(t *testing.T) {
	tests := []struct {
		name    string
		action  Func
		wantCmd []string
	}{
		{"dovecot", ReloadDovecot, []string{"/usr/sbin/dovecot", "reload"}},
		{"postfix", ReloadPostfix, []string{"/usr/sbin/postfix", "reload"}},
		{"nginx", ReloadNginx, []string{"/usr/sbin/nginx", "-s", "reload"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFake()

			got := tt.action(context.Background(), newEnv(fake), Request{}, byID())

			assertBody(t, got, ContentTypeJSON, successBody)
			assertExec(t, fake, 0, tt.wantCmd, "")
		})
	}
}

func TestReloadReportsFailure(t *testing.T) {
	fake := newFake()
	fake.ExecResults = []dockerclient.ExecResult{
		{ExitCode: 1, Output: []byte("nginx: configuration file test failed")},
	}

	got := ReloadNginx(context.Background(), newEnv(fake), Request{}, byID())

	want := `{
    "type": "danger",
    "msg": "command failed: nginx: configuration file test failed"
}`
	assertBody(t, got, ContentTypeJSON, want)
}

func TestSieveList(t *testing.T) {
	fake := newFake()
	fake.ExecResults = []dockerclient.ExecResult{
		{ExitCode: 0, Output: []byte("urlaub  ACTIVE\n")},
	}
	req := Request{"username": "user@beispiel.de"}

	got := SieveList(context.Background(), newEnv(fake), req, byID())

	assertBody(t, got, ContentTypeText, "urlaub  ACTIVE\n")
	assertExec(t, fake, 0,
		[]string{"/usr/bin/doveadm", "sieve", "list", "-u", "user@beispiel.de"}, "")
}

func TestSievePrint(t *testing.T) {
	fake := newFake()
	fake.ExecResults = []dockerclient.ExecResult{
		{ExitCode: 0, Output: []byte(`require "vacation";`)},
	}
	req := Request{"username": "user@beispiel.de", "script_name": "urlaub"}

	got := SievePrint(context.Background(), newEnv(fake), req, byID())

	assertBody(t, got, ContentTypeText, `require "vacation";`)
	assertExec(t, fake, 0,
		[]string{"/usr/bin/doveadm", "sieve", "get", "-u", "user@beispiel.de", "urlaub"}, "")
}

func TestSieveRequiresFields(t *testing.T) {
	tests := []struct {
		name   string
		action Func
		req    Request
		want   string
	}{
		{"list ohne username", SieveList, Request{}, "username is missing"},
		{"print ohne username", SievePrint, Request{"script_name": "x"}, "username is missing"},
		{"print ohne script_name", SievePrint, Request{"username": "u"}, "script_name is missing"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFake()

			got := tt.action(context.Background(), newEnv(fake), tt.req, byID())

			want := "{\n    \"type\": \"danger\",\n    \"msg\": \"" + tt.want + "\"\n}"
			assertBody(t, got, ContentTypeJSON, want)
		})
	}
}

// Ein Skriptname mit Anführungszeichen bleibt ein einzelnes Argument.
func TestSievePrintPassesNameVerbatim(t *testing.T) {
	fake := newFake()
	req := Request{"username": "u@b.de", "script_name": "'; rm -rf /; '"}

	SievePrint(context.Background(), newEnv(fake), req, byID())

	call, _ := fake.LastExec()
	if call.Cmd[5] != "'; rm -rf /; '" {
		t.Errorf("Argument = %q, want unveraendert", call.Cmd[5])
	}
	if len(call.Cmd) != 6 {
		t.Errorf("Cmd = %v, erwarte genau 6 Argumente", call.Cmd)
	}
}

func TestSogoRenameUser(t *testing.T) {
	fake := newFake()
	req := Request{
		"old_username": "alt@beispiel.de",
		"new_username": "neu@beispiel.de",
	}

	got := SogoRenameUser(context.Background(), newEnv(fake), req, byID())

	assertBody(t, got, ContentTypeJSON, successBody)
	assertExec(t, fake, 0,
		[]string{"sogo-tool", "rename-user", "alt@beispiel.de", "neu@beispiel.de"}, "sogo")
}

func TestSogoRenameUserRequiresBothNames(t *testing.T) {
	tests := []struct {
		name string
		req  Request
		want string
	}{
		{"ohne alt", Request{"new_username": "n"}, "old_username is missing"},
		{"ohne neu", Request{"old_username": "a"}, "new_username is missing"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFake()

			got := SogoRenameUser(context.Background(), newEnv(fake), tt.req, byID())

			want := "{\n    \"type\": \"danger\",\n    \"msg\": \"" + tt.want + "\"\n}"
			assertBody(t, got, ContentTypeJSON, want)
		})
	}
}
