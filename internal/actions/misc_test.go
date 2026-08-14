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
		{ExitCode: 0, Output: []byte("vacation  ACTIVE\n")},
	}
	req := Request{"username": "user@example.org"}

	got := SieveList(context.Background(), newEnv(fake), req, byID())

	assertBody(t, got, ContentTypeText, "vacation  ACTIVE\n")
	assertExec(t, fake, 0,
		[]string{"/usr/bin/doveadm", "sieve", "list", "-u", "user@example.org"}, "")
}

func TestSievePrint(t *testing.T) {
	fake := newFake()
	fake.ExecResults = []dockerclient.ExecResult{
		{ExitCode: 0, Output: []byte(`require "vacation";`)},
	}
	req := Request{"username": "user@example.org", "script_name": "vacation"}

	got := SievePrint(context.Background(), newEnv(fake), req, byID())

	assertBody(t, got, ContentTypeText, `require "vacation";`)
	assertExec(t, fake, 0,
		[]string{"/usr/bin/doveadm", "sieve", "get", "-u", "user@example.org", "vacation"}, "")
}

func TestSieveRequiresFields(t *testing.T) {
	tests := []struct {
		name   string
		action Func
		req    Request
		want   string
	}{
		{"list without a username", SieveList, Request{}, "username is missing"},
		{"print without a username", SievePrint, Request{"script_name": "x"}, "username is missing"},
		{"print without a script_name", SievePrint, Request{"username": "u"}, "script_name is missing"},
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

// A script name containing quotes stays a single argument.
func TestSievePrintPassesNameVerbatim(t *testing.T) {
	fake := newFake()
	req := Request{"username": "u@example.org", "script_name": "'; rm -rf /; '"}

	SievePrint(context.Background(), newEnv(fake), req, byID())

	call, _ := fake.LastExec()
	if call.Cmd[5] != "'; rm -rf /; '" {
		t.Errorf("argument = %q, want it unchanged", call.Cmd[5])
	}
	if len(call.Cmd) != 6 {
		t.Errorf("Cmd = %v, expected exactly 6 arguments", call.Cmd)
	}
}

func TestSogoRenameUser(t *testing.T) {
	fake := newFake()
	req := Request{
		"old_username": "old@example.org",
		"new_username": "new@example.org",
	}

	got := SogoRenameUser(context.Background(), newEnv(fake), req, byID())

	assertBody(t, got, ContentTypeJSON, successBody)
	assertExec(t, fake, 0,
		[]string{"sogo-tool", "rename-user", "old@example.org", "new@example.org"}, "sogo")
}

func TestSogoRenameUserRequiresBothNames(t *testing.T) {
	tests := []struct {
		name string
		req  Request
		want string
	}{
		{"without the old name", Request{"new_username": "n"}, "old_username is missing"},
		{"without the new name", Request{"old_username": "a"}, "new_username is missing"},
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
