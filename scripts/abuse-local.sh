#!/usr/bin/env bash
# T19 local bootstrap: builds crimson-api, starts TWO throwaway instances (temp CRIMSON_LEDGER_HOME, SQLite),
# creates keys and runs the bad-actor suite against them, then stops everything.
#   main: very high rate limit, trusts X-Forwarded-For so each test gets its own client IP (the failed-auth lockout is per IP)
#   lock: ignores X-Forwarded-For, only for the brute-force lockout test
#   rate: default 120/min limit, for the rate-limit test (B17)
# Usage: scripts/it-bootstrap.sh [go test args...]     env: IT_KEEP=1 keeps the work dir and prints its path.
set -euo pipefail
umask 077
cd "$(dirname "$0")/.."

work="$(mktemp -d)"; pids=()
cleanup() { for p in "${pids[@]:-}"; do [ -z "$p" ] || kill "$p" 2>/dev/null || true; done
  if [ -n "${IT_KEEP:-}" ]; then echo "kept $work" >&2; else rm -rf "$work"; fi; }
trap cleanup EXIT
die() { echo "it-bootstrap: $*" >&2; exit 1; }

go build -o "$work/crimson-api" ./api/cmd/crimson-api
port() { python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1])'; }

start() { # name rate trust_proxy -> sets ${name}_URL ${name}_HOME
  local name="$1" rate="$2" trust="$3" p h
  p="$(port)"; h="$work/$name"; mkdir -p "$h" && chmod 700 "$h"
  cat > "$h/api.json" <<JSON
{ "version": 1, "store": "sqlite", "listen": "127.0.0.1:$p", "db_path": "$h/data/ledger.db", "retention_days": 30,
  "max_body_bytes": 262144, "tls_cert": "", "tls_key": "", "rate_limit_per_min": $rate, "trust_proxy_headers": $trust }
JSON
  chmod 600 "$h/api.json"
  CRIMSON_LEDGER_HOME="$h" "$work/crimson-api" serve 2>"$h/serve.log" & pids+=("$!")
  for _ in $(seq 1 80); do curl -fsS "http://127.0.0.1:$p/v1/health" >/dev/null 2>&1 && break; sleep 0.1; done
  curl -fsS "http://127.0.0.1:$p/v1/health" >/dev/null || die "$name did not start: $(cat "$h/serve.log")"
  eval "${name}_URL=http://127.0.0.1:$p; ${name}_HOME=$h"
}
key() { CRIMSON_LEDGER_HOME="$1" "$work/crimson-api" keys create --scope "$2" --label "it-$2" 2>/dev/null; }

start main 1000000 true
start rate 120 false
start lock 1000000 false   # X105: its client IP gets locked out; nothing else may use this instance

export CL_TARGET=local CL_URL="$main_URL" CL_TRUST_XFF=1 CL_I_UNDERSTAND_THIS_WRITES_DATA=yes
export CL_INGEST_KEY="$(key "$main_HOME" ingest)" CL_VIEW_KEY="$(key "$main_HOME" view)"
export CL_FLOOD_INGEST_KEY="$(key "$main_HOME" ingest)"   # X404 exhausts its hourly cap; no other test may share it
export CL_RATE_URL="$rate_URL" CL_RATE_INGEST_KEY="$(key "$rate_HOME" ingest)"
export CL_LOCK_URL="$lock_URL"
export CL_API_PIDS="${pids[*]}"
export CL_API_BIN="$work/crimson-api" CL_HOME="$main_HOME"
# keys live in the environment of this process only; nothing is written to disk.
go test -tags integration -count=1 -v "${@:-./tests/abuse/...}"
