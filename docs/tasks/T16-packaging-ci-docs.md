# T16: Packaging, CI (including macOS and Windows runners), install docs

**Depends on:** T15 · **Blocks:** T17-T21 · **PLAN.md sections:** [2 D5](../PLAN.md#d5-native-ui-system-webview-with-embedded-pages-no-http-server-no-fallback), [9 libraries](../PLAN.md#9-required-system-libraries-end-users-and-builders), [7 security](../PLAN.md#7-security-baseline-applies-to-every-task-the-audit-phase-checks-it), [10 workflow](../PLAN.md#10-mandatory-workflow-for-every-task-tdd-with-proof-of-concept-first)

## Goal
Turn builds into downloadable, verifiable packages for Linux, Windows and macOS; run the checks that Docker on Linux cannot (macOS build, Windows
WebView2 run, real windows); write the user-facing install docs including the required-library table.

## Outputs
```
.circleci/config.yml                          extended (existing file): jobs check, build-linux, build-windows, build-macos, it-local, release
App/scripts/package.sh                        tar.gz (linux, macos), zip (windows) + SHA256SUMS (+ cosign/minisign signature if key configured)
App/scripts/build-macos.sh                    runs on macOS: universal binary via lipo (arm64 + amd64), ad-hoc codesign
App/scripts/windows-smoke.ps1                 runs on the Windows executor
App/docs/INSTALL.md                           per-OS install, required libraries, first run, config location, uninstall
App/docs/OPERATIONS.md                        run API locally, deploy to AWS, backups, key rotation, upgrading
App/README.md                                 5-minute overview + links
```
The repo already uses CircleCI (`.circleci/config.yml`); extend it rather than adding another CI system (cheapest: stay on the free plan; macOS executors consume credits, so run
`build-macos` only on tags and manual triggers).

## Package layout
```
crimson-ledger-<ver>-linux-amd64.tar.gz     crimson-ledger, crimson-scout, lib/{crimson-ledger-ui,crimson-scout-ui}, LICENSE, INSTALL.md
crimson-ledger-<ver>-windows-amd64.zip      same with .exe
crimson-ledger-<ver>-macos-universal.tar.gz same; or .app bundles (decide in PoC 3)
crimson-api-<ver>-<os>-<arch>.tar.gz|zip    crimson-api, crimson-db (API is a separate download: not everyone hosts)
crimson-ledger-lambda-<ver>.zip             public.zip, admin.zip, template.yaml, deploy.sh
SHA256SUMS, SHA256SUMS.sig
```

## Step 0: PoC (`scripts/poc/t16/`)
1. **macOS cgo build** of a hello-WebView with `webview_go` on a macOS executor (CircleCI `macos` with Xcode) or a Mac: PASS = window opens under CI's screen capture, or at least `otool -L` shows only system frameworks. Record Xcode/macOS image version and credits per run (cost gate: keep < 5 min).
2. **Universal binary**: build arm64 and amd64 cgo binaries and `lipo -create`; run `lipo -info`. PASS = both architectures.
3. **Gatekeeper**: an unsigned downloaded binary is blocked; decide the cheapest honest route: ad-hoc signing + documented `xattr -d com.apple.quarantine` instructions (free) vs Apple Developer ID ($99/yr, notarisation). Default = free route with clear instructions; record the decision and the exact user steps.
4. **Windows executor**: run the launcher on a machine with WebView2 (passes) and, in a clean VM image without it, see the missing-runtime dialog and exit 78. PASS = both observed (screenshots saved in the log). Verify `icacls` of the config file (T01 follow-up) shows only the owner.
5. **Windows SmartScreen** reputation note: unsigned binaries warn; record the free mitigation (publish SHA256SUMS, document "More info -> Run anyway") and the paid option (code-signing cert) as a Follow-up, not built.
6. **Linux package smoke** in clean containers `ubuntu:24.04`, `fedora:latest`, `archlinux:latest`: extract, run launcher: without libs -> exact message + 78; after installing the documented packages -> UI starts under `xvfb-run` and exits cleanly via `--selftest` flag (exits 0 after loading the page and calling one binding). PASS per distro. **This is the proof that the install-instruction table in PLAN 9 is correct per distro**: record the exact package names that worked and update PLAN 9 if any differ.
7. **Release reproducibility**: build twice with `-trimpath` and identical inputs; SHA256 of Go binaries equal. PASS prints hashes (pure-Go ones; cgo ones may differ: record).

## Step 1: RED
- `scripts/package.sh` test: given a fake `dist/`, produces the exact file names, checksums verify (`sha256sum -c`), archives contain no `.git`, no `*.db`, no `.env`, no `scripts/poc`.
- `selftest` flag in both UI binaries (written in T13/T14 follow-up if missing): `--selftest` loads the page, calls `getSettings` through the real window, prints `SELFTEST OK`, exits 0; test asserts it never opens a network connection.
- CI config lint: `circleci config validate`; a Go test parses `.circleci/config.yml` and asserts the macOS job has `filters: tags` (cost guard).
- Docs tests: every command in `INSTALL.md` code fences tagged `sh` is run in the distro containers (PoC 6 automated) via `scripts/doc-test.sh`; links checked with `lychee` or a small Go link checker.

## Step 2: GREEN
CircleCI jobs: `check` (docker, `docker build --target check .`), `it-local` (T18 local target via compose), `build-linux` (docker), `build-windows` (docker, plus a `windows` executor job for `windows-smoke.ps1`), `build-macos` (macos executor, tags only), `release` (collect artifacts, `package.sh`, checksums, attach to the GitHub release with `gh`). Secrets (signing key, GitHub token) live in CircleCI contexts, never in the repo; jobs that use them run only on protected branches/tags.
`INSTALL.md` must contain, per OS: the library table from PLAN 9, the exact message users see when a library is missing, where config lives (`~/.crimson-ledger/`), how to get an API key (local `crimson-api keys create`, or the deploy output), and how to verify the download checksum.

## Step 3: REFACTOR checklist
- [ ] CI config uses YAML anchors/commands to avoid duplicated steps; no inline multi-line shell > 10 lines (move to `scripts/`).
- [ ] Docs written for a non-developer: short sentences, copy-paste commands, no jargon without a one-line explanation, headings match user questions ("I get a message about missing libraries").
- [ ] Version stamped once (`VERSION` file) and flows to `-X main.version`, package names, template tags.

## Step 4: AUDIT
- check.sh green; `circleci config validate`; `shellcheck scripts/*.sh`; `doc-test` green in all three Linux containers.
- Security: release artifacts have checksums (and signature if configured); CI secrets only in protected contexts; third-party CI images/orbs pinned by digest/version; the release job refuses to run from a fork PR; macOS/Windows jobs do not upload artifacts from untrusted branches.
- Cost: free-plan credit use estimated per release (macOS minutes x rate); documented in OPERATIONS.md.

## Done when
- [ ] Tagging `vX.Y.Z` yields all six package families with checksums.
- [ ] The three Linux distros and Windows pass the missing-library test and the "installed" selftest; macOS build runs a selftest on the runner.
- [ ] INSTALL.md and OPERATIONS.md verified by `doc-test`.

## PoC log
Status 2026-11-17: **done in code, nothing needing a real window, Docker distro run, macOS or Windows machine has been run yet.**

Done and verified here:
- `internal/ui/selftest` (+ `Terminate` on `window.Window`), wired into both `*-ui` mains: unit tests green; `go vet -tags webview` clean. Not yet run in a real window.
- `scripts/package.sh` + `scripts/package-test.sh`: prints `package-test OK` (names, `sha256sum -c`, no `.git`/`.env`/`*.db`/`scripts/poc`, API kept out of the viewer archive).
- `internal/archtest/ci_test.go`: macOS job is tags-only, release job is tags-only, in the `app-release` context and waits for macOS and Windows.
- `.circleci/config.yml` parses as YAML (jobs check, build-linux, windows-smoke, build-macos, app-release; workflow `app`, tags `app-vX.Y.Z` so it never collides with the radar-live `vX.Y.Z` release). `circleci config validate` NOT run (CLI missing).
- `VERSION` (0.1.0), `docs/INSTALL.md`, `docs/OPERATIONS.md`, `README.md`.

Written, not run: `scripts/build-macos.sh`, `scripts/windows-smoke.ps1`, `scripts/doc-test.sh` (needs `make dist` then Docker pulls of ubuntu/fedora/archlinux), `shellcheck` (not installed here).

## Open PoCs (need the real environment)
1. macOS cgo build, universal `lipo -info`, Gatekeeper route (default chosen: ad-hoc sign + `xattr -dr com.apple.quarantine`, documented in INSTALL.md). Xcode `16.4.0` / `m4pro.medium` in the CI config are unconfirmed guesses; check against the account's available images.
2. Windows: WebView2 present and missing runs, `icacls` check.
3. `scripts/doc-test.sh` in all three distros; fix PLAN 9 if package names differ.
4. Release reproducibility (two `-trimpath` builds, equal SHA256).

## Follow-ups
- F16-1: `main.version` is only declared in `crimson-api`; add it to viewer, scout and db mains so `-X main.version` takes effect (VERSION flows into names and ldflags only).
- F16-2: pin `win` orb / machine image / Xcode versions after the first real run; no `lint` of `INSTALL.md` links yet (T16 Step 1 link checker).
- F16-3: `doc-test.sh` runs only the library-install command per distro, not every `sh` fence.
- Code-signing certificates (Windows) and Developer ID notarisation (macOS) only if the user decides to pay.
