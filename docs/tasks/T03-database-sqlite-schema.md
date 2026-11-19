# T03: Database folder: SQLite schema, migrations, `crimson-db`

**Depends on:** T00, T01 · **Blocks:** T04 · **PLAN.md sections:** [2 D3](../PLAN.md#d3-database-one-store-interface-two-implementations), [4 config](../PLAN.md#4-config-crimson-ledger), [7 security](../PLAN.md#7-security-baseline-applies-to-every-task-the-audit-phase-checks-it), [10 workflow](../PLAN.md#10-mandatory-workflow-for-every-task-tdd-with-proof-of-concept-first)

## Goal
The `database/` folder owns the SQLite schema, an embedded forward-only migration runner, safe open settings, and
the admin CLI `crimson-db`. No query code for the API lives here (that is T04); this task makes the file exist,
versioned, backed up and prunable.

## Outputs
```
database/doc.go
database/migrations/0001_init.sql
database/migrations/embed.go            //go:embed *.sql  -> var FS embed.FS
database/db.go                          Open(path) (*sql.DB, error)   [pragmas, 0600, single writer]
database/migrate.go                     Migrate(ctx, db) error ; Version(ctx, db) (int, error)
database/maintenance.go                Backup(ctx, db, destDir) (string, error) ; Prune(ctx, db, cutoff) ; Integrity(ctx, db)
database/cmd/crimson-db/main.go         migrate | status | backup | prune | check | vacuum
database/*_test.go
```
Driver: `modernc.org/sqlite` (pure Go, `CGO_ENABLED=0`), imported as `_ "modernc.org/sqlite"`.

## Schema (`0001_init.sql`)
```sql
-- Every ruleset has its own universe of contacts (same GUID on two servers is two rows).
CREATE TABLE contacts (
  ruleset    TEXT    NOT NULL CHECK (ruleset IN ('normal','pvp','rp','rppvp')),
  guid       TEXT    NOT NULL,
  name       TEXT    NOT NULL,
  raw        TEXT    NOT NULL,
  reaction   TEXT    NOT NULL CHECK (reaction IN ('friendly','hostile','neutral')),
  first_seen INTEGER NOT NULL,
  last_seen  INTEGER NOT NULL,
  last_map   INTEGER,
  last_x     REAL,
  last_y     REAL,
  last_kind  TEXT CHECK (last_kind IN ('exact','approx')),
  PRIMARY KEY (ruleset, guid)
) WITHOUT ROWID;
CREATE INDEX contacts_by_seen ON contacts (ruleset, last_seen DESC);

-- Name search: one row per normalized word of the name; prefix match = range scan on (ruleset, word).
CREATE TABLE contact_words (
  ruleset TEXT NOT NULL, word TEXT NOT NULL, guid TEXT NOT NULL,
  PRIMARY KEY (ruleset, word, guid)
) WITHOUT ROWID;

CREATE TABLE fixes (
  ruleset TEXT NOT NULL, guid TEXT NOT NULL, t INTEGER NOT NULL, map_id INTEGER NOT NULL,
  x REAL NOT NULL, y REAL NOT NULL, kind TEXT NOT NULL CHECK (kind IN ('exact','approx')),
  PRIMARY KEY (ruleset, guid, t, map_id)
) WITHOUT ROWID;
CREATE INDEX fixes_by_time ON fixes (t);          -- retention pruning

CREATE TABLE charts (
  map_id INTEGER PRIMARY KEY, zone TEXT NOT NULL,
  x0 REAL NOT NULL, y0 REAL NOT NULL, x1 REAL NOT NULL, y1 REAL NOT NULL
);

CREATE TABLE kills (
  ruleset TEXT NOT NULL, t INTEGER NOT NULL, killer TEXT NOT NULL, victim TEXT NOT NULL, map_id INTEGER NOT NULL,
  PRIMARY KEY (ruleset, t, killer, victim)
) WITHOUT ROWID;
CREATE INDEX kills_by_killer ON kills (ruleset, killer);

CREATE TABLE ping_versions (ruleset TEXT PRIMARY KEY, version INTEGER NOT NULL);   -- the "tripwire" for ETags
CREATE TABLE batches (batch_id TEXT PRIMARY KEY, received_at INTEGER NOT NULL);     -- idempotency

CREATE TABLE api_keys (
  id TEXT PRIMARY KEY, scope TEXT NOT NULL CHECK (scope IN ('ingest','view')), label TEXT NOT NULL,
  hash BLOB NOT NULL, created_at INTEGER NOT NULL, expires_at INTEGER, disabled INTEGER NOT NULL DEFAULT 0,
  last_used_at INTEGER
);
```
`schema_migrations(version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)` is created by the runner, not by a migration file.

## Interfaces
```go
// Open opens (creating with mode 0600) the SQLite file with safe pragmas and one connection.
func Open(path string) (*sql.DB, error)

// Migrate applies every embedded migration newer than the database, each in its own transaction.
// It refuses (ErrNewerDatabase) when the database is newer than this binary.
func Migrate(ctx context.Context, db *sql.DB) error

var ErrNewerDatabase = errors.New("database was created by a newer crimson-ledger")
```
DSN pragmas: `_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)`.
`db.SetMaxOpenConns(1)` (SQLite is a single writer; a pool of 1 removes `SQLITE_BUSY` entirely at this load).

## Step 0: PoC (`scripts/poc/t03/`)
1. **Pure-Go driver works with `CGO_ENABLED=0`.** `CGO_ENABLED=0 go run .` opens `:memory:`, creates a table, inserts,
   reads back. PASS prints `PASS open`.
2. **Pragmas apply.** Query `PRAGMA journal_mode;`, `PRAGMA foreign_keys;`, `PRAGMA busy_timeout;` after opening with the DSN
   above. PASS = `wal`, `1`, `5000`.
3. **Prefix range scan uses the primary key.** Insert 100 k words; `EXPLAIN QUERY PLAN SELECT guid FROM contact_words WHERE
   ruleset=? AND word >= ? AND word < ?` must say `SEARCH ... USING PRIMARY KEY` (not `SCAN`). Record elapsed time for the query (< 5 ms).
   Prefix upper bound: `q + "\U0010FFFF"`.
4. **`VACUUM INTO` backup** produces a file that opens and passes `PRAGMA integrity_check` while another connection writes. PASS prints `ok`.
5. **File mode.** After `Open("x.db")`, `stat` mode is 0600 (create the file with `os.OpenFile(..., 0o600)` *before* handing the path to sqlite).
6. **Downgrade guard idea.** Insert `schema_migrations` version 99, assert `Migrate` returns `ErrNewerDatabase` (PoC the check, `SELECT MAX(version)`).

## Step 1: RED
- `TestOpen_Pragmas`, `TestOpen_FileMode0600`, `TestOpen_CreatesParentDir0700`.
- `TestMigrate_FreshDB_AppliesAll`, `TestMigrate_Idempotent` (run twice, version unchanged), `TestMigrate_RollsBackBrokenMigration` (use a test `fs.FS` with a bad second file; assert first applied, DB at version 1, error mentions filename), `TestMigrate_RefusesNewerDatabase`.
- `TestMigrationFilesAreSequential` (names `NNNN_name.sql`, no gaps, no duplicates).
- `TestSchema_ChecksRejectBadRows` (insert `reaction='x'` fails).
- `TestBackup_ProducesOpenableFile`, `TestBackup_FileMode0600`, `TestPrune_DeletesOldFixesAndBatchesOnly` (contacts and kills untouched).
- CLI: `TestCLI_StatusPrintsVersion` using `run(args, stdout) int` (main only calls `os.Exit(run(...))`).

## Step 2: GREEN (key code)
```go
func Migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations
		(version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	cur, err := Version(ctx, db)
	if err != nil { return err }
	files, err := listMigrations(migrations.FS)          // sorted, validated names
	if err != nil { return err }
	if len(files) > 0 && cur > files[len(files)-1].version { return ErrNewerDatabase }
	for _, f := range files {
		if f.version <= cur { continue }
		if err := applyOne(ctx, db, f); err != nil { return fmt.Errorf("migration %s: %w", f.name, err) }
	}
	return nil
}
```
`applyOne`: `BeginTx`, `Exec(sql)`, `INSERT INTO schema_migrations`, `Commit`; deferred `Rollback`.
Backup: `VACUUM INTO '<dest>'` with the dest path validated (no quote characters; parameter binding is not allowed in
`VACUUM INTO` on every version, so build the literal with `strings.ReplaceAll(path, "'", "''")`), file renamed to
`ledger-YYYYMMDD-HHMMSS.db`, chmod 0600, keep newest N=7 (`--keep`).
Prune: `DELETE FROM fixes WHERE t < ?`, `DELETE FROM batches WHERE received_at < ?`, `DELETE FROM kills WHERE t < ?`; run in chunks of 10 000 rows so the write lock is never held long.

CLI (`crimson-db`): `migrate`, `status`, `backup [--keep 7]`, `prune [--days N]` (default from `api.json`), `check`
(integrity + foreign_key_check), `vacuum`. Reads the DB path from `api.json` (`config.Load`), `--db` overrides. Exit
codes: 0 ok, 1 error, 2 usage.

## Step 3: REFACTOR checklist
- [ ] SQL lives only in `.sql` files and in small `const` strings in `maintenance.go`; no SQL assembled from user input except the validated backup path.
- [ ] `main.go` <= 30 lines; a `run(args []string, out io.Writer) int` function holds the logic.
- [ ] Each migration file starts with a comment describing why; no destructive statements without a `-- DESTRUCTIVE` marker (a test greps for `DROP ` and requires the marker).
- [ ] Constants: `defaultBusyTimeout`, `pruneChunk`, `keepBackups`.

## Step 4: AUDIT
- `scripts/check.sh ./database/...` green (coverage >= 80%).
- `CGO_ENABLED=0 go build ./database/cmd/crimson-db` for linux, windows, darwin (cross-compile three times, log it).
- Security: data directory 0700, DB 0600, backups 0600; `crimson-db` refuses to run when the path is a symlink; WAL/SHM files get the same mode (test: check `-wal` and `-shm` after a write); no query uses string concatenation (`grep -n 'Sprintf.*SELECT\|+ *"SELECT'` returns nothing).

## Done when
- [ ] `crimson-db migrate && crimson-db status` works on a clean `CRIMSON_LEDGER_HOME`.
- [ ] Schema matches this file; adding `0002` in a test proves forward migration.
- [ ] check.sh green.

## PoC log
Command: `CGO_ENABLED=0 go run ./scripts/poc/t03` (PoC removed once the real tests covered it)
```
PASS open wal 1 5000
PASS prefix SEARCH contact_words USING PRIMARY KEY (ruleset=? AND word>? AND word<?) 100 70.78µs
PASS backup ok
mode  -rw-------   mode -wal -rw-------   mode -shm -rw-------
PASS downgrade-guard
```
RED: `go vet ./database/` failed with `undefined: Open` (symbols missing).
GREEN: `go test -race ./database/...` ok; coverage 84.3% combined (gate 80%).
Cross-compile `CGO_ENABLED=0 go build ./database/cmd/crimson-db` for linux, windows, darwin: ok. `go vet` for windows and darwin: ok.
No SQL concatenation grep: clean.
AUDIT caveat: staticcheck, golangci-lint and govulncheck are not installed on this machine, so `scripts/check.sh` was NOT run. Run it before sign-off.

## Follow-ups
- Driver pinned to `modernc.org/sqlite v1.59.0`: v1.60+ requires Go 1.26, the module is on Go 1.25.
- `Prune` selects chunks by primary key tuple because `fixes` is WITHOUT ROWID.
- CLI logic lives in `cmd/crimson-db/run.go`; `Backup` takes a `keep` argument.
- Run `scripts/check.sh ./database/...` once the lint tools are installed.
