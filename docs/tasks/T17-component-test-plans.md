# T17: Test plans for viewer, logger, api and database

**Depends on:** T00 (write the harnesses early; each component task then fills its matrix) · **Blocks:** T18 · **PLAN.md sections:** [10 workflow](../PLAN.md#10-mandatory-workflow-for-every-task-tdd-with-proof-of-concept-first), [12 test strategy](../PLAN.md#12-test-strategy-overview-details-in-t17-t19), [13 perf budgets](../PLAN.md#13-performance-budgets-measured-in-t21-every-task-logs-its-benchmarks-against-these), [7 security](../PLAN.md#7-security-baseline-applies-to-every-task-the-audit-phase-checks-it)

## Goal
Define, per component, *what must be tested, with which tool, at which coverage*, and build the shared test harnesses once so every task
uses the same ones. This file is the checklist a reviewer uses to say "the viewer is tested enough". Integration and abuse tests against deployed
systems are T18 and T19; this task covers everything that runs **without** a deployed target.

## Shared harnesses (built in this task, used by all)
```
internal/testkit/clock.go          FakeClock (Now, Advance, After)
internal/testkit/home.go           TempHome(t) sets CRIMSON_LEDGER_HOME to t.TempDir() (0700)
internal/testkit/session.go        GenerateSession(seed, opts) -> []string combat-log lines  AND  []domain.Ping (deterministic, seeded)
internal/testkit/ping.go           builders: APing(opts...), AContact(...), AFix(...), valid by default, one option per field to break
internal/testkit/httpx.go          NewTestAPI(t, store) (*httptest.Server, keys{ingest,view}) wired with the real apiserver
internal/testkit/canary.go         Canary key constant + AssertNoCanary(t, outputs ...string) used in every secret-leak test
internal/testkit/faultapi/         adversarial/faulty API server used by logger/viewer/apiclient tests (also T18): modes listed below
internal/testkit/golden.go         Golden(t, name, got) with -update flag
```
`faultapi` modes (one constructor option each): `Status(code)`, `SlowBody(d)`, `SlowHeaders(d)`, `Truncate(n)`, `WrongContentType`, `HugeBody(n)`, `Redirect(to)`,
`RetryAfter(sec)`, `Flaky(n of m)`, `SchemaDrift` (extra/missing fields), `EvilStrings` (XSS/bidi/control chars in names), `NaNCoords`, `DeepJSON(depth)`,
`CloseMidBody`, `TLSBadCert`, `HTTPDowngrade`.

## Global rules (every component)
* Test pyramid targets: **unit 70 %, component 20 %, end-to-end 10 %** by count; none of them may need a network except loopback.
* Deterministic: no `time.Now()`, `rand` without seed, or `sleep` in tests (inject `testkit.FakeClock`); tests pass with `-race -count=10 -shuffle=on`.
* Coverage gates (statement, enforced by `scripts/check.sh` via `MIN_COVER`): domain 90, store 85, apiserver 90, apiclient 90, config 85, logparse 85, apps 85, database 80. A gate may only be lowered with a written reason in the task's Follow-ups.
* Mutation testing on security-critical packages (`internal/apiserver` auth/keys, `internal/domain` validation, `internal/config` validation) using `go-mutesting` or `gremlins` in the build image: **mutation score >= 80 %**; surviving mutants reviewed manually and either killed with a new test or justified. Run nightly/pre-release, not on every commit.
* Fuzzing: every parser of untrusted input has a `Fuzz*` target with a seed corpus in `testdata/fuzz/`; 60-90 s per target in AUDIT, 10 min nightly. Crashers become permanent regression files.
* Property tests (`testing/quick` or `pgregory.net/rapid`): ping encode/decode round trip, batcher output always valid, ETag stability, key generate/parse round trip, store apply order independence (apply pings in any permutation -> same final state for contacts/fixes/kills).
* Golden files for every JSON response shape (PLAN 5.3) and every user-facing error message.
* Flaky policy: a flaky test is a bug; quarantine is not allowed; fix or delete within the task.
* Each test named `TestUnit_WhenCondition_Expectation`; table rows have `name`.

## Component matrices

### Database (`database/`, `internal/store/*`)
| Area | Tests |
|---|---|
| Schema | constraints reject bad enums; PK uniqueness; every index exists (`sqlite_master` query); no table without PK |
| Migrations | fresh apply, idempotent, rollback of a failed migration, newer-DB refusal, sequential naming, destructive marker rule |
| Open/pragmas | WAL, foreign keys, busy timeout, 0600/0700, single connection, symlink refusal |
| Contract suite | all 13 rules (T04) on SQLite, DynamoDB Local (T09), and real DynamoDB once per release |
| Concurrency | 8 writers + 8 readers under `-race`; no `SQLITE_BUSY`; final state equals serial application |
| Integrity | `PRAGMA integrity_check` / `foreign_key_check` after the soak; backup restores and re-passes the suite |
| Retention | prune deletes only what it should; TTL attribute present on DynamoDB items (assert on written items) |
| Capacity (Dynamo) | consumed capacity per operation recorded and compared with the T09 budget |
| Failure injection | disk full (tmpfs 1 MB), read-only file, killed process mid-write (SIGKILL loop 50x then `integrity_check`), DynamoDB throttle/unprocessed items |
| Performance | benchmarks in PLAN 13 (`ApplyPing`, `Sweep`, `Search`, `Trail`) with `benchstat` baseline |

### API (`api/`, `internal/apiserver`)
| Area | Tests |
|---|---|
| Routing | every route x every method (405/404 shape), trailing slash, case, double slash, encoded slash |
| Auth | PLAN 5.4 table, scopes, key states, uniform 401, lockout, cache TTL revocation, timing equality (statistical, tagged `slow`) |
| Validation | one test row per limit in PLAN 5.2; every query parameter in T07 rules |
| Handlers | golden JSON, ETag/304, gzip response, pagination `since`, ruleset isolation |
| Middleware | order (auth before body read), panic recovery, headers on every response, request-id uniqueness |
| Limits & timeouts | body, header, slow client, concurrent requests, graceful shutdown |
| Local host | CLI commands, config defaults, TLS, bind rules, pepper file handling |
| Lambda | recorded Function URL events, base64 bodies, cold-start failure closed, admin handler events, template tests |
| Observability | logs JSON, contain request id/route/status/duration, never key/body/query `q` |
| Performance | PLAN 13 rows + allocations/op tracked |
| Fuzz | ping body, query strings, auth header, key parser, gzip wrapper |

### Logger (`logger/`, `internal/logparse`, `internal/ui/settings`)
| Area | Tests |
|---|---|
| Parser | ported go-radar tests (all must pass) + new fuzz + 1 M-line throughput benchmark |
| Tailer | growth, partial line, rotation, truncation, CRLF, long line, file vanishes, permission denied, symlink escape |
| Batcher | limits, coalescing thresholds, merge, determinism, UUIDv4, property: output always passes `ValidatePing` |
| Queue/outbox | bounds, FIFO, restart persistence, corruption tolerance, perms, drop accounting |
| Uploader vs `faultapi` | every fault mode: retries, same `batchId`, no retry on 4xx, stop on 401/403, honours Retry-After, never panics, bounded memory under permanent failure |
| Settings & key widget | validation, masking protocol (T12), reveal/re-mask, persistence 0600, canary never in logs/outbox/activity |
| State machine | table of (state, event) -> state; invalid transitions rejected |
| Lifecycle | start/stop/flush, context cancel leaks no goroutines (`goleak`), clean exit within 3 s |
| Missing libs | launcher container test (T12 PoC 8) on 3 distros |
| Performance/soak | 1 h simulated session: RSS flat, CPU < 1 % idle, upload rate within T09 capacity |

### Viewer (`viewer/`)
| Area | Tests |
|---|---|
| Poller | ETag/304, backoff, pause/resume, 401 stop, interval change, jitter bounds (fake clock) |
| Bindings | each bound function with fake `apiclient`; argument validation (guid, q, map); no filesystem writes besides `viewer.json` |
| Rendering (JS, Node+jsdom test image) | coordinate transforms (golden from go-radar), list grouping, map picker filter/keyboard, trail drawing, bounty panel, empty/error/loading states |
| Security | XSS corpus (names, zone strings) renders as text; CSP exact; no `innerHTML`; key absent from DOM until reveal; canary tests |
| Faulty server | `faultapi` modes through the real poller: banner shown, no crash, UI recovers when server recovers, never shows raw error bodies |
| Visual regression | 4 fixture states screenshot-compared (Xvfb WebView), 1 % tolerance |
| Missing libs | launcher container test on 3 distros |
| Performance | PLAN 13 rows (first paint, 60 fps with 2 000 blips, idle CPU, memory soak) |
| Accessibility | keyboard path through all controls; shape+colour encoding test; axe-core run on the settings page in the Node image |

### Cross-cutting
* `scripts/check.sh` is the only gate; CI runs it inside `Dockerfile.build --target check`.
* A **test inventory** file `docs/test-inventory.md` is generated by `scripts/test-inventory.sh` (counts per package, fuzz targets, coverage) and attached to the release; T20 reviewers use it.

## Step 0: PoC (`scripts/poc/t17/`)
1. **Mutation tool works** on a package with `CGO_ENABLED=0` in the build image; record runtime on `internal/domain` (budget: < 10 min) and one killed + one survived mutant example. PASS prints score.
2. **`goleak`** detects an intentionally leaked goroutine and passes on a clean one. PASS.
3. **`faultapi` modes** each reproducible with a 10-line client (e.g. `SlowBody` makes a client with a 1 s timeout fail). PASS table printed for all 16 modes.
4. **Deterministic session generator**: same seed -> byte-identical lines; parsed by the ported parser into expected contact counts (golden). PASS.
5. **`-shuffle=on -count=10`** on a sample package passes (finds order dependence early). PASS.
6. **`benchstat`** comparison between two runs prints a delta table and the gate script exits non-zero on a 15 % regression. PASS (used by T21).
7. **Node test image** runs one jsdom test and one axe-core check offline (`npm ci` from a lockfile with integrity hashes). PASS.

## Step 1: RED
Write the harnesses' own tests first: `TestFakeClock`, `TestGenerateSession_Deterministic`, `TestFaultAPI_Modes` (table over all modes), `TestAssertNoCanary` (fails when the canary is present), `TestGolden_UpdateFlag`.

## Step 2: GREEN
Implement the harnesses (small, boring, documented). Add a `Makefile` target `make test-matrix` that prints each matrix row with the test names that satisfy it (a script greps for the names recorded in the matrices; missing rows fail the target) so coverage of the *plan* is mechanically checked.

## Step 3: REFACTOR checklist
- [ ] Harness packages have usage examples in `doc.go`; builders have one option per field and sensible valid defaults.
- [ ] `faultapi` mode names are the same strings used in the matrices and in T18/T19.
- [ ] No test helper is duplicated in a component package after this task (grep for local `fakeClock`, `newTestServer`).

## Step 4: AUDIT
- check.sh green; `-race -shuffle=on -count=10` for `./internal/testkit/...`.
- `make test-matrix` lists no missing rows for components already built; remaining rows are tracked as Follow-ups per component task.
- Security: `testkit` is under `internal/` and imported only from `_test.go` files (arch rule: non-test code must not import `internal/testkit`); `faultapi` binds `127.0.0.1` only.

## Done when
- [ ] Harnesses exist and are used by at least T05, T11, T13, T14.
- [ ] Matrices above are copied into each component task's Done-when as links, and each row maps to named tests by the end of that task.

## PoC log
Run 2026-11-17 (Go 1.25.0, linux/amd64). Items 3-5 are covered by real tests instead of throwaway programs:
- PoC 3 (all 16 faultapi modes reproducible): `go test ./internal/testkit/faultapi` runs one subtest per mode (`TestFaultAPI_Modes`, plus TLSBadCert, HTTPDowngrade and healthy). PASS.
- PoC 4 (deterministic session generator): `TestGenerateSession_Deterministic` (same seed byte-identical, other seed differs, pings validate) and `TestGenerateSession_LinesParseToTheExpectedPlayers` (ported parser: 4 players x 8 lines each). PASS.
- PoC 5 (`-shuffle=on -count=10`): `go test -race -shuffle=on -count=10 ./internal/testkit/...` ok (testkit 81.3 %, faultapi 94.1 %). PASS.
- PoC 1 (mutation tool), 2 (goleak), 6 (benchstat gate), 7 (Node image, jsdom + axe-core): **not run**, see Follow-ups F17-1..4.

## Progress
Done 2026-11-17: `internal/testkit` (`FakeClock`, `TempHome`, `APing`/`AContact`/`AFix` builders with one option per field, `GenerateSession`, `Golden` with `-update`,
`Canary` + `AssertNoCanary`, `NewTestAPI` over the real apiserver and SQLite) and `internal/testkit/faultapi` (all 16 modes, loopback only).
Arch rule `TestTestkitIsImportedOnlyFromTests` added. `scripts/check.sh ./internal/testkit/...` passes gofmt, vet (3 OSes), staticcheck and golangci-lint; its govulncheck step fails
only on the known Go 1.25.0 stdlib findings (same as the rest of the repo). Tests and the 85 % coverage gate pass by hand.

## Follow-ups
- F17-1: PoC 1, mutation tool (`go-mutesting`/`gremlins`) in `Dockerfile.build`; score on `internal/domain`, `internal/apiserver` auth/keys, `internal/config`.
- F17-2: PoC 2, add `go.uber.org/goleak` and a `testkit.NoLeaks(t)` helper.
- F17-3: PoC 6, `benchstat` regression gate script (15 % rule) for T21.
- F17-4: PoC 7, Node test image with jsdom + axe-core and a lockfile with integrity hashes.
- F17-5: `make test-matrix` and `scripts/test-inventory.sh` (needs the matrices' test names recorded per component).
- F17-6: migrate duplicates onto testkit: `storetest.FakeClock`, `apiserver` `fakeClock`/`harness`, local builders in `storetest/builders.go`; then grep-check no local `fakeClock`/`newTestServer` remains.
- F17-7: use the harnesses from T05, T11, T13, T14 tests (Done-when) and copy the matrices into those task files.
- F17-8: `GenerateSession` lines are parser-valid but not yet fed through the tracker to compare against `Pings` (needs chart bounds).
