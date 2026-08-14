package actions

import (
	"context"
	"testing"

	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
	"bodsch.me/mailcow-dockerapi/internal/dockerclient/dockertest"
)

func TestFilterQIDs(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"nur gueltige", []string{"A1B2", "ff00"}, []string{"A1B2", "ff00"}},
		{"ungueltige entfallen", []string{"A1B2", "; rm -rf /", "zz"}, []string{"A1B2"}},
		{"leer bleibt leer", []string{}, []string{}},
		{"leerer eintrag entfaellt", []string{""}, []string{}},
		{"leerzeichen entfaellt", []string{"AB CD"}, []string{}},
		// Pythons $ hätte ein angehängtes \n durchgelassen.
		{"zeilenumbruch entfaellt", []string{"ABCD\n"}, []string{}},
		{"pfadangabe entfaellt", []string{"../../etc/passwd"}, []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterQIDs(tt.in)

			if len(got) != len(tt.want) {
				t.Fatalf("filterQIDs(%v) = %v, want %v", tt.in, got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// Die postsuper-Actions setzen je Queue-ID ein eigenes Schalterpaar ab.
func TestPostsuperActionsBuildArgv(t *testing.T) {
	tests := []struct {
		name    string
		action  Func
		wantCmd []string
	}{
		{"delete", MailqDelete, []string{"/usr/sbin/postsuper", "-d", "A1B2", "-d", "C3D4"}},
		{"hold", MailqHold, []string{"/usr/sbin/postsuper", "-h", "A1B2", "-h", "C3D4"}},
		{"unhold", MailqUnhold, []string{"/usr/sbin/postsuper", "-H", "A1B2", "-H", "C3D4"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFake()
			req := Request{"items": []any{"A1B2", "C3D4"}}

			got := tt.action(context.Background(), newEnv(fake), req, byID())

			assertBody(t, got, ContentTypeJSON, successBody)
			assertExec(t, fake, 0, tt.wantCmd, "")
		})
	}
}

// In Python war das Ergebnis von filter() ein Generator und damit immer wahr;
// bei ausschließlich ungültigen Angaben lief postsuper ohne Argumente.
func TestPostsuperRejectsInvalidQIDsInsteadOfRunningBare(t *testing.T) {
	fake := newFake()
	req := Request{"items": []any{"; rm -rf /", "nicht-hex"}}

	got := MailqDelete(context.Background(), newEnv(fake), req, byID())

	want := `{
    "type": "danger",
    "msg": "no valid queue ids given"
}`
	assertBody(t, got, ContentTypeJSON, want)

	if len(fake.ExecCalls) != 0 {
		t.Errorf("es wurde ein Kommando abgesetzt: %v", fake.ExecCalls)
	}
}

func TestPostsuperRequiresItems(t *testing.T) {
	fake := newFake()

	got := MailqDelete(context.Background(), newEnv(fake), Request{}, byID())

	want := `{
    "type": "danger",
    "msg": "items is missing"
}`
	assertBody(t, got, ContentTypeJSON, want)
}

func TestMailqCat(t *testing.T) {
	fake := newFake()
	fake.ExecResults = []dockerclient.ExecResult{
		{ExitCode: 0, Output: []byte("*** ENVELOPE RECORDS ***")},
	}
	req := Request{"items": []any{"A1B2", "C3D4"}}

	got := MailqCat(context.Background(), newEnv(fake), req, byID())

	assertBody(t, got, ContentTypeText, "*** ENVELOPE RECORDS ***")
	assertExec(t, fake, 0, []string{"/usr/sbin/postcat", "-q", "A1B2", "C3D4"}, "postfix")
}

// Für jede Queue-ID wird ein eigener postqueue-Aufruf abgesetzt.
func TestMailqDeliverRunsOncePerQID(t *testing.T) {
	fake := newFake()
	req := Request{"items": []any{"A1B2", "C3D4"}}

	got := MailqDeliver(context.Background(), newEnv(fake), req, byID())

	want := `{
    "type": "success",
    "msg": "Scheduled immediate delivery"
}`
	assertBody(t, got, ContentTypeJSON, want)

	if len(fake.ExecCalls) != 2 {
		t.Fatalf("Exec-Aufrufe = %d, want 2", len(fake.ExecCalls))
	}
	assertExec(t, fake, 0, []string{"/usr/sbin/postqueue", "-i", "A1B2"}, "postfix")
	assertExec(t, fake, 1, []string{"/usr/sbin/postqueue", "-i", "C3D4"}, "postfix")
}

func TestMailqList(t *testing.T) {
	fake := newFake()
	fake.ExecResults = []dockerclient.ExecResult{
		{ExitCode: 0, Output: []byte(`{"queue_name":"deferred"}`)},
	}

	got := MailqList(context.Background(), newEnv(fake), Request{}, byID())

	assertBody(t, got, ContentTypeText, `{"queue_name":"deferred"}`)
	assertExec(t, fake, 0, []string{"/usr/sbin/postqueue", "-j"}, "postfix")
}

func TestMailqFlush(t *testing.T) {
	fake := newFake()

	got := MailqFlush(context.Background(), newEnv(fake), Request{}, byID())

	assertBody(t, got, ContentTypeJSON, successBody)
	assertExec(t, fake, 0, []string{"/usr/sbin/postqueue", "-f"}, "postfix")
}

func TestMailqSuperDelete(t *testing.T) {
	fake := newFake()

	got := MailqSuperDelete(context.Background(), newEnv(fake), Request{}, byID())

	assertBody(t, got, ContentTypeJSON, successBody)
	assertExec(t, fake, 0, []string{"/usr/sbin/postsuper", "-d", "ALL"}, "")
}

// Die exec-Actions sprechen ausschließlich laufende Container an.
func TestMailqActionsListOnlyRunningContainers(t *testing.T) {
	actions := map[string]Func{
		"delete":       MailqDelete,
		"hold":         MailqHold,
		"unhold":       MailqUnhold,
		"cat":          MailqCat,
		"deliver":      MailqDeliver,
		"list":         MailqList,
		"flush":        MailqFlush,
		"super_delete": MailqSuperDelete,
	}

	for name, action := range actions {
		t.Run(name, func(t *testing.T) {
			fake := newFake()
			req := Request{"items": []any{"A1B2"}}

			action(context.Background(), newEnv(fake), req, byID())

			if len(fake.ListCalls) == 0 {
				t.Fatal("keine Container-Auswahl erfolgt")
			}
			if fake.ListCalls[0].All {
				t.Error("all = true, want false")
			}
		})
	}
}

func TestMailqFlushReportsFailure(t *testing.T) {
	fake := newFake()
	fake.ExecResults = []dockerclient.ExecResult{
		{ExitCode: 1, Output: []byte("postqueue: fatal")},
	}

	got := MailqFlush(context.Background(), newEnv(fake), Request{}, byID())

	want := `{
    "type": "danger",
    "msg": "command failed: postqueue: fatal"
}`
	assertBody(t, got, ContentTypeJSON, want)
}

// Ohne laufenden Container lieferte das Original einen Rumpf mit "null".
func TestMailqCatWithoutMatch(t *testing.T) {
	fake := &dockertest.Fake{}
	req := Request{"items": []any{"A1B2"}}

	got := MailqCat(context.Background(), newEnv(fake), req, byID())

	want := `{
    "type": "danger",
    "msg": "no container found"
}`
	assertBody(t, got, ContentTypeJSON, want)
}
