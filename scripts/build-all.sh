#!/usr/bin/env bash
# Builds every artifact into dist/ and writes dist/SHA256SUMS. Usage: scripts/build-all.sh [version]
#   dist/linux-<arch>/{crimson-ledger,crimson-scout,crimson-api,crimson-db,lib/*-ui}   native arch only (cgo UI needs it)
#   dist/windows-amd64/*.exe                                                          CGO_ENABLED=0
#   dist/lambda/{public,admin}.zip                                                    linux/arm64 bootstrap
# macOS is not built here (Apple SDK licence): run `make build-macos` on a Mac or in CI (T16).
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION="${1:-dev}"
LD="-s -w -X main.version=${VERSION}"
rm -rf dist && mkdir -p dist

if ! pkg-config --exists webkit2gtk-4.0 && pkg-config --exists webkit2gtk-4.1; then
  # webview_go asks for 4.0; a copy of the 4.1 .pc under the old name is enough (see scripts/build-linux.sh).
  shim="$(mktemp -d)"; trap 'rm -rf "$shim"' EXIT
  cp "$(pkg-config --variable=pcfiledir webkit2gtk-4.1)/webkit2gtk-4.1.pc" "$shim/webkit2gtk-4.0.pc"
  export PKG_CONFIG_PATH="$shim${PKG_CONFIG_PATH:+:$PKG_CONFIG_PATH}"
fi

arch="$(go env GOARCH)"
lin="dist/linux-$arch"; mkdir -p "$lin/lib"
CGO_ENABLED=0 go build -trimpath -ldflags="$LD" -o "$lin/crimson-ledger" ./viewer/cmd/crimson-ledger
CGO_ENABLED=0 go build -trimpath -ldflags="$LD" -o "$lin/crimson-scout"  ./logger/cmd/crimson-scout
CGO_ENABLED=0 go build -trimpath -ldflags="$LD" -o "$lin/crimson-api"    ./api/cmd/crimson-api
CGO_ENABLED=0 go build -trimpath -ldflags="$LD" -o "$lin/crimson-db"     ./database/cmd/crimson-db
CGO_ENABLED=1 go build -trimpath -tags webview -ldflags="$LD" -o "$lin/lib/crimson-ledger-ui" ./viewer/cmd/crimson-ledger-ui
CGO_ENABLED=1 go build -trimpath -tags webview -ldflags="$LD" -o "$lin/lib/crimson-scout-ui"  ./logger/cmd/crimson-scout-ui

win="dist/windows-amd64"; mkdir -p "$win/lib"
export GOOS=windows GOARCH=amd64 CGO_ENABLED=0
go build -trimpath -ldflags="$LD" -o "$win/crimson-ledger.exe" ./viewer/cmd/crimson-ledger
go build -trimpath -ldflags="$LD" -o "$win/crimson-scout.exe"  ./logger/cmd/crimson-scout
go build -trimpath -ldflags="$LD" -o "$win/crimson-api.exe"    ./api/cmd/crimson-api
go build -trimpath -ldflags="$LD" -o "$win/crimson-db.exe"     ./database/cmd/crimson-db
go build -trimpath -tags webview -ldflags="-H=windowsgui $LD" -o "$win/lib/crimson-ledger-ui.exe" ./viewer/cmd/crimson-ledger-ui
go build -trimpath -tags webview -ldflags="-H=windowsgui $LD" -o "$win/lib/crimson-scout-ui.exe"  ./logger/cmd/crimson-scout-ui
unset GOOS GOARCH CGO_ENABLED

scripts/build-lambda.sh
mkdir -p dist/lambda
for n in public admin; do (cd "api/build/$n" && zip -qj "$OLDPWD/dist/lambda/$n.zip" bootstrap); done

(cd dist && find . -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 sha256sum > SHA256SUMS)
echo "dist ready:"; (cd dist && find . -type f ! -name SHA256SUMS | sort)
