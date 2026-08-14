package actions

import (
	"context"

	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
)

// The lifecycle actions list with all=True and include stopped containers —
// unlike the exec actions, which only address running ones.
const lifecycleListAll = true

// Stop implements container_post__stop.
//
// As in the original, the action applies to every match of the selection, not only
// to the first.
func Stop(ctx context.Context, env Env, _ Request, t dockerclient.Target) Result {
	return forEachContainer(ctx, env, t, env.Docker.Stop)
}

// Start implements container_post__start.
func Start(ctx context.Context, env Env, _ Request, t dockerclient.Target) Result {
	return forEachContainer(ctx, env, t, env.Docker.Start)
}

// Restart implements container_post__restart.
func Restart(ctx context.Context, env Env, _ Request, t dockerclient.Target) Result {
	return forEachContainer(ctx, env, t, env.Docker.Restart)
}

// forEachContainer applies op to every match.
//
// When the selection finds nothing the original still reports success; that
// behaviour is kept, because mailcow relies on it when stopping containers that are
// already stopped.
func forEachContainer(
	ctx context.Context,
	env Env,
	t dockerclient.Target,
	op func(context.Context, string) error,
) Result {
	list, err := env.Docker.List(ctx, t, lifecycleListAll)
	if err != nil {
		return Danger(err.Error())
	}

	for _, c := range list {
		if err := op(ctx, c.ID); err != nil {
			return Danger(err.Error())
		}
	}

	return Success()
}

// Top implements container_post__top: the process list of the first match.
func Top(ctx context.Context, env Env, _ Request, t dockerclient.Target) Result {
	c, errRes := firstContainer(ctx, env, t, lifecycleListAll)
	if errRes != nil {
		return *errRes
	}

	top, err := env.Docker.Top(ctx, c.ID)
	if err != nil {
		return Danger(err.Error())
	}

	return JSON(Message{Type: TypeSuccess, Msg: top})
}

// Stats implements container_post__stats: a single sample for the first match.
func Stats(ctx context.Context, env Env, _ Request, t dockerclient.Target) Result {
	c, errRes := firstContainer(ctx, env, t, lifecycleListAll)
	if errRes != nil {
		return *errRes
	}

	raw, err := env.Docker.Stats(ctx, c.ID)
	if err != nil {
		return Danger(err.Error())
	}

	return JSON(Message{Type: TypeSuccess, Msg: raw})
}
