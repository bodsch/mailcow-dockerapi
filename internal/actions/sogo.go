package actions

import (
	"context"

	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
)

// SogoRenameUser implements container_post__exec__sogo__rename_user.
//
// The method in the original is named container_post__exec__sogo__rename_user
// while the comment above it says "task: rename" — the name is what matters, since
// that is what the lookup resolves.
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
