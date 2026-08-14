package actions

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
)

// nonWordPattern entspricht \W+ aus DockerApi.py:333.
//
// Pythons \W ist bei str-Mustern Unicode-bewusst und lässt Buchstaben und
// Ziffern aller Schriftsysteme stehen. Gos \W umfasst dagegen nur ASCII,
// weshalb die Zeichenklasse hier ausgeschrieben ist – andernfalls entstünden
// für Postfächer mit Umlauten abweichende Verzeichnisnamen.
var nonWordPattern = regexp.MustCompile(`[^\p{L}\p{N}_]+`)

// sanitizeName entfernt alle Zeichen, die kein Wortzeichen sind.
func sanitizeName(s string) string {
	return nonWordPattern.ReplaceAllString(s, "")
}

// indexName bildet aus "domain.tld/benutzer" den Indexnamen
// "benutzer@domain.tld". Enthält der Wert keinen Schrägstrich, gibt es
// kein Indexverzeichnis.
func indexName(maildir string) (string, bool) {
	parts := strings.Split(maildir, "/")
	if len(parts) < 2 {
		return "", false
	}

	return parts[1] + "@" + parts[0], true
}

// MaildirCleanup entspricht container_post__exec__maildir__cleanup.
//
// Das Postfach wandert samt Index nach /var/vmail/_garbage; gelöscht wird
// nichts. Die Verzeichnisprüfung mit [[ -d ]] verlangt eine Shell.
func MaildirCleanup(ctx context.Context, env Env, req Request, t dockerclient.Target) Result {
	maildir, ok := req.String("maildir")
	if !ok {
		return Danger("maildir is missing")
	}

	c, errRes := firstContainer(ctx, env, t, execListAll)
	if errRes != nil {
		return *errRes
	}

	var (
		saneName  = sanitizeName(maildir)
		timestamp = strconv.FormatInt(env.now().Unix(), 10)
		garbage   = "/var/vmail/_garbage/" + timestamp + "_" + saneName
	)

	script := moveIfDirectory("/var/vmail/"+maildir, garbage)

	if idx, ok := indexName(maildir); ok {
		script += " && " + moveIfDirectory(
			"/var/vmail_index/"+idx,
			garbage+"_index",
		)
	}

	res, err := env.Docker.Exec(ctx, c.ID, dockerclient.ExecOptions{
		Cmd:  bashCommand(script),
		User: "vmail",
	})
	if err != nil {
		return Danger(err.Error())
	}

	return execHandler(res)
}

// MaildirMove entspricht container_post__exec__maildir__move.
func MaildirMove(ctx context.Context, env Env, req Request, t dockerclient.Target) Result {
	oldMaildir, ok := req.String("old_maildir")
	if !ok {
		return Danger("old_maildir is missing")
	}

	newMaildir, ok := req.String("new_maildir")
	if !ok {
		return Danger("new_maildir is missing")
	}

	c, errRes := firstContainer(ctx, env, t, execListAll)
	if errRes != nil {
		return *errRes
	}

	script := moveIfDirectory("/var/vmail/"+oldMaildir, "/var/vmail/"+newMaildir)

	oldIdx, oldOK := indexName(oldMaildir)
	newIdx, newOK := indexName(newMaildir)
	if oldOK && newOK {
		// Das Ziel trägt den Zusatz _index, die Quelle nicht. Diese
		// Ungleichheit stammt aus DockerApi.py:363 und bleibt erhalten,
		// damit bestehende Installationen dasselbe Ergebnis sehen.
		script += " && " + moveIfDirectory(
			"/var/vmail_index/"+oldIdx,
			"/var/vmail_index/"+newIdx+"_index",
		)
	}

	res, err := env.Docker.Exec(ctx, c.ID, dockerclient.ExecOptions{
		Cmd:  bashCommand(script),
		User: "vmail",
	})
	if err != nil {
		return Danger(err.Error())
	}

	return execHandler(res)
}

// moveIfDirectory baut "if [[ -d 'src' ]]; then /bin/mv 'src' 'dst'; fi".
func moveIfDirectory(src, dst string) string {
	quotedSrc := shellQuote(src)

	return "if [[ -d " + quotedSrc + " ]]; then /bin/mv " + quotedSrc + " " + shellQuote(dst) + "; fi"
}
