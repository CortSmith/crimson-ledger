#!/usr/bin/env bash
# Build the viewer and scout for Linux into dist/. Needs: gcc pkg-config libwebkit2gtk-4.1-dev libgtk-3-dev.
# webview_go asks pkg-config for webkit2gtk-4.0 at build time; distros with only 4.1 get a throwaway shim .pc.
set -euo pipefail
cd "$(dirname "$0")/.."
if ! pkg-config --exists webkit2gtk-4.0; then
  pkg-config --exists webkit2gtk-4.1 || { echo "Install: sudo apt install gcc pkg-config libwebkit2gtk-4.1-dev libgtk-3-dev" >&2; exit 1; }
  shim="$(mktemp -d)"; trap 'rm -rf "$shim"' EXIT
  src="$(pkg-config --variable=pcfiledir webkit2gtk-4.1)/webkit2gtk-4.1.pc"
  cp "$src" "$shim/webkit2gtk-4.0.pc"   # unchanged copy: only the file name matters, the -l flags must keep pointing at 4.1
  export PKG_CONFIG_PATH="$shim${PKG_CONFIG_PATH:+:$PKG_CONFIG_PATH}"
fi
mkdir -p dist/lib
CGO_ENABLED=0 go build -o dist/crimson-ledger ./viewer/cmd/crimson-ledger
CGO_ENABLED=1 go build -tags webview -o dist/lib/crimson-ledger-ui ./viewer/cmd/crimson-ledger-ui
CGO_ENABLED=0 go build -o dist/crimson-scout ./logger/cmd/crimson-scout
CGO_ENABLED=1 go build -tags webview -o dist/lib/crimson-scout-ui ./logger/cmd/crimson-scout-ui
echo "built: dist/crimson-ledger and dist/crimson-scout (run these; they start the matching lib/*-ui)"
