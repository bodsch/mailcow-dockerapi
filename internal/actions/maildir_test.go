package actions

import (
	"context"
	"strings"
	"testing"
)

// Pythons \W ist Unicode-bewusst; Gos \W umfasst nur ASCII. Ohne die
// ausgeschriebene Zeichenklasse entstünden für Postfächer mit Umlauten
// abweichende Verzeichnisnamen.
func TestSanitizeNameKeepsUnicodeLetters(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"schraegstrich entfaellt", "beispiel.de/user", "beispieldeuser"},
		{"punkt entfaellt", "a.b.c", "abc"},
		{"unterstrich bleibt", "a_b", "a_b"},
		{"ziffern bleiben", "user123", "user123"},
		{"umlaut bleibt", "beispiel.de/müller", "beispieldemüller"},
		{"kyrillisch bleibt", "пример", "пример"},
		{"sonderzeichen entfallen", "a'; rm -rf /", "armrf"},
		{"leer", "", ""},
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
		{"beispiel.de/user", "user@beispiel.de", true},
		{"beispiel.de/user/Unterordner", "user@beispiel.de", true},
		{"ohneschraegstrich", "", false},
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
	req := Request{"maildir": "beispiel.de/user"}

	got := MaildirCleanup(context.Background(), newEnv(fake), req, byID())

	assertBody(t, got, ContentTypeJSON, successBody)

	garbage := "/var/vmail/_garbage/1700000000_beispieldeuser"
	wantScript := "if [[ -d '/var/vmail/beispiel.de/user' ]]; then" +
		" /bin/mv '/var/vmail/beispiel.de/user' '" + garbage + "'; fi" +
		" && if [[ -d '/var/vmail_index/user@beispiel.de' ]]; then" +
		" /bin/mv '/var/vmail_index/user@beispiel.de' '" + garbage + "_index'; fi"

	assertExec(t, fake, 0, bashCommand(wantScript), "vmail")
}

// Ohne Schrägstrich gibt es kein Indexverzeichnis.
func TestMaildirCleanupWithoutIndex(t *testing.T) {
	fake := newFake()
	req := Request{"maildir": "einzelwert"}

	MaildirCleanup(context.Background(), newEnv(fake), req, byID())

	wantScript := "if [[ -d '/var/vmail/einzelwert' ]]; then" +
		" /bin/mv '/var/vmail/einzelwert' '/var/vmail/_garbage/1700000000_einzelwert'; fi"

	assertExec(t, fake, 0, bashCommand(wantScript), "vmail")
}

func TestMaildirCleanupQuotesInput(t *testing.T) {
	fake := newFake()
	req := Request{"maildir": "beispiel.de/o'brien"}

	MaildirCleanup(context.Background(), newEnv(fake), req, byID())

	call, _ := fake.LastExec()
	// Der Pfad ist maskiert, der Zielname zusätzlich von Sonderzeichen befreit.
	if !containsAll(call.Cmd[2],
		`'/var/vmail/beispiel.de/o'\''brien'`,
		`/var/vmail/_garbage/1700000000_beispieldeobrien`,
	) {
		t.Errorf("Skript =\n%s", call.Cmd[2])
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
		"old_maildir": "beispiel.de/alt",
		"new_maildir": "beispiel.de/neu",
	}

	got := MaildirMove(context.Background(), newEnv(fake), req, byID())

	assertBody(t, got, ContentTypeJSON, successBody)

	// Der Zusatz _index am Ziel stammt aus DockerApi.py:363.
	wantScript := "if [[ -d '/var/vmail/beispiel.de/alt' ]]; then" +
		" /bin/mv '/var/vmail/beispiel.de/alt' '/var/vmail/beispiel.de/neu'; fi" +
		" && if [[ -d '/var/vmail_index/alt@beispiel.de' ]]; then" +
		" /bin/mv '/var/vmail_index/alt@beispiel.de' '/var/vmail_index/neu@beispiel.de_index'; fi"

	assertExec(t, fake, 0, bashCommand(wantScript), "vmail")
}

func TestMaildirMoveWithoutIndex(t *testing.T) {
	fake := newFake()
	req := Request{"old_maildir": "alt", "new_maildir": "neu"}

	MaildirMove(context.Background(), newEnv(fake), req, byID())

	wantScript := "if [[ -d '/var/vmail/alt' ]]; then /bin/mv '/var/vmail/alt' '/var/vmail/neu'; fi"

	assertExec(t, fake, 0, bashCommand(wantScript), "vmail")
}

func TestMaildirMoveRequiresBothPaths(t *testing.T) {
	tests := []struct {
		name string
		req  Request
		want string
	}{
		{"ohne alt", Request{"new_maildir": "neu"}, "old_maildir is missing"},
		{"ohne neu", Request{"old_maildir": "alt"}, "new_maildir is missing"},
		{"ohne beide", Request{}, "old_maildir is missing"},
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
