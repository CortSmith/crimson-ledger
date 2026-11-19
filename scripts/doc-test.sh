#!/usr/bin/env bash
# Runs the library-install commands from docs/INSTALL.md in clean distro containers, then the packaged scout's
# --selftest under xvfb. Proves the install table is right per distro. Needs docker and a built dist/linux-amd64.
# Usage: scripts/doc-test.sh [ubuntu|fedora|arch ...]   (default: all three)
set -euo pipefail
cd "$(dirname "$0")/.."
[ -x dist/linux-amd64/crimson-scout ] || { echo "run 'make dist' first (needs dist/linux-amd64)" >&2; exit 1; }

# distro | image | doc-row prefix | extra packages the headless test itself needs (not from the docs)
run() {
  local name="$1" image="$2" row="$3" extra="$4" prep="$5"
  local cmd; cmd="$(grep -F "| $row |" docs/INSTALL.md | sed -E 's/.*`(sudo [^`]+)`.*/\1/; s/^sudo //')"
  [ -n "$cmd" ] || { echo "$name: no install command in INSTALL.md" >&2; return 1; }
  echo "== $name: $cmd"
  docker run --rm -v "$PWD/dist/linux-amd64:/app:ro" "$image" sh -c "
    $prep
    out=\$(/app/crimson-scout 2>&1); code=\$?
    [ \$code -eq 78 ] || { echo 'expected exit 78 without libraries, got '\$code; echo \"\$out\"; exit 1; }
    echo \"\$out\" | grep -q 'missing required libraries' || { echo 'missing-library message not shown'; exit 1; }
    $cmd
    $extra
    xvfb-run -a /app/crimson-scout --selftest | grep -qx 'SELFTEST OK'"
}
want=("$@"); [ ${#want[@]} -gt 0 ] || want=(ubuntu fedora arch)
for d in "${want[@]}"; do
  case "$d" in
    ubuntu) run ubuntu ubuntu:24.04 'Debian, Ubuntu' 'apt-get install -y xvfb >/dev/null' 'apt-get update >/dev/null' ;;
    fedora) run fedora fedora:latest 'Fedora' 'dnf install -y xorg-x11-server-Xvfb >/dev/null' ':' ;;
    arch)   run arch archlinux:latest 'Arch' 'pacman -S --noconfirm xorg-server-xvfb >/dev/null' 'pacman -Sy --noconfirm >/dev/null' ;;
    *) echo "unknown distro $d" >&2; exit 2 ;;
  esac
done
echo "doc-test OK"
