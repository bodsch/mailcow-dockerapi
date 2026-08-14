// Package actions holds the container operations DockerApi.py exposed as methods
// with composed names.
//
// In Python those names were built at runtime and resolved with getattr
// (main.py:159). The namespace is part of the public contract: mailcow's PubSub
// messages produce exactly the same identifiers. Registry therefore maps them
// unchanged — only now they are lookupable, checkable and testable.
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

// Env bundles what actions need in order to run.
type Env struct {
	Docker dockerclient.API
	// DBRoot is the MySQL root password for the system actions.
	DBRoot string
	Log    *slog.Logger
	// Now produces the timestamp for maildir moves; nil means time.Now.
	Now func() time.Time
}

func (e Env) now() time.Time {
	if e.Now == nil {
		return time.Now()
	}
	return e.Now()
}

func (e Env) logger() *slog.Logger {
	if e.Log == nil {
		return slog.New(discardHandler{})
	}
	return e.Log
}

// Func is the signature of every action.
type Func func(ctx context.Context, env Env, req Request, t dockerclient.Target) Result

// Result is a fully encoded HTTP response.
//
// The Python implementation had three shapes: a JSON object, plain text
// (exec_run_handler with 'utf8_text_only') and — only for system__df — a bare
// string, which FastAPI in turn encoded as a JSON value.
type Result struct {
	ContentType string
	Body        []byte
}

// Content types, as FastAPI set them.
const (
	ContentTypeJSON = "application/json"
	ContentTypeText = "text/plain"
)

// Message is the most common response shape: {"type": ..., "msg": ...}.
type Message struct {
	Type string `json:"type"`
	Msg  any    `json:"msg"`
}

// MessageWithText adds the "text" field the mysql actions carry. The field is
// emitted even when it is empty — json.dumps in Python always wrote it too.
type MessageWithText struct {
	Type string `json:"type"`
	Msg  any    `json:"msg"`
	Text string `json:"text"`
}

// Response types the mailcow frontend evaluates.
const (
	TypeSuccess = "success"
	TypeDanger  = "danger"
	TypeWarning = "warning"
	TypeInfo    = "info"
	TypeError   = "error"
)

// MsgCommandCompleted is the success message from exec_run_handler.
const MsgCommandCompleted = "command completed successfully"

// JSON encodes v the way json.dumps(v, indent=4) would have.
//
// Two details are decisive for interchangeability: Go escapes <, > and & by
// default and Python does not — and those characters turn up in mailq output all
// the time. And the encoder appends a newline that json.dumps does not produce.
func JSON(v any) Result {
	var buf bytes.Buffer

	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "    ")

	if err := enc.Encode(v); err != nil {
		// This can only happen for values that cannot be encoded; the response
		// then stays in the expected error format.
		return JSON(Message{Type: TypeDanger, Msg: err.Error()})
	}

	return Result{
		ContentType: ContentTypeJSON,
		Body:        bytes.TrimRight(buf.Bytes(), "\n"),
	}
}

// Text returns a response with content type text/plain. It corresponds to
// exec_run_handler('utf8_text_only', ...).
func Text(s string) Result {
	return Result{ContentType: ContentTypeText, Body: []byte(s)}
}

// Success matches {'type': 'success', 'msg': 'command completed successfully'}.
func Success() Result {
	return JSON(Message{Type: TypeSuccess, Msg: MsgCommandCompleted})
}

// Danger builds an error response in the original's format.
func Danger(msg string) Result {
	return JSON(Message{Type: TypeDanger, Msg: msg})
}

// Dangerf is Danger with formatting.
func Dangerf(format string, args ...any) Result {
	return Danger(fmt.Sprintf(format, args...))
}

// execHandler corresponds to exec_run_handler('generic', ...) from
// DockerApi.py:617.
func execHandler(res dockerclient.ExecResult) Result {
	if res.ExitCode == 0 {
		return Success()
	}
	return Danger("command failed: " + string(res.Output))
}

// Messages for cases that would have raised a NameError in Python, or produced a
// response body of "null".
const (
	MsgNoContainerFound = "no container found"
	MsgNoTarget         = "no or invalid id defined"
)

// firstContainer returns the first match of the selection.
//
// Most actions in DockerApi.py return from inside the loop over the match list and
// therefore only ever process the first container. When the loop found nothing the
// function implicitly returned None — the HTTP body was then "null". Here there is
// an error message instead.
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

// discardHandler drops log output when no logger is set.
type discardHandler struct{}

func (discardHandler) Enabled(context.Context, slog.Level) bool  { return false }
func (discardHandler) Handle(context.Context, slog.Record) error { return nil }
func (d discardHandler) WithAttrs([]slog.Attr) slog.Handler      { return d }
func (d discardHandler) WithGroup(string) slog.Handler           { return d }
