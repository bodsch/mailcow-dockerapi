package actions

import (
	"context"
	"strings"

	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
)

// FtsRescan entspricht container_post__exec__system__fts_rescan.
//
// Ist username gesetzt, wird gezielt neu indiziert, sonst bei gesetztem
// all-Feld für alle Postfächer. Die Reihenfolge der Prüfung entspricht dem
// Original.
func FtsRescan(ctx context.Context, env Env, req Request, t dockerclient.Target) Result {
	var cmd []string

	switch {
	case req.Has("username"):
		username, ok := req.String("username")
		if !ok {
			return Danger("username is invalid")
		}
		cmd = []string{"/usr/bin/doveadm", "fts", "rescan", "-u", username}
	case req.Has("all"):
		cmd = []string{"/usr/bin/doveadm", "fts", "rescan", "-A"}
	default:
		return Danger("username or all is missing")
	}

	c, errRes := firstContainer(ctx, env, t, execListAll)
	if errRes != nil {
		return *errRes
	}

	res, err := env.Docker.Exec(ctx, c.ID, dockerclient.ExecOptions{Cmd: cmd, User: "vmail"})
	if err != nil {
		return Danger(err.Error())
	}

	if res.ExitCode == 0 {
		return JSON(Message{Type: TypeSuccess, Msg: "fts_rescan: rescan triggered"})
	}

	return JSON(Message{Type: TypeWarning, Msg: "fts_rescan error"})
}

// dfFallback ist die Antwort, die das Original bei einem Fehler lieferte.
const dfFallback = "0,0,0,0,0,0"

// DF entspricht container_post__exec__system__df.
//
// Die Antwort ist – anders als bei allen übrigen Actions – kein Objekt,
// sondern eine nackte Zeichenkette. FastAPI kodierte den Rückgabewert
// seinerseits als JSON, der Rumpf enthielt also die Anführungszeichen.
// Dieses Format wertet das mailcow-Frontend aus und bleibt erhalten.
func DF(ctx context.Context, env Env, req Request, t dockerclient.Target) Result {
	dir, ok := req.String("dir")
	if !ok {
		return Danger("dir is missing")
	}

	c, errRes := firstContainer(ctx, env, t, execListAll)
	if errRes != nil {
		return *errRes
	}

	// Die Pipeline erzwingt eine Shell. Nur das Verzeichnis stammt aus der
	// Anfrage und wird maskiert; der Rest ist unveränderlich.
	script := "/bin/df -H " + shellQuote(dir) +
		" | /usr/bin/tail -n1 | /usr/bin/tr -s [:blank:] | /usr/bin/tr ' ' ','"

	res, err := env.Docker.Exec(ctx, c.ID, dockerclient.ExecOptions{
		Cmd:  bashCommand(script),
		User: "nobody",
	})
	if err != nil {
		return JSON(dfFallback)
	}

	if res.ExitCode != 0 {
		return JSON(dfFallback)
	}

	return JSON(strings.TrimRight(string(res.Output), " \t\n\r\v\f"))
}

// MySQLUpgrade entspricht container_post__exec__system__mysql_upgrade.
//
// Meldet mysql_upgrade, dass bereits alles aktuell ist, bleibt der Container
// unangetastet; andernfalls wird er neu gestartet.
func MySQLUpgrade(ctx context.Context, env Env, _ Request, t dockerclient.Target) Result {
	c, errRes := firstContainer(ctx, env, t, execListAll)
	if errRes != nil {
		return *errRes
	}

	res, err := env.Docker.Exec(ctx, c.ID, dockerclient.ExecOptions{
		Cmd:  []string{"/usr/bin/mysql_upgrade", "-uroot", "-p" + env.DBRoot},
		User: "mysql",
	})
	if err != nil {
		return Danger(err.Error())
	}

	output := string(res.Output)

	if res.ExitCode != 0 {
		return JSON(MessageWithText{
			Type: TypeError,
			Msg:  "mysql_upgrade: error running command",
			Text: output,
		})
	}

	if strings.Contains(output, "is already upgraded to") {
		return JSON(MessageWithText{
			Type: TypeSuccess,
			Msg:  "mysql_upgrade: already upgraded",
			Text: output,
		})
	}

	if err := env.Docker.Restart(ctx, c.ID); err != nil {
		return Danger(err.Error())
	}

	return JSON(MessageWithText{
		Type: TypeWarning,
		Msg:  "mysql_upgrade: upgrade was applied",
		Text: output,
	})
}

// MySQLTzinfoToSQL entspricht container_post__exec__system__mysql_tzinfo_to_sql.
func MySQLTzinfoToSQL(ctx context.Context, env Env, _ Request, t dockerclient.Target) Result {
	c, errRes := firstContainer(ctx, env, t, execListAll)
	if errRes != nil {
		return *errRes
	}

	// Die Pipeline erzwingt eine Shell; das Passwort wird maskiert.
	script := "/usr/bin/mysql_tzinfo_to_sql /usr/share/zoneinfo" +
		" | /bin/sed 's/Local time zone must be set--see zic manual page/FCTY/'" +
		" | /usr/bin/mysql -uroot -p" + shellQuote(env.DBRoot) + " mysql"

	res, err := env.Docker.Exec(ctx, c.ID, dockerclient.ExecOptions{
		Cmd:  bashCommand(script),
		User: "mysql",
	})
	if err != nil {
		return Danger(err.Error())
	}

	output := string(res.Output)

	if res.ExitCode != 0 {
		return JSON(MessageWithText{
			Type: TypeError,
			Msg:  "mysql_tzinfo_to_sql: error running command",
			Text: output,
		})
	}

	return JSON(MessageWithText{
		Type: TypeInfo,
		Msg:  "mysql_tzinfo_to_sql: command completed successfully",
		Text: output,
	})
}
