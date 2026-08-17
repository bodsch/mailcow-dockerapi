# mailcow-dockerapi

A Go rewrite of the `dockerapi` service that ships with
[mailcow-dockerized](https://github.com/mailcow/mailcow-dockerized) — the broker
between the mailcow UI and the Docker daemon.

It is a **drop-in replacement** for the Python implementation in
`original/dockerapi`: same routes, same JSON structures, same PubSub contract, and
the same log format. Swapping the image is enough. Where the behaviour differs
anyway, it is listed in [DEVIATIONS.md](DEVIATIONS.md).

The house style this repository shares with
[mailcow-watchdog](https://github.com/mailcow/mailcow-dockerized) is written down
in [CONVENTIONS.md](CONVENTIONS.md).

---

## Why

The Python version worked, but three things were hard:

- **Robustness.** Two stats endpoints waited in an unbounded `while True` for a
  Redis key that a failed collection would never write, so the request hung
  instead of failing. Shared state was mutated from several asyncio tasks without
  a lock.
- **Command construction.** Shell commands were assembled as strings and quoted
  at every point of use. Most of them do not need a shell at all.
- **Testability.** Nothing was testable without a Docker daemon. The bugs listed
  in [DEVIATIONS.md](DEVIATIONS.md) had been in production for years.

The runtime image went from a Python base with the docker, aiodocker, psutil,
FastAPI and uvicorn stacks plus an `openssl` entrypoint script to a single static
binary on distroless.

---

## Architecture

```
cmd/dockerapi/          startup sequence, wiring, shutdown
internal/config/        the mailcow.conf environment, typed and validated
internal/actions/       the 29 container operations and their registry
internal/api/           HTTP routes and response encoding
internal/dockerclient/  Docker access behind a narrow interface
internal/peers/         the container behind a remote address, for the log
internal/stats/         host and container measurements
internal/store/         the Redis cache
internal/pubsub/        the receiver for MC_CHANNEL
internal/tlsgen/        the self-signed server certificate
internal/metrics/       Prometheus collectors
internal/obs/           the /metrics, /healthz and /readyz endpoint
internal/logging/       the structured logger, including the Python format
original/               the replaced implementation, for reference
```

The heart of it is `internal/actions/registry.go`: a map from names like
`container_post__exec__mailq__delete` onto functions. mailcow builds those names
itself, in its PubSub messages and in its PHP code — they are part of the
interface. A test compares the registry against the method names in
`original/dockerapi/modules/DockerApi.py`; a missing action fails it.

---

## Interface

| Method | Path | Purpose |
|---|---|---|
| GET | `/host/stats` | host figures |
| GET | `/containers/json?all=<bool>` | every container as a map of id → inspect |
| GET | `/containers/{id}/json` | one container (running ones only) |
| POST | `/containers/{id}/{action}` | run an operation |
| POST | `/container/{id}/stats/update` | a container's measurements (path is singular) |

The status code is always 200; errors live in the body's `type` field.

For `POST /containers/{id}/exec` the body names the operation:

```json
{ "cmd": "mailq", "task": "flush" }
```

The same thing goes to `MC_CHANNEL` over Redis, there with the container **name**:

```json
{
  "api_call": "container_post",
  "post_action": "exec",
  "container_name": "postfix-mailcow",
  "request": { "cmd": "mailq", "task": "flush" }
}
```

---

## What's new

- **Prometheus metrics** on `:9394/metrics` (`DOCKERAPI_METRICS_LISTEN`): HTTP
  requests and latency per route pattern, actions by registry name and by the
  channel they arrived through, rejected calls by reason, PubSub messages by
  outcome, and statistics requests by kind.
- **`/healthz` and `/readyz`.** Liveness deliberately does *not* depend on Redis or
  the Docker daemon — otherwise an outage there would have the orchestrator kill
  the very thing reporting on it. Readiness stays negative until Redis answers.
- **Selectable log format** via `LOG_LEVEL` and `LOG_FORMAT`. The Python format
  stays the default so existing log processing keeps working.
- **Connection errors name the container.** A failed TLS handshake used to read
  `http: TLS handshake error from 172.22.1.12:58798: EOF` — an address that
  belongs to a different container after every restart. `internal/peers` resolves
  it through the container listing (`com.docker.compose.service` included) and
  falls back to the daemon's embedded DNS, so the line carries `peer_container`,
  `peer_service` and `peer_network`. See [Operation](#operation).
- **Graceful shutdown** on SIGINT/SIGTERM.
- **Startup waits for Redis** instead of answering requests it cannot serve.

A rising `mailcow_dockerapi_actions_rejected_total{reason="unknown_call"}` is the
signal that the frontend is asking for calls this build does not implement — that
is the metric to alert on after a mailcow upgrade.

---

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `REDIS_SLAVEOF_IP` | – | when set, this Redis applies instead of `redis-mailcow` |
| `REDIS_SLAVEOF_PORT` | `6379` | the matching port |
| `REDISPASS` | – | the Redis password |
| `REDIS_DB` | `0` | the database number |
| `REDIS_CHANNEL` | `MC_CHANNEL` | the channel jobs arrive on |
| `DBROOT` | – | the MySQL root password for the `system` actions |
| `DOCKER_HOST` | `unix:///var/run/docker.sock` | the Docker endpoint |
| `DOCKERAPI_LISTEN` | `:443` | the server's address |
| `DOCKERAPI_CERT` | `/app/dockerapi_cert.pem` | the certificate file |
| `DOCKERAPI_KEY` | `/app/dockerapi_key.pem` | the key file |
| `DOCKERAPI_STATS_TIMEOUT` | `30s` | the deadline when waiting for measurements |
| `DOCKERAPI_METRICS_LISTEN` | `:9394` | the metrics/health endpoint; empty disables it |
| `LOG_LEVEL` | `info` (`debug` under `DEV_MODE`) | `debug`, `info`, `warn`, `error` |
| `LOG_FORMAT` | `python` | `python`, `json` or `text` |

The first six names come from the Python implementation and are unchanged.

`DOCKERAPI_METRICS_LISTEN` is an address to **bind**, not a URL to scrape: `":9394"`, or
`"127.0.0.1:9394"` to keep it off the network. A URL such as
`http://127.0.0.1:9394/metrics` is rejected at startup — the paths of the three
endpoints are fixed and a scheme has nowhere to go.

Inside a container, `127.0.0.1` is the container's own loopback. A published port
(`-p 9394:9394`) forwards to the container's bridge address instead, finds
nothing listening there, and a scrape fails with a **reset connection** rather than
a refused one — which reads like a server problem and is not one. Bind `":9394"`
when the endpoint has to be reachable from outside the container.

`:9394` sits deliberately outside 9100-9999, the Prometheus project's fully
allocated exporter registry, and avoids every port mailcow uses internally. The
watchdog serves the same three endpoints one port below, on 9393.

If the certificate pair is missing, the service creates one at startup (RSA 4096,
SHA-256, 3650 days, `CN=dockerapi`, `subjectAltName=DNS:dockerapi`) — the same
values `docker-entrypoint.sh` passed to `openssl`.

---

## Development

```sh
make build     # static binary into bin/
make test      # race detector
make cover     # coverage profile
make lint      # golangci-lint
make vuln      # govulncheck
make ci        # fmt, vet, lint, vuln, test, build
make image     # container image
```

The `go` directive pins a patch version: `1.26.6` is the first release without the
five standard-library advisories govulncheck flags. mailcow-watchdog pins the
same one.

Tests that need a real Docker daemon carry the `integration` build tag and do not
run in the default pass:

```sh
make test-integration
```

The shell quoting and the demuxing of the Docker stream have fuzz targets; the
quoting checks its result against a real `sh`:

```sh
make fuzz
```

### Comparing against the original

The actual evidence for interchangeability is a side-by-side run: both
implementations against the same Docker daemon and the same Redis, receiving the
same requests, with the responses compared (volatile fields such as timestamps and
load normalised first).

```sh
make compare
```

Expected output: every route matches, with one difference marked `EXP.` — the bug
in the original described under 1.9 in [DEVIATIONS.md](DEVIATIONS.md).

---

## Operation

```sh
docker run --rm \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -e REDISPASS=... \
  -e DBROOT=... \
  -p 8443:443 \
  mailcow/dockerapi:latest
```

The service needs access to the Docker socket and thereby controls every container
on the host. It does not belong on an open network.

### Who is connecting

Connections come from other containers of the stack, so their addresses are
resolved before they are logged — the container name, its compose service and the
network are fields of the line:

```json
{"level":"WARN","msg":"the TLS handshake failed","component":"http",
 "err":"tls: client offered only unsupported versions: [301]",
 "peer_ip":"172.22.1.12","peer_port":"58798",
 "peer_container":"mailcowdockerized-watchdog-mailcow-1",
 "peer_service":"watchdog-mailcow","peer_network":"mailcowdockerized_mailcow-network"}
```

A peer that connects and hangs up without sending anything is a TCP port check —
a container healthcheck or a watchdog asking whether `:443` is open, which repeats
every few seconds for as long as the stack runs. Those lines are `debug`, not
`info`:

```sh
LOG_LEVEL=debug docker logs -f dockerapi-mailcow | grep peer_container
```

An address with no `peer_container` belongs to no container of this daemon: a
process in the host's namespace (it then shows the network's gateway address), or
something behind the host's NAT — worth a look, because nothing inside mailcow
reaches the service that way.

The mapping is cached for 30 s and rebuilt from `GET /containers/json`, so a peer
probing every few seconds costs one Docker call per half minute, not one per
probe.
