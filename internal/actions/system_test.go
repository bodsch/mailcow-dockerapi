package actions

import (
	"context"
	"strings"
	"testing"

	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
)

func TestFtsRescanForUser(t *testing.T) {
	fake := newFake()
	req := Request{"username": "user@beispiel.de"}

	got := FtsRescan(context.Background(), newEnv(fake), req, byID())

	want := `{
    "type": "success",
    "msg": "fts_rescan: rescan triggered"
}`
	assertBody(t, got, ContentTypeJSON, want)
	assertExec(t, fake, 0,
		[]string{"/usr/bin/doveadm", "fts", "rescan", "-u", "user@beispiel.de"}, "vmail")
}

func TestFtsRescanForAll(t *testing.T) {
	fake := newFake()
	req := Request{"all": true}

	FtsRescan(context.Background(), newEnv(fake), req, byID())

	assertExec(t, fake, 0, []string{"/usr/bin/doveadm", "fts", "rescan", "-A"}, "vmail")
}

// Ist username gesetzt, hat es Vorrang vor all – wie im Original.
func TestFtsRescanPrefersUsername(t *testing.T) {
	fake := newFake()
	req := Request{"username": "user@beispiel.de", "all": true}

	FtsRescan(context.Background(), newEnv(fake), req, byID())

	assertExec(t, fake, 0,
		[]string{"/usr/bin/doveadm", "fts", "rescan", "-u", "user@beispiel.de"}, "vmail")
}

func TestFtsRescanReportsWarningOnFailure(t *testing.T) {
	fake := newFake()
	fake.ExecResults = []dockerclient.ExecResult{{ExitCode: 1}}

	got := FtsRescan(context.Background(), newEnv(fake), Request{"all": true}, byID())

	want := `{
    "type": "warning",
    "msg": "fts_rescan error"
}`
	assertBody(t, got, ContentTypeJSON, want)
}

// Ein Benutzername mit Anführungszeichen darf das Kommando nicht verlassen.
func TestFtsRescanQuotesUsername(t *testing.T) {
	fake := newFake()
	req := Request{"username": "o'brien@beispiel.de'; rm -rf /"}

	FtsRescan(context.Background(), newEnv(fake), req, byID())

	call, _ := fake.LastExec()
	if call.Cmd[4] != "o'brien@beispiel.de'; rm -rf /" {
		t.Errorf("Argument = %q, want unveraendert", call.Cmd[4])
	}
	// Ohne Shell im Argv kann nichts interpretiert werden.
	for _, arg := range call.Cmd {
		if arg == "/bin/bash" || arg == "-c" {
			t.Errorf("Kommando laeuft ueber eine Shell: %v", call.Cmd)
		}
	}
}

// Die Antwort ist eine nackte Zeichenkette, die FastAPI als JSON kodierte.
func TestDFReturnsQuotedString(t *testing.T) {
	fake := newFake()
	fake.ExecResults = []dockerclient.ExecResult{
		{ExitCode: 0, Output: []byte("/dev/sda1,50G,20G,30G,40%,/\n")},
	}
	req := Request{"dir": "/var/vmail"}

	got := DF(context.Background(), newEnv(fake), req, byID())

	assertBody(t, got, ContentTypeJSON, `"/dev/sda1,50G,20G,30G,40%,/"`)
	assertExec(t, fake, 0, bashCommand(
		"/bin/df -H '/var/vmail' | /usr/bin/tail -n1 | /usr/bin/tr -s [:blank:] | /usr/bin/tr ' ' ','",
	), "nobody")
}

func TestDFFallbackOnFailure(t *testing.T) {
	fake := newFake()
	fake.ExecResults = []dockerclient.ExecResult{{ExitCode: 1, Output: []byte("df: kein Zugriff")}}
	req := Request{"dir": "/var/vmail"}

	got := DF(context.Background(), newEnv(fake), req, byID())

	assertBody(t, got, ContentTypeJSON, `"0,0,0,0,0,0"`)
}

func TestDFQuotesDirectory(t *testing.T) {
	fake := newFake()
	req := Request{"dir": "/var/vmail'; touch /tmp/x; '"}

	DF(context.Background(), newEnv(fake), req, byID())

	call, _ := fake.LastExec()
	want := "/bin/df -H '/var/vmail'\\''; touch /tmp/x; '\\''' | /usr/bin/tail -n1" +
		" | /usr/bin/tr -s [:blank:] | /usr/bin/tr ' ' ','"
	if call.Cmd[2] != want {
		t.Errorf("Skript =\n%s\nwant\n%s", call.Cmd[2], want)
	}
}

