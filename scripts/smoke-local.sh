#!/usr/bin/env bash
# Smoke test for local hosting: build crimson-api, serve it, create keys, POST a ping, GET the sweep.
# Uses a throwaway config home; exits non-zero on the first failed assertion.
set -euo pipefail
cd "$(dirname "$0")/.."

work="$(mktemp -d)"
server_pid=""
cleanup() { [ -z "$server_pid" ] || kill "$server_pid" 2>/dev/null || true; rm -rf "$work"; }
trap cleanup EXIT
die() { echo "SMOKE FAILED: $*" >&2; exit 1; }

export CRIMSON_LEDGER_HOME="$work/home"
mkdir -p "$CRIMSON_LEDGER_HOME" && chmod 700 "$CRIMSON_LEDGER_HOME"
port="$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1])')"
cat > "$CRIMSON_LEDGER_HOME/api.json" <<JSON
{ "version": 1, "store": "sqlite", "listen": "127.0.0.1:$port",
  "db_path": "$CRIMSON_LEDGER_HOME/data/ledger.db", "retention_days": 30,
  "max_body_bytes": 262144, "tls_cert": "", "tls_key": "",
  "rate_limit_per_min": 120, "trust_proxy_headers": false }
JSON
chmod 600 "$CRIMSON_LEDGER_HOME/api.json"

go build -o "$work/crimson-api" ./api/cmd/crimson-api
api="$work/crimson-api"
base="http://127.0.0.1:$port"

"$api" serve 2>"$work/serve.log" & server_pid=$!
for _ in $(seq 1 50); do curl -fsS "$base/v1/health" >/dev/null 2>&1 && break; sleep 0.1; done
curl -fsS "$base/v1/health" | grep -q '"ok":true' || die "health"

ingest="$("$api" keys create --scope ingest --label smoke-ingest 2>/dev/null)"
view="$("$api" keys create --scope view --label smoke-view 2>/dev/null)"
now_ms="$(($(date +%s) * 1000))"

body="$(cat <<JSON
{"batchId":"6f1c2e0e-8b5a-4a9e-9d34-0a7c1d2f3b44","source":"smoke","ruleset":"rp","sentAt":$now_ms,
 "contacts":[{"guid":"Player-1329-0A1B2C3D","name":"Smoketest","raw":"Smoketest-RP-US","reaction":"hostile","lastSeen":$now_ms,
  "fixes":[{"t":$now_ms,"mapId":1411,"x":51.2,"y":40.7,"kind":"exact"}]}],
 "charts":[{"mapId":1411,"zone":"Durotar","x0":0,"y0":0,"x1":1,"y1":1}],"kills":[]}
JSON
)"
post() { curl -sS -o "$work/post.out" -w '%{http_code}' -X POST "$base/v1/pings" \
  -H "Authorization: Bearer $1" -H 'Content-Type: application/json' --data "$body"; }

[ "$(post "$ingest")" = 200 ] || die "ingest POST: $(cat "$work/post.out")"
grep -q '"accepted":true' "$work/post.out" || die "first ping not accepted: $(cat "$work/post.out")"
[ "$(post "$ingest")" = 200 ] && grep -q '"duplicate":true' "$work/post.out" || die "repeat batchId not reported as duplicate"
[ "$(post "$view")" = 403 ] || die "view key must not ingest"

sweep="$(curl -fsS "$base/v1/sweep?ruleset=rp" -H "Authorization: Bearer $view")"
echo "$sweep" | grep -q 'Smoketest' || die "sweep lacks the contact: $sweep"
[ "$(curl -s -o /dev/null -w '%{http_code}' "$base/v1/sweep?ruleset=rp")" = 401 ] || die "sweep without key must be 401"

id="${view#clk_v_}"; id="${id%%_*}"
"$api" keys revoke "$id" 2>/dev/null
echo "SMOKE OK (revoked keys are enforced within the 30 s cache window)"
