package actions

import (
	"context"
	"regexp"

	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
)

// The exec actions list without all=True and therefore only address running
// containers — the way DockerApi.py did.
const execListAll = false

// qidPattern describes a Postfix queue id.
//
// In Python the pattern was ^[0-9a-fA-F]+$, where $ also covers a trailing
// newline. In Go $ binds to the end of the text without (?m), so the check is
// slightly stricter.
var qidPattern = regexp.MustCompile(`^[0-9a-fA-F]+$`)

// filterQIDs keeps only valid queue ids.
func filterQIDs(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if qidPattern.MatchString(item) {
			out = append(out, item)
		}
	}

	return out
}

// requireQIDs reads the items field and validates the queue ids in it.
//
// DockerApi.py bound the result of filter() to a variable and tested that for
// truthiness. A generator is always true, so a list of nothing but invalid entries
// produced a postsuper call with no arguments. Here an empty selection is an error.
func requireQIDs(req Request) ([]string, *Result) {
	items, ok := req.Strings("items")
	if !ok {
		res := Danger("items is missing")
		return nil, &res
	}

	qids := filterQIDs(items)
	if len(qids) == 0 {
		res := Danger("no valid queue ids given")
		return nil, &res
	}

	return qids, nil
}

// postsuperFlagged issues postsuper with one flag per queue id.
//
// The original built a string from these and handed it to /bin/bash -c. Since the
// ids are already checked against hexadecimal characters, that is equivalent to the
// direct argv — which does without a shell.
func postsuperFlagged(ctx context.Context, env Env, req Request, t dockerclient.Target, flag string) Result {
	qids, errRes := requireQIDs(req)
	if errRes != nil {
		return *errRes
	}

	c, errRes := firstContainer(ctx, env, t, execListAll)
	if errRes != nil {
		return *errRes
	}

	cmd := []string{"/usr/sbin/postsuper"}
	for _, qid := range qids {
		cmd = append(cmd, flag, qid)
	}

	res, err := env.Docker.Exec(ctx, c.ID, dockerclient.ExecOptions{Cmd: cmd})
	if err != nil {
		return Danger(err.Error())
	}

	return execHandler(res)
}

// MailqDelete implements container_post__exec__mailq__delete.
func MailqDelete(ctx context.Context, env Env, req Request, t dockerclient.Target) Result {
	return postsuperFlagged(ctx, env, req, t, "-d")
}

// MailqHold implements container_post__exec__mailq__hold.
func MailqHold(ctx context.Context, env Env, req Request, t dockerclient.Target) Result {
	return postsuperFlagged(ctx, env, req, t, "-h")
}

// MailqUnhold implements container_post__exec__mailq__unhold.
func MailqUnhold(ctx context.Context, env Env, req Request, t dockerclient.Target) Result {
	return postsuperFlagged(ctx, env, req, t, "-H")
}

// MailqCat implements container_post__exec__mailq__cat.
func MailqCat(ctx context.Context, env Env, req Request, t dockerclient.Target) Result {
	qids, errRes := requireQIDs(req)
	if errRes != nil {
		return *errRes
	}

	c, errRes := firstContainer(ctx, env, t, execListAll)
	if errRes != nil {
		return *errRes
	}

	cmd := append([]string{"/usr/sbin/postcat", "-q"}, qids...)

	res, err := env.Docker.Exec(ctx, c.ID, dockerclient.ExecOptions{Cmd: cmd, User: "postfix"})
	if err != nil {
		return Danger(err.Error())
	}

	return Text(string(res.Output))
}

// MailqDeliver implements container_post__exec__mailq__deliver.
//
// Every queue id gets its own postqueue call. The original did not evaluate the
// exit codes either (see the todo comment there); the response reports success
// regardless.
func MailqDeliver(ctx context.Context, env Env, req Request, t dockerclient.Target) Result {
	qids, errRes := requireQIDs(req)
	if errRes != nil {
		return *errRes
	}

	c, errRes := firstContainer(ctx, env, t, execListAll)
	if errRes != nil {
		return *errRes
	}

	for _, qid := range qids {
		_, err := env.Docker.Exec(ctx, c.ID, dockerclient.ExecOptions{
			Cmd:  []string{"/usr/sbin/postqueue", "-i", qid},
			User: "postfix",
		})
		if err != nil {
			return Danger(err.Error())
		}
	}

	return JSON(Message{Type: TypeSuccess, Msg: "Scheduled immediate delivery"})
}

// MailqList implements container_post__exec__mailq__list.
func MailqList(ctx context.Context, env Env, _ Request, t dockerclient.Target) Result {
	return execText(ctx, env, t, []string{"/usr/sbin/postqueue", "-j"}, "postfix")
}

// MailqFlush implements container_post__exec__mailq__flush.
func MailqFlush(ctx context.Context, env Env, _ Request, t dockerclient.Target) Result {
	return execGeneric(ctx, env, t, []string{"/usr/sbin/postqueue", "-f"}, "postfix")
}

// MailqSuperDelete implements container_post__exec__mailq__super_delete.
func MailqSuperDelete(ctx context.Context, env Env, _ Request, t dockerclient.Target) Result {
	return execGeneric(ctx, env, t, []string{"/usr/sbin/postsuper", "-d", "ALL"}, "")
}

// execGeneric runs cmd in the first match and evaluates the exit code.
func execGeneric(ctx context.Context, env Env, t dockerclient.Target, cmd []string, user string) Result {
	c, errRes := firstContainer(ctx, env, t, execListAll)
	if errRes != nil {
		return *errRes
	}

	res, err := env.Docker.Exec(ctx, c.ID, dockerclient.ExecOptions{Cmd: cmd, User: user})
	if err != nil {
		return Danger(err.Error())
	}

	return execHandler(res)
}

// execText runs cmd and returns its output unchanged, as text.
func execText(ctx context.Context, env Env, t dockerclient.Target, cmd []string, user string) Result {
	c, errRes := firstContainer(ctx, env, t, execListAll)
	if errRes != nil {
		return *errRes
	}

	res, err := env.Docker.Exec(ctx, c.ID, dockerclient.ExecOptions{Cmd: cmd, User: user})
	if err != nil {
		return Danger(err.Error())
	}

	return Text(string(res.Output))
}
