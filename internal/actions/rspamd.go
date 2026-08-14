package actions

import (
	"context"
	"regexp"
	"strings"

	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
)

// rspamdPasswordFile ist die Datei, in der der Controller sein Passwort erwartet.
const rspamdPasswordFile = "/etc/rspamd/override.d/worker-controller-password.inc"

var (
	// rspamdHashPattern schneidet den Hash ab dem Präfix $2$ aus einer Zeile.
	rspamdHashPattern = regexp.MustCompile(`\$2\$.+$`)
	// rspamdSanitize entfernt alles, was nicht zum Hash gehört.
	rspamdSanitize = regexp.MustCompile(`[^0-9a-zA-Z$]+`)
)

// RspamdWorkerPassword entspricht container_post__exec__rspamd__worker_password.
//
// Ablauf wie im Original: rspamadm erzeugt den Hash, dieser wird in die
// Override-Datei geschrieben, zur Kontrolle zurückgelesen und der Container
// anschließend neu gestartet.
//
// Beide Kommandos laufen über eine interaktive Shell mit angehängtem stdin –
// ein gewöhnliches docker exec liefert bei rspamadm pw kein Ergebnis.
func RspamdWorkerPassword(ctx context.Context, env Env, req Request, t dockerclient.Target) Result {
	raw, ok := req.String("raw")
	if !ok {
		return Danger("raw is missing")
	}

	c, errRes := firstContainer(ctx, env, t, execListAll)
	if errRes != nil {
		return *errRes
	}

	genCmd := "/usr/bin/rspamadm pw -e -p " + shellQuote(raw) + " 2> /dev/null"

	out, err := env.Docker.ExecInteractive(ctx, c.ID, dockerclient.InteractiveOptions{
		Command: genCmd,
		User:    "_rspamd",
	})
	if err != nil {
		env.logger().Error("failed changing Rspamd password", "error", err)
		return Danger("command did not complete")
	}

	if !applyRspamdHash(ctx, env, c.ID, out) {
		env.logger().Error("failed changing Rspamd password")
		return Danger("command did not complete")
	}

	env.logger().Info("success changing Rspamd password")
	return Success()
}

// applyRspamdHash sucht in der Ausgabe nach dem erzeugten Hash, schreibt ihn
// in die Override-Datei und startet den Container neu. Der Rückgabewert
// entspricht dem matched-Merker des Originals.
func applyRspamdHash(ctx context.Context, env Env, containerID, output string) bool {
	matched := false

	for _, line := range strings.Split(output, "\n") {
		if !strings.Contains(line, "$2$") {
			continue
		}

		hash := rspamdHashPattern.FindString(strings.TrimSpace(line))
		if hash == "" {
			// $2$ stand am Zeilenende ohne folgenden Hash. In Python
			// scheiterte hier der Zugriff auf group(0).
			continue
		}

		hash = rspamdSanitize.ReplaceAllString(hash, "")
		if !strings.HasPrefix(hash, "$2$") {
			continue
		}

		writeCmd := "/bin/echo 'enable_password = \"" + hash + "\";' > " +
			rspamdPasswordFile + " && cat " + rspamdPasswordFile

		verify, err := env.Docker.ExecInteractive(ctx, containerID, dockerclient.InteractiveOptions{
			Command: writeCmd,
			User:    "_rspamd",
		})
		if err != nil {
			continue
		}

		// Erst wenn die zurückgelesene Datei den Hash enthält, gilt der
		// Wechsel als vollzogen.
		if !strings.Contains(verify, hash) {
			continue
		}

		if err := env.Docker.Restart(ctx, containerID); err != nil {
			continue
		}

		matched = true
	}

	return matched
}
