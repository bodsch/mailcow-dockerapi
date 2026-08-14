package actions

import (
	"os/exec"
	"strings"
	"testing"
)

func TestShellQuote(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"harmlos", "postfix", "'postfix'"},
		{"leer", "", "''"},
		{"mit leerzeichen", "zwei woerter", "'zwei woerter'"},
		{"einfaches anfuehrungszeichen", "o'brien", `'o'\''brien'`},
		{"nur anfuehrungszeichen", "'", `''\'''`},
		{"backslash bleibt", `pfad\zu`, `'pfad\zu'`},
		{"dollar wird neutralisiert", "$(rm -rf /)", "'$(rm -rf /)'"},
		{"semikolon", "a; rm -rf /", "'a; rm -rf /'"},
		{"backtick", "`id`", "'`id`'"},
		{"newline", "a\nb", "'a\nb'"},
		{"umlaut", "müller@beispiel.de", "'müller@beispiel.de'"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shellQuote(tt.in); got != tt.want {
				t.Errorf("shellQuote(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// roundtrip lässt eine echte Shell das maskierte Wort auswerten und liefert
// zurück, was dort ankommt.
func roundtrip(t *testing.T, s string) (string, bool) {
	t.Helper()

	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("keine sh verfuegbar")
	}

	out, err := exec.Command(sh, "-c", "printf %s "+shellQuote(s)).Output()
	if err != nil {
		return "", false
	}

	return string(out), true
}

// Der wirksamste Test für die Maskierung ist die Shell selbst.
func TestShellQuoteRoundtripThroughRealShell(t *testing.T) {
	inputs := []string{
		"postfix",
		"o'brien",
		"'; touch /tmp/pwned; '",
		"$(id)",
		"`id`",
		"a b\tc",
		`\\`,
		"müller@beispiel.de",
		"*",
		"~root",
		"a\nb",
		"--flag=wert",
		"'''",
	}

	for _, in := range inputs {
		t.Run(in, func(t *testing.T) {
			got, ok := roundtrip(t, in)
			if !ok {
				t.Fatalf("Shell-Aufruf fehlgeschlagen fuer %q", in)
			}
			if got != in {
				t.Errorf("roundtrip(%q) = %q", in, got)
			}
		})
	}
}

func FuzzShellQuote(f *testing.F) {
	for _, seed := range []string{"postfix", "o'brien", "$(id)", "a\nb", "'"} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, s string) {
		// NUL-Bytes lassen sich nicht über argv übergeben, und ungültiges
		// UTF-8 überlebt den Weg durch die Shell nicht verlustfrei.
		if strings.ContainsRune(s, 0) || !isValidUTF8(s) {
			t.Skip()
		}

		quoted := shellQuote(s)

		// Das Ergebnis muss ein einzelnes, vollständig geschlossenes Wort sein.
		if !strings.HasPrefix(quoted, "'") || !strings.HasSuffix(quoted, "'") {
			t.Fatalf("shellQuote(%q) = %q ist nicht eingeschlossen", s, quoted)
		}

		got, ok := roundtrip(t, s)
		if !ok {
			t.Fatalf("Shell-Aufruf fehlgeschlagen fuer %q", s)
		}
		if got != s {
			t.Fatalf("roundtrip(%q) = %q", s, got)
		}
	})
}

func isValidUTF8(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}

func TestBashCommand(t *testing.T) {
	got := bashCommand("echo hallo")

	want := []string{"/bin/bash", "-c", "echo hallo"}
	if len(got) != len(want) {
		t.Fatalf("bashCommand = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("bashCommand[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
