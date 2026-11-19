#!/usr/bin/env bash
# Packages dist/ (from build-all.sh, plus dist/macos-universal from build-macos.sh) into release files and
# writes SHA256SUMS (and SHA256SUMS.sig with minisign when MINISIGN_KEY points at a secret key).
# Usage: scripts/package.sh <version> [dist-dir] [out-dir]
set -euo pipefail
cd "$(dirname "$0")/.."
ver="${1:?usage: package.sh <version> [dist] [out]}"
dist="${2:-dist}"; out="${3:-release}"
[[ "$ver" =~ ^[0-9A-Za-z][0-9A-Za-z._-]*$ ]] || { echo "bad version '$ver'" >&2; exit 2; }
dist="$(cd "$dist" && pwd)"; mkdir -p "$out"; out="$(cd "$out" && pwd)"
rm -f "$out"/*.tar.gz "$out"/*.zip "$out"/SHA256SUMS "$out"/SHA256SUMS.sig

stage="$(mktemp -d)"; trap 'rm -rf "$stage"' EXIT
junk=(-name .git -o -name .env -o -name '*.db' -o -name '*.db-*' -o -name poc)

# stage <family> <platform> <binary-glob...>: copy only the named binaries (and lib/) into a clean directory.
pack() { # family platform ext archive-kind binaries...
  local fam="$1" plat="$2" ext="$3" kind="$4"; shift 4
  local src="$dist/$plat" name="$fam-$ver-$plat" dir
  [ -d "$src" ] || return 0
  dir="$stage/$name"; mkdir -p "$dir"
  for b in "$@"; do
    case "$b" in
      lib/*) mkdir -p "$dir/lib"; cp "$src/$b$ext" "$dir/lib/" ;;
      *)     cp "$src/$b$ext" "$dir/" ;;
    esac
  done
  [ -f LICENSE ] && cp LICENSE "$dir/"
  [ -f docs/INSTALL.md ] && cp docs/INSTALL.md "$dir/"
  find "$dir" \( "${junk[@]}" \) -prune -exec rm -rf {} +
  if [ "$kind" = zip ]; then (cd "$stage" && zip -qrX "$out/$name.zip" "$name")
  else tar -C "$stage" --sort=name --mtime='2020-01-01 00:00Z' --owner=0 --group=0 --numeric-owner -czf "$out/$name.tar.gz" "$name"; fi
}

for plat in linux-amd64 linux-arm64 macos-universal; do
  pack crimson-ledger "$plat" "" tar crimson-ledger crimson-scout lib/crimson-ledger-ui lib/crimson-scout-ui
  pack crimson-api    "$plat" "" tar crimson-api crimson-db
done
pack crimson-ledger windows-amd64 .exe zip crimson-ledger crimson-scout lib/crimson-ledger-ui lib/crimson-scout-ui
pack crimson-api    windows-amd64 .exe zip crimson-api crimson-db

if [ -d "$dist/lambda" ]; then
  l="$stage/lambda"; mkdir -p "$l"
  cp "$dist/lambda/public.zip" "$dist/lambda/admin.zip" api/deploy/template.yaml api/deploy/deploy.sh "$l/"
  (cd "$l" && zip -qrX "$out/crimson-ledger-lambda-$ver.zip" .)
fi

(cd "$out" && find . -maxdepth 1 -type f \( -name '*.tar.gz' -o -name '*.zip' \) -printf '%f\n' | sort | xargs sha256sum > SHA256SUMS)
if [ -n "${MINISIGN_KEY:-}" ]; then minisign -S -s "$MINISIGN_KEY" -m "$out/SHA256SUMS" -x "$out/SHA256SUMS.sig"; fi
ls -1 "$out"
