package actions

import (
	"context"

	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
)

// ReloadDovecot entspricht container_post__exec__reload__dovecot.
func ReloadDovecot(ctx context.Context, env Env, _ Request, t dockerclient.Target) Result {
	return execGeneric(ctx, env, t, []string{"/usr/sbin/dovecot", "reload"}, "")
}

// ReloadPostfix entspricht container_post__exec__reload__postfix.
func ReloadPostfix(ctx context.Context, env Env, _ Request, t dockerclient.Target) Result {
	return execGeneric(ctx, env, t, []string{"/usr/sbin/postfix", "reload"}, "")
}

// ReloadNginx entspricht container_post__exec__reload__nginx.
//
// Das Original rief hier /bin/sh statt /bin/bash auf – der nginx-Container
// bringt keine bash mit. Ohne Shell entfällt die Unterscheidung.
func ReloadNginx(ctx context.Context, env Env, _ Request, t dockerclient.Target) Result {
	return execGeneric(ctx, env, t, []string{"/usr/sbin/nginx", "-s", "reload"}, "")
}
