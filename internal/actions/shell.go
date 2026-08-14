package actions

import "strings"

// shellQuote schließt s so in einfache Anführungszeichen ein, dass eine
// POSIX-Shell den Inhalt unverändert als ein einziges Wort übernimmt.
//
// DockerApi.py setzte die Anführungszeichen von Hand in den Kommandostring
// und maskierte den Inhalt an jeder Verwendungsstelle erneut. Hier passiert
// beides an einer Stelle, die getestet ist.
//
// Verfahren: innerhalb einfacher Anführungszeichen hat kein Zeichen eine
// Sonderbedeutung, auch der Backslash nicht. Ein enthaltenes Anführungszeichen
// beendet den Bereich deshalb zwangsläufig und wird durch die Folge
// "schließen, maskiertes Anführungszeichen, wieder öffnen" ersetzt.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// bashCommand baut das Argv für ein Kommando, das eine Shell benötigt –
// etwa wegen einer Pipe, einer Umleitung oder einer Bedingung.
func bashCommand(script string) []string {
	return []string{"/bin/bash", "-c", script}
}
