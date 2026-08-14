package actions

import (
	"context"
	"testing"

	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
)

// execScript returns an exec response depending on the command that was called.
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
		"doveadm mailbox list -u user@example.org": "INBOX\nDrafts\n",
		"doveadm acl get -u user@example.org INBOX": "ID           Global  Rights\n" +
			"user=colleague@example.org     lookup read write\n",
		"doveadm acl get -u user@example.org Drafts": "ID  Global  Rights\n",
	})
	req := Request{"id": "user@example.org"}

	got := DoveadmGetACL(context.Background(), newEnv(fake), req, byID())

	want := `[
    {
        "user": "user@example.org",
        "id": "colleague@example.org",
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

// An empty result has to encode as [], not as null.
func TestDoveadmGetACLEmptyIsArrayNotNull(t *testing.T) {
	fake := newFake()
	fake.ExecFn = execScript(map[string]string{
		"doveadm mailbox list -u user@example.org": "",
	})
	req := Request{"id": "user@example.org"}

	got := DoveadmGetACL(context.Background(), newEnv(fake), req, byID())

	assertBody(t, got, ContentTypeJSON, "[]")
}

// For a shared folder only the entry pointing at the requested user counts.
func TestDoveadmGetACLForSharedFolder(t *testing.T) {
	fake := newFake()
	fake.ExecFn = execScript(map[string]string{
		"doveadm mailbox list -u user@example.org": "Shared/owner@example.org/Project\n",
		"doveadm acl get -u owner@example.org Project": "ID  Global  Rights\n" +
			"user=stranger@example.org   lookup\n" +
			"user=user@example.org    lookup read insert\n",
	})
	req := Request{"id": "user@example.org"}

	got := DoveadmGetACL(context.Background(), newEnv(fake), req, byID())

	want := `[
    {
        "user": "owner@example.org",
        "id": "user@example.org",
        "mailbox": "Project",
        "rights": [
            "lookup",
            "read",
            "insert"
        ]
    }
]`
	assertBody(t, got, ContentTypeJSON, want)
}

// A shared path that is too short is skipped.
func TestDoveadmGetACLSkipsShortSharedPath(t *testing.T) {
	fake := newFake()
	fake.ExecFn = execScript(map[string]string{
		"doveadm mailbox list -u user@example.org": "Shared/eigner\n",
	})
	req := Request{"id": "user@example.org"}

	got := DoveadmGetACL(context.Background(), newEnv(fake), req, byID())

	assertBody(t, got, ContentTypeJSON, "[]")
}

// Lines without rights or without an equals sign raised a ValueError or an
// IndexError in Python.
func TestDoveadmGetACLSkipsMalformedLines(t *testing.T) {
	fake := newFake()
	fake.ExecFn = execScript(map[string]string{
		"doveadm mailbox list -u user@example.org": "INBOX\n",
		"doveadm acl get -u user@example.org INBOX": "ID  Global  Rights\n" +
			"onlyOneField\n" +
			"withoutAnEqualsSign  lookup\n" +
			"user=good@example.org  read\n",
	})
	req := Request{"id": "user@example.org"}

	got := DoveadmGetACL(context.Background(), newEnv(fake), req, byID())

	want := `[
    {
        "user": "user@example.org",
        "id": "good@example.org",
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
		"user":    "owner@example.org",
		"mailbox": "Project",
		"id":      "guest@example.org",
	}

	got := DoveadmDeleteACL(context.Background(), newEnv(fake), req, byID())

	assertBody(t, got, ContentTypeJSON, successBody)
	assertExec(t, fake, 0, []string{
		"doveadm", "acl", "delete", "-u", "owner@example.org", "Project",
		"user=guest@example.org",
	}, "")
}

func TestDoveadmDeleteACLRejectsEmptyFields(t *testing.T) {
	tests := []struct {
		name string
		req  Request
		want string
	}{
		{"without user", Request{"mailbox": "P", "id": "x"}, "user is missing"},
		{"empty user", Request{"user": "", "mailbox": "P", "id": "x"}, "user is missing"},
		{"without mailbox", Request{"user": "u", "id": "x"}, "mailbox is missing"},
		{"without id", Request{"user": "u", "mailbox": "P"}, "id is missing"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFake()

			got := DoveadmDeleteACL(context.Background(), newEnv(fake), tt.req, byID())

			want := "{\n    \"type\": \"danger\",\n    \"msg\": \"" + tt.want + "\"\n}"
			assertBody(t, got, ContentTypeJSON, want)

			if len(fake.ExecCalls) != 0 {
				t.Errorf("a command was issued anyway: %v", fake.ExecCalls)
			}
		})
	}
}

func TestDoveadmSetACL(t *testing.T) {
	fake := newFake()
	req := Request{
		"user":    "owner@example.org",
		"mailbox": "Project",
		"id":      "guest@example.org",
		"rights":  []any{"lookup", "read", "write-seen"},
	}

	got := DoveadmSetACL(context.Background(), newEnv(fake), req, byID())

	assertBody(t, got, ContentTypeJSON, successBody)
	assertExec(t, fake, 0, []string{
		"doveadm", "acl", "set", "-u", "owner@example.org", "Project",
		"user=guest@example.org", "lookup", "read", "write-seen",
	}, "")
}

// Only rights from the allow-list may get through.
func TestDoveadmSetACLFiltersRights(t *testing.T) {
	fake := newFake()
	req := Request{
		"user":    "u",
		"mailbox": "P",
		"id":      "g",
		"rights":  []any{"LOOKUP", "invented", "; rm -rf /", "read"},
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
	req := Request{"user": "u", "mailbox": "P", "id": "g", "rights": []any{"invented"}}

	got := DoveadmSetACL(context.Background(), newEnv(fake), req, byID())

	want := `{
    "type": "danger",
    "msg": "no valid rights given"
}`
	assertBody(t, got, ContentTypeJSON, want)

	if len(fake.ExecCalls) != 0 {
		t.Errorf("a command was issued anyway: %v", fake.ExecCalls)
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
		{"onlyOneField", "", "", false},
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
