#!/usr/bin/env bash
# Convert the repo's zone PNGs (maps) to WebP in viewer/maps (needs cwebp: apt install webp). Usage: scripts/maps-to-webp.sh [quality]
set -euo pipefail
cd "$(dirname "$0")/.."
Q="${1:-80}"
command -v cwebp >/dev/null || { echo "cwebp is missing. Install: sudo apt install webp" >&2; exit 1; }
for f in maps/*.png; do cwebp -quiet -q "$Q" "$f" -o "viewer/maps/$(basename "${f%.png}").webp"; done
du -sh maps viewer/maps
