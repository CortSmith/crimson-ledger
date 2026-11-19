#!/usr/bin/env bash
# Smoke test for the API image: builds it, runs it hardened on a random loopback port with a throwaway home,
# creates keys, posts a ping, restarts the container and checks the data survived. Needs docker, curl.
set -euo pipefail
cd "$(dirname "$0")/.."

IMG="crimson-ledger-api:smoke"
name="cl-smoke-$$"
work="$(mktemp -d)"
cleanup() { docker rm -f "$name" >/dev/null 2>&1 || true; rm -rf "$work"; }
trap cleanup EXIT
die() { echo "DOCKER SMOKE FAILED: $*" >&2; exit 1; }

command -v hadolint >/dev/null && hadolint Dockerfile.api Dockerfile.build || echo "(hadolint not installed, skipped)"
docker compose config -q || die "docker-compose.yml is invalid"
[ "$(grep -o 'golang:1.26-bookworm@sha256:[0-9a-f]*' Dockerfile.api | sort -u)" = \
  "$(grep -o 'golang:1.26-bookworm@sha256:[0-9a-f]*' Dockerfile.build | sort -u)" ] || die "Go image digests differ between Dockerfiles"

docker build -q -f Dockerfile.api -t "$IMG" . >/dev/null
size="$(docker image inspect "$IMG" --format '{{.Size}}')"
[ "$size" -lt 25000000 ] || die "image is $size bytes, limit 25 MB"

home="$work/home"; mkdir -m 700 "$home"
port="$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1])')"
hardening=(--read-only --cap-drop ALL --security-opt no-new-privileges:true --tmpfs /tmp --user "$(id -u):$(id -g)"
           -v "$home:/home/nonroot/.crimson-ledger")
run_api() { docker run -d --name "$name" "${hardening[@]}" -p "127.0.0.1:$port:8443" "$IMG" >/dev/null; }
wait_healthy() { for _ in $(seq 1 100); do curl -fsS "http://127.0.0.1:$port/v1/health" >/dev/null 2>&1 && return 0; sleep 0.1; done; return 1; }

start=$(date +%s.%N)
run_api; wait_healthy || { docker logs "$name" >&2; die "API never became healthy"; }
echo "cold start to healthy: $(echo "$(date +%s.%N) - $start" | bc) s"

keycmd() { docker run --rm "${hardening[@]}" --entrypoint /usr/local/bin/crimson-api "$IMG" keys "$@" 2>/dev/null; }
ingest="$(keycmd create --scope ingest --label smoke-ingest)"
view="$(keycmd create --scope view --label smoke-view)"
now_ms="$(($(date +%s) * 1000))"
body='{"batchId":"6f1c2e0e-8b5a-4a9e-9d34-0a7c1d2f3b44","source":"smoke","ruleset":"rp","sentAt":'$now_ms',
 "contacts":[{"guid":"Player-1329-0A1B2C3D","name":"Smoketest","raw":"Smoketest-RP-US","reaction":"hostile","lastSeen":'$now_ms',
  "fixes":[{"t":'$now_ms',"mapId":1411,"x":51.2,"y":40.7,"kind":"exact"}]}],
 "charts":[{"mapId":1411,"zone":"Durotar","x0":0,"y0":0,"x1":1,"y1":1}],"kills":[]}'
code="$(curl -sS -o "$work/post.out" -w '%{http_code}' -X POST "http://127.0.0.1:$port/v1/pings" \
  -H "Authorization: Bearer $ingest" -H 'Content-Type: application/json' --data "$body")"
[ "$code" = 200 ] || die "ingest POST $code: $(cat "$work/post.out")"

docker restart "$name" >/dev/null; wait_healthy || die "API not healthy after restart"
curl -fsS "http://127.0.0.1:$port/v1/sweep?ruleset=rp" -H "Authorization: Bearer $view" | grep -q Smoketest \
  || die "data did not survive the restart"

for _ in $(seq 1 40); do [ "$(docker inspect -f '{{.State.Health.Status}}' "$name")" = healthy ] && break; sleep 1; done
[ "$(docker inspect -f '{{.State.Health.Status}}' "$name")" = healthy ] || die "docker healthcheck is not healthy"
[ "$(docker inspect -f '{{.HostConfig.ReadonlyRootfs}}' "$name")" = true ] || die "rootfs is not read-only"
[ "$(docker inspect -f '{{.HostConfig.CapDrop}}' "$name")" = "[ALL]" ] || die "capabilities not dropped"
[ "$(docker inspect -f '{{json .HostConfig.PortBindings}}' "$name" | grep -o '"HostIp":"[^"]*"' | sort -u)" = '"HostIp":"127.0.0.1"' ] \
  || die "port is published beyond loopback"
docker history --no-trunc "$IMG" | grep -q 'clk_' && die "a key-looking string is in the image history"
docker exec "$name" /bin/sh -c true >/dev/null 2>&1 && die "the image has a shell"
echo "DOCKER SMOKE OK (image ${size} bytes)"
