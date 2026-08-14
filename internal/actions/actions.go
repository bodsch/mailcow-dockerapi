// Package actions enthält die Container-Operationen, die DockerApi.py als
// Methoden mit zusammengesetzten Namen bereitstellte.
//
// In Python wurden diese Namen zur Laufzeit gebildet und per getattr
// aufgelöst (main.py:159). Der Namensraum ist Teil des öffentlichen Vertrags:
// die PubSub-Nachrichten von mailcow erzeugen exakt dieselben Bezeichner.
// Deshalb bildet Registry sie unverändert ab – nur eben nachschlagbar,
// prüfbar und testbar.
package actions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
)

// Env bündelt, was Actions zur Ausführung brauchen.
type Env struct {
	Docker dockerclient.API
	// DBRoot ist das MySQL-root-Passwort für die system-Actions.
	DBRoot string
	Logger *slog.Logger
	// Now erzeugt den Zeitstempel für maildir-Verschiebungen; nil bedeutet time.Now.
	Now func() time.Time
}

func (e Env) now() time.Time {
	if e.Now == nil {
		return time.Now()
	}
	return e.Now()
}

func (e Env) logger() *slog.Logger {
	if e.Logger == nil {
		return slog.New(discardHandler{})
	}
	return e.Logger
}

// Func ist die Signatur aller Actions.
type Func func(ctx context.Context, env Env, req Request, t dockerclient.Target) Result

// Result ist eine fertig kodierte HTTP-Antwort.
//
// Die Python-Fassung kannte drei Ausprägungen: ein JSON-Objekt, reinen Text
// (exec_run_handler mit 'utf8_text_only') und – nur bei system__df – einen
// nackten String, den FastAPI seinerseits als JSON-Wert kodierte.
type Result struct {
	ContentType string
	Body        []byte
}

// Content-Type-Werte, wie FastAPI sie gesetzt hat.
const (
	ContentTypeJSON = "application/json"
	ContentTypeText = "text/plain"
)

// Message ist die verbreitetste Antwortform: {"type": ..., "msg": ...}.
type Message struct {
	Type string `json:"type"`
	Msg  any    `json:"msg"`
}

// MessageWithText ergänzt Message um das Feld "text", das die mysql-Actions
// mitliefern. Das Feld wird bewusst auch dann ausgegeben, wenn es leer ist –
// json.dumps in Python hat es ebenfalls immer geschrieben.
type MessageWithText struct {
	Type string `json:"type"`
	Msg  any    `json:"msg"`
	Text string `json:"text"`
}

// Antworttypen, wie sie das mailcow-Frontend auswertet.
const (
	TypeSuccess = "success"
	TypeDanger  = "danger"
	TypeWarning = "warning"
	TypeInfo    = "info"
	TypeError   = "error"
)

// MsgCommandCompleted ist die Erfolgsmeldung aus exec_run_handler.
const MsgCommandCompleted = "command completed successfully"

// JSON kodiert v so, wie json.dumps(v, indent=4) es getan hätte.
//
// Zwei Feinheiten sind für die Austauschbarkeit entscheidend: Go maskiert
// standardmäßig <, > und & zu < und so weiter – Python tut das nicht,
// und die Zeichen kommen in Mailq-Ausgaben regelmäßig vor. Und der Encoder
// hängt einen Zeilenumbruch an, den json.dumps nicht erzeugt.
func JSON(v any) Result {
	var buf bytes.Buffer

	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "    ")

	if err := enc.Encode(v); err != nil {
		// Kann nur bei nicht kodierbaren Werten auftreten; die Antwort bleibt
		// dann im erwarteten Fehlerformat.
		return JSON(Message{Type: TypeDanger, Msg: err.Error()})
	}

	return Result{
		ContentType: ContentTypeJSON,
		Body:        bytes.TrimRight(buf.Bytes(), "\n"),
	}
}

// Text liefert eine Antwort mit Content-Type text/plain.
// Entspricht exec_run_handler('utf8_text_only', ...).
func Text(s string) Result {
	return Result{ContentType: ContentTypeText, Body: []byte(s)}
}

// Success entspricht {'type': 'success', 'msg': 'command completed successfully'}.
func Success() Result {
	return JSON(Message{Type: TypeSuccess, Msg: MsgCommandCompleted})
}

// Danger baut eine Fehlerantwort im Format des Originals.
func Danger(msg string) Result {
	return JSON(Message{Type: TypeDanger, Msg: msg})
}

// Dangerf ist Danger mit Formatierung.
func Dangerf(format string, args ...any) Result {
	return Danger(fmt.Sprintf(format, args...))
}

// execHandler entspricht exec_run_handler('generic', ...) aus DockerApi.py:617.
func execHandler(res dockerclient.ExecResult) Result {
	if res.ExitCode == 0 {
		return Success()
	}
	return Danger("command failed: " + string(res.Output))
}

// Fehlermeldungen für Fälle, die in Python zu einer NameError-Ausnahme oder
// einer Antwort mit dem Rumpf "null" geführt hätten.
const (
	MsgNoContainerFound = "no container found"
	MsgNoTarget         = "no or invalid id defined"
)

// firstContainer liefert den ersten Treffer der Auswahl.
//
// Die meisten Actions in DockerApi.py kehren innerhalb der Schleife über die
// Trefferliste zurück, verarbeiten also ausschließlich den ersten Container.
// Fand die Schleife nichts, lieferte die Funktion implizit None – der
// HTTP-Rumpf war dann "null". Hier gibt es stattdessen eine Fehlermeldung.
func firstContainer(ctx context.Context, env Env, t dockerclient.Target, all bool) (dockerclient.Container, *Result) {
	list, err := env.Docker.List(ctx, t, all)
	if err != nil {
		res := Danger(err.Error())
		return dockerclient.Container{}, &res
	}
	if len(list) == 0 {
		res := Danger(MsgNoContainerFound)
		return dockerclient.Container{}, &res
	}

	return list[0], nil
}

// discardHandler verwirft Log-Ausgaben, wenn kein Logger gesetzt ist.
type discardHandler struct{}

func (discardHandler) Enabled(context.Context, slog.Level) bool  { return false }
func (discardHandler) Handle(context.Context, slog.Record) error { return nil }
func (d discardHandler) WithAttrs([]slog.Attr) slog.Handler      { return d }
func (d discardHandler) WithGroup(string) slog.Handler           { return d }
