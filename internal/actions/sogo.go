package actions

import (
	"context"

	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
)

// SogoRenameUser entspricht container_post__exec__sogo__rename_user.
//
// Der Methodenname im Original lautet container_post__exec__sogo__rename_user,
// der Kommentar darüber spricht von "task: rename" – maßgeblich ist der Name,
// denn nach ihm wird aufgelöst.
func SogoRenameUser(ctx context.Context, env Env, req Request, t dockerclient.Target) Result {
	oldUsername, ok := req.String("old_username")
	if !ok {
		return Danger("old_username is missing")
	}

	newUsername, ok := req.String("new_username")
	if !ok {
		return Danger("new_username is missing")
	}

	return execGeneric(ctx, env, t,
		[]string{"sogo-tool", "rename-user", oldUsername, newUsername}, "sogo")
}
