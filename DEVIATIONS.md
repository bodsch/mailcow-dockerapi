# Deviations from the Python implementation

The Go implementation is meant as a replacement that needs no change to the
mailcow frontend: same routes, same JSON structures, same PubSub contract, status
code always 200. This document records where the behaviour differs anyway — and
why.

References point at `original/dockerapi/`.

## 1. Fixed bugs

These places could lead to an exception, an unintended command or a hanging
request in the Python implementation.

### 1.1 Waiting forever for measurements

`main.py:75` and `main.py:187` waited in a `while True` for a key to appear in
Redis, with no way out. When it never did — because Redis was unreachable, the
collection failed, or the container id was invalid — the request never returned.

**Now:** `internal/stats` gives up after `DOCKERAPI_STATS_TIMEOUT` (default 30 s)
and reports the collection's cause, falling back to `timeout waiting for stats`.

### 1.2 Invalid container id at `/container/{id}/stats/update`

The handler in `main.py:178` did not validate the id; the check lived in
`get_container_stats` (`DockerApi.py:547`), which wrote nothing to Redis for
invalid input. The result was the endless wait from 1.1.

**Now:** The check happens in the handler and the response is
`{"type": "danger", "msg": "no or invalid id defined"}`.

### 1.3 `postsuper` with no arguments

`DockerApi.py:88` (and three similar places) bound the result of `filter()` to a
variable and tested it for truthiness. A generator is always true in Python. When
`items` consisted of nothing but invalid queue ids, `postsuper` ran with an empty
argument list.

**Now:** An empty selection returns
`{"type": "danger", "msg": "no valid queue ids given"}`; no command is issued.

### 1.4 `traceback` was never imported

`DockerApi.py:615` called `traceback.print_exc` in the error path of
`exec_cmd_container` without importing the module — the error path failed with a
`NameError` of its own.

**Now:** Regular error handling with logging.

### 1.5 Unbound method name in the PubSub receiver

When `post_action: exec` arrived without `cmd`, `task` or `request`,
`api_call_method_name` stayed unbound in `main.py:232`; the following access
raised a `NameError`.

**Now:** The fields are validated up front; the log names the missing one
(`api call: cmd missing`, and so on).

### 1.6 Races on shared state

`host_stats_isUpdating` (a flag) and `containerIds_to_update` (a list) were
modified from several asyncio tasks without a lock. `list.remove` could also
raise a `ValueError` (`DockerApi.py:575`).

**Now:** `internal/stats` tracks running collections under a lock. Concurrent
requests trigger one collection and share its result. The tests run with `-race`.

### 1.7 Exceptions while parsing the ACL output

`DockerApi.py:441` split every line with `acl.split(maxsplit=1)` and indexed
`split('=')[1]`. A line without whitespace produced a `ValueError`, one without an
equals sign an `IndexError` — either aborted the whole request.

**Now:** Such lines are skipped and the remaining entries come through.

### 1.8 `postcat` without a match

`DockerApi.py:126` checked `postcat_return`, which was never assigned when the
match list was empty — a `NameError`.

**Now:** The response from 2.1 applies.

### 1.9 An unknown action reported an internal Python error

`main.py:159` supplied a fallback for a name that did not resolve:

```python
api_call_method = getattr(dockerapi, name, lambda container_id: Response(...))
return api_call_method(request_json, container_id=container_id)
```

The fallback takes a positional parameter named `container_id` but is called with
`request_json` in that position **and** with `container_id=` as a keyword. Python
raises `TypeError: got multiple values for argument 'container_id'`, and the
surrounding error handling passes that text on as `msg`.

The intended message `container_post - unknown api call` therefore never appeared.
The comparison run against the live Python implementation shows:

```
py: {"type": "danger", "msg": "post_containers.<locals>.<lambda>() got multiple values for argument 'container_id'"}
go: {"type": "danger", "msg": "container_post - unknown api call"}
```

**Now:** The message the original intended.

## 2. Visible behaviour changes

These change the response in cases where the Python implementation produced no
usable result.

### 2.1 No matching container

Most actions returned from inside the loop over the match list. When that list was
empty the function implicitly returned `None`, and the HTTP body was `null`.

**Now:** `{"type": "danger", "msg": "no container found"}`.

`stop`, `start` and `restart` are exempt: they still report success even when
nothing matched. mailcow uses them to stop containers that are already stopped.

### 2.2 Missing required fields

When a field was missing from the body, the action either ended in a `KeyError`
(caught into `{"type": "danger", "msg": "'fieldname'"}`) or returned `null`.

**Now:** A named message, such as
`{"type": "danger", "msg": "maildir is missing"}`.

### 2.3 `container_post__stats` includes `precpu_stats`

