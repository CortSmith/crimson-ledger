# Crimson Ledger

A native desktop viewer (Linux, Windows, macOS) and a live logging client for World of Warcraft combat logs,
talking to an API you can run on your own machine or on AWS.

| Part | What it is |
|---|---|
| `crimson-ledger` | the viewer window; reads everything from the API |
| `crimson-scout` | reads your combat log and uploads it live |
| `crimson-api`, `crimson-db` | the API server and its database tools |

## 5 minutes

1. Download your system's package from the releases page and [check it](docs/INSTALL.md#1-check-your-download).
2. Start the API: `./crimson-api keys create --scope ingest --label me`, `... --scope view ...`, then `./crimson-api serve`.
3. Start `crimson-scout`, paste address and ingest key, press **Test connection**, then **Start**.
4. Start `crimson-ledger`, paste address and view key.

## Docs

- [INSTALL.md](docs/INSTALL.md): per-system install, required libraries, first run, uninstall
- [OPERATIONS.md](docs/OPERATIONS.md): run the API, deploy to AWS, backups, keys, upgrades, releasing
- [PLAN.md](docs/PLAN.md): design and task list

## Build

`make check` (needs Go and the dev tools), `make dist` (Docker: Linux, Windows and Lambda artifacts into `dist/`),
`scripts/build-macos.sh` on a Mac, then `scripts/package.sh <version>`.

## Zone maps

`maps/` holds the source zone PNGs (made with `extract_maps.py` from a game install). `scripts/maps-to-webp.sh` (or `.py`) converts them to the WebP files the viewer bundles in `viewer/maps/`.
