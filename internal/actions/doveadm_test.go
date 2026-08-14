package actions

import (
	"context"
	"testing"

	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
)

// execScript liefert eine Exec-Antwort abhängig vom aufgerufenen Kommando.
func execScript(responses map[string]string) func(string, dockerclient.ExecOptions) (dockerclient.ExecResult, error) {
	return func(_ string, opts dockerclient.ExecOptions) (dockerclient.ExecResult, error) {
		key := ""
		for _, arg := range opts.Cmd {
			if key != "" {
				key += " "
			}
			key += arg
		}

		return dockerclient.ExecResult{Output: []byte(responses[key])}, nil
	}
}

func TestDoveadmGetACLForOwnMailbox(t *testing.T) {
	fake := newFake()
	fake.ExecFn = execScript(map[string]string{
		"doveadm mailbox list -u user@beispiel.de": "INBOX\nEntwuerfe\n",
		"doveadm acl get -u user@beispiel.de INBOX": "ID           Global  Rights\n" +
			"user=kollege@beispiel.de     lookup read write\n",
		"doveadm acl get -u user@beispiel.de Entwuerfe": "ID  Global  Rights\n",
	})
	req := Request{"id": "user@beispiel.de"}

	got := DoveadmGetACL(context.Background(), newEnv(fake), req, byID())

	want := `[
    {
        "user": "user@beispiel.de",
        "id": "kollege@beispiel.de",
        "mailbox": "INBOX",
        "rights": [
            "lookup",
            "read",
            "write"
        ]
    }
]`
	assertBody(t, got, ContentTypeJSON, want)
}

// Ein leeres Ergebnis muss als [] kodiert werden, nicht als null.
func TestDoveadmGetACLEmptyIsArrayNotNull(t *testing.T) {
	fake := newFake()
	fake.ExecFn = execScript(map[string]string{
		"doveadm mailbox list -u user@beispiel.de": "",
	})
	req := Request{"id": "user@beispiel.de"}

	got := DoveadmGetACL(context.Background(), newEnv(fake), req, byID())

	assertBody(t, got, ContentTypeJSON, "[]")
}

// Bei einem freigegebenen Ordner zählt nur der Eintrag, der auf den
// angefragten Benutzer verweist.
func TestDoveadmGetACLForSharedFolder(t *testing.T) {
	fake := newFake()
	fake.ExecFn = execScript(map[string]string{
		"doveadm mailbox list -u user@beispiel.de": "Shared/eigner@beispiel.de/Projekt\n",
		"doveadm acl get -u eigner@beispiel.de Projekt": "ID  Global  Rights\n" +
			"user=fremd@beispiel.de   lookup\n" +
			"user=user@beispiel.de    lookup read insert\n",
	})
	req := Request{"id": "user@beispiel.de"}

	got := DoveadmGetACL(context.Background(), newEnv(fake), req, byID())

	want := `[
    {
        "user": "eigner@beispiel.de",
        "id": "user@beispiel.de",
        "mailbox": "Projekt",
        "rights": [
            "lookup",
            "read",
            "insert"
        ]
    }
]`
	assertBody(t, got, ContentTypeJSON, want)
}

// Ein zu kurzer Shared-Pfad wird übergangen.
func TestDoveadmGetACLSkipsShortSharedPath(t *testing.T) {
	fake := newFake()
	fake.ExecFn = execScript(map[string]string{
		"doveadm mailbox list -u user@beispiel.de": "Shared/eigner\n",
	})
	req := Request{"id": "user@beispiel.de"}

	got := DoveadmGetACL(context.Background(), newEnv(fake), req, byID())

	assertBody(t, got, ContentTypeJSON, "[]")
}

// Zeilen ohne Rechte oder ohne Gleichheitszeichen lösten in Python einen
// ValueError beziehungsweise IndexError aus.
func TestDoveadmGetACLSkipsMalformedLines(t *testing.T) {
	fake := newFake()
	fake.ExecFn = execScript(map[string]string{
		"doveadm mailbox list -u user@beispiel.de": "INBOX\n",
		"doveadm acl get -u user@beispiel.de INBOX": "ID  Global  Rights\n" +
			"nurEinFeld\n" +
			"ohneGleichheitszeichen  lookup\n" +
			"user=gut@beispiel.de  read\n",
	})
	req := Request{"id": "user@beispiel.de"}

	got := DoveadmGetACL(context.Background(), newEnv(fake), req, byID())

	want := `[
    {
        "user": "user@beispiel.de",
        "id": "gut@beispiel.de",
        "mailbox": "INBOX",
        "rights": [
            "read"
        ]
    }
]`
	assertBody(t, got, ContentTypeJSON, want)
}

