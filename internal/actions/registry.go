package actions

import "strings"

// Registry bildet die Methodennamen aus DockerApi.py auf die portierten
// Funktionen ab.
//
// Die Schlüssel sind unverändert übernommen: mailcow bildet sie im PHP-Code
// und in den PubSub-Nachrichten selbst und erwartet genau diese Bezeichner.
var Registry = map[string]Func{
	"container_post__stop":    Stop,
	"container_post__start":   Start,
	"container_post__restart": Restart,
	"container_post__top":     Top,
	"container_post__stats":   Stats,

	"container_post__exec__mailq__delete":       MailqDelete,
	"container_post__exec__mailq__hold":         MailqHold,
	"container_post__exec__mailq__cat":          MailqCat,
	"container_post__exec__mailq__unhold":       MailqUnhold,
	"container_post__exec__mailq__deliver":      MailqDeliver,
	"container_post__exec__mailq__list":         MailqList,
	"container_post__exec__mailq__flush":        MailqFlush,
	"container_post__exec__mailq__super_delete": MailqSuperDelete,

	"container_post__exec__system__fts_rescan":          FtsRescan,
	"container_post__exec__system__df":                  DF,
	"container_post__exec__system__mysql_upgrade":       MySQLUpgrade,
	"container_post__exec__system__mysql_tzinfo_to_sql": MySQLTzinfoToSQL,

	"container_post__exec__reload__dovecot": ReloadDovecot,
	"container_post__exec__reload__postfix": ReloadPostfix,
	"container_post__exec__reload__nginx":   ReloadNginx,

	"container_post__exec__sieve__list":  SieveList,
	"container_post__exec__sieve__print": SievePrint,

	"container_post__exec__maildir__cleanup": MaildirCleanup,
	"container_post__exec__maildir__move":    MaildirMove,

	"container_post__exec__rspamd__worker_password": RspamdWorkerPassword,

	"container_post__exec__sogo__rename_user": SogoRenameUser,

	"container_post__exec__doveadm__get_acl":    DoveadmGetACL,
	"container_post__exec__doveadm__delete_acl": DoveadmDeleteACL,
	"container_post__exec__doveadm__set_acl":    DoveadmSetACL,
}

// MsgUnknownAPICall ist die Meldung für einen nicht auflösbaren Namen.
const MsgUnknownAPICall = "container_post - unknown api call"

// Lookup schlägt eine Action nach.
func Lookup(name string) (Func, bool) {
	fn, ok := Registry[name]
	return fn, ok
}

// MethodName bildet den Namen einer Action, so wie main.py:155 und main.py:222
// ihn zusammensetzten.
//
// Für post_action "exec" fließen zusätzlich cmd und task ein, für alles andere
// nur die Aktion selbst.
func MethodName(postAction, cmd, task string) string {
	if postAction == "exec" {
		return strings.Join([]string{"container_post", postAction, cmd, task}, "__")
	}

	return strings.Join([]string{"container_post", postAction}, "__")
}