`DockerApi.py:76` took the first record of a running stream. That one carries no
previous sample, so no CPU load could be computed from it.

**Now:** The same query as for `/container/{id}/stats/update` is used — with
`precpu_stats` populated. The response therefore holds more, not less.

### 2.4 Quoted values in the ACL response

`DockerApi.py:423` quoted `id`, `user` and `mailbox` for the shell and then put
the quoted strings into the JSON response unchanged. A mailbox containing quotes
appeared there mangled.

**Now:** The quoting applies to the command only; the response carries the
original values.

### 2.5 Ordering in `/containers/json`

Python preserved the insertion order of the containers, while Go's JSON encoder
sorts object keys. This does not matter for the consumer — `json_decode` in PHP
returns an associative array.

### 2.6 Docker errors name the operation

Errors from the daemon are wrapped with the operation that failed, so the `msg`
field reads `listing containers: Cannot connect to the Docker daemon` rather than
the bare driver message. The `type` field and the status code are unchanged.

## 3. Deliberately preserved quirks

These look like bugs but stay as they are, because the frontend builds on them.

- **`system__df` returns a bare string.** FastAPI encoded the return value as JSON
  in turn, so the body reads `"50G,20G,..."` including the quotes — unlike every
  other action. On failure, `"0,0,0,0,0,0"`.
- **Status code always 200.** Errors come with 200 too; what is evaluated is the
  `type` field.
- **`maildir__move` appends `_index` to the destination only.** The source is
  `/var/vmail_index/<name>`, the destination `/var/vmail_index/<name>_index`
  (`DockerApi.py:363`).
- **`mailq__deliver` does not check the exit codes** and always reports
  `Scheduled immediate delivery` (`DockerApi.py:160`).
- **The action namespace** (`container_post__exec__mailq__delete` and so on) is
  preserved character for character; a test compares it against
  `original/dockerapi/modules/DockerApi.py`.
- **Field order and indentation** of the JSON responses match
  `json.dumps(..., indent=4)`.
- **The log format** stays `LEVEL:     message` by default, because operators have
  built their log processing around it. `LOG_FORMAT=json` switches to structured
  output.

## 4. Technical differences with no effect on the interface

- **One Docker client instead of two.** The Python implementation kept `docker`
  and `aiodocker` side by side.
- **Commands without a shell where possible.** Where no pipe, redirection or
  conditional is needed, the argv goes straight to `docker exec`. For
  `system__df`, `system__mysql_tzinfo_to_sql`, `maildir__cleanup`,
  `maildir__move` and the rspamd password change a shell is still required; there
  a tested function does the quoting (`internal/actions/shell.go`, fuzzed against
  a real `sh`).
- **`\W` in `maildir__cleanup`.** Python's `\W` is Unicode-aware, Go's is not. The
  character class is therefore spelled out as `[^\p{L}\p{N}_]+`, so mailboxes with
  non-ASCII letters end up under the same directory name.
- **The queue id check is slightly stricter.** Python's `$` in `^[0-9a-fA-F]+$`
  allows a trailing newline, Go's does not.
- **`isalnum` limited to ASCII.** Python's `str.isalnum()` accepts letters of every
  script. Docker ids are hexadecimal; nothing changes for valid input.
- **`REDIS_SLAVEOF_PORT` has to be a number.** A typo now fails at startup instead
  of surfacing later as a connection error.
- **The certificate comes from the program.** `docker-entrypoint.sh` and `openssl`
  are gone; `internal/tlsgen` produces the same material (RSA 4096, SHA-256, 3650
  days, `CN=dockerapi`, `O=mailcow`, `subjectAltName=DNS:dockerapi`). An existing
  pair is reused.
- **Bounded request body.** At most 4 MiB; FastAPI had no limit.
- **Graceful shutdown** on SIGINT and SIGTERM.
- **Startup waits for Redis** rather than answering requests it cannot serve.
- **Connection errors carry the peer's container.** uvicorn reported a failed TLS
  handshake as a bare address, and so did this implementation as long as
  `http.Server.ErrorLog` was unset — the line went out through `log.Default()`,
  which `slog.SetDefault` points at the handler, as one info line of plain text.
  `internal/peers` resolves the address instead, through `GET /containers/json`
  with the daemon's embedded DNS as a fallback, and the line becomes
  `the TLS handshake failed` with `peer_container`, `peer_service` and
  `peer_network` as fields. A handshake that ends in `EOF` or a reset is a TCP port
  check rather than a client — a healthcheck or a watchdog probing whether `:443`
  is open, which repeats every few seconds for as long as the stack runs. Those
  are logged at debug level, everything else at warn; the info level therefore
  stays quiet where it used to fill up.
