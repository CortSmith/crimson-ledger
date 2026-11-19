# Threat model (T20 Phase A, draft for independent review)

Status: written 2026-11-17 by the build agent from PLAN sections 2, 4, 5, 7 and the T19 catalogue. **Not yet reviewed by a
non-author**; T20 requires that before sign-off. Cells marked `GAP` have no passing test yet and are findings (see findings.md).

## Data flow (trust boundaries marked `||`)
```
[WoW log file] -> (logger: tail/parse) -- HTTPS + Bearer ingest key --||--> [API: Lambda or local] -- store API --> [DynamoDB | SQLite file]
[viewer window + WebView] <-- bound Go calls -- (viewer Go) -- HTTPS + Bearer view key --||--> [API]
[operator terminal] -- crimson-api keys / admin Lambda (IAM) --> [key table]     [~/.crimson-ledger config files: key at rest]
```

## Assets
| Asset | Where | Why it matters |
|---|---|---|
| API keys (ingest, view) | config files at rest (0600), in transit, key table (HMAC only) | write/read access to the data |
| Pepper | file 0600 / SSM SecureString | offline guessing of key hashes |
| Player data (names, positions, kills) | store | game data, personal-ish |
| Availability and AWS bill | API, Lambda concurrency | cost abuse |
| User machines | logger, viewer | RCE or key theft via hostile server data |

## Actors
Anonymous internet; leaked-key holder (ingest or view); malicious server / MITM; curious co-tenant on a shared PC
(file permissions matter); supply-chain attacker. Out of scope: malware running as the same user (defeats file permissions; documented).

## STRIDE (each cell: control -> test ID)
| Component | S Spoofing | T Tampering | R Repudiation | I Info disclosure | D Denial | E Elevation |
|---|---|---|---|---|---|---|
| API | stolen key: scopes, revoke, expiry, lockout -> X101 X102 X103 X104 X105 X107 | body tampering: TLS + validation, forged data caps -> X401 X402 X403 X404 X407 | request-id logs with key id -> GAP (no log-content test) | uniform errors, no secrets -> X203 X307 X308 X311 | limits, timeouts -> X501 X502 X503 X504 | per-route scope -> X201 X202 X204 X205 |
| Database | n/a | direct file edit needs local user, 0600 -> X802 | n/a | file modes, backups 0600 -> X802 | disk full / locks -> GAP (T17 only) | injection: parameterised only -> X306 X406 |
| Logger | fake server: TLS verify, no downgrade -> X601 X602 X603 | own log file tampering: accepted, user's own data | activity log -> GAP | key at rest 0600, masked in UI -> X802 X803 | huge lines/log: caps -> X604 | no listener -> GAP (ss check, T20 manual) |
| Viewer | fake server -> X601 X602 | n/a | n/a | key not in DOM until reveal -> GAP (X606 is owned by T12 canary tests; confirm by name) | hostile data caps -> X604 | WebView escape: CSP, no navigation -> X405 (API side only; viewer xss tests in T14) |

## Accepted residual risks (owner: project owner, review by 2027-05-17)
* X305 slow name enumeration by an authorised view key.
* F-X106 key-id existence timing (Low).
* Open decision: per-key ruleset allowlist (see findings.md; recommended).
