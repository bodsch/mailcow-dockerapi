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
		{"harmless", "postfix", "'postfix'"},
		{"empty", "", "''"},
		{"with a space", "two words", "'two words'"},
		{"a single quote", "o'brien", `'o'\''brien'`},
		{"nothing but a quote", "'", `''\'''`},
		{"a backslash survives", `path\to`, `'path\to'`},
		{"a dollar is neutralised", "$(rm -rf /)", "'$(rm -rf /)'"},
		{"a semicolon", "a; rm -rf /", "'a; rm -rf /'"},
		{"a backtick", "`id`", "'`id`'"},
		{"a newline", "a\nb", "'a\nb'"},
		{"a non-ASCII letter", "müller@example.org", "'müller@example.org'"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shellQuote(tt.in); got != tt.want {
				t.Errorf("shellQuote(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// roundtrip has a real shell evaluate the quoted word and returns what arrives on
// the other side.
func roundtrip(t *testing.T, s string) (string, bool) {
	t.Helper()

	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh available")
	}

	out, err := exec.Command(sh, "-c", "printf %s "+shellQuote(s)).Output()
	if err != nil {
		return "", false
	}

	return string(out), true
}

// The most effective test for the quoting is the shell itself.
func TestShellQuoteRoundtripThroughRealShell(t *testing.T) {
	inputs := []string{
		"postfix",
		"o'brien",
		"'; touch /tmp/pwned; '",
		"$(id)",
		"`id`",
		"a b\tc",
		`\\`,
		"müller@example.org",
		"*",
		"~root",
		"a\nb",
		"--flag=value",
		"'''",
	}

	for _, in := range inputs {
		t.Run(in, func(t *testing.T) {
			got, ok := roundtrip(t, in)
			if !ok {
				t.Fatalf("the shell call failed for %q", in)
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
		// NUL bytes cannot be passed through argv, and invalid UTF-8 does not
		// survive the trip through the shell intact.
		if strings.ContainsRune(s, 0) || !isValidUTF8(s) {
			t.Skip()
		}

		quoted := shellQuote(s)

		// The result has to be a single, fully closed word.
		if !strings.HasPrefix(quoted, "'") || !strings.HasSuffix(quoted, "'") {
			t.Fatalf("shellQuote(%q) = %q is not enclosed", s, quoted)
		}

		got, ok := roundtrip(t, s)
		if !ok {
			t.Fatalf("the shell call failed for %q", s)
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
	got := bashCommand("echo hello")

	want := []string{"/bin/bash", "-c", "echo hello"}
	if len(got) != len(want) {
		t.Fatalf("bashCommand = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("bashCommand[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
