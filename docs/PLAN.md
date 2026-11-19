# Crimson Ledger: rebuild plan (master file)

Rebuild of the Windows-only `go-radar` ("wow-radar") as **Crimson Ledger**: a native desktop viewer for Linux,
Windows and macOS that reads *everything* from an HTTP API, a native logging client ("scout") that uploads live
combat-log data, an API that runs on your own machine or on AWS, and a database.

This file holds everything shared by all tasks. **Every task has its own file in [tasks/](tasks/)**, written so
that a subagent can be given *only that file plus the PLAN.md sections it names* and nothing else.

Status of the plan: **T00-T11 done except everything that needs a real AWS account (T09 and T10 Follow-ups)** (govulncheck needs a newer Go 1.25.x patch toolchain; repo-wide lint debt in T03/T04 code is F08-1), T11 done (API client), T12 done in code (UI shell; real-window PoCs 1-3, 5, 6, 8, 9 still open, see its PoC log), T13 done in code except real-window run and open PoCs/coverage (see its Follow-ups F13-1..6), T14 done in code (viewer: poller, maps, bindings, page; see its Follow-ups F14-1..6), T15 done (Dockerfiles, compose, smoke; trivy/arm64 PoCs open, see F15-1..4), T16 done in code (selftest flag, package.sh, CI config, install/ops docs; macOS, Windows, distro-container and real-window PoCs open, see its PoC log), T17 harnesses done (`internal/testkit`, `faultapi`; PoCs 1, 2, 6, 7, `make test-matrix` and migration of old helpers open, see F17-1..8), T18 done in code for the local target (A/B/C green, shuffled twice; AWS half, A19, kin-openapi/Schemathesis open, see F18-1..8; F18-3 is a contract inconsistency to decide), T19 started: local bad-actor suite green for classes 1-5 except X106/X107/X505 and most of 8 (`make abuse-local`; X601-X605 done against apiclient; AWS, logger/viewer MITM, timing, scanners open, see F19-1..5), next finish T19 then T20. Date written: 2026-11-17.

---------------------------------------------------------------------------------------------------------

## 0. Reading order for a subagent

1. This file, sections named in your task's `Context` block (anchors below). Do not read the rest.
2. Your task file in `tasks/`.
3. Only the source files your task's `Inputs` lists.

## 1. Goals and non-goals

**Goals**

