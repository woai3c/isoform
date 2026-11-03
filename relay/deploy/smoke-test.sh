#!/usr/bin/env bash
# Builds the relay image, runs it the way the hosted deploy does (plaintext
# behind a TLS proxy, JSON logs) and checks what a phone and a hub need:
#   - GET /v1/health answers 200 {"ok":true,...};
#   - a WebSocket upgrade on /v1/hub/ws without a credential answers 401;
#   - the same upgrade with a fresh hub credential completes (101);
#   - SIGTERM stops the container cleanly within its grace period.
# Needs docker and curl. Usage: relay/deploy/smoke-test.sh [image-tag]
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
image="${1:-isoform-relay:smoke}"
name="isoform-relay-smoke-$$"
port="${YANSU_SMOKE_PORT:-18791}"
base="http://127.0.0.1:$port"

cleanup() {
    docker rm -f "$name" >/dev/null 2>&1 || true
}
trap cleanup EXIT

fail() {
    echo "smoke: FAIL: $*" >&2
    echo "smoke: container log:" >&2
    docker logs "$name" >&2 2>&1 || true
    exit 1
}

echo "smoke: building $image from $here/.."
docker build -q -t "$image" "$here/.." >/dev/null

echo "smoke: starting $name on $base"
docker run -d --name "$name" \
    -p "127.0.0.1:$port:8080" \
    -e ISOFORM_RELAY_ADDR=0.0.0.0:8080 \
    -e ISOFORM_RELAY_BEHIND_TLS_PROXY=1 \
    -e ISOFORM_RELAY_TRUSTED_PROXIES=172.16.0.0/12,fdaa::/16 \
    -e ISOFORM_RELAY_CLIENT_IP_HEADER=Fly-Client-IP \
    -e ISOFORM_RELAY_LOG_FORMAT=json \
    --read-only --tmpfs /tmp \
    --tmpfs /var/lib/isoform:rw,size=64m,mode=0700,uid=10001,gid=10001 \
    "$image" >/dev/null

for _ in $(seq 1 50); do
    if curl -fsS -m 2 "$base/v1/health" >/dev/null 2>&1; then
        break
    fi
    if [ "$(docker inspect -f '{{.State.Running}}' "$name")" != "true" ]; then
        fail "container exited before answering"
    fi
    sleep 0.2
done

health="$(curl -sS -m 5 -w '\n%{http_code}' "$base/v1/health")" || fail "health request failed"
status="${health##*$'\n'}"
body="${health%$'\n'*}"
[ "$status" = "200" ] || fail "/v1/health answered $status: $body"
case "$body" in
    *'"ok":true'*) ;;
    *) fail "/v1/health body: $body" ;;
esac
echo "smoke: health ok ($body)"

ws_key="$(head -c 16 /dev/urandom | base64)"
ws_headers=(-H "Connection: Upgrade" -H "Upgrade: websocket" -H "Sec-WebSocket-Version: 13" -H "Sec-WebSocket-Key: $ws_key")

status="$(curl -sS -m 5 -o /dev/null -w '%{http_code}' "${ws_headers[@]}" "$base/v1/hub/ws")" || fail "anonymous upgrade request failed"
[ "$status" = "401" ] || fail "anonymous WebSocket upgrade answered $status, want 401"
echo "smoke: anonymous upgrade refused with 401"

hub_id="$(python3 -c 'import uuid; print(uuid.uuid4())' 2>/dev/null || cat /proc/sys/kernel/random/uuid)"
credential="$(head -c 32 /dev/urandom | base64 | tr '+/' '-_' | tr -d '=')"
# curl exits 28 (time-out) once the socket is open and silent; the status
# line has been written by then.
status="$(curl -sS -m 3 -o /dev/null -w '%{http_code}' "${ws_headers[@]}" \
    -H "X-Yansu-Hub: $hub_id" -H "Authorization: Bearer $credential" "$base/v1/hub/ws" 2>/dev/null || true)"
[ "$status" = "101" ] || fail "authenticated WebSocket upgrade answered '$status', want 101"
echo "smoke: authenticated upgrade switched protocols (101)"

status="$(curl -sS -m 5 -o /dev/null -w '%{http_code}' -H "Origin: https://evil.example" "$base/v1/health")" || fail "origin request failed"
[ "$status" = "403" ] || fail "browser-origin request answered $status, want 403"
echo "smoke: browser origin refused with 403"

started="$(date +%s)"
docker stop -t 20 "$name" >/dev/null
exit_code="$(docker inspect -f '{{.State.ExitCode}}' "$name")"
elapsed=$(( $(date +%s) - started ))
[ "$exit_code" = "0" ] || fail "container exited $exit_code on SIGTERM"
[ "$elapsed" -lt 15 ] || fail "shutdown took ${elapsed}s"
docker logs "$name" 2>&1 | grep -q '"msg":"Isoform relay shutting down"' || fail "no structured shutdown log line"
echo "smoke: clean shutdown in ${elapsed}s with JSON logs"
echo "smoke: PASS"
