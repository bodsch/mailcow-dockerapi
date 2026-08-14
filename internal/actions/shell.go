package actions

import "strings"

// shellQuote wraps s in single quotes so that a POSIX shell takes the content
// through unchanged, as a single word.
//
// DockerApi.py placed the quotes by hand inside the command string and escaped the
// content anew at every point of use. Here both happen in one place, which is
// tested.
//
// The mechanism: inside single quotes no character is special, not even the
// backslash. A quote in the content therefore necessarily ends the region, so it is
// replaced by the sequence "close, escaped quote, open again".
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// bashCommand builds the argv for a command that needs a shell — because of a
// pipe, a redirection or a conditional.
func bashCommand(script string) []string {
	return []string{"/bin/bash", "-c", script}
}