func TestDFRequiresDir(t *testing.T) {
	fake := newFake()

	got := DF(context.Background(), newEnv(fake), Request{}, byID())

	want := `{
    "type": "danger",
    "msg": "dir is missing"
}`
	assertBody(t, got, ContentTypeJSON, want)
}

func TestMySQLUpgradeAlreadyUpgraded(t *testing.T) {
	fake := newFake()
	fake.ExecResults = []dockerclient.ExecResult{
		{ExitCode: 0, Output: []byte("mysql.user is already upgraded to 10.11")},
	}

	got := MySQLUpgrade(context.Background(), newEnv(fake), Request{}, byID())

	want := `{
    "type": "success",
    "msg": "mysql_upgrade: already upgraded",
    "text": "mysql.user is already upgraded to 10.11"
}`
	assertBody(t, got, ContentTypeJSON, want)
	assertExec(t, fake, 0,
		[]string{"/usr/bin/mysql_upgrade", "-uroot", "-p" + testDBRoot}, "mysql")

	if len(fake.Restarted) != 0 {
		t.Errorf("Container wurde neu gestartet: %v", fake.Restarted)
	}
}

// Wurde tatsächlich aktualisiert, muss der Container neu starten.
func TestMySQLUpgradeRestartsAfterUpgrade(t *testing.T) {
	fake := newFake()
	fake.ExecResults = []dockerclient.ExecResult{
		{ExitCode: 0, Output: []byte("Upgrading MySQL tables")},
	}

	got := MySQLUpgrade(context.Background(), newEnv(fake), Request{}, byID())

	want := `{
    "type": "warning",
    "msg": "mysql_upgrade: upgrade was applied",
    "text": "Upgrading MySQL tables"
}`
	assertBody(t, got, ContentTypeJSON, want)

	if len(fake.Restarted) != 1 || fake.Restarted[0] != testContainerID {
		t.Errorf("Neustarts = %v, want [%s]", fake.Restarted, testContainerID)
	}
}

func TestMySQLUpgradeError(t *testing.T) {
	fake := newFake()
	fake.ExecResults = []dockerclient.ExecResult{
		{ExitCode: 1, Output: []byte("Access denied")},
	}

	got := MySQLUpgrade(context.Background(), newEnv(fake), Request{}, byID())

	want := `{
    "type": "error",
    "msg": "mysql_upgrade: error running command",
    "text": "Access denied"
}`
	assertBody(t, got, ContentTypeJSON, want)
}

func TestMySQLTzinfoToSQL(t *testing.T) {
	fake := newFake()

	got := MySQLTzinfoToSQL(context.Background(), newEnv(fake), Request{}, byID())

	want := `{
    "type": "info",
    "msg": "mysql_tzinfo_to_sql: command completed successfully",
    "text": ""
}`
	assertBody(t, got, ContentTypeJSON, want)

	wantScript := "/usr/bin/mysql_tzinfo_to_sql /usr/share/zoneinfo" +
		" | /bin/sed 's/Local time zone must be set--see zic manual page/FCTY/'" +
		" | /usr/bin/mysql -uroot -p'" + testDBRoot + "' mysql"
	assertExec(t, fake, 0, bashCommand(wantScript), "mysql")
}

// Ein Passwort mit Anführungszeichen darf die Pipeline nicht zerlegen.
func TestMySQLTzinfoQuotesPassword(t *testing.T) {
	fake := newFake()
	env := newEnv(fake)
	env.DBRoot = "pass'wort"

	MySQLTzinfoToSQL(context.Background(), env, Request{}, byID())

	call, _ := fake.LastExec()
	if !strings.Contains(call.Cmd[2], `-p'pass'\''wort' mysql`) {
		t.Errorf("Skript =\n%s", call.Cmd[2])
	}
}

func TestMySQLTzinfoError(t *testing.T) {
	fake := newFake()
	fake.ExecResults = []dockerclient.ExecResult{
		{ExitCode: 1, Output: []byte("ERROR 1045")},
	}

	got := MySQLTzinfoToSQL(context.Background(), newEnv(fake), Request{}, byID())

	want := `{
    "type": "error",
    "msg": "mysql_tzinfo_to_sql: error running command",
    "text": "ERROR 1045"
}`
	assertBody(t, got, ContentTypeJSON, want)
}
