#!/usr/bin/env bash
# Test for scripts/package.sh: builds a fake dist/, packages it, checks names, checksums and contents.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
t="$(mktemp -d)"; trap 'rm -rf "$t"' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }

d="$t/dist"
for plat in linux-amd64 windows-amd64 macos-universal; do
  ext=""; [ "$plat" = windows-amd64 ] && ext=".exe"
  mkdir -p "$d/$plat/lib"
  for b in crimson-ledger crimson-scout crimson-api crimson-db; do echo x > "$d/$plat/$b$ext"; done
  for b in crimson-ledger-ui crimson-scout-ui; do echo x > "$d/$plat/lib/$b$ext"; done
done
mkdir -p "$d/lambda"; echo x > "$d/lambda/public.zip"; echo x > "$d/lambda/admin.zip"
# Junk that must never ship:
echo secret > "$d/linux-amd64/.env"; echo db > "$d/linux-amd64/ledger.db"; mkdir -p "$d/linux-amd64/.git" "$d/linux-amd64/scripts/poc"
echo x > "$d/linux-amd64/.git/HEAD"; echo x > "$d/linux-amd64/scripts/poc/a.go"

out="$t/release"
"$here/package.sh" 1.2.3 "$d" "$out" > /dev/null

for f in crimson-ledger-1.2.3-linux-amd64.tar.gz crimson-ledger-1.2.3-windows-amd64.zip \
         crimson-ledger-1.2.3-macos-universal.tar.gz crimson-api-1.2.3-linux-amd64.tar.gz \
         crimson-api-1.2.3-windows-amd64.zip crimson-api-1.2.3-macos-universal.tar.gz \
         crimson-ledger-lambda-1.2.3.zip SHA256SUMS; do
  [ -f "$out/$f" ] || fail "missing $f"
done
(cd "$out" && sha256sum -c --strict SHA256SUMS > /dev/null) || fail "checksums do not verify"

list="$t/list"
for f in "$out"/*.tar.gz; do tar -tzf "$f"; done > "$list"
for f in "$out"/*.zip; do unzip -Z1 "$f"; done >> "$list"
if grep -Eq '(^|/)(\.git|\.env|scripts/poc)(/|$)|\.db$' "$list"; then fail "archive holds a forbidden file"; fi
tar -tzf "$out/crimson-ledger-1.2.3-linux-amd64.tar.gz" | grep -q 'lib/crimson-scout-ui$' || fail "ledger archive lacks lib/crimson-scout-ui"
tar -tzf "$out/crimson-ledger-1.2.3-linux-amd64.tar.gz" | grep -q 'crimson-api$' && fail "ledger archive must not hold the API"
tar -tzf "$out/crimson-api-1.2.3-linux-amd64.tar.gz" | grep -q 'crimson-db$' || fail "api archive lacks crimson-db"
unzip -Z1 "$out/crimson-ledger-lambda-1.2.3.zip" | grep -qx 'template.yaml' || fail "lambda bundle lacks template.yaml"
"$here/package.sh" 'bad version' "$d" "$t/x" 2>/dev/null && fail "accepted a bad version"
echo "package-test OK"
