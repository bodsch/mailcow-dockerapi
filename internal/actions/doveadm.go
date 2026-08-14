package actions

import (
	"context"
	"strings"

	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
)

// ACL is one entry in the response of doveadm__get_acl.
//
// The field order matches the dict in the original, because json.dumps in Python
// preserves insertion order.
type ACL struct {
	User    string   `json:"user"`
	ID      string   `json:"id"`
	Mailbox string   `json:"mailbox"`
	Rights  []string `json:"rights"`
}

// availableRights is the allow-list from DockerApi.py:494.
var availableRights = map[string]bool{
	"admin":         true,
	"create":        true,
	"delete":        true,
	"expunge":       true,
	"insert":        true,
	"lookup":        true,
	"post":          true,
	"read":          true,
	"write":         true,
	"write-deleted": true,
	"write-seen":    true,
}

// DoveadmGetACL implements container_post__exec__doveadm__get_acl.
//
// It first lists the user's mailboxes, then queries the rights for each of them.
// Shared folders ("Shared/...") belong to another user; there only the entry
// pointing at the requested user counts.
func DoveadmGetACL(ctx context.Context, env Env, req Request, t dockerclient.Target) Result {
	id, ok := req.String("id")
	if !ok {
		return Danger("id is missing")
	}

	c, errRes := firstContainer(ctx, env, t, execListAll)
	if errRes != nil {
		return *errRes
	}

	mailboxes, err := env.Docker.Exec(ctx, c.ID, dockerclient.ExecOptions{
		Cmd: []string{"doveadm", "mailbox", "list", "-u", id},
	})
	if err != nil {
		return Danger(err.Error())
	}

	// An empty result has to encode as [] rather than null.
	formatted := []ACL{}
	seen := map[string]bool{}

	for _, folder := range strings.Split(string(mailboxes.Output), "\n") {
		if !strings.Contains(folder, "Shared") {
			formatted = appendOwnACLs(ctx, env, c.ID, id, folder, seen, formatted)
			continue
		}

		if strings.Contains(folder, "/") {
			formatted = appendSharedACLs(ctx, env, c.ID, id, folder, seen, formatted)
		}
	}

	return JSON(formatted)
}

// appendOwnACLs handles a mailbox owned by the requested user.
func appendOwnACLs(
	ctx context.Context,
	env Env,
	containerID, id, mailbox string,
	seen map[string]bool,
	acc []ACL,
) []ACL {
	if seen[mailbox] {
		return acc
	}

	for _, entry := range aclEntries(ctx, env, containerID, id, mailbox) {
		seen[mailbox] = true
		acc = append(acc, ACL{
			User:    id,
			ID:      entry.userID,
			Mailbox: mailbox,
			Rights:  entry.rights,
		})
	}

	return acc
}

// appendSharedACLs handles a shared folder of the form "Shared/owner/path".
func appendSharedACLs(
	ctx context.Context,
	env Env,
	containerID, id, folder string,
	seen map[string]bool,
	acc []ACL,
) []ACL {
	parts := strings.Split(folder, "/")
	if len(parts) < 3 {
		return acc
	}

	owner := parts[1]
	mailbox := strings.Join(parts[2:], "/")

	if seen[mailbox] {
		return acc
	}

	for _, entry := range aclEntries(ctx, env, containerID, owner, mailbox) {
		// Only the requested user's entry is of interest.
		if entry.userID != id || seen[mailbox] {
			continue
		}

		seen[mailbox] = true
		acc = append(acc, ACL{
			User:    owner,
			ID:      id,
			Mailbox: mailbox,
			Rights:  entry.rights,
		})
	}

	return acc
}

// aclEntry is one parsed line of `doveadm acl get`.
type aclEntry struct {
	userID string
	rights []string
}

// aclEntries runs `doveadm acl get` and parses the output.
//
// The first line is a column header and is skipped. Lines that do not split into
// an identifier and rights are ignored; in Python they raised a ValueError or an
// IndexError.
func aclEntries(ctx context.Context, env Env, containerID, user, mailbox string) []aclEntry {
	res, err := env.Docker.Exec(ctx, containerID, dockerclient.ExecOptions{
		Cmd: []string{"doveadm", "acl", "get", "-u", user, mailbox},
	})
	if err != nil {
		return nil
	}

	lines := strings.Split(strings.TrimSpace(string(res.Output)), "\n")
	if len(lines) < 2 {
		return nil
	}

	entries := make([]aclEntry, 0, len(lines)-1)
	for _, line := range lines[1:] {
		field, rest, ok := splitFirstField(line)
		if !ok {
			continue
		}

		_, userID, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}

		entries = append(entries, aclEntry{userID: userID, rights: strings.Fields(rest)})
	}

	return entries
}

// splitFirstField separates the first field from the rest — like Python's
// str.split(maxsplit=1), which skips leading whitespace and treats a run of
// whitespace as one separator.
func splitFirstField(s string) (first, rest string, ok bool) {
	const space = " \t\n\r\v\f"

	s = strings.TrimLeft(s, space)
	idx := strings.IndexAny(s, space)
	if idx < 0 {
		return "", "", false
	}

	return s[:idx], strings.TrimLeft(s[idx:], space), true
}

// DoveadmDeleteACL implements container_post__exec__doveadm__delete_acl.
func DoveadmDeleteACL(ctx context.Context, env Env, req Request, t dockerclient.Target) Result {
	user, ok := req.NonEmptyString("user")
	if !ok {
		return Danger("user is missing")
	}

	mailbox, ok := req.NonEmptyString("mailbox")
	if !ok {
		return Danger("mailbox is missing")
	}

	id, ok := req.NonEmptyString("id")
	if !ok {
		return Danger("id is missing")
	}

	return execGeneric(ctx, env, t,
		[]string{"doveadm", "acl", "delete", "-u", user, mailbox, "user=" + id}, "")
}

// DoveadmSetACL implements container_post__exec__doveadm__set_acl.
//
// Only rights from the allow-list are passed on; unknown ones are dropped
// silently, as in the original.
func DoveadmSetACL(ctx context.Context, env Env, req Request, t dockerclient.Target) Result {
	user, ok := req.NonEmptyString("user")
	if !ok {
		return Danger("user is missing")
	}

	mailbox, ok := req.NonEmptyString("mailbox")
	if !ok {
		return Danger("mailbox is missing")
	}

	id, ok := req.NonEmptyString("id")
	if !ok {
		return Danger("id is missing")
	}

	requested, ok := req.Strings("rights")
	if !ok {
		return Danger("rights is missing")
	}

	rights := make([]string, 0, len(requested))
	for _, right := range requested {
		if right = strings.ToLower(right); availableRights[right] {
			rights = append(rights, right)
		}
	}

	if len(rights) == 0 {
		return Danger("no valid rights given")
	}

	cmd := append([]string{"doveadm", "acl", "set", "-u", user, mailbox, "user=" + id}, rights...)

	return execGeneric(ctx, env, t, cmd, "")
}
