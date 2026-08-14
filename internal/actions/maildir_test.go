package actions

import (
	"context"
	"strings"
	"testing"
)

// Python's \W is Unicode-aware; Go's \W covers ASCII only. Without the character
// class spelled out, mailboxes with non-ASCII letters would end up in differently
// named directories.
func TestSanitizeNameKeepsUnicodeLetters(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"a slash is dropped", "example.org/user", "exampleorguser"},
		{"a dot is dropped", "a.b.c", "abc"},
		{"an underscore survives", "a_b", "a_b"},
		{"digits survive", "user123", "user123"},
		{"a non-ASCII letter survives", "example.org/müller", "exampleorgmüller"},
		{"Cyrillic survives", "пример", "пример"},
		{"punctuation is dropped", "a'; rm -rf /", "armrf"},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeName(tt.in); got != tt.want {
				t.Errorf("sanitizeName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestIndexName(t *testing.T) {
	tests := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"example.org/user", "user@example.org", true},
		{"example.org/user/Subfolder", "user@example.org", true},
		{"noslash", "", false},
		{"", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, ok := indexName(tt.in)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if got != tt.want {
				t.Errorf("indexName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestMaildirCleanupMovesMailboxAndIndex(t *testing.T) {
	fake := newFake()
	req := Request{"maildir": "example.org/user"}

	got := MaildirCleanup(context.Background(), newEnv(fake), req, byID())

	assertBody(t, got, ContentTypeJSON, successBody)

	garbage := "/var/vmail/_garbage/1700000000_exampleorguser"
	wantScript := "if [[ -d '/var/vmail/example.org/user' ]]; then" +
		" /bin/mv '/var/vmail/example.org/user' '" + garbage + "'; fi" +
		" && if [[ -d '/var/vmail_index/user@example.org' ]]; then" +
		" /bin/mv '/var/vmail_index/user@example.org' '" + garbage + "_index'; fi"

	assertExec(t, fake, 0, bashCommand(wantScript), "vmail")
}

// Without a slash there is no index directory.
func TestMaildirCleanupWithoutIndex(t *testing.T) {
	fake := newFake()
	req := Request{"maildir": "singlevalue"}

	MaildirCleanup(context.Background(), newEnv(fake), req, byID())

	wantScript := "if [[ -d '/var/vmail/singlevalue' ]]; then" +
		" /bin/mv '/var/vmail/singlevalue' '/var/vmail/_garbage/1700000000_singlevalue'; fi"

	assertExec(t, fake, 0, bashCommand(wantScript), "vmail")
}

func TestMaildirCleanupQuotesInput(t *testing.T) {
	fake := newFake()
	req := Request{"maildir": "example.org/o'brien"}

	MaildirCleanup(context.Background(), newEnv(fake), req, byID())

	call, _ := fake.LastExec()
	// The path is quoted, and the destination name additionally stripped of
	// punctuation.
	if !containsAll(call.Cmd[2],
		`'/var/vmail/example.org/o'\''brien'`,
		`/var/vmail/_garbage/1700000000_exampleorgobrien`,
	) {
		t.Errorf("script =\n%s", call.Cmd[2])
	}
}

func TestMaildirCleanupRequiresMaildir(t *testing.T) {
	fake := newFake()

	got := MaildirCleanup(context.Background(), newEnv(fake), Request{}, byID())

	want := `{
    "type": "danger",
    "msg": "maildir is missing"
}`
	assertBody(t, got, ContentTypeJSON, want)
}

func TestMaildirMove(t *testing.T) {
	fake := newFake()
	req := Request{
		"old_maildir": "example.org/old",
		"new_maildir": "example.org/new",
	}

	got := MaildirMove(context.Background(), newEnv(fake), req, byID())

	assertBody(t, got, ContentTypeJSON, successBody)

	// The _index suffix on the destination comes from DockerApi.py:363.
	wantScript := "if [[ -d '/var/vmail/example.org/old' ]]; then" +
		" /bin/mv '/var/vmail/example.org/old' '/var/vmail/example.org/new'; fi" +
		" && if [[ -d '/var/vmail_index/old@example.org' ]]; then" +
		" /bin/mv '/var/vmail_index/old@example.org' '/var/vmail_index/new@example.org_index'; fi"

	assertExec(t, fake, 0, bashCommand(wantScript), "vmail")
}

func TestMaildirMoveWithoutIndex(t *testing.T) {
	fake := newFake()
	req := Request{"old_maildir": "old", "new_maildir": "new"}

	MaildirMove(context.Background(), newEnv(fake), req, byID())

	wantScript := "if [[ -d '/var/vmail/old' ]]; then /bin/mv '/var/vmail/old' '/var/vmail/new'; fi"

	assertExec(t, fake, 0, bashCommand(wantScript), "vmail")
}

func TestMaildirMoveRequiresBothPaths(t *testing.T) {
	tests := []struct {
		name string
		req  Request
		want string
	}{
		{"without the old path", Request{"new_maildir": "new"}, "old_maildir is missing"},
		{"without the new path", Request{"old_maildir": "old"}, "new_maildir is missing"},
		{"without either", Request{}, "old_maildir is missing"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFake()

			got := MaildirMove(context.Background(), newEnv(fake), tt.req, byID())

			want := "{\n    \"type\": \"danger\",\n    \"msg\": \"" + tt.want + "\"\n}"
			assertBody(t, got, ContentTypeJSON, want)
		})
	}
}

func containsAll(haystack string, needles ...string) bool {
	for _, n := range needles {
		if !strings.Contains(haystack, n) {
			return false
		}
	}
	return true
}