func TestDoveadmGetACLRequiresID(t *testing.T) {
	fake := newFake()

	got := DoveadmGetACL(context.Background(), newEnv(fake), Request{}, byID())

	want := `{
    "type": "danger",
    "msg": "id is missing"
}`
	assertBody(t, got, ContentTypeJSON, want)
}

func TestDoveadmDeleteACL(t *testing.T) {
	fake := newFake()
	req := Request{
		"user":    "eigner@beispiel.de",
		"mailbox": "Projekt",
		"id":      "gast@beispiel.de",
	}

	got := DoveadmDeleteACL(context.Background(), newEnv(fake), req, byID())

	assertBody(t, got, ContentTypeJSON, successBody)
	assertExec(t, fake, 0, []string{
		"doveadm", "acl", "delete", "-u", "eigner@beispiel.de", "Projekt",
		"user=gast@beispiel.de",
	}, "")
}

func TestDoveadmDeleteACLRejectsEmptyFields(t *testing.T) {
	tests := []struct {
		name string
		req  Request
		want string
	}{
		{"ohne user", Request{"mailbox": "P", "id": "x"}, "user is missing"},
		{"leerer user", Request{"user": "", "mailbox": "P", "id": "x"}, "user is missing"},
		{"ohne mailbox", Request{"user": "u", "id": "x"}, "mailbox is missing"},
		{"ohne id", Request{"user": "u", "mailbox": "P"}, "id is missing"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFake()

			got := DoveadmDeleteACL(context.Background(), newEnv(fake), tt.req, byID())

			want := "{\n    \"type\": \"danger\",\n    \"msg\": \"" + tt.want + "\"\n}"
			assertBody(t, got, ContentTypeJSON, want)

			if len(fake.ExecCalls) != 0 {
				t.Errorf("es wurde ein Kommando abgesetzt: %v", fake.ExecCalls)
			}
		})
	}
}

func TestDoveadmSetACL(t *testing.T) {
	fake := newFake()
	req := Request{
		"user":    "eigner@beispiel.de",
		"mailbox": "Projekt",
		"id":      "gast@beispiel.de",
		"rights":  []any{"lookup", "read", "write-seen"},
	}

	got := DoveadmSetACL(context.Background(), newEnv(fake), req, byID())

	assertBody(t, got, ContentTypeJSON, successBody)
	assertExec(t, fake, 0, []string{
		"doveadm", "acl", "set", "-u", "eigner@beispiel.de", "Projekt",
		"user=gast@beispiel.de", "lookup", "read", "write-seen",
	}, "")
}

// Nur Rechte aus der Positivliste dürfen durchkommen.
func TestDoveadmSetACLFiltersRights(t *testing.T) {
	fake := newFake()
	req := Request{
		"user":    "u",
		"mailbox": "P",
		"id":      "g",
		"rights":  []any{"LOOKUP", "erfunden", "; rm -rf /", "read"},
	}

	DoveadmSetACL(context.Background(), newEnv(fake), req, byID())

	call, _ := fake.LastExec()
	want := []string{"doveadm", "acl", "set", "-u", "u", "P", "user=g", "lookup", "read"}

	if len(call.Cmd) != len(want) {
		t.Fatalf("Cmd = %v, want %v", call.Cmd, want)
	}
	for i := range want {
		if call.Cmd[i] != want[i] {
			t.Errorf("Cmd[%d] = %q, want %q", i, call.Cmd[i], want[i])
		}
	}
}

func TestDoveadmSetACLWithoutValidRights(t *testing.T) {
	fake := newFake()
	req := Request{"user": "u", "mailbox": "P", "id": "g", "rights": []any{"erfunden"}}

	got := DoveadmSetACL(context.Background(), newEnv(fake), req, byID())

	want := `{
    "type": "danger",
    "msg": "no valid rights given"
}`
	assertBody(t, got, ContentTypeJSON, want)

	if len(fake.ExecCalls) != 0 {
		t.Errorf("es wurde ein Kommando abgesetzt: %v", fake.ExecCalls)
	}
}

func TestSplitFirstField(t *testing.T) {
	tests := []struct {
		in        string
		wantFirst string
		wantRest  string
		wantOK    bool
	}{
		{"user=a lookup read", "user=a", "lookup read", true},
		{"  user=a   lookup read", "user=a", "lookup read", true},
		{"user=a\tlookup", "user=a", "lookup", true},
		{"nurEinFeld", "", "", false},
		{"", "", "", false},
		{"   ", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			first, rest, ok := splitFirstField(tt.in)

			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if first != tt.wantFirst || rest != tt.wantRest {
				t.Errorf("= (%q, %q), want (%q, %q)", first, rest, tt.wantFirst, tt.wantRest)
			}
		})
	}
}
