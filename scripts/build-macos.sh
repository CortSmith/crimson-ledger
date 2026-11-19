#!/usr/bin/env bash
# Runs on macOS only. Builds arm64 + amd64 cgo binaries, joins them with lipo into dist/macos-universal, ad-hoc signs.
# Usage: scripts/build-macos.sh [version]   Needs Go 1.25+ and the Xcode Command Line Tools.
set -euo pipefail
cd "$(dirname "$0")/.."
[ "$(uname -s)" = Darwin ] || { echo "build-macos.sh runs on macOS only" >&2; exit 1; }
VERSION="${1:-$(cat VERSION)}"
LD="-s -w -X main.version=${VERSION}"
out="dist/macos-universal"; tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
rm -rf "$out"; mkdir -p "$out/lib"

# name|package|tags
targets=(
  "crimson-ledger|./viewer/cmd/crimson-ledger|"
  "crimson-scout|./logger/cmd/crimson-scout|"
  "crimson-api|./api/cmd/crimson-api|"
  "crimson-db|./database/cmd/crimson-db|"
  "lib/crimson-ledger-ui|./viewer/cmd/crimson-ledger-ui|webview"
  "lib/crimson-scout-ui|./logger/cmd/crimson-scout-ui|webview"
)
for t in "${targets[@]}"; do
  IFS='|' read -r name pkg tags <<<"$t"
  for arch in arm64 amd64; do
    mkdir -p "$tmp/$arch/$(dirname "$name")"
    GOOS=darwin GOARCH=$arch CGO_ENABLED=1 CC="clang -arch $([ $arch = amd64 ] && echo x86_64 || echo arm64)" \
      go build -trimpath ${tags:+-tags "$tags"} -ldflags="$LD" -o "$tmp/$arch/$name" "$pkg"
  done
  lipo -create -output "$out/$name" "$tmp/arm64/$name" "$tmp/amd64/$name"
  codesign --force --sign - "$out/$name"   # ad-hoc: no Developer ID; users clear quarantine (docs/INSTALL.md)
  echo "$name: $(lipo -info "$out/$name")"
done
# Only system frameworks may be linked.
if otool -L "$out/lib/crimson-ledger-ui" | tail -n +2 | grep -Ev '^\s*(/System/Library/|/usr/lib/)'; then
  echo "unexpected non-system library linked" >&2; exit 1
fi
