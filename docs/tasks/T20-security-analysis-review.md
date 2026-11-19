# T20: Security analysis and review: viewer, logger, api, database

**Depends on:** T19 (and all build tasks) · **Blocks:** release · **PLAN.md sections:** [7 security baseline](../PLAN.md#7-security-baseline-applies-to-every-task-the-audit-phase-checks-it), [2 decisions](../PLAN.md#2-decisions-and-why-each-was-chosen-for-lowest-cost-then-security), [4 config](../PLAN.md#4-config-crimson-ledger), [5 contract](../PLAN.md#5-shared-api-contract-single-source-of-truth), [10 workflow](../PLAN.md#10-mandatory-workflow-for-every-task-tdd-with-proof-of-concept-first)

## Goal
An independent, written security analysis of all four components and the cloud deployment: threat model, tool-driven scans, manual code
review with per-component checklists, a findings register, and a sign-off gate. **The reviewer must not be the author** of the code under
review: use a fresh reviewer agent/person that gets only this file, PLAN sections 2/4/5/7, and the code.

## Outputs
```
docs/security/threat-model.md        assets, trust boundaries, data-flow diagram, STRIDE table per component, abuse cases -> test IDs (T19)
docs/security/review-api.md          review report + checklist results
docs/security/review-database.md
docs/security/review-logger.md
docs/security/review-viewer.md
docs/security/review-cloud.md        AWS + Docker + CI
docs/security/findings.md            register: ID, component, severity (Critical/High/Medium/Low/Info), status, owner, fix commit, test ID
docs/security/sbom/*.json            CycloneDX per binary
docs/security/signoff.md             release gate checklist signed with date and reviewer
scripts/security-scan.sh             all tools below in one command (Docker), exits non-zero on gate failure
```

## Phase A: Threat model (written first, reviewed before scanning)
Data-flow (trust boundaries marked `||`):
```
[WoW log file] -> (logger: tail/parse) -- HTTPS+Bearer ingest key --||--> [API: Lambda or local] -- store API --> [DynamoDB | SQLite file]
[viewer: window+WebView] <-- bound Go calls -- (viewer Go) -- HTTPS+Bearer view key --||--> [API]
[operator terminal] -- crimson-api keys / admin Lambda (IAM) --> [key table]      [~/.crimson-ledger config files: key at rest]
```
Assets: API keys (at rest in two config files, in transit), pepper, player data (names, positions: personal-ish data about game players), availability and
AWS bill, the machines running the apps. Actors: anonymous internet, leaked-key holder, malicious server/MITM, local malware as the same user (out of scope for
config files: same-user compromise defeats file permissions; documented), curious co-tenant on a shared PC (file perms matter), supply-chain attacker.
STRIDE per component (fill a table row each; examples):
| Component | S | T | R | I | D | E |
|---|---|---|---|---|---|---|
| API | stolen key -> mitigations: scopes, revoke, expiry, rate limit | body tampering -> TLS + validation | no audit trail -> request-id logs with key id | key/data leaks -> uniform errors, no secrets in logs | flood -> limits, reserved concurrency | scope confusion -> per-route scope, tests X2xx |
| Database | n/a | direct file edit by local user (0600) | n/a | backups readable (0600) | disk full/locks | SQL/expr injection -> parameterised only |
| Logger | fake server -> TLS verify/CA | log file tampering (user's own) | activity log | key at rest 0600, key masked in UI | huge log/lines -> caps | none (no listener) |
| Viewer | hostile server data -> escape/CSP | n/a | n/a | key in DOM -> reveal protocol | hostile data DoS -> caps | WebView escape -> CSP, no navigation, no devtools |
Each STRIDE cell links to a control and a test ID; empty cells are findings.

## Phase B: Automated analysis (`scripts/security-scan.sh`, all in the build image or pinned Docker images; free tools only)
| Tool | Target | Gate |
|---|---|---|
| `gosec`, `staticcheck`, `golangci-lint` (errorlint, bodyclose, noctx) | all Go | zero findings or inline-justified |
| `govulncheck` (call-graph aware) | all binaries | zero reachable vulns |
| `osv-scanner` + `go list -m -u all` | go.mod / lockfiles / Node test deps | no HIGH+ without exception entry |
| `gitleaks detect` (full history) + `trufflehog filesystem` | repo | zero secrets; test canaries allow-listed by exact value |
| `syft` -> CycloneDX SBOM; `grype` on SBOM and on images | binaries + Docker images | no HIGH/CRITICAL |
| `trivy image` + `hadolint` | Docker images/Dockerfiles | as above |
| `cfn-lint`, `cfn-nag`, `checkov` | `template.yaml` | zero failures except documented (e.g. Function URL AuthType NONE is intentional: finding "accepted by design", compensated by key auth + limits) |
| `semgrep` (rules `p/golang`, `p/owasp-top-ten`, `p/javascript`, `p/dockerfile`) | repo | zero HIGH |
| `eslint` with `eslint-plugin-no-unsanitized` and `security` | viewer/logger web JS | zero errors |
| `go test -race`, `-fuzz` nightly corpora, mutation score (T17) | security-critical pkgs | thresholds in T17 |
| OWASP ZAP baseline + `schemathesis` + `ffuf` (T19 `abuse-scan.sh`) | running API | zero medium+ |
| `nuclei -t http/misconfiguration,http/exposures` | running API | zero findings |
| `testssl.sh` or `sslyze` | AWS Function URL and local TLS | TLS 1.2+, no weak ciphers (AWS-managed; record) |
| `checksec`/`readelf` on Linux binaries; `-trimpath`, PIE (`-buildmode=pie` for cgo UI), stack protector via cgo flags | binaries | PIE on, no RWX segments |
| Windows: `Get-AuthenticodeSignature` (info), icacls on config | Windows artifacts | owner-only ACL |
Record tool versions and raw outputs in `docs/security/scan-<date>/`.

## Phase C: Manual review checklists (reviewer ticks each with file:line evidence or a finding ID)

### API (`internal/apiserver`, `api/`)
- [ ] Auth runs before routing to any handler and before reading bodies; no route bypass (table of routes vs scope reviewed line by line).
- [ ] HMAC: pepper >= 32 bytes, from file 0600 / SSM SecureString; secrets compared with `subtle.ConstantTimeCompare`; no `==` on secrets (grep).
- [ ] Key format parsing strict; no regex catastrophic backtracking (RE2: fine; reviewed).
- [ ] `crypto/rand` only (grep for `math/rand` in non-test code: none in security paths).
- [ ] Every JSON decode: size limit + `DisallowUnknownFields` + trailing-data check; gzip bounded; depth bounded.
- [ ] Error responses: single constructor, no `err.Error()` to clients; panics recovered with generic 500.
- [ ] Logging: no keys/body/query; log injection prevented (structured JSON, control chars escaped); `requestId` correlates.
- [ ] Rate limiting and lockout correct under concurrency (review the mutex/atomic code; race tests); fail-closed on limiter error.
- [ ] Client IP derivation cannot be spoofed in default config; proxy mode documented.
- [ ] Timeouts on server and on every outbound call (SSM, DynamoDB) with context deadlines.
- [ ] Resource caps: body, header, result sizes, per-key counters (kills, contacts); integer overflow checks on limits/pagination arithmetic.
- [ ] Revocation latency documented (cache TTL) and acceptable; cache bounded.
- [ ] No debug/pprof/metrics endpoints compiled in release builds.
- [ ] Dependencies minimal; each third-party module justified in `docs/security/deps.md` (purpose, maintainer health, license).

### Database (`database/`, `internal/store/*`)
- [ ] All SQL parameterised; the only dynamic SQL is the validated backup path literal; DynamoDB expressions are constants with placeholders; key builders reject separator characters.
- [ ] No `Scan` in code or IAM; `Query` always bounded with `Limit`.
- [ ] Least privilege: SQLite file 0600 in 0700 dir (+ WAL/SHM), backups 0600; DynamoDB role restricted to one table + index; key items only via admin role with `LeadingKeys`.
- [ ] Encryption at rest: DynamoDB default (AWS-owned key, free); SQLite: documented as not encrypted (disk encryption is the user's choice); backups likewise. Decision recorded with rationale (cost/complexity) in findings as "accepted".
- [ ] Data minimisation: only fields in the contract stored; retention TTL/prune enforced and tested; no raw log lines; no IPs stored.
- [ ] Integrity: constraints and `CHECK`s present; migrations forward-only with downgrade guard; idempotency verified (no double counting).
- [ ] Backup/restore tested; restore cannot resurrect revoked keys incorrectly (documented: restore brings back old key states).
- [ ] Privacy review: stored player names/positions are game data; no accounts, emails or real-person data; deletion procedure documented (prune by GUID admin command is a Follow-up if needed).

### Logger (`logger/`, `internal/logparse`, `internal/apiclient`, `internal/ui`)
- [ ] Reads the log with read-only flags; ignores symlinks that leave the folder; never writes under the WoW directory.
- [ ] Parser safe on hostile lines (fuzzed): no unbounded allocation, no regex ReDoS (RE2), line cap.
- [ ] Config and outbox permissions; key never in outbox/logs/activity/window title; masked by default; reveal protocol per T12; auto re-mask verified.
- [ ] TLS: system roots + optional CA file only; no skip-verify; redirect policy; error text safe.
- [ ] Bounded memory/disk; clean shutdown; no goroutine leaks (`goleak`).
- [ ] No listener opened (verified at runtime with `ss`); no fallback to a local website; missing-library path exercised (T12/T16).
- [ ] Auto-update: none (no self-update code => no update attack surface); documented.
- [ ] Privacy: uploads only derived contacts/fixes/kills; the user is told what is uploaded (UI text) and can stop at any time.

### Viewer (`viewer/`, WebView)
- [ ] CSP exact and enforced (violation events monitored in a debug build, count 0 in all tests); nonce per load; no `unsafe-inline/eval`.
- [ ] No `innerHTML`/`outerHTML`/`insertAdjacentHTML`/`document.write` with data (lint + grep); all data through `textContent`/canvas.
- [ ] Bound function surface reviewed: each binding validates arguments (types, lengths, GUID regex) and returns data only; none can read files, run commands, or open URLs; `revealKey` is the only secret-returning call; clipboard binding writes only validated text.
- [ ] Navigation, new-window, downloads, permission requests (camera/geo/notifications), file drop all denied.
- [ ] WebView settings: devtools off in release, context menu minimal, no remote debugging port (check `WEBKIT_INSPECTOR_SERVER`/WebView2 `--remote-debugging-port` not set).
- [ ] Hostile data handling (caps, NaN guards, evil strings) verified by T18-C/T19-X6xx.
- [ ] Maps/assets embedded; no external requests (verified by a network-deny test: run the app in a container with `--network none` + local fake: renders and reports connection error only).
- [ ] Windows: WebView2 user-data folder location (must be under the app's own dir in `%LOCALAPPDATA%`, not shared) and its permissions; macOS: no entitlements beyond defaults; Linux: no extra portals.

### Cloud, containers, CI (`api/deploy`, Dockerfiles, `.circleci`)
- [ ] IAM policies minimal and verified by `simulate-principal-policy` (T10/T19 X703); no `*` actions/resources except own log group.
- [ ] Function URL: only on the public function; admin invoke is IAM-only; resource policies reviewed.
- [ ] SSM SecureString; no secrets in env vars, template parameters or outputs.
- [ ] S3 artifact bucket private, encrypted, lifecycle set; DynamoDB TTL enabled; PITR/streams off by design.
- [ ] Budget alarm and reserved concurrency present; log retention 14 d.
- [ ] Containers: digest-pinned bases, non-root, read-only, no caps, healthcheck, no secrets in layers (`docker history`), `.dockerignore` effective.
- [ ] CI: secrets only in protected contexts, no `pull_request` from forks with secrets, third-party actions/orbs pinned by version/digest, artifacts checksummed.
- [ ] Supply chain: `go.sum` committed, `go mod verify` in CI, `GOFLAGS=-mod=readonly`, `GONOSUMDB` unset, base images scanned.

## Phase D: Findings workflow
1. Every finding -> `findings.md` row (severity per CVSS-like rubric: Critical = remote key/data disclosure or write without auth; High = privilege escalation, persistent DoS/cost abuse, key at rest readable by other local users; Medium = limited disclosure, missing defence in depth; Low = hardening; Info).
2. Fix in the owning component **test-first** (unit/regression test + T19 test where relevant), then re-run the relevant scan/review item.
3. SLA before release: Critical/High = fixed and re-verified; Medium = fixed or accepted by the owner in writing with rationale and review date; Low/Info = tracked.
4. Design decision to settle in this task: **optional per-key ruleset allowlist** (limits the blast radius of a leaked view key). Recommendation: implement (small: one column/attribute + check in `authenticate`), because it is cheap and shrinks X301-class exposure. Record decision + tests.

## Step 0: PoC (`scripts/poc/t20/`)
1. **Whole scan script runs end to end** in the build image on the current tree and produces `scan-<date>/` with every tool's output. PASS = all tools executed (exit codes recorded; failures allowed at this stage), runtime < 15 min.
2. **Detect a planted bug**: add a deliberate `InsecureSkipVerify`, a `fmt.Sprintf("SELECT ... %s")`, an `innerHTML = name`, a world-readable chmod 0644 on config; confirm gosec/semgrep/eslint/check.sh each catch the right one. PASS prints which tool caught which; any miss becomes a custom semgrep rule committed in `scripts/semgrep/`.
3. **SBOM generation** for the Go binaries (`syft` on built binaries; Go embeds module info) and Docker images; `grype` reads them. PASS prints component counts.
4. **STRIDE completeness check**: script verifies each threat-model row references >= 1 test ID that exists (greps test names). PASS = no dangling references.
5. **cfn-nag/checkov** on the T10 template produce a baseline; list the findings that are intentional. PASS.
6. **Network-deny run** of the viewer and logger (`docker run --network none` + Xvfb): both start, show connection errors, never crash. PASS.
7. **Windows WebView2 data folder**: inspect where `go-webview2` stores user data by default and that it's per-user (VM check). PASS or finding.

## Step 1: RED
Write the threat model, then run `security-scan.sh` and the manual checklists; every unchecked item/failed tool output becomes a finding or a new failing test. Commit the initial findings register (this is the "red" state).

## Step 2: GREEN
Fix findings (test first), re-run scans and checklist items until the gate in `signoff.md` is satisfiable.

## Step 3: REFACTOR checklist
- [ ] Reports are factual: each claim has file:line, command output, or test ID; no unevidenced "looks fine".
- [ ] Duplicated findings merged; severities consistent; accepted risks have an owner and a review date within 6 months.
- [ ] `deps.md` lists every direct dependency with justification; any dependency without one is removed.

## Step 4: AUDIT (this task's gate)
`docs/security/signoff.md` must show, each with evidence link:
- [ ] Threat model complete (no empty STRIDE cells).
- [ ] All Phase B tools green (or exceptions listed and accepted).
- [ ] All Phase C checklists fully ticked by a reviewer who is not the author.
- [ ] T19 green on local and aws; `abuse-report.md` attached.
- [ ] Zero open Critical/High; Medium resolved/accepted.
- [ ] Re-scan after the last fix (no scan older than the last code change: scripted check compares commit hashes).
- [ ] SBOMs attached; checksums published.

## Done when
- [ ] Sign-off file complete and committed; release job (T16) refuses to run without it for the tagged commit (script checks the file's commit hash).

## PoC log
* 2026-11-17: Phase A draft written (`docs/security/threat-model.md`, unreviewed by a non-author). PoC 4 done as `scripts/stride-check.sh`: it correctly flags X606 as dangling (no test of that name; owned by T12 canaries). Gaps logged as F-T20-1..3 in findings.md. Phases B-D, PoCs 1-3, 5-7 not started; the scan tools are not yet installed.

## Follow-ups
- Re-run quarterly and on every dependency bump or new endpoint.
