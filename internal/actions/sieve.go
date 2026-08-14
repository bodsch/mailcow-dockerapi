package actions

import (
	"context"

	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
)

// SieveList entspricht container_post__exec__sieve__list.
func SieveList(ctx context.Context, env Env, req Request, t dockerclient.Target) Result {
	username, ok := req.String("username")
	if !ok {
		return Danger("username is missing")
	}

	return execText(ctx, env, t, []string{"/usr/bin/doveadm", "sieve", "list", "-u", username}, "")
}

// SievePrint entspricht container_post__exec__sieve__print.
func SievePrint(ctx context.Context, env Env, req Request, t dockerclient.Target) Result {
	username, ok := req.String("username")
	if !ok {
		return Danger("username is missing")
	}

	scriptName, ok := req.String("script_name")
	if !ok {
		return Danger("script_name is missing")
	}

	return execText(ctx, env, t,
		[]string{"/usr/bin/doveadm", "sieve", "get", "-u", username, scriptName}, "")
}
