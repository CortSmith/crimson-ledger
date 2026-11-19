# T04: `Store` interface, shared contract suite, SQLite implementation

**Depends on:** T02, T03 · **Blocks:** T05, T06, T07, T09 · **PLAN.md sections:** [2 D3](../PLAN.md#d3-database-one-store-interface-two-implementations), [5 API contract](../PLAN.md#5-shared-api-contract-single-source-of-truth), [6 vocabulary](../PLAN.md#6-shared-vocabulary), [10 workflow](../PLAN.md#10-mandatory-workflow-for-every-task-tdd-with-proof-of-concept-first)

## Goal
Define the one interface the API uses to talk to a database, write a **contract test suite** that any implementation
must pass, and implement SQLite against it. T09 reuses the suite for DynamoDB, so *the suite is the spec*.

## Outputs
```
internal/store/store.go                 Store, KeyStore, query/result types, sentinel errors
internal/store/storetest/suite.go       func Run(t *testing.T, newStore func(t *testing.T, clock *FakeClock) store.Store)
internal/store/storetest/clock.go       FakeClock
internal/store/sqlitestore/sqlitestore.go (+ apply.go, read.go, keys.go, prune.go)
internal/store/sqlitestore/sqlitestore_test.go    just calls storetest.Run with a temp-file DB
```

## Interface
```go
package store

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

type Store interface {
	// ApplyPing stores a validated ping. It is idempotent on p.BatchID: a repeat returns Duplicate=true and changes nothing.
	ApplyPing(ctx context.Context, p domain.Ping, now time.Time) (ApplyResult, error)

	// Version is a cheap number that changes whenever data for the ruleset changes; used for ETags.
	Version(ctx context.Context, rs domain.Ruleset) (int64, error)

	Sweep(ctx context.Context, q SweepQuery) ([]domain.SweepContact, error)
	SearchPlayers(ctx context.Context, rs domain.Ruleset, q string, limit int) ([]PlayerSummary, error)
	Trail(ctx context.Context, rs domain.Ruleset, guid string, f TrailFilter) (Trail, error) // ErrNotFound
	Bounty(ctx context.Context, rs domain.Ruleset, limit int) ([]domain.KillTotal, error)
	Charts(ctx context.Context) ([]domain.Chart, error)

	Keys() KeyStore
	// Prune deletes fixes, kills and batch markers older than cutoff. A no-op on DynamoDB (TTL does it).
	Prune(ctx context.Context, cutoff time.Time) (PruneResult, error)
	Close() error
}

type KeyStore interface {
	Put(ctx context.Context, k KeyRecord) error              // ErrConflict if id exists
	Get(ctx context.Context, id string) (KeyRecord, error)   // ErrNotFound
	List(ctx context.Context) ([]KeyRecord, error)
	SetDisabled(ctx context.Context, id string, disabled bool) error
	Touch(ctx context.Context, id string, now time.Time) error // best-effort last_used_at; may be throttled
}

type SweepQuery struct {
	Ruleset domain.Ruleset
	Now     time.Time
	MaxAge  time.Duration // default 15 min, max 24 h
	MapID   *int          // optional filter on the last known map
	GUID    string        // optional single contact
}
type ApplyResult struct{ Accepted, Duplicate bool; Contacts, Fixes, Kills int }
```
Contract rules every implementation must satisfy (each is one test in the suite):

1. `ApplyPing` twice with the same `BatchID` -> second returns `Duplicate`, counts and `Version` unchanged.
2. Contact upsert: newer `lastSeen` wins position/reaction; an **older** ping never moves a contact backwards (out-of-order uploads).
3. Fixes are keyed by `(guid,t,mapId)`: re-sending a fix does not duplicate it. `Trail` returns newest first, honours `map`, `since`, `limit`, and `total` counts all fixes for the guid.
4. Kills are keyed by `(t,killer,victim)`; `Bounty` ranks by kill count desc, then `lastKill` desc, then guid asc (stable). A repeated kill is not double counted.
5. Charts: **first write wins**; a later ping with a different rectangle for an existing `mapId` is ignored (a rogue key must not move maps).
6. `SearchPlayers`: `NormalizeName` of `q` is a **prefix of any word** of the normalized name ("mc" finds "Mc Cheeki" and "Bob Mcgee"? no: "mcgee" word starts with "mc", so yes). Case/accents ignored. `q` shorter than 2 returns empty. Ordered by `lastSeen` desc, ties by guid.
7. `Sweep` returns contacts with `lastSeen >= now-MaxAge`, filtered by `MapID`/`GUID`, ordered by `lastSeen` desc then guid.
8. Rulesets are isolated: data for `pvp` never appears under `normal`.
9. `Version` increases by exactly 1 per *accepted* ping (not per duplicate).
10. Keys: put/get/list/disable round trip; `Get` of an unknown id is `ErrNotFound`; the stored `hash` bytes round-trip exactly.
11. `Prune(cutoff)` removes old fixes/kills/batch markers and keeps contacts, charts, keys (SQLite). The suite takes a `capabilities` flag so DynamoDB can opt out (`Prune` there returns zero and the test is skipped).
12. Context cancellation: a cancelled context returns `context.Canceled` without partial writes (SQLite: a transaction).
13. Concurrency: 8 goroutines applying overlapping pings (race detector on) produce the same final state as serial application.

## Step 0: PoC (`scripts/poc/t04/`)
1. **Upsert that never moves backwards**, in SQLite:
   ```sql
   INSERT INTO contacts (ruleset,guid,name,raw,reaction,first_seen,last_seen,last_map,last_x,last_y,last_kind)
   VALUES (?,?,?,?,?,?,?,?,?,?,?)
   ON CONFLICT (ruleset,guid) DO UPDATE SET
     name=excluded.name, raw=excluded.raw, reaction=excluded.reaction,
     first_seen=MIN(first_seen, excluded.first_seen),
     last_seen=excluded.last_seen, last_map=excluded.last_map, last_x=excluded.last_x, last_y=excluded.last_y, last_kind=excluded.last_kind
   WHERE excluded.last_seen >= contacts.last_seen;
   ```
   Insert t=200, then t=100: row keeps t=200. But `first_seen` must still lower to 100: the `WHERE` blocks the whole update, so
   the PoC must show the two-statement fix (`UPDATE contacts SET first_seen=MIN(...)` separately). PASS = both asserted.
2. **Version counter** `INSERT INTO ping_versions ... ON CONFLICT DO UPDATE SET version=version+1 RETURNING version` works on the bundled SQLite (needs >= 3.35). PASS prints the version.
3. **Whole ping in one transaction** with 200 contacts x 50 fixes = 10 000 inserts finishes under 150 ms using a prepared statement per table. Record the time. (If slower, switch to multi-row `INSERT ... VALUES (...),(...)` chunks of 500.)
4. **Word-prefix search** from T03 PoC re-run through the Go code path with accent folding via `domain.NormalizeName`.
5. **Cancellation** mid-transaction rolls everything back (cancel the context after the 2nd insert; table is empty).

## Step 1: RED
Write `storetest.Run` completely (all 13 rules above as subtests, each named `rule_NN_description`) and a
`sqlitestore_test.go` that calls it. Run: every subtest fails (`not implemented`). Save output.
`FakeClock` is a struct with `Now()` and `Advance(d)`; the store receives `now` as a parameter so the suite controls time.

## Step 2: GREEN
Structure for readability: `ApplyPing` = `begin tx` -> `checkBatch` -> `upsertContacts` -> `insertFixes` -> `upsertCharts`
-> `insertKills` -> `bumpVersion` -> `recordBatch` -> `commit`, one small unexported method each, all taking `*sql.Tx`.
Prepared statements are created per call inside the transaction (SQLite makes this cheap) so no shared state.
Search words: for each contact, delete then re-insert `contact_words` only when the normalized name changed.
`Sweep` SQL: `SELECT ... FROM contacts WHERE ruleset=? AND last_seen >= ? [AND last_map=?] ORDER BY last_seen DESC, guid ASC LIMIT 2000`.
Never interpolate: optional filters are chosen between fixed SQL strings (a 4-way switch), not concatenated.

## Step 3: REFACTOR checklist
- [x] `sqlitestore` files <= 250 lines each; each exported method <= 25 lines (delegates to helpers).
- [x] Row scanning in one place (`scanContact`, `scanFix`); no duplicated column lists (shared `const contactCols`).
- [x] The suite has helper builders (`aPing(opts...)`, `aContact(...)`) so each rule reads in <= 12 lines.
- [x] Error wrapping: `fmt.Errorf("apply ping %s: %w", p.BatchID, err)`; no raw `sql.ErrNoRows` leaks (mapped to `ErrNotFound`).
- [x] Constants for limits (`maxSweepRows = 2000`, `maxTrailRows = 2000`).

## Step 4: AUDIT
- `scripts/check.sh ./internal/store/...` green; coverage >= 85% on `sqlitestore`.
- `go test -race -count=20 ./internal/store/sqlitestore` (flake hunt for the concurrency rule).
- Benchmarks: `BenchmarkApplyPing_Max` (target < 150 ms), `BenchmarkSweep_10kContacts` (target < 20 ms). Log them; Lambda/SQLite time matters for cost.
- Security: grep proves no string-built SQL; run `sqlite3 ledger.db 'PRAGMA foreign_key_check; PRAGMA integrity_check;'` after the concurrency test (via `crimson-db check`).

## Done when
- [x] All 13 contract rules pass on SQLite and are documented in the suite (it is the spec for T09).
- [x] `store.Store` has no SQLite types in its signatures.
- [x] check.sh green.

## PoC log
`CGO_ENABLED=0 go run ./scripts/poc/t04` (PoC removed once the contract suite covered it):
```
PASS upsert keeps newest, lowers first_seen
PASS version counter 2
PASS 10000 inserts in 49.5ms
PASS cancel rolls back, kills = 0 context canceled
```
Word-prefix search (PoC point 4) was proved in T03 and is now rule 6 of the suite.
Design change from the PoC: the upsert is done in Go (select old row, then insert / lower first_seen / conditional update)
rather than `ON CONFLICT ... WHERE`, because the two-statement fix is needed anyway and this also tells us when to rewrite `contact_words`.

RED: `go test ./internal/store/...` -> `sqlitestore_test.go:13:12: undefined: Open` (build failed).
GREEN: `go test -race ./internal/store/...` ok; `-count=20 -race` ok (flake hunt on rule 13).
Benchmarks (5 iterations, this machine): `ApplyPing_Max` 62 ms (target < 150), `Sweep_10kContacts` 5.7 ms (target < 20).
Coverage `sqlitestore` 84.5%+ (project gate 80). Windows and darwin `go vet` clean. `archtest` ok. No string-built SQL (grep clean).
AUDIT: `scripts/check.sh` could NOT run: staticcheck, golangci-lint and govulncheck are not installed here (same as T03). Run it once they are.

## Follow-ups
- Run `scripts/check.sh ./internal/store/...` when the lint tools are installed; coverage is a hair under the 85% task target (84.5%+ vs gate 80%).
- `KeyStore.Touch` is not throttled in SQLite; T05 decides whether to throttle in the caller.
- Contract rules use fixed `batch-NNNN` ids: the store trusts validated input (T02).
