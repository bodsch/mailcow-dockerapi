#!/usr/bin/env bash
#
# Runs both implementations side by side against the same Docker daemon and the
# same Redis, then compares their responses.
#
# This is the actual evidence that the Go implementation can replace the Python
# one without the mailcow frontend noticing.
#
#   ./scripts/compare-with-python.sh
#
set -euo pipefail

cd "$(dirname "$0")/.."

NET=dockerapi-compare-net
REDIS=dockerapi-compare-redis
GO=dockerapi-compare-go
PY=dockerapi-compare-py
PROBE=dockerapi-compare-probe

GO_PORT=18443
PY_PORT=18444

cleanup() {
  docker rm -f "$GO" "$PY" "$REDIS" "$PROBE" >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
}
trap cleanup EXIT

if [ ! -f original/dockerapi/main.py ]; then
  echo "The original under original/dockerapi is missing — without it there is" >&2
  echo "no side-by-side comparison." >&2
  exit 1
fi

cleanup

echo "==> building the images"
docker build -q -t mailcow/dockerapi:compare-go . >/dev/null
docker build -q -t mailcow/dockerapi:compare-py ./original/dockerapi >/dev/null

echo "==> starting the environment"
docker network create "$NET" >/dev/null
docker run -d --name "$REDIS" --network "$NET" redis:7-alpine >/dev/null

# A container to read against.
docker run -d --name "$PROBE" --network "$NET" alpine:3.23 sleep 600 >/dev/null

common=(
  --network "$NET"
  -v /var/run/docker.sock:/var/run/docker.sock
  -e REDIS_SLAVEOF_IP="$REDIS"
  -e REDIS_SLAVEOF_PORT=6379
  -e REDISPASS=
  -e DBROOT=comparepass
)

docker run -d --name "$GO" "${common[@]}" -p "$GO_PORT:443" mailcow/dockerapi:compare-go >/dev/null
docker run -d --name "$PY" "${common[@]}" -p "$PY_PORT:443" mailcow/dockerapi:compare-py >/dev/null

echo "==> waiting for readiness"
for _ in $(seq 1 60); do
  if curl -sk --max-time 2 "https://localhost:$GO_PORT/containers/json" >/dev/null 2>&1 &&
     curl -sk --max-time 2 "https://localhost:$PY_PORT/containers/json" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

echo "==> comparing"
probe_id="$(docker inspect -f '{{.Id}}' "$PROBE")"
python3 scripts/compare-with-python.py "$probe_id"
