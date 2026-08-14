package actions

import (
	"context"

	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
)

// ReloadDovecot implements container_post__exec__reload__dovecot.
func ReloadDovecot(ctx context.Context, env Env, _ Request, t dockerclient.Target) Result {
	return execGeneric(ctx, env, t, []string{"/usr/sbin/dovecot", "reload"}, "")
}

// ReloadPostfix implements container_post__exec__reload__postfix.
func ReloadPostfix(ctx context.Context, env Env, _ Request, t dockerclient.Target) Result {
	return execGeneric(ctx, env, t, []string{"/usr/sbin/postfix", "reload"}, "")
}

// ReloadNginx implements container_post__exec__reload__nginx.
//
// The original invoked /bin/sh here rather than /bin/bash — the nginx container
// ships no bash. Without a shell the distinction goes away.
func ReloadNginx(ctx context.Context, env Env, _ Request, t dockerclient.Target) Result {
	return execGeneric(ctx, env, t, []string{"/usr/sbin/nginx", "-s", "reload"}, "")
}
