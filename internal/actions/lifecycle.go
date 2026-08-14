package actions

import (
	"context"

	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
)

// Die Lifecycle-Actions listen mit all=True und beziehen gestoppte Container
// mit ein – anders als die exec-Actions, die nur laufende ansprechen.
const lifecycleListAll = true

// Stop entspricht container_post__stop.
//
// Wie im Original wirkt die Aktion auf alle Treffer der Auswahl, nicht nur
// auf den ersten.
func Stop(ctx context.Context, env Env, _ Request, t dockerclient.Target) Result {
	return forEachContainer(ctx, env, t, env.Docker.Stop)
}

// Start entspricht container_post__start.
func Start(ctx context.Context, env Env, _ Request, t dockerclient.Target) Result {
	return forEachContainer(ctx, env, t, env.Docker.Start)
}

// Restart entspricht container_post__restart.
func Restart(ctx context.Context, env Env, _ Request, t dockerclient.Target) Result {
	return forEachContainer(ctx, env, t, env.Docker.Restart)
}

// forEachContainer wendet op auf jeden Treffer an.
//
// Findet die Auswahl nichts, meldet das Original trotzdem Erfolg; dieses
// Verhalten bleibt erhalten, weil mailcow beim Stoppen bereits gestoppter
// Container darauf baut.
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

// Top entspricht container_post__top: die Prozessliste des ersten Treffers.
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

// Stats entspricht container_post__stats: ein einzelnes Messwert-Sample des
// ersten Treffers.
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
