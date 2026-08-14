package actions

import (
	"context"
	"regexp"

	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
)

// Die exec-Actions listen ohne all=True und sprechen damit ausschließlich
// laufende Container an – so wie DockerApi.py es tat.
const execListAll = false

// qidPattern beschreibt eine Postfix-Queue-ID.
//
// In Python lautete das Muster ^[0-9a-fA-F]+$; dort schließt $ auch einen
// abschließenden Zeilenumbruch ein. In Go bindet $ ohne (?m) an das Textende,
// die Prüfung ist also etwas strenger.
var qidPattern = regexp.MustCompile(`^[0-9a-fA-F]+$`)

// filterQIDs behält nur gültige Queue-IDs.
func filterQIDs(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if qidPattern.MatchString(item) {
			out = append(out, item)
		}
	}

	return out
}

// requireQIDs liest das Feld items und prüft die enthaltenen Queue-IDs.
//
// DockerApi.py band das Ergebnis von filter() an eine Variable und prüfte
// diese auf Wahrheitswert. Ein Generator ist immer wahr, weshalb bei
// ausschließlich ungültigen Einträgen ein postsuper-Aufruf ohne Argumente
// abgesetzt wurde. Hier führt eine leere Auswahl zu einer Fehlermeldung.
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

// postsuperFlagged setzt postsuper mit einem Schalter je Queue-ID ab.
//
// Das Original baute daraus eine Zeichenkette und übergab sie an /bin/bash -c.
// Da die IDs bereits auf Hexadezimalzeichen geprüft sind, ist das gleichwertig
// zum direkten Argv – dieses kommt jedoch ohne Shell aus.
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

// MailqDelete entspricht container_post__exec__mailq__delete.
func MailqDelete(ctx context.Context, env Env, req Request, t dockerclient.Target) Result {
	return postsuperFlagged(ctx, env, req, t, "-d")
}

// MailqHold entspricht container_post__exec__mailq__hold.
func MailqHold(ctx context.Context, env Env, req Request, t dockerclient.Target) Result {
	return postsuperFlagged(ctx, env, req, t, "-h")
}

// MailqUnhold entspricht container_post__exec__mailq__unhold.
func MailqUnhold(ctx context.Context, env Env, req Request, t dockerclient.Target) Result {
	return postsuperFlagged(ctx, env, req, t, "-H")
}

// MailqCat entspricht container_post__exec__mailq__cat.
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

// MailqDeliver entspricht container_post__exec__mailq__deliver.
//
// Für jede Queue-ID wird ein eigener postqueue-Aufruf abgesetzt. Die
// Exit-Codes wertete schon das Original nicht aus (siehe der dortige
// todo-Kommentar); die Antwort meldet unabhängig davon Erfolg.
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

// MailqList entspricht container_post__exec__mailq__list.
func MailqList(ctx context.Context, env Env, _ Request, t dockerclient.Target) Result {
	return execText(ctx, env, t, []string{"/usr/sbin/postqueue", "-j"}, "postfix")
}

// MailqFlush entspricht container_post__exec__mailq__flush.
func MailqFlush(ctx context.Context, env Env, _ Request, t dockerclient.Target) Result {
	return execGeneric(ctx, env, t, []string{"/usr/sbin/postqueue", "-f"}, "postfix")
}

// MailqSuperDelete entspricht container_post__exec__mailq__super_delete.
func MailqSuperDelete(ctx context.Context, env Env, _ Request, t dockerclient.Target) Result {
	return execGeneric(ctx, env, t, []string{"/usr/sbin/postsuper", "-d", "ALL"}, "")
}

// execGeneric führt cmd im ersten Treffer aus und wertet den Exit-Code aus.
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

// execText führt cmd aus und gibt die Ausgabe unverändert als Text zurück.
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
