package actions

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
)

// nonWordPattern is \W+ from DockerApi.py:333.
//
// Python's \W is Unicode-aware for str patterns and keeps letters and digits of
// every script. Go's \W covers ASCII only, which is why the character class is
// spelled out here — otherwise mailboxes with non-ASCII letters would end up in
// differently named directories.
var nonWordPattern = regexp.MustCompile(`[^\p{L}\p{N}_]+`)

// sanitizeName removes every character that is not a word character.
func sanitizeName(s string) string {
	return nonWordPattern.ReplaceAllString(s, "")
}

// indexName turns "domain.tld/user" into the index name "user@domain.tld". A value
// without a slash has no index directory.
func indexName(maildir string) (string, bool) {
	parts := strings.Split(maildir, "/")
	if len(parts) < 2 {
		return "", false
	}

	return parts[1] + "@" + parts[0], true
}

// MaildirCleanup implements container_post__exec__maildir__cleanup.
//
// The mailbox and its index move to /var/vmail/_garbage; nothing is deleted. The
// directory test with [[ -d ]] requires a shell.
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

// MaildirMove implements container_post__exec__maildir__move.
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
		// The destination carries the _index suffix, the source does not. That
		// asymmetry comes from DockerApi.py:363 and is preserved so existing
		// installations see the same result.
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

// moveIfDirectory builds "if [[ -d 'src' ]]; then /bin/mv 'src' 'dst'; fi".
func moveIfDirectory(src, dst string) string {
	quotedSrc := shellQuote(src)

	return "if [[ -d " + quotedSrc + " ]]; then /bin/mv " + quotedSrc + " " + shellQuote(dst) + "; fi"
}
