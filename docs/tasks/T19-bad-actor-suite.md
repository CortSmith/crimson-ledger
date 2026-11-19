# T19: Bad-actor suite: attempts to pull data the API must not give

**Depends on:** T18 (same harness, same two targets) · **Blocks:** T20 · **PLAN.md sections:** [5 API contract](../PLAN.md#5-shared-api-contract-single-source-of-truth), [7 security baseline](../PLAN.md#7-security-baseline-applies-to-every-task-the-audit-phase-checks-it), [2 D6 keys](../PLAN.md#d6-api-keys-scoped-hashed-no-accounts), [10 workflow](../PLAN.md#10-mandatory-workflow-for-every-task-tdd-with-proof-of-concept-first)

## Goal
Think and act like an attacker against the **deployed** API and database (local compose and AWS stack) and prove that information
not meant to leave the system cannot be pulled, data cannot be forged beyond what a key allows, and abuse is bounded in cost.
Every attack is an automated test with an ID, a stated attacker, a goal, and an expected defence. A test **passes when the attack fails**.

## Outputs
```
tests/abuse/actors.go          Anonymous(), ViewKeyHolder(), IngestKeyHolder(), RevokedKey(), ExpiredKey(), ManInTheMiddle() (proxy)
tests/abuse/*_test.go          //go:build integration  (grouped by attack class below)
tests/abuse/corpus/            sqli.txt, nosqli.txt, pathtrav.txt, xss.txt, unicode.txt, headers.txt (small, curated; seeds, not a scanner dump)
scripts/abuse-scan.sh          runs ZAP baseline, schemathesis, ffuf (docker images pinned) against the target
docs/security/abuse-report.md  generated: attack ID -> actor -> result -> defence -> evidence
```

## Assets the attacker wants (what "not meant to be pulled" means)
| Asset | Must never be obtainable by |
|---|---|
| API key records, hashes, pepper, key ids of other users, labels | anyone via the API |
| Rows outside the contract: batch markers, rate counters, raw items, schema | anyone |
| Data of a ruleset not named in the request, "all rulesets" dumps | anyone |
| Bulk export beyond documented bounds (all fixes of everyone, all names) | a view key within bounds: sweep <= 2000, search <= 50/query, trail <= 2000 for a **known** GUID, rate limited |
| Stack traces, versions beyond `/v1/health`, env vars, file paths, AWS identifiers, SQL/DynamoDB errors | anyone |
| Write access (ingest) | view key / anonymous |
| Read access | ingest key / anonymous |
| Admin actions (create/revoke keys) | any HTTP caller (no such route exists) |
| Existence oracles: which key ids exist, which GUIDs expired | anyone |
| Unbounded cost (Lambda/DynamoDB spend) | anyone |

## Attack catalogue (each row is >= 1 test; payloads from `corpus/` unless stated)
### 1. Authentication bypass (actor: Anonymous)
| ID | Attack | Defence expected |
|---|---|---|
| X101 | No header, empty Bearer, `Bearer null/undefined/true/0`, `bearer` lowercase variants, `Basic`, `Token`, duplicate `Authorization` headers, header with CRLF/null bytes/20 KB | 401 uniform body |
| X102 | Key supplied in query (`?key=`, `?api_key=`, `?access_token=`), cookie, `X-API-Key`, body field | ignored -> 401 (and unknown query param -> 400 on reads; verify order: auth first, so 401) |
| X103 | Truncated real token, token with extra suffix/prefix, swapped scope letter (`clk_i_` -> `clk_v_` same id/secret), uppercase/lowercase id, Unicode lookalikes, Unicode normalization (NFKC) variants | 401, never 200 |
| X104 | Injection in key id/secret: SQL (`' OR 1=1--`), DynamoDB expression chars (`#`, `:`, `${}`, `begins_with(`), path traversal | 401, no 5xx, no change in server behaviour (compare timing + body) |
| X105 | Brute force: 10 000 guesses of the 8-char id with random secret from one IP, then from rotating `X-Forwarded-For` | lockout after 10; XFF ignored when `trust_proxy_headers=false` (local) / uses Lambda sourceIp (AWS); total successful guesses 0; entropy argument recorded (id 36^8, secret 256-bit) |
| X106 | Timing oracle: unknown id vs wrong secret vs disabled key (secret correct) | 401/401/403 are intentionally different only for a **correct secret**; unknown vs wrong-secret timing medians within 20 % |
| X107 | Replay of a revoked/expired key after the cache window | 403 `key_disabled` (<= TTL) |
| X108 | Use a key from the wrong stack (token from stack A on stack B with a different pepper) | 401 |

### 2. Authorization / privilege (actors: ViewKeyHolder, IngestKeyHolder)
| ID | Attack | Defence |
|---|---|---|
| X201 | View key `POST /v1/pings` (valid body) | 403, store unchanged (version equal) |
| X202 | Ingest key on every GET route | 403 |
| X203 | Guess admin/management routes: `/admin`, `/v1/keys`, `/v1/admin/keys`, `/internal`, `/debug/pprof`, `/debug/vars`, `/metrics`, `/actuator`, `/.env`, `/.git/config`, `/swagger`, `/openapi.json`, `/v1/export`, `/v1/dump`, `/v1/db`, `/graphql`, `/robots.txt`, `/favicon.ico` | 404 uniform (no framework banner), no data |
| X204 | HTTP verb tampering: `X-HTTP-Method-Override: POST`, `_method=POST`, `PUT/PATCH/DELETE` on all routes | 405; override headers ignored |
| X205 | Path tricks: `/v1/sweep/`, `/v1//sweep`, `/v1/./sweep`, `/v1/%2e%2e/health`, `/V1/SWEEP`, `/v1/sweep%00`, `/v1/sweep;x=1`, `/v1/players/..%2fsweep/trail` | 404/400, never reaches another handler with elevated scope |
| X206 | Invoke the **admin Lambda** anonymously via the Lambda Invoke API (unsigned HTTPS to `lambda.<region>.amazonaws.com/2015-03-31/functions/<name>/invocations`) and via any URL | 403/404 (aws target only) |
| X207 | Ingest key tries to read back what it wrote through any side channel: error messages, response fields, timing, `ETag` | responses contain counts only |

### 3. Data exfiltration through the read API (actor: ViewKeyHolder)
| ID | Attack | Defence |
|---|---|---|
| X301 | Omit `ruleset`; `ruleset=*`, `all`, `%`, `pvp,rp`, `ruleset[]=`, `ruleset=pvp&ruleset=rp`, `RULESET=pvp`, URL-encoded variants | 400 for all; data of only one ruleset ever returned |
| X302 | Dump everything: `limit=1e9`, `limit=-1`, `limit=0`, `limit=2001`, `limit=99999999999999999999`, `offset=`, `page=`, `cursor=`, `skip=` | 400 (unknown/over max); page size never exceeds documented caps |
| X303 | Mass trail scraping: iterate `since=` from 0 for one known GUID | returns <= 2000 per call, newest-first cursor works as designed; total reachable fixes bounded by retention; rate limit trips |
| X304 | GUID enumeration: sequential/random GUID guesses on trail | 404 identical for unknown and expired; malformed 400; rate limited; no existence oracle via timing (medians within 20 %) |
| X305 | Name scraping by 2-letter prefixes (676 queries), wildcard probing `q=%`, `_`, `*`, `.`, `\`, `%00`, `a%`, `[a-z]`, regex/glob syntax, unicode wildcards | wildcards treated as literal text -> empty; scraping capped by per-key rate limit; test documents the **accepted residual risk** that an authorised view key can enumerate player names slowly (any API user can) |
| X306 | Injection through `q`, `guid`, `map`, `since`, `ruleset`, `maxAgeSec`: SQL (`' OR '1'='1`, `;DROP TABLE`, `UNION SELECT`), DynamoDB key-separator abuse (`a#b`, `C#pvp#guid`, `W#pvp#xx`), expression metachars | 400 (validation) or literal no-match; zero cross-entity data; no 5xx; response bodies identical to a normal empty result |
| X307 | Force verbose errors: invalid UTF-8, 100 KB query, deeply nested JSON (`[[[...]]]` depth 100 000) in a ping, huge numbers, duplicate JSON keys, BOM, `\u0000` | 400/413; body contains only `code/message/requestId`; no Go error text, no file path, no `sqlite`/`dynamodb` strings (regex check on every error body in the whole suite) |
| X308 | Cache/ETag side channels: `If-None-Match` for another ruleset's ETag; `If-Match`; `Range` requests; `Accept-Encoding` tricks (BREACH-style: reflect secrets? none are reflected) | ETag reveals only version bucket; Range ignored; no secret ever appears in a response |
| X309 | CORS probe: `Origin: https://evil.example` with credentials on every route + preflight | no `Access-Control-*` headers at all |
| X310 | `maxAgeSec=999999999` to reach beyond retention/24 h | 400 over max; never returns data older than retention |
| X311 | Response content sniffing: confirm `Content-Type: application/json; charset=utf-8` + `nosniff` on every response incl. errors | ok |

### 4. Data poisoning and forgery (actor: IngestKeyHolder; assume a leaked ingest key)
| ID | Attack | Defence |
|---|---|---|
| X401 | Rewrite chart rectangles for an existing map | ignored (first write wins), WARN logged, sweep/charts unchanged |
| X402 | Timestamps: far future/past, `sentAt` skew, `t` 5 min + 1 ms in the future | 400 |
| X403 | Fake kills to inflate someone's bounty: 200/ping x many pings | per-key hourly cap (`MaxKillsPerKeyHour`) -> 429; killer==victim rejected; duplicates not double counted |
| X404 | Flood 2 000+ fake contacts to push real ones out of sweep results | per-key distinct-contact hourly cap (`MaxContactsPerKeyHour = 5000`, T06 follow-up if missing) -> 429; sweep sort by `lastSeen` documented; test asserts cap exists and real data still visible after a capped flood |
| X405 | Stored XSS/UI spoofing via names: HTML, `javascript:`, bidi override U+202E, zero-width joiners, homoglyph (`Ｍc`), 24-rune max, control chars | rejected (control/Cf) or stored literally and **rendered inert** (viewer T14 xss tests; here: API returns them JSON-escaped, never as HTML) |
| X406 | Key-separator injection in names/guids for DynamoDB keys (`#`, `\u0000`) | validation rejects in guid; name words restricted to `[a-z0-9]` after normalisation (if this test fails, fix `NormalizeName` in T02 and add the regression there first) |
| X407 | Replay old batches after the 24 h marker TTL | idempotent upserts: no duplicates, kills not double counted (transaction), version bump only |
| X408 | Impersonate another source: send `source` of another logger | `source` has no authority; nothing keyed on it (assert no behavioural difference) |
| X409 | Poison via gzip tricks: multi-member gzip, gzip with trailing garbage, `Content-Length` lies, chunked with huge chunk sizes | 400/413, server healthy |

### 5. Resource abuse and cost abuse (actors: Anonymous, any key)
| ID | Attack | Defence |
|---|---|---|
| X501 | Request flood from one IP (local: 2 000 req/10 s; aws: 200 parallel) | 429s from limiter (local) / Lambda reserved-concurrency throttles (aws); service recovers; no 5xx from our code |
| X502 | Slowloris, slow POST, many idle connections (local) | timeouts close them <= 15 s; other clients unaffected |
| X503 | Decompression bomb, oversized body, huge header count | rejected early, memory spike < 20 MB (measure RSS delta locally) |
| X504 | Algorithmic complexity: many words per name, 200 contacts all with max words, worst-case search prefix with 100 k matches | latency within PLAN 13 budget x2; search result capped |
| X505 | Make the API read DynamoDB expensively: sweep every 1 s from a view key | per-key rate limit + 304 path cost shown to be 1 `GetItem` (consumed capacity from CloudWatch < 1 RCU) |
| X506 | Cost ceiling proof (aws): run 10 min of abusive traffic and compute max possible monthly cost from reserved concurrency (5) x timeout (10 s) x memory | documented upper bound in the report; AWS Budget alarm exists (`describe-budgets`) |

### 6. Network-position attacker vs the apps (actor: ManInTheMiddle proxy; runs against `apiclient`, logger, viewer)
| ID | Attack | Defence |
|---|---|---|
| X601 | MITM presents self-signed/expired/wrong-host cert | connection refused, clear message, no key sent (proxy asserts zero `Authorization` bytes) |
| X602 | TLS downgrade: server URL `http://` for a non-loopback host in config | config validation rejects; apps refuse |
| X603 | Redirect to attacker host / to `http://` | refused; key not sent |
| X604 | Hostile response bodies (see T18 C-table: huge, deep JSON, evil strings, NaN) | bounded, rejected, no crash (viewer/logger process stays up, memory bounded) |
| X605 | Malicious server returns `Set-Cookie`, `WWW-Authenticate`, `Location` weirdness | ignored (no cookie jar) |
| X606 | Key exfiltration from the app UI: scrape DOM/`Eval` payloads/window title/logs before reveal | key absent (canary tests, T12) |

### 7. Cloud configuration probes (actor: Anonymous with internet access; aws target only)
| ID | Probe | Expected |
|---|---|---|
| X701 | Unauthenticated `aws`-API style probes: DynamoDB endpoint `GetItem`/`Scan` without SigV4, S3 artifacts bucket `GET/LIST`, SSM | all denied (403/AccessDenied/MissingAuthenticationToken) |
| X702 | Lambda **public** function URL only: no second URL, no resource policy allowing `*` invoke except the URL | `aws lambda list-function-url-configs` shows exactly one; admin has none; `get-policy` on admin = ResourceNotFound |
| X703 | Role abuse by test: assume the public Lambda role (test account only) and try `Scan`, `DeleteItem`, `ssm:GetParameters*` on other params, `s3:*`, `iam:*` | AccessDenied for everything outside the policy (via `simulate-principal-policy` + real call) |
| X704 | Secrets in logs/outputs: CloudWatch logs search for `clk_`, pepper bytes (canary), `Authorization`; CloudFormation outputs/parameters/events | none found |
| X705 | Table exposure: no public policy, TTL on, PITR/streams off (cost), encryption at rest on | matches template |
| X706 | S3 bucket: public access block 4/4, no policy, lifecycle present | ok |

### 8. Local hosting probes (local target)
| ID | Probe | Expected |
|---|---|---|
| X801 | Default bind from another LAN host / other container (`docker run --network host curl`) | refused unless user opted in (`--insecure-lan`/TLS) |
| X802 | File permission audit of `~/.crimson-ledger` after a full run: dir 0700, all files 0600 (api.json, logger.json, viewer.json, pepper, db, wal/shm, outbox, backups, tls key) | exact modes |
| X803 | Process arguments/environment (`/proc/<pid>/cmdline`, `environ`) contain no key/pepper | none |
| X804 | Container posture: non-root, read-only, no caps, no shell (`docker exec ... sh` fails) | ok |

## Step 0: PoC (`scripts/poc/t19/`)
1. **Actor plumbing**: Go helper issues raw HTTP (including illegal requests Go's client normalises: duplicate headers, `//` paths, `%00`) using `net.Dial` + hand-written request bytes; PASS = server receives exactly the bytes sent (compare with a recording server).
2. **Timing measurement method**: 2 000 interleaved requests per class, compare medians and use a Mann-Whitney U test (`gonum`/hand-rolled) at p > 0.01 threshold; calibrate on two identical classes first (must report "no difference"). PASS prints calibration.
3. **Zap/ffuf/schemathesis in Docker** reach the local API; record their raw findings as the starting RED list. PASS = tools run, report saved.
4. **Lambda direct-invoke probe**: confirm an unsigned HTTPS POST to the Lambda invoke endpoint returns `MissingAuthenticationToken/403` for the admin function (scratch stack). PASS.
5. **Role-abuse test mechanics**: `aws sts assume-role` into the scratch public-function role from the test admin (test role has a trust policy for the tester only) and attempt the denied calls. PASS prints AccessDenied lines.
6. **Cost ceiling formula** validated against CloudWatch numbers from a 10-minute flood (invocations x avg billed ms x 128 MB). PASS prints estimate vs actual within 25 %.
7. **Deep JSON**: Go's decoder depth/memory for a 256 KiB body of `[` x 262144 -> measure time and RSS; PASS = rejected < 1 s, < 20 MB. (If not, add an explicit depth guard in `DecodePing` first, in T02.)

## Step 1: RED
Implement every attack as a test with the **secure expectation**. Run against local; every failing attack is a real finding: log it in `docs/security/findings.md` (ID, severity, owner task) and fix it in the owning component with a unit test first. Expected early failures to look for: X404 (contact cap missing), X406 (word charset), X307 deep JSON, X304 timing, X802 on Windows-less runs.

## Step 2: GREEN
Fix findings; re-run until local and aws are green. Scanner reports (ZAP/schemathesis/ffuf) must show **zero medium+ alerts** or each alert has a justified `false-positive` entry in `docs/security/findings.md`.

## Step 3: REFACTOR checklist
- [ ] Attack IDs (`X101`...) are in test names and in `abuse-report.md`; one attack per test function; payload tables in `corpus/` with a comment on origin.
- [ ] Tests assert on **outcomes** (status, body shape, state unchanged), never on implementation details.
- [ ] A generic `assertNoLeak(t, resp)` runs on every response in the suite (regexes: `clk_`, `sqlite`, `dynamodb`, `goroutine`, `panic`, `/home/`, `/var/`, `arn:aws`, `.go:`, stack-frame patterns).
- [ ] Rate-limit-sensitive tests use a dedicated key per test to avoid cross-talk.

## Step 4: AUDIT
- Two full green runs (local, aws) with the generated `abuse-report.md`.
- Pre/post state check: version counters and key table unchanged except the intended poisoning rows; no residual lockouts (wait for window expiry).
- Safety: only run against stacks you own (`.it-stack.json` guard); AWS flood tests capped (<= 200 parallel, <= 2 000 requests); notify nobody needed since own account but keep within AWS acceptable-use (no DoS of shared infrastructure; Lambda throttling is the target behaviour).

## Done when
- [ ] Every catalogue row has a passing test (or a documented, accepted residual risk with owner and review date).
- [ ] Zero open High/Critical findings; Medium findings have fixes or accepted-risk entries.
- [ ] `abuse-report.md` generated and handed to T20.

## Status (2026-11-17)
Local target: **done for the rows below, green three times shuffled** (`make abuse-local`, ~22 s; a third throwaway instance `lock` isolates X105). Implemented: X101-X105, X201,
X202, X203, X204, X205, X207, X301, X302, X303-X311, X401-X409, X501, X502, X503, X504, X802; PoC 1 (raw bytes, `actors_test.go`).
Added 2026-11-17: X106 (bounded-gap check; finding F-X106 Low accepted), X107 (revoke refused <= 40 s), X803 (`make abuse-local` ~55 s now).
Added 2026-11-17 (later): X601-X605 (`tests/abuse/x6_mitm_test.go`, real `apiclient` vs hostile throwaway servers, green first run; X606 stays with T12 canary tests).
Not yet done (see Follow-ups): everything AWS (X206, X701-X706, X108, X506), X505, X6xx against the logger and viewer processes, X801, X804, scanners, `abuse-report.md` generator, PoCs 2-7.

## PoC log
* PoC 1 PASS: `TestSendRawSendsExactBytes` (duplicate headers, `//` and `%00` arrive verbatim).

## Follow-ups
* F19-1: X106 uses a median-ratio bound, not Mann-Whitney (PoC 2); X107 covers revoke, not expiry; X505 (aws only: consumed capacity). X105 runs 60 guesses, not 10 000 (lockout trips at 10; the entropy argument is not yet recorded in the report). X303 only checks the per-call cap on a one-fix trail, not retention-bounded total scraping. X501 uses 500 requests, not 2 000.
* F19-2: X601-X605 done against `apiclient` (self-signed cert, no-downgrade, redirects, hostile bodies, cookies). Open: a recording-proxy `ManInTheMiddle()` actor with a CA-trusted wrong-host cert, and the same rows against the logger and viewer processes.
* F19-3: AWS rows (X108, X206, X506, X701-X706); needs the AWS stack (T18 F18-1).
* F19-4: X801, X803, X804 (container posture) and `scripts/abuse-scan.sh` (ZAP/schemathesis/ffuf), `abuse-report.md` generator.
* F19-5: X404 only proves a cap exists (5200 contacts get a 429); the exact value 5000 is not asserted.
- Decide in T20 whether keys get an optional ruleset allowlist (limits blast radius of a leaked view key).
