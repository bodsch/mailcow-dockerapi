package actions

import "strings"

// Registry maps the method names from DockerApi.py onto the ported functions.
//
// The keys are taken over unchanged: mailcow builds them itself, in its PHP code
// and in its PubSub messages, and expects exactly these identifiers.
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

// MsgUnknownAPICall is the message for a name that does not resolve.
const MsgUnknownAPICall = "container_post - unknown api call"

// Lookup resolves an action.
func Lookup(name string) (Func, bool) {
	fn, ok := Registry[name]
	return fn, ok
}

// MethodName builds an action's name the way main.py:155 and main.py:222 composed
// it.
//
// For the post_action "exec", cmd and task are part of the name; for anything else
// only the action itself is.
func MethodName(postAction, cmd, task string) string {
	if postAction == "exec" {
		return strings.Join([]string{"container_post", postAction, cmd, task}, "__")
	}

	return strings.Join([]string{"container_post", postAction}, "__")
}
