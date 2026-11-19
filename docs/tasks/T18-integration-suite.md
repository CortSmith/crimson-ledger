# T18: Integration suite against the deployed API and database (local and AWS)

**Depends on:** T08 (local), T10 (AWS), T13, T14, T17 · **Blocks:** T19, T21 · **PLAN.md sections:** [5 API contract](../PLAN.md#5-shared-api-contract-single-source-of-truth), [12 test strategy](../PLAN.md#12-test-strategy-overview-details-in-t17-t19), [7 security](../PLAN.md#7-security-baseline-applies-to-every-task-the-audit-phase-checks-it), [10 workflow](../PLAN.md#10-mandatory-workflow-for-every-task-tdd-with-proof-of-concept-first)

## Goal
One test suite, run unchanged against **two real deployments**: `local` (Docker compose API + SQLite) and `aws` (Lambda + DynamoDB stack).
It covers three situations:
* **A. Good API behaviour**: correct usage returns correct results (functional + contract).
* **B. Bad requests to the API**: misuse and malformed input are rejected safely and consistently.
* **C. Bad API behaviour seen by the apps**: logger, viewer and `apiclient` survive a faulty or hostile server (fault injection).
Bad-actor data-exfiltration attempts are the separate suite in T19 (same harness).

## Outputs
```
tests/integration/target.go            Target{Kind, BaseURL, IngestKey, ViewKey, Admin}, FromEnv(), guards
tests/integration/bootstrap_local.sh   (scripts/it-bootstrap.sh) creates keys via compose `keys` service -> .it-keys.env
tests/integration/bootstrap_aws.sh     deploys a DEDICATED test stack (crimson-ledger-it), creates keys via admin Lambda, prints env
tests/integration/teardown_aws.sh      deletes the test stack, bucket, SSM parameter
tests/integration/a_good_test.go       //go:build integration
tests/integration/b_bad_requests_test.go
tests/integration/c_faulty_api_test.go
tests/integration/helpers.go           doRaw(), withKey(), newRun() (run id), cleanup registry
api/openapi.yaml                       OpenAPI 3.1 description of PLAN section 5 (also drives schemathesis)
Makefile: it-local, it-aws, it-fault
```
Run: `CL_TARGET=local|aws CL_URL=... CL_INGEST_KEY=... CL_VIEW_KEY=... go test -tags integration -count=1 ./tests/integration/...`
(keys come from env only; never flags). Compose shortcut: `docker compose --profile it run --rm it-local`.

## Safety guards (so a test can never hurt real data)
* Targets are a **dedicated test stack/instance**, never the user's real one: local uses a temp `CRIMSON_LEDGER_HOME`; AWS uses stack `crimson-ledger-it` with its own table and keys.
* `FromEnv` refuses to run unless `CL_I_UNDERSTAND_THIS_WRITES_DATA=yes` and, for `aws`, the URL is listed in `.it-stack.json` written by `bootstrap_aws.sh` (so a pasted production URL fails).
* Each run uses a unique `source` (`it-<runid>`) and unique GUIDs/batch ids; all written test data lives in ruleset `rp`; cleanup deletes what the API allows (nothing; the API has no delete) so isolation relies on the dedicated stack being destroyed or pruned (`crimson-db prune --days 0` locally).
* AWS suite keeps total requests under 2 000 per run (cost and politeness) and never exceeds 20 requests/s.

## A. Good behaviour (each runs on both targets)
| ID | Scenario | Expected |
|---|---|---|
| A01 | `GET /v1/health` without a key | 200 `{ok:true,version}` and nothing else; no store access |
| A02 | Ingest example ping (plain JSON) | 200 accepted, counts match |
| A03 | Ingest same ping gzip-encoded | 200 accepted |
| A04 | Repeat same `batchId` | 200 `duplicate:true`; sweep/version unchanged |
| A05 | Sweep after ingest | contains the contacts with last position; fields match PLAN 5.3 exactly (schema validated against `openapi.yaml`) |
| A06 | Sweep conditional GET | first 200 + ETag; second with `If-None-Match` 304; after a new ping 200 again |
| A07 | Players search by prefix, accents, case | finds expected; <2 chars -> 400 |
| A08 | Trail with `since` pagination | every fix once, newest first, `total` right |
| A09 | Bounty after kills | ranking correct, stable ties, repeated kill not double counted |
| A10 | Charts | first write wins; later different rectangle ignored |
| A11 | Out-of-order pings (older after newer) | contact keeps newer position; fix history contains both |
| A12 | Ruleset isolation | data in `rp` invisible under `pvp` |
| A13 | Max-size ping (200 contacts x 50 fixes) | 200 within the budget in PLAN 13 + network |
| A14 | Concurrent ingest (8 clients x 20 pings) + concurrent reads | all accepted; final state equals expected set; no 5xx |
| A15 | Key scopes | ingest key works on POST, view key works on GET |
| A16 | Key lifecycle via admin path (local `keys` CLI / AWS admin Lambda): create, use, revoke | after revoke, 403 `key_disabled` within the cache TTL (poll up to 35 s) |
| A17 | Response hygiene | headers per T05 on every response; `Content-Type: application/json; charset=utf-8`; gzip response when requested |
| A18 | **Real apps end to end** | headless `crimson-scout --selftest-upload <fixture log>` uploads a recorded session; `crimson-ledger --selftest-sweep` reads it back and prints the same contact count (needs T13/T14 selftest flags) |
| A19 | Persistence (local) | `docker compose restart api` -> data still there; `crimson-db check` clean |
| A20 | Persistence/TTL (aws) | written fix items carry `ttl` (read item via the test IAM role); table has TTL enabled (`describe-time-to-live`) |

## B. Bad requests to the API (every row asserts status, error `code`, and that **no data changed**: version/ETag identical afterwards)
| ID | Request | Expected |
|---|---|---|
| B01 | Missing / empty / malformed `Authorization` (7 variants) | 401 `unauthorized`, identical bodies |
| B02 | Unknown key id; known id + wrong secret | 401, same body; response time difference < 20 % (median of 200 each, `slow` tag) |
| B03 | Revoked, expired key | 403 `key_disabled` |
| B04 | Wrong scope both ways | 403 `forbidden` |
| B05 | Malformed JSON, trailing data, empty body, `null`, array instead of object | 400 `invalid_ping`/`invalid_request` |
| B06 | Unknown field in ping | 400 naming the field path |
| B07 | Each limit exceeded by one (201 contacts, 51 fixes, name 25 runes, ...) | 400 with the exact path |
| B08 | NaN/Inf/out-of-range coordinates, timestamps 25 h old / 6 min future, clock skew | 400 |
| B09 | Bad GUID/UUID/ruleset/kind/reaction values; control and bidi chars in names | 400 |
| B10 | Wrong `Content-Type`; unsupported `Content-Encoding` (br, deflate) | 415 |
| B11 | Body over limit (plain and gzip bomb) | 413 within 1 s; server stays healthy (A01 after) |
| B12 | Query abuse: unknown param, duplicate param, missing ruleset, limit 0/-1/huge/NaN, 1 KB `q`, control chars | 400 `invalid_request` |
| B13 | Wrong method on every route; `HEAD`, `OPTIONS`, `TRACE` | 405 with `Allow`; no CORS headers ever |
| B14 | Unknown routes, `/v1`, `/`, trailing slash, `//v1/health`, `/v1/../v1/health` | 404 standard shape (or normalised without leaking) |
| B15 | Unknown valid GUID trail | 404; byte-identical to an expired GUID |
| B16 | Burst of 150 bad-key requests from one client | lockout after 10 -> 429 + `Retry-After`; a valid key from the same IP is also blocked for the window; recovers after it |
| B17 | Rate limit with a valid key (limit + 20 requests in a minute) | 429 `rate_limited` with `Retry-After`; no 5xx |
| B18 | Slowloris (local only): trickle headers/body | connection dropped by the timeouts (<= 15 s); other requests unaffected |
| B19 | Oversized headers (20 KB), 5 000 header lines | rejected (431/400) without crashing |
| B20 | Failed requests leave **no side effects** | after the whole B-suite, version for each ruleset equals the value before it started |
| B21 | AWS: request flood of 200 parallel requests (reserved concurrency 5) | mixture of 200/429; no 5xx from our code; stack healthy afterwards; billing metrics sane |

## C. Faulty or hostile API seen by the apps (`faultapi` from T17; runs in-process, no cloud needed, but also against a real app process)
For each fault mode, run (1) the `apiclient`, (2) the logger pipeline, (3) the viewer poller, and assert the **expected app behaviour**:
| Fault | Logger | Viewer / client |
|---|---|---|
| 500 / 502 / 503 sequences | retries with backoff, same `batchId`, queue grows then drains | banner, keeps old data, retries with backoff |
| 429 + `Retry-After: 3600` | waits <= 60 s cap, never blocks Stop | same |
| 401 / 403 | stops uploading, shows "API key rejected", keeps queue | stops polling, shows settings prompt |
| Slow headers/body | per-attempt timeout 15 s, no goroutine leak | UI stays responsive, no frozen canvas |
| Truncated/invalid JSON, wrong content type, schema drift (extra fields ok, missing fields -> error) | drops/ retries per rules, never panics | error banner, no crash, no partial render |
| 100 MB body | aborted at 4 MiB, memory spike < 20 MB | same |
| Redirect to another host | refused, key not sent (assert on the other host) | same |
| Evil strings (XSS, bidi, 10 MB name) | n/a | rendered as text; oversize rejected; CSP violation count 0 |
| NaN/Inf/out-of-range coordinates, 1 M contacts | n/a | rejected/capped (2 000), frame time within budget |
| Self-signed / expired / wrong-host certificate | refuses to connect with a clear message (cert error text, no key) | same |
| `http://` non-loopback URL in config | refuses to start uploading, explains why | same |
| Server recovers after outage | queue drains oldest-first, nothing lost within bounds | data resumes, banner clears |
| Connection closed mid-body | retry, no duplicate data | retry |
Each row is one table-driven test: `TestC_<fault>_<app>`.

## Step 0: PoC (`scripts/poc/t18/`)
1. **Target abstraction**: one Go test file with `FromEnv` runs `GET /v1/health` against (a) compose API and (b) a scratch AWS stack from T10. PASS prints both 200s and the guard refusing a wrong URL.
2. **OpenAPI validation**: write the minimal `openapi.yaml` for `/v1/health` + `/v1/sweep`, validate a live response with `kin-openapi` (`openapi3filter`). PASS prints valid; then break a field and see invalid.
3. **Schemathesis** (`docker run schemathesis/schemathesis run --checks all --base-url ... api/openapi.yaml -H 'Authorization: Bearer ...'`) against the local API finds no 5xx and no schema violations. PASS records the report; any finding becomes a RED test.
4. **Revocation timing**: measure real revoke-to-403 latency locally and on AWS (container-level cache 30 s). PASS prints both; set the A16 poll window to p99 + 5 s.
5. **AWS test stack lifecycle**: `bootstrap_aws.sh` + `teardown_aws.sh` run twice in a row without leftovers (`aws cloudformation list-stacks`, `aws s3 ls`, `aws ssm describe-parameters`). PASS prints empty lists. Record time (target < 5 min) and cost (should be $0).
6. **Fault injection reachability**: confirm the real `crimson-scout`/`crimson-ledger` selftest binaries can be pointed at `faultapi` via config (`CRIMSON_LEDGER_HOME` temp dir with `logger.json`/`viewer.json`; loopback `http://` allowed). PASS prints behaviour for 3 modes.
7. **Burst safety on AWS**: run B21 at 50 then 200 parallel requests while watching `Throttles`, `ConcurrentExecutions`; confirm the reserved concurrency cap holds. PASS prints metrics.

## Step 1: RED
Write all A, B, C tests first against the skeleton (they fail: no target / not implemented). Each test starts with `t.Parallel()` only where safe (shared state: version counters) and records its `requestId`s so failures can be correlated with logs. Save the first failing run output.

## Step 2: GREEN
Fix **product** bugs found (in the owning task's package, with a unit test added there first) until both targets pass. Differences between targets are allowed only where documented in a `targets_diff` table in the test file (e.g. B18 local-only, A20 aws-only); anything else is a bug.

## Step 3: REFACTOR checklist
- [ ] Helpers hide HTTP plumbing; each scenario reads like the table row (<= 25 lines).
- [ ] Every assertion message includes the scenario id and the `requestId`.
- [ ] No sleeps: polling helper `eventually(t, cond, timeout)` with backoff.
- [ ] Table IDs (A01...) appear in test names so a report can be generated mechanically (`scripts/it-report.sh` -> markdown pass/fail by ID, attached to the release).

## Step 4: AUDIT
- Full run green on `local` and `aws` (two logged runs, with timings).
- Run count/cost: AWS run request count, Lambda invocations, DynamoDB consumed capacity read from CloudWatch afterwards (feeds T21).
- Security: logs of the AWS run searched for secrets: `aws logs filter-log-events --filter-pattern '"clk_"'` returns nothing; the integration env file is git-ignored and 0600; the guard test proves a wrong URL aborts before any request.
- Flake hunt: run local suite 5x consecutively and shuffled; zero flakes.

## Done when
- [ ] All A/B/C rows implemented with IDs and green on both targets (except documented target differences).
- [ ] `it-report.md` generated for both targets and stored with the release.
- [ ] `openapi.yaml` is the contract artifact and passes Schemathesis.

## Status (2026-11-17)
Done in code for the **local** target: `tests/integration/` (`target.go` guards + unit test, `helpers_test.go`, `a_good_test.go`, `b_bad_requests_test.go`, `c_faulty_api_test.go`), `api/openapi.yaml`, `scripts/it-bootstrap.sh`, `make it-local` / `make it-fault`.
`scripts/it-bootstrap.sh` builds `crimson-api`, starts two throwaway instances (main: rate limit 1 000 000/min and `trust_proxy_headers` so each test owns a client IP, because the failed-auth lockout is per IP; rate: default 120/min for B17), creates keys (kept in the process environment only) and runs the suite.
Local run: all A/B/C green, two consecutive `-shuffle=on` runs, ~70 s (A16 and B03 each wait out the 30 s key cache).
Differences from the table (the `targets_diff`): A19, A20, B21 skip (compose restart / AWS only); A16, B03 need `CL_API_BIN` (local); B16 needs a target that trusts `X-Forwarded-For`; B18 local only; B03 tests revoke only (no expiry key); C covers the `apiclient` only.

## PoC log
* PoC 1 (target abstraction): local half done (guard refuses wrong URL, tested in `target_test.go`); AWS half open (no account).
* PoC 2 (kin-openapi), 3 (schemathesis), 4 (revocation timing), 5, 7: open. Revocation timing observed locally: 403 `key_disabled` arrives at ~30 s (the cache TTL), A16/B03 poll up to 35 s.
* PoC 6: covered in-process via `faultapi` + `apiclient`; real-binary run open.

## Follow-ups
* F18-1: AWS half: `bootstrap_aws.sh`, `teardown_aws.sh`, `.it-stack.json`, admin-Lambda key lifecycle (A16/B03), A20, B21, two logged AWS runs, CloudWatch cost read (T21). Needs a real AWS account.
* F18-2: A19 (persistence across `docker compose restart api`, `crimson-db check`) needs a compose `it` profile.
* F18-3 (**plan/contract bug**): PLAN 5.2 allows 200 contacts x 50 fixes but the body cap is 256 KiB; the full shape is ~700 KB and gets 413. A13 therefore sends 200 x 12 and pins the 413 of the full shape. Decide: raise the cap, lower the fix/contact limit, or have the logger batch to fit (check it does).
* F18-4: B06 expects the unknown-field *path* in the error; the server deliberately answers every JSON-structure error with one generic message (`ingest_test.go`). Either amend the plan row or return the path; test asserts the code only.
* F18-5: C columns for the logger pipeline (queue, same `batchId`, Stop never blocked) and the viewer poller (banner, keep old data) and the oversize/CSP rows; need those harnesses.
* F18-6: validate live responses against `api/openapi.yaml` with `kin-openapi` (A05) and run Schemathesis; needs new module dependencies.
* F18-7: B02 timing comparison (`slow` tag), B03 expired-key case, `scripts/it-report.sh` (markdown pass/fail by ID), `it-compose` profile.
* F18-8: Go's `MaxHeaderBytes` adds 4 KiB, so the real header limit is ~20 KiB, not 16 KiB (B19 sends 32 KiB). Harmless; note in T08/T20.
