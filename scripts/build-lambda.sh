#!/usr/bin/env bash
# Builds the two Lambda bootstrap binaries (linux/arm64, static) into api/build/{public,admin}/ and zips nothing:
# `aws cloudformation package` zips and uploads them. Run through `docker compose run --rm lambda-package`
# so the toolchain is the same everywhere.
set -euo pipefail
cd "$(dirname "$0")/.."
for pair in "public:crimson-api-lambda" "admin:crimson-api-admin"; do
  out="api/build/${pair%%:*}"
  cmd="./api/cmd/${pair##*:}"
  rm -rf "$out"
  mkdir -p "$out"
  GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -tags lambda.norpc -o "$out/bootstrap" "$cmd"
  size="$(wc -c <"$out/bootstrap")"
  echo "built $out/bootstrap (${size} bytes)"
  [ "$size" -lt 15000000 ] || { echo "bootstrap is larger than 15 MB" >&2; exit 1; }
done