| # | Goal |
|---|---|
| G1 | `viewer` (binary `crimson-ledger`): native window on Linux, Windows, macOS. All data comes from the API. No local history, no log parsing. |
| G2 | `logger` (binary `crimson-scout`): tails the WoW combat log, parses it on the player's machine, uploads small batches to the API live. Server address, API key and other settings are saved and always visible. The key shows as asterisks with an eye button that reveals it. |
| G3 | `api` (binary `crimson-api`): one codebase, two hosting modes. (a) **Local**: run on your own machine, config in `~/.crimson-ledger/`. (b) **AWS**: Lambda + DynamoDB, deployed with one command. |
| G4 | `database`: schema, migrations and a small admin CLI (`crimson-db`). The API talks to the database directly. |
| G5 | Docker: one image runs API + database; one image builds everything on any device; a top-level `docker-compose.yml` runs the tasks. |
| G6 | Cheapest viable options everywhere, with security best practices in all three runtime parts (viewer, logger, api). |
| G7 | All app config lives in `~/.crimson-ledger/` (Windows: `%USERPROFILE%\.crimson-ledger\`). |
| G8 | Viewer and logger **never** fall back to a locally hosted website. If a required system library is missing they exit with a clear message naming the missing libraries and how to install them. |

**Non-goals** (do not build): accounts/passwords/sign-up, a web (browser) viewer, the Python tracker, the
heatmap, ads, importing old `radar-history*.json` files (possible later task), multi-region AWS, a CDN.

## 2. Decisions and why (each was chosen for lowest cost, then security)

### D1. Endpoint style between apps and API: REST + JSON over HTTPS, polled

| Option | Verdict | Reason |
|---|---|---|
| **REST/JSON, versioned `/v1`, ETag + `since` cursor polling, batched gzip POST for ingest** | **Chosen** | Runs behind an API Gateway HTTP API and a plain `net/http` server with the same handler. No idle connections to pay for. `304 Not Modified` replies cost almost nothing. Debuggable with `curl`. |
| gRPC | Rejected | Needs HTTP/2 end to end; an API Gateway HTTP API cannot do it for Lambda. Heavier client deps. |
| GraphQL | Rejected | Resolver overhead and query-cost abuse surface for no gain on ~6 fixed reads. |
| WebSocket / SSE | Rejected | Needs a persistent connection; on AWS that means API Gateway WebSocket (billed per minute connected) or streamed Lambda. The radar is fine with a 5-30 s poll. |

Rules that follow: one handler built from `net/http` (`http.Handler`), reused by the local server and the Lambda
adapter; every read is a `GET` that supports `If-None-Match`; every write is one idempotent `POST` with a
client-generated `batchId`; errors use one JSON shape (section 5).

### D2. Parsing happens in the logger, not the API

The logger parses the combat log locally (port of `go-radar/internal/parser` + `tracker`) and uploads compact
*pings* (contacts, fixes, charts, kills). Why: far less data to send, less Lambda CPU time to pay for, raw logs
(which contain your own character data) never leave the machine. The API therefore **never trusts** a ping: it
re-validates every field (section 5.4).

### D3. Database: one `Store` interface, two implementations

| Host | Engine | Cost |
|---|---|---|
| Local and Docker | **SQLite** via `modernc.org/sqlite` (pure Go, no cgo) in one file | $0 |
| AWS | **DynamoDB single table, provisioned capacity inside the always-free tier** (<= 25 WCU and 25 RCU across table + index, 25 GB), TTL for retention | $0 at hobby load. On-demand has no free request allowance, so it is *more* expensive here; T21 measures both and keeps provisioned unless throttling is observed. Billing mode is a template parameter. |

Both pass the *same* contract test suite (`internal/store/storetest`), so behaviour cannot drift. Rejected: RDS /
Aurora (always-on cost, $12-45+/month), Lambda+EFS+SQLite (EFS cost, locking pain), Postgres in Docker for local
(heavier than needed). The `database/` folder owns the SQLite schema, migrations, the DynamoDB table definition
and `crimson-db` (migrate, status, backup, vacuum, key admin).

### D4. AWS shape: API Gateway HTTP API + Lambda (arm64) + DynamoDB + SSM, deployed by `aws cloudformation deploy`

* Lambda `provided.al2023` on **arm64** (cheapest per ms), 128 MB. Go binary named `bootstrap`.
* **API Gateway HTTP API** (1 USD per million requests; REST API and CloudFront rejected) in front of the Lambda. It gives a custom domain (regional ACM certificate, optional Route 53 record) and a stage throttle, and TLS is terminated by AWS. The Function URL (free) was dropped because it cannot carry a custom domain. Decided 2026-11-18.
* **No WAF** (about $5+/month minimum). Abuse is bounded instead by: reserved concurrency (default 5), the
  per-key + per-IP counters in DynamoDB (section 7), body limit 256 KiB, and an AWS Budget alarm.
* Template is plain SAM syntax; `aws cloudformation package/deploy` handles it, so **no SAM CLI and no Node/CDK**
  is required (the AWS CLI is the only tool). Template is at `api/deploy/template.yaml`.
* Key administration on AWS is a second, **IAM-only** Lambda invoked with `aws lambda invoke`; no admin route
  exists on the public URL.
* Secrets: the HMAC pepper for key hashing lives in **SSM Parameter Store (standard, free)**, read at cold start.

### D5. Native UI: system WebView with *embedded* pages, no HTTP server, no fallback

The viewer needs the existing canvas radar (`go-radar/web/app.js`) and the logger needs a form with a masked
field. Both use the OS WebView with HTML embedded in the binary and loaded with `SetHtml` (no `localhost`
listener, no `file://`, no external URLs). Go functions are exposed to the page with `Bind`.

| OS | WebView | Go binding | cgo? |
|---|---|---|---|
| Windows 10 1809+/11 | Edge **WebView2** (Evergreen runtime) | `github.com/jchv/go-webview2` (already used in this repo) | no |
| macOS 11+ | system **WKWebView** | `github.com/webview/webview_go` | yes (clang) |
| Linux | **WebKitGTK 4.1** + GTK 3 | `github.com/webview/webview_go` | yes (gcc, dev headers) |

Real WebView code is behind build tag `webview` (T00) so tests, vet and the AUDIT gate run on any machine without GTK/WebKit
headers; release builds pass `-tags webview`.

Fixed *and tested* behaviours (see T12):

* **Launcher + UI split.** `crimson-ledger` (launcher) is pure Go (`CGO_ENABLED=0`) and starts first. It checks
  for the libraries with `dlopen` (Linux), the registry (Windows) or the OS version (macOS). Only if they are
  present does it start the sibling `lib/crimson-ledger-ui`. This is required because a cgo binary dies in the
  dynamic loader with an unreadable message *before* any of our code runs. The same pair exists for `crimson-scout`.
* Failure output: stderr always, plus a native dialog where one is guaranteed (Windows `MessageBoxW`, macOS
  `osascript`, Linux `zenity`/`kdialog` if present). Exit code `78` (`EX_CONFIG`).
* Maps are bundled **in the viewer binary** (WebP) and handed to the page as `data:` URIs through a bound
  function, because map images are static assets, not database data. Chart *rectangles* come from the API.
* **macOS cannot be built inside a Linux container** (Apple SDK licence). Linux and Windows binaries build in
  Docker on any host; macOS binaries build with `make build-macos` on a Mac or on the CI macOS runner (T16).
  The API (pure Go) builds for all three OSes in Docker.

### D6. API keys: scoped, hashed, no accounts

* Two scopes: `ingest` (logger) and `view` (viewer). A key has a scope, a label, an optional expiry, a state.
* Format: `clk_<scope-letter>_<id>_<secret>`, e.g. `clk_i_k3f9a2x1_<43 base64url chars>`; 32 random bytes of secret.
* Stored as `HMAC-SHA256(pepper, secret)`; lookup by `id`; compared with `subtle.ConstantTimeCompare`. (A slow
  password hash is not needed for 256-bit random secrets; a pepper protects a leaked database.)
* Sent as `Authorization: Bearer <key>` only. Never in URLs, never logged.

### D7. Config format: JSON in `~/.crimson-ledger/`, 0600 files, 0700 directory

JSON is in the standard library (no dependency). See section 4 for every file and field.

## 3. Repository layout (repo root; this was `App/` before the repo was reduced to this one project)

```
./
  docs/PLAN.md, docs/tasks/T00..T17-*.md       <- this plan
  go.mod                                        module crimsonledger, go 1.25
  docker-compose.yml
  Dockerfile.api                                runs api + database (T15)
  Dockerfile.build                              builds every service on any device (T15)
  Makefile                                      thin wrappers: test, lint, build-*, check
  scripts/                                      check.sh (the AUDIT gate), poc/ helpers
  internal/                                     shared by all apps
    config/      ~/.crimson-ledger resolution, load/save, validation          (T01)
    secret/      Mask, Redact, key generation/parsing                         (T01, T05)
    domain/      Contact, Fix, Ping, Chart, Kill, Ruleset + validation        (T02)
    store/       Store interface; storetest/ contract; sqlitestore/; dynamostore/  (T04, T09)
    apiserver/   http.Handler, middleware, handlers                           (T05-T07)
    apiclient/   https-only client with retry, ETag cache                     (T11)
    ui/          window, html bundle + CSP, preflight (missing libs), launcher (T12)
    logparse/    parser, tracker, tailer ported from go-radar                 (T13)
  api/
    cmd/crimson-api/           serve, keys, version                           (T08)
    cmd/crimson-api-lambda/    public Lambda handler                          (T10)
    cmd/crimson-api-admin/     IAM-only admin Lambda                          (T10)
    deploy/template.yaml, deploy/deploy.sh                                    (T10)
  database/
    migrations/0001_init.sql ...                                              (T03)
    dynamodb/table.json, dynamodb/README.md                                   (T09)
    cmd/crimson-db/                                                           (T03)
  logger/
    cmd/crimson-scout/ (launcher)  cmd/crimson-scout-ui/ (UI)  web/           (T13)
  viewer/
    cmd/crimson-ledger/ (launcher) cmd/crimson-ledger-ui/ (UI) web/ maps/     (T14)
```

Imports: apps import `internal/*`; apps never import each other. `api` must not import `internal/ui` or
`internal/logparse`. An architecture test (T00) enforces this with `go list -deps`.

## 4. Config: `~/.crimson-ledger/`

```
~/.crimson-ledger/            0700   (override: env CRIMSON_LEDGER_HOME, used by tests and Docker)
  api.json                    0600   API server (local mode)
  logger.json                 0600   scout
  viewer.json                 0600   viewer
  data/ledger.db              0600   SQLite file (local mode); data/ is 0700
  data/backups/               0700
```

Rules (implemented once in `internal/config`, T01): home = `os.UserHomeDir()` joined with `.crimson-ledger`;
create with 0700; files written atomically (write temp in same dir, `fsync`, `rename`); on read, refuse a config
whose permissions are group/world readable on Unix (message: `chmod 600 <file>`); on Windows set an owner-only
ACL with `icacls` equivalent via `golang.org/x/sys/windows`; unknown JSON fields are an error (catches typos);
every file has `"version": 1`; defaults are filled in memory, a missing file is created with defaults on first run.

`api.json`
```json
{ "version": 1, "store": "sqlite", "listen": "127.0.0.1:8443",
  "db_path": "~/.crimson-ledger/data/ledger.db", "retention_days": 30,
  "max_body_bytes": 262144, "tls_cert": "", "tls_key": "",
  "rate_limit_per_min": 120, "trust_proxy_headers": false }
```
`logger.json`
```json
{ "version": 1, "server_url": "https://example.lambda-url.us-east-1.on.aws",
  "api_key": "clk_i_...", "source_name": "my-pc", "logs_dir": "",
  "ca_file": "", "ruleset": "auto", "upload_interval_ms": 2000, "batch_max_contacts": 200,
  "tail_from_start": false, "auto_start_upload": true }
```
`viewer.json`
```json
{ "version": 1, "server_url": "https://example.lambda-url.us-east-1.on.aws",
  "api_key": "clk_v_...", "ca_file": "", "ruleset": "auto", "poll_interval_s": 15,
  "orientation": "std", "selected_map": 0, "show_history": false }
```
`ca_file` (optional, logger and viewer) is a PEM file added to the trusted roots, used for a self-signed LAN API
(`crimson-api tls-selfsign`, T08). There is deliberately no "skip verification" option.
`server_url` must be `https://`, except loopback hosts (`127.0.0.1`, `::1`, `localhost`) which may be `http://`.
`api_key` is the only secret field. The UI shows it as `••••••••••••` (fixed length, so the length does not leak)
until the eye is pressed; see T12 for the exact reveal protocol.

## 5. Shared API contract (single source of truth)

Base path `/v1`. All bodies UTF-8 JSON. Times are integer **epoch milliseconds, UTC**. Map coordinates are floats
0-100 (percent of the map rectangle, as in go-radar). GUIDs match `^(Player|Creature|Pet|Vehicle)-\d+-[0-9A-F]{4,16}$`.

### 5.1 Endpoints

| Method + path | Scope | Purpose | Notes |
|---|---|---|---|
| `GET /v1/health` | none | liveness | returns `{"ok":true,"version":"..."}` only; no store access |
| `POST /v1/pings` | ingest | upload one batch | `Content-Encoding: gzip` allowed; idempotent on `batchId`; max 256 KiB after decompression |
| `GET /v1/sweep` | view | live contacts | query `ruleset`, optional `zone` (mapId) or `guid`, `maxAgeSec` (default 900); `ETag` |
| `GET /v1/players` | view | name search | `q` (>=2 chars, normalized prefix of any name word), `limit` (<=50) |
| `GET /v1/players/{guid}/trail` | view | position history | optional `map`, `limit` (<=2000), `since` |
| `GET /v1/bounty` | view | kill ranking | `ruleset`, `limit` (<=100) |
| `GET /v1/charts` | view | map rectangles | `ETag`, long cache |

Key administration is **not HTTP**: local = `crimson-api keys ...`, AWS = IAM-only admin Lambda (T10).

### 5.2 Ping body

```json
{ "batchId": "6f1c2e0e-8b5a-4a9e-9d34-0a7c1d2f3b44", "source": "my-pc", "ruleset": "pvp", "sentAt": 1731800000000,
  "contacts": [ { "guid": "Player-1329-0A1B2C3D", "name": "Mc Cheeki", "raw": "Mc-Cheeki-PvP-US",
                  "reaction": "hostile", "lastSeen": 1731799999000,
                  "fixes": [ { "t": 1731799999000, "mapId": 1411, "x": 51.2, "y": 40.7, "kind": "exact" } ] } ],
  "charts": [ { "mapId": 1411, "zone": "Durotar", "x0": 0, "y0": 0, "x1": 1, "y1": 1 } ],
  "kills":  [ { "t": 1731799990000, "killer": "Player-1329-0A1B2C3D", "victim": "Player-1329-0A1B2C3E", "mapId": 1411 } ] }
```
Limits (enforced by `domain.ValidatePing`, T02): `batchId` UUIDv4; `source` 1-32 chars `[A-Za-z0-9_.-]`;
`ruleset` in `normal|pvp|rp|rppvp`; contacts <= 200; fixes per contact <= 50; charts <= 50; kills <= 200;
`name` <= 24 runes and `raw` <= 64 runes with no control characters; `reaction` in `friendly|hostile|neutral`;
`kind` in `exact|approx`; `t` within [now-24h, now+5min]; coordinates finite and in [0,100]; **unknown JSON
fields are rejected**; duplicate `batchId` returns `200 {"accepted":false,"duplicate":true}`.
Response `200 {"accepted":true,"contacts":N,"fixes":N,"kills":N}`.

### 5.3 Read bodies (shapes only)

```json
// GET /v1/sweep
{ "serverTime": 1731800000000, "ruleset": "pvp",
  "contacts": [ { "guid": "...", "name": "...", "reaction": "hostile", "lastSeen": 0,
                  "mapId": 1411, "x": 51.2, "y": 40.7, "kind": "exact" } ] }
// GET /v1/players?q=mc   -> { "players": [ { "guid","name","reaction","fixes","firstSeen","lastSeen" } ] }
// GET /v1/players/{guid}/trail -> { "guid","name","total","maps":[{"mapId","zone","count"}],"fixes":[{t,mapId,x,y,kind}] }
// GET /v1/bounty -> { "ranking": [ { "guid","name","kills","lastKill" } ] }
// GET /v1/charts -> { "charts": [ { "mapId","zone","x0","y0","x1","y1" } ] }
```

### 5.4 Errors and status codes

```json
{ "error": { "code": "invalid_ping", "message": "contacts[3].fixes[0].x out of range", "requestId": "a1b2c3d4" } }
```
| Status | `code` | When |
|---|---|---|
| 400 | `invalid_request`, `invalid_ping` | bad JSON, unknown field, failed validation |
| 401 | `unauthorized` | missing/invalid key (same message for unknown vs wrong secret) |
| 403 | `forbidden`, `key_disabled` | wrong scope, revoked or expired key |
| 404 | `not_found` | unknown guid |
| 405 | `method_not_allowed` | |
| 413 | `too_large` | body over limit (also decompressed size) |
| 415 | `unsupported_media_type` | POST without `application/json` |
| 429 | `rate_limited` | with `Retry-After` seconds |
| 500 | `internal` | never leaks internals; details go to the log with `requestId` |

Every response carries `X-Request-Id`, `Cache-Control: no-store` (except ETag reads: `private, max-age=0, must-revalidate`),
`X-Content-Type-Options: nosniff`.

## 6. Shared vocabulary

Reuse the names from `radar-live` / `radar-cloud/NAMING.md`: **contact** (tracked player), **fix** (one position),
**trail** (a contact's fixes), **ping** (an uploaded batch), **sweep** (a read of live contacts), **chart** (map +
rectangle), **ruleset**, **bounty**. New: **scout** (the logger), **ledger** (the database as a whole).
Wire names in section 5 are frozen once T05 lands.

## 7. Security baseline (applies to every task; the AUDIT phase checks it)

Common
* Go modules pinned (`go.sum` committed); `govulncheck ./...` clean; `gosec` clean or each finding justified inline with `// #nosec Gxxx: reason`.
* No secret in flags, URLs, logs, panics or error strings. All logging goes through `secret.Redact`.
* Every network call has a timeout; every read is size-limited; every JSON decode uses `DisallowUnknownFields`.
* `go test -race ./...` passes. Fuzz tests exist for every parser of untrusted input (ping JSON, log lines, key format, config).

API
* TLS-only on AWS (API Gateway). Local mode binds `127.0.0.1` by default; binding a non-loopback address
  requires `tls_cert`/`tls_key` **or** an explicit `--insecure-lan` flag that prints a warning every start.
* Auth middleware runs before routing to handlers, before body reads; constant-time compare; scope checked per route.
* Rate limits: per key and per client IP (token bucket in memory locally; DynamoDB counter with TTL on AWS).
  Failed-auth counter per IP: 10 failures/5 min then 429 for 5 min. `trust_proxy_headers` is off by default.
* Server timeouts: `ReadHeaderTimeout` 5 s, `ReadTimeout` 15 s, `WriteTimeout` 15 s, `IdleTimeout` 60 s, `MaxHeaderBytes` 16 KiB.
* Least-privilege IAM: the public Lambda may only `dynamodb:GetItem/PutItem/UpdateItem/Query/BatchWriteItem` on the one table and `ssm:GetParameter` on one parameter.
* Container: distroless `nonroot`, read-only root filesystem, `cap_drop: ALL`, `no-new-privileges`, no shell.
* Budget alarm at $5 and CloudWatch log retention 14 days (cost + data minimisation).
* Security is **verified, not assumed**: T19 (bad-actor suite) and T20 (threat model + review) are release gates.

Logger
* Config file 0600; key never printed; HTTPS enforced (loopback excepted); redirects to a different host are refused;
  system CA store only (no `InsecureSkipVerify` anywhere, enforced by a lint grep in `scripts/check.sh`).
* Reads the log file read-only; never writes to the WoW folder; never follows a path outside `logs_dir`.
* Upload queue is bounded (memory + 5 MB on disk max); on overflow oldest batches drop and the UI says so.

Viewer
* WebView hardened: CSP meta (`default-src 'none'`, nonce for the one script, `img-src data:`, `connect-src 'none'`);
  no navigation away from the embedded page (navigation handler denies everything); dev tools off in release builds;
  no `eval`; all API data inserted with `textContent`, never `innerHTML`.
* The API key never enters the page unless the eye was pressed; all HTTP is done in Go, the page only calls bound functions.

## 8. Cost estimate (hobby load: 1 logger, 1-3 viewers, 15 s polls; verify prices before deploying)

| Item | Local | AWS |
|---|---|---|
| Compute | your PC | Lambda arm64 128 MB: always-free tier 1 M requests + 400k GB-s per month. 3 viewers at 15 s = about 17 k requests/day = 0.5 M/month, inside the free tier |
| API front door | none | API Gateway HTTP API: 1 USD per million requests (about 1 to 2 USD a month with a scout running around the clock) |
| Database | SQLite file: $0 | DynamoDB provisioned 10 WCU + 10 RCU (+ index 5/5): inside the always-free 25/25 units, 25 GB free: $0 |
| Secrets | file 0600 | SSM standard parameter: $0 |
| Logs | files | CloudWatch Logs: 5 GB ingest free; 14-day retention |
| Estimate | **$0** | **about $0 / month** (worst case a few cents if free tier is exceeded) |
| Forbidden because they cost money at idle | n/a | NAT gateway, VPC for Lambda, API Gateway, WAF, RDS/Aurora, KMS customer keys, provisioned concurrency, X-Ray, custom CloudWatch metrics, PITR. `scripts/cost-lint.sh` (T21) fails the build if the template contains any of them. |

Cost guards: 304 replies via ETag, batched pings (2 s flush, <= 200 contacts), TTL retention (30 d default),
reserved concurrency cap, Budget alarm.

## 9. Required system libraries (end users and builders)

**Run (end users).** Missing ones are detected by the launcher and reported (D5).

| OS | Required to run | Install |
|---|---|---|
| Linux | `libwebkit2gtk-4.1.so.0`, `libgtk-3.so.0` | Debian/Ubuntu: `sudo apt install libwebkit2gtk-4.1-0 libgtk-3-0` · Fedora: `sudo dnf install webkit2gtk4.1 gtk3` · Arch: `sudo pacman -S webkit2gtk-4.1 gtk3` · openSUSE: `sudo zypper install libwebkit2gtk-4_1-0 libgtk-3-0` |
| Windows | Microsoft Edge **WebView2 Runtime** (built into Windows 11 and current Windows 10) | https://developer.microsoft.com/microsoft-edge/webview2/ (Evergreen Standalone) |
| macOS | macOS 11 or newer (WKWebView is part of the OS) | none |

**Build.**

| OS | Needed |
|---|---|
| Any (Docker route) | Docker 24+ with buildx. Produces Linux + Windows binaries and all API artifacts. |
| Linux (native) | Go 1.25+, `gcc`, `pkg-config`, `libwebkit2gtk-4.1-dev`, `libgtk-3-dev` |
| Windows (native) | Go 1.25+ only (`CGO_ENABLED=0`) |
| macOS (native) | Go 1.25+, Xcode Command Line Tools (`xcode-select --install`) |
| Deploying to AWS | AWS CLI v2 with credentials |

Proven already on this machine (2026-11-17): the WebKit/GTK **runtime** libs are installed, the `-dev` headers are
**not** (so even Linux builds go through Docker here), `aws` and `node` exist, `sam` does not (not needed).

## 10. Mandatory workflow for every task (TDD with proof-of-concept first)

Each task file repeats these five phases with its own concrete content. **A phase may not start until the
previous phase's exit command is green.** Agents record evidence in the task file as they go.

| Phase | What you do | Exit criterion (record the output in the task file) |
|---|---|---|
| **0. PoC** | Write the smallest throwaway program in `scripts/poc/<task>/` (own `go.mod` allowed) that proves each risky call works on this machine *before* it goes into the codebase. Run it. Paste the command and its output under `PoC log`. | Every PoC assertion in the task prints `PASS`. If one fails, stop, write the finding under `PoC log`, and revise the design; do not code around it. |
| **1. RED** | Write the real tests first: table-driven unit tests, contract tests, fuzz seeds. Run them. They must fail for the right reason (compile error for a missing symbol is fine, wrong assertion is not). | `go test ./<pkg>/... 2>&1 \| tail` shows failures, saved to `PoC log`/`RED log`. |
| **2. GREEN** | Write the least code that passes. Port proven PoC code, do not rewrite it. | `go test -race ./<pkg>/...` green. |
| **3. REFACTOR** | Make it human readable (checklist below). Behaviour must not change: tests stay green and are not edited except to rename. | tests green + checklist ticked in the task file. |
| **4. AUDIT** | Run `scripts/check.sh <pkg>` (gofmt, `go vet`, `staticcheck`, `golangci-lint` with `gosec`, `govulncheck`, `-race`, coverage gate, secret-grep, import rules) and the task's security checks. | `scripts/check.sh` exits 0; output saved. |

**Performance gate (part of AUDIT, section 13).** Every task that has a hot path adds benchmarks and records them against the
budget table in section 13. Speed work may never remove or weaken a validation, auth check, limit or timeout: any optimisation
PR must show the security tests (T19) still pass.

**Delete the PoC** (or move it to `scripts/poc/archive/` with its log) once the real tests cover the same assertion.

Refactor checklist (applies to every task)
- [ ] Every exported identifier has a doc comment starting with its name; every package has a `doc.go` or package comment.
- [ ] Functions <= 40 lines and <= 4 parameters (use an options struct beyond that); no nesting deeper than 3.
- [ ] Names say what, not how (`parseFixLine`, not `doStuff`); no abbreviations except `ctx`, `err`, `req`, `resp`.
- [ ] No magic numbers: limits are named constants with a comment saying where the number comes from.
- [ ] Errors wrapped with `%w` and context (`fmt.Errorf("load logger config: %w", err)`); sentinel errors for caller decisions.
- [ ] No global mutable state; time, randomness and the filesystem are injected (`clock`, `io.Reader`, `fs.FS`) so tests are deterministic.
- [ ] Interfaces declared where used (consumer side), small (<= 5 methods).
- [ ] No dead code, no commented-out code, no TODO without an issue id in the task file's `Follow-ups`.
- [ ] Tests read as documentation: `TestXxx_WhenY_ReturnsZ`, one behaviour per table row, a `name` per row.
- [ ] `go doc ./<pkg>` reads well top to bottom.

Definition of Done (task level): phases 0-4 logged in the task file, `scripts/check.sh` exits 0, outputs and
Done-when checkboxes in the task file ticked, no open items except named Follow-ups.

## 11. Task index and dependencies

| Task | Title | Depends on | Delivers |
|---|---|---|---|
| [T00](tasks/T00-scaffold-and-tooling.md) | Scaffold, module, tooling, AUDIT gate | none | layout, `scripts/check.sh`, arch test |
| [T01](tasks/T01-config-home.md) | `~/.crimson-ledger` config + secret helpers | T00 | `internal/config`, `internal/secret` |
| [T02](tasks/T02-domain-model.md) | Domain model + ping validation (done) | T00 | `internal/domain` |
| [T03](tasks/T03-database-sqlite-schema.md) | Database folder: SQLite schema, migrations, `crimson-db` | T00, T01 | `database/` |
| [T04](tasks/T04-store-interface-sqlite.md) | `Store` interface, contract suite, SQLite store | T02, T03 | `internal/store/*` |
| [T05](tasks/T05-api-auth-keys.md) | API core: router, auth, keys, rate limits, headers | T01, T04 | `internal/apiserver` core |
| [T06](tasks/T06-api-ingest.md) | `POST /v1/pings` | T02, T04, T05 | ingest handler |
| [T07](tasks/T07-api-read-endpoints.md) | Read endpoints (sweep, players, trail, bounty, charts) | T04, T05 | read handlers |
| [T08](tasks/T08-api-local-hosting.md) | Local hosting: `crimson-api serve`, keys CLI, TLS | T05, T06, T07 | `api/cmd/crimson-api` |
| [T09](tasks/T09-dynamodb-store.md) | DynamoDB store, same contract suite | T04 | `internal/store/dynamostore` |
| [T10](tasks/T10-aws-lambda-deploy.md) | Lambda handler, admin Lambda, SAM template, deploy | T05-T07, T09 | `api/deploy/*` |
| [T11](tasks/T11-api-client.md) | Shared API client | T02, T05 (contract only) | `internal/apiclient` |
| [T12](tasks/T12-ui-shell-preflight.md) | UI shell: window, CSP, masked-key widget, preflight, launcher | T01 | `internal/ui` |
| [T13](tasks/T13-logger-scout.md) | Logger "scout" app | T01, T02, T11, T12 | `logger/` |
| [T14](tasks/T14-viewer-crimson-ledger.md) | Viewer "crimson-ledger" app | T01, T11, T12 | `viewer/` |
| [T15](tasks/T15-docker-compose.md) | Dockerfiles + compose | T08, T10, T12-T14 | `Dockerfile.*`, `docker-compose.yml` |
| [T16](tasks/T16-packaging-ci-docs.md) | Packaging, CI (incl. macOS), install docs | T15 | release artifacts |
| [T17](tasks/T17-component-test-plans.md) | Test plans: viewer, logger, api, database | T00 (written early, filled per task) | coverage matrices, fixtures, harnesses |
| [T18](tasks/T18-integration-suite.md) | Integration suite against deployed API + database, local and AWS; good and bad behaviour | T08, T10, T13, T14 | `tests/integration/` |
| [T19](tasks/T19-bad-actor-suite.md) | Bad-actor suite: attempts to pull data the API must not give | T18 | `tests/abuse/` |
| [T20](tasks/T20-security-analysis-review.md) | Security analysis and review: viewer, logger, api, database | T19 | threat model, review report, sign-off |
| [T21](tasks/T21-performance-and-cost-review.md) | Performance review + cheapest-cloud verification | T10, T18 | benchmarks, load test, `cost-lint.sh`, report |

T17 is written right after T00 and each component task fills its section; T18-T21 are the release gates and run last.
Parallel lanes once T00-T02 land: **A** T03 -> T04 -> T05 -> T06/T07 -> T08 (-> T09 -> T10); **B** T11; **C** T12 -> T13 and T14.
Lanes only meet at T15.

```
T00 -> T01 --+-> T03 -> T04 -> T05 -> T06 -> T08 ----+
     -> T02 -+                  \-> T07 -/            |
                       T04 -> T09 -> T10 -------------+-> T15 -> T16 -> T17
              T11 (contract only) --+-> T13 ----------+
              T12 -----------------+-> T14 -----------+
```

## 12. Test strategy overview (details in T17-T19)

| Layer | Where | Runs against | Gate |
|---|---|---|---|
| Unit + contract + fuzz | next to the code | in-process | every task AUDIT |
| Component tests (per app) | T17 matrices | in-process fakes, temp dirs, `httptest` | every task AUDIT |
| Integration, good + bad behaviour | `tests/integration/` (T18) | **a running API + database**: `local` (compose) and `aws` (deployed stack) targets, same tests | release |
| Bad actor / data exfiltration | `tests/abuse/` (T19) | same two targets | release |
| Security review | T20 | code + deployed stack | release |
| Performance + cost | T21 | benchmarks, load test on both targets, template lint | release |

Integration tests are plain `go test -tags integration ./tests/...` with `CL_TARGET=local|aws`, `CL_URL`, `CL_INGEST_KEY`, `CL_VIEW_KEY`
(env vars, never flags). They create their own **namespaced test data** (ruleset `rp` plus a `source` of `it-<runid>`) and clean up.

## 13. Performance budgets (measured in T21; every task logs its benchmarks against these)

Principle: **as fast as possible without risk to security.** Order of work: correct -> secure -> measured -> fast. Allowed
optimisations: prepared statements, batching, fewer allocations (`sync.Pool` only with proof), ETag/304, in-memory key cache (30 s),
gzip, avoiding reflection on hot JSON paths (only if the benchmark proves a gain >= 20%). Not allowed: skipping validation, longer
cache lifetimes for revocable keys, disabling limits, unsafe/`unsafe` package, hand-rolled crypto.

| Path | Budget (this machine, p95) | Why it matters |
|---|---|---|
| `ValidatePing` (200 contacts x 50 fixes) | < 5 ms | Lambda bills per ms |
| `ApplyPing` SQLite, max ping | < 150 ms | local ingest latency |
| `Sweep` 10 k contacts | < 20 ms | polled by every viewer |
| `GET /v1/sweep` 304 path (end to end, in-process) | < 1 ms | the common case |
| Auth (cached key) | < 50 us | on every request |
| Lambda cold start (arm64, 128 MB) | < 400 ms to first byte | free-tier GB-s |
| Lambda warm request | < 25 ms p95 server time | |
| Logger: parse 1 M log lines | < 3 s, < 100 MB RSS | live tailing on a gaming PC must not hurt frames |
| Logger idle CPU | < 1 % of one core | |
| Viewer: first paint after data | < 300 ms; 2 000 blips at 60 fps | |
| Viewer idle CPU between polls | < 1 % | |

## 14. Verified facts and open risks

Verified by running code on 2026-11-17 (Linux, Go 1.24.2 installed, toolchain auto-switch on):
* `purego.Dlopen` under `CGO_ENABLED=0` reports `libwebkit2gtk-4.1.so.0` and `libgtk-3.so.0` present and a bogus library absent. **The preflight design works.**
* `github.com/ebitengine/purego` v0.11.3 requires Go >= 1.25, so the module is `go 1.25`. Docker and CI build with Go 1.26.11+ because the Go 1.25 line has no fixes for the standard-library advisories govulncheck reports (GO-2026-66xx).

Open risks (each has a PoC step in a task; do not assume):
| Risk | Resolved in |
|---|---|
| Which pkg-config name `webview_go` wants on Linux (`webkit2gtk-4.0` vs `4.1`) and whether a build tag is needed | T12 PoC 1 |
| `go-webview2` `SetHtml` + `Bind` behaviour and CSP on Windows (cannot be run on this Linux box; verify on the Windows CI runner or a VM) | T12 PoC 3 |
| `SetHtml` size limits when the page embeds the radar JS (~100 KB) | T12 PoC 2 |
| DynamoDB name-prefix search model matches SQLite behaviour | T09 PoC 2 |
| Lambda cold-start time for the Go arm64 binary reading SSM | T10 PoC 1 |
| Eye-button reveal leaking the key into the DOM longer than needed | T12 PoC 4 |
