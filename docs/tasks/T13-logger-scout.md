# T13: Logger ("scout"): tail, parse, upload

**Depends on:** T01, T02, T11, T12 · **Blocks:** T15, T18 · **PLAN.md sections:** [2 D2, D5](../PLAN.md#d2-parsing-happens-in-the-logger-not-the-api), [4 config](../PLAN.md#4-config-crimson-ledger), [5.2 ping](../PLAN.md#52-ping-body), [7 security (logger)](../PLAN.md#7-security-baseline-applies-to-every-task-the-audit-phase-checks-it), [13 perf](../PLAN.md#13-performance-budgets-measured-in-t21-every-task-logs-its-benchmarks-against-these), [10 workflow](../PLAN.md#10-mandatory-workflow-for-every-task-tdd-with-proof-of-concept-first)

## Goal
`crimson-scout`: a native window (Linux/Windows/macOS) that tails the newest `WoWCombatLog*.txt`, turns lines into contacts/fixes/kills,
and uploads batched pings to the API. Settings (server URL, API key, source name, logs folder, ruleset, interval, CA file) are saved in
`~/.crimson-ledger/logger.json` and always shown; the key is masked with an eye button (T12 widget).

## Inputs (port from, with tests)
`go-radar/internal/{parser,tracker,tailer,clock,ruleset,bounty}`. Copy into `internal/logparse/` **with their tests first** (tests are the spec), then
adapt. Keep the parsing semantics (names `Mc-Cheeki-PvP-US` -> `Mc Cheeki`, ruleset auto-detect, exact vs approx fixes, orientation not needed here).
Remove everything that was UI/HTTP/history/JSON-file persistence.

## Outputs
```
internal/logparse/{parser,tracker,tailer,ruleset,kills}/       ported + tests
logger/internal/pipeline/pipeline.go      tailer -> parser -> tracker -> batcher -> uploader (goroutines + channels, context-cancelled)
logger/internal/pipeline/batcher.go       coalesce + build domain.Ping (<= limits)
logger/internal/pipeline/queue.go         bounded retry queue (memory + <= 5 MB on disk in ~/.crimson-ledger/data/outbox/)
logger/internal/app/app.go                state machine + bindings for the UI
logger/cmd/crimson-scout/main.go          launcher (T12)
logger/cmd/crimson-scout-ui/main.go       UI process: loads config, builds page, binds app, Run
logger/web/{index.html,app.css,app.js}    settings form + live status (embedded)
logger/internal/*_test.go
```

## Behaviour
* States: `Idle` (settings incomplete) -> `Connecting` (Test connection) -> `Watching` (no log yet) -> `Uploading` -> `Paused` (user) / `Error` (auth failed, network down: shows reason, keeps retrying with backoff; auth errors stop retrying and say "API key rejected").
* UI shows: settings form (always visible), Start/Stop button, status line, counters (lines read, contacts, pings sent, queued, last success time), last 50 activity lines (never contains key or raw log lines).
* Log discovery: newest `WoWCombatLog*.txt` in `logs_dir`; default path tries known locations per OS (Windows `C:\Program Files (x86)\World of Warcraft\_forever_\Logs`, plus a folder picker via native dialog; macOS `/Applications/World of Warcraft/_forever_/Logs`; Linux `~/Games/...` + folder picker). Picker is a bound Go function using the OS dialog library already allowed by T12 (`zenity`/`osascript`/`GetOpenFileName`); if unavailable the user types the path.
* Tail: starts at end of file unless `tail_from_start`; handles rotation (new file appears), truncation, partial lines (buffer until `\n`), CRLF, very long lines (cap 64 KB, skip).
* **Coalescing** (cost control, T09 capacity): per flush interval (`upload_interval_ms`, default 2 s) send each contact at most once with at most one new fix unless it moved > 0.2 % of the map or changed reaction; at most `batch_max_contacts` per ping (split into several pings); empty flushes send nothing.
* Upload: `apiclient.PostPing`; on retryable failure the ping goes to the outbox (bounded) and is retried oldest-first with the same `batchId`; on overflow the **oldest** are dropped and the UI says "N pings dropped (offline too long)". 401/403 -> stop, surface error. 413/400 -> drop that ping, log a code-only message (bug indicator).
* Privacy: raw log lines are never uploaded or written to disk by the logger; the outbox holds only pings (already validated by `domain.ValidatePing` before enqueue).
* Clean exit: Stop flushes pending contacts once (3 s deadline) before quitting.

## Step 0: PoC (`scripts/poc/t13/`)
1. **Parse throughput**: run the ported parser over `go-radar` test fixtures concatenated to 1 M lines; PASS = < 3 s and < 100 MB RSS (budget PLAN 13). Record lines/s. If over, profile (`pprof`) and note top costs *before* optimising.
2. **Tailer correctness** with a fake growing file: partial line completion, rotation to a newer file, truncation, CRLF, 70 KB line. PASS prints each case.
3. **Coalescer**: feed 10 minutes of simulated movement (60 contacts, 5 events/s each) and prove the output stays <= 100 contacts per flush and total writes/minute is within the T09 capacity assumption; print WCU estimate (contacts x ~1 + fixes x 0.2).
4. **Outbox bound**: enqueue 10 MB of pings with a dead server; disk use <= 5 MB, oldest dropped, count reported. PASS prints sizes.
5. **Round trip to a stub API**: pings pass `domain.ValidatePing` and the T06 decode path for 1 000 generated sessions (property test). PASS = 0 rejections.
6. **Folder default discovery** per OS from a table of fixture dirs. PASS = expected path chosen.
7. **Idle CPU**: pipeline running on an idle file for 60 s uses < 1 % of one core (`/proc` or `getrusage`). PASS prints value.

## Step 1: RED
- Ported tests first (`go test ./internal/logparse/...` must pass after port, they are the behaviour spec).
- `TestBatcher_*`: limits enforced (split at 200 contacts, 50 fixes), duplicate GUID merge, coalescing thresholds, empty flush, deterministic ordering, `batchId` UUIDv4 and unique.
- `TestQueue_*`: bounded memory/disk, FIFO, persistence across restart, corrupt file skipped (not fatal), file perms 0600, oldest-dropped counter.
- `TestPipeline_EndToEnd` with a fake clock, fake file source, `httptest` API: lines in -> expected pings out; server 503 x3 then 200 -> same `batchId` retried; 401 -> state `Error` and no more sends; Stop flushes.
- `TestApp_Bindings`: `getSettings` (no key, has `keyMask`), `saveSettings` validation, `revealKey`, `testConnection`, `start`, `stop`, state events; settings persisted to `logger.json` with 0600 (reuse T12 settings tests via the shared package).
- `TestNoLocalWebsite`: `go list -deps ./logger/...` contains no `net/http` **server** use: assert no `net.Listen`/`ListenAndServe` via the grep in check.sh and no `httptest` in non-test files.
- `FuzzParseLine` (random bytes; never panics, never allocates unbounded).

## Step 2: GREEN (key code)
```go
// pipeline.Run owns goroutine lifetimes; every stage stops when ctx is cancelled.
func (p *Pipeline) Run(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)              // golang.org/x/sync: allowed
	lines := make(chan string, 1024)                 // bounded: backpressure slows the tailer, never grows memory
	events := make(chan logparse.Event, 1024)
	g.Go(func() error { return p.tail.Run(ctx, lines) })
	g.Go(func() error { return p.parse.Run(ctx, lines, events) })
	g.Go(func() error { return p.batch.Run(ctx, events, p.flushTick(), p.out) })
	g.Go(func() error { return p.upload.Run(ctx, p.out) })
	return g.Wait()
}
```
Settings save path: `app.saveSettings(in SettingsInput)` -> `config.LoggerConfig` -> `Validate` -> `config.Save`. Changing server/key while running restarts the uploader only.

## Step 3: REFACTOR checklist
- [ ] Each pipeline stage is one type with `Run(ctx, in, out) error`; no shared mutable state between stages except via channels.
- [ ] Ported code has doc comments and no leftovers from the old app (grep for `history`, `http`, `webview2`, `json` persistence).
- [ ] Time via `clock.Clock`; randomness via injected `io.Reader` (UUIDs).
- [ ] Magic numbers (0.2 %, 64 KB, 5 MB, 50 activity lines) are named constants with a why-comment.
- [ ] UI text strings in one `strings.go` (future translation) and error messages readable by non-programmers.

## Step 4: AUDIT
- check.sh green for `./internal/logparse/... ./logger/...`, coverage >= 85%.
- `go test -race ./logger/... -count=10`; `FuzzParseLine` 90 s.
- Performance (record against PLAN 13): parse 1 M lines, steady-state alloc/op of the parse+track loop (`-benchmem`), RSS after 1 h simulated session (< 100 MB, no growth: sample every 5 min and compare), CPU idle/active. A `pprof` heap snapshot at start and end of the soak is attached to the log.
- Security: read-only file open flags (`os.O_RDONLY`); path from config must be a directory and log files must be regular files inside it (reject symlinks escaping the folder); outbox dir 0700, files 0600; no key in outbox or activity lines (test with canary key); HTTPS enforced by the T11 client; the confirmation text when the user switches to an `http://` loopback URL is shown in the UI.

## Done when
- [ ] Running the app against T08 locally uploads a recorded session and the viewer (T14) shows it (manual + T18 automation).
- [ ] Missing-library case verified (T12 PoC 8) for `crimson-scout`.
- [ ] check.sh green; performance numbers logged.

## PoC log
Date: 2026-11-17. **Step 0 PoCs were NOT run before coding** (deviation from PLAN 10; the real tests below cover items 2, 4, 5 and 6 in part). Still open: 1 (1 M line throughput), 3 (10 min coalescer WCU estimate), 7 (idle CPU), and the real-window items.

Done (all `go test -race` green; `go vet` host/windows/darwin clean):
- `internal/logparse/{parser,tailer,ruleset,clock}` ported from go-radar with their tests (tests pass unchanged apart from import paths).
- `internal/logparse/tracker` is a **new, slimmer** tracker (not a port: the old one was tied to history/screenshot/radar UI). Converts world coords to percent (`x% = (y1-y)/(y1-y0)`, `y% = (x1-x)/(x1-x0)`), exact + approx fixes, coalescing in `Flush`.
- `internal/logparse/kills`: new PvP kill detector (PARTY_KILL / SPELL_INSTAKILL / UNIT_DIED + last player hit within 5 s, pet owners, feign ignored, dedupe 3 s). Replaces the old bounty analyzer, which was store-coupled.
- `logger/internal/pipeline`: batcher (split at 200 contacts, UUIDv4 ids from an injected reader, sorted output, ruleset auto-detect), disk-backed bounded outbox (5 MB, 0700/0600, oldest dropped, corrupt files skipped), tail -> parse -> batch -> upload stages with bounded channels, retry with the same batchId, 401/403 stops with `ErrKeyRejected`, 4xx drops with a code-only message, final flush on Stop (3 s).
- `logger/internal/app`: state machine, bindings (`start`, `stop`, `getStatus`, `pickFolder`, `defaultLogsDir` plus the T12 settings functions), per-OS log folder discovery, OS folder dialog (zenity / osascript / PowerShell), activity ring (50, redacted).
- `logger/web` + `cmd/crimson-scout{,-ui}`: settings form with masked key, status, counters, activity. The page **polls** `getStatus` every second instead of Go calling `Eval` (Eval from a non-UI thread is not safe in webview_go).

Coverage: pipeline 90 %, tailer 90 %, parser 93 %, kills 89 %, tracker 79 %, **app 63 %** (below the 85 % target: picker, RealDeps and launch error paths untested).

Deviations / decisions:
- Chart rectangles are sent as the 0..100 square: the server (`validateChart`) rejects world coordinates outside 0..100, and fixes are already percent. Real world bounds are not uploaded. Needs a PLAN 5 decision if the viewer must draw them.
- Tests use a fake `Sender` instead of `httptest`; the retry/auth behaviour is covered, the real HTTP path is T18.
- `Namer` strips the realm suffix only after 6 distinct names, so the first contacts of a session may carry e.g. "Alpha Realm PvP US" until the suffix is learned.
- Coalescing: one fix per contact per flush (the latest); skipped when moved < 0.2 % and unchanged within 30 s (heartbeat keeps `lastSeen` fresh). The task text's "at most one new fix unless it moved > 0.2 %" was read this way.

AUDIT: gofmt, vet, staticcheck clean; golangci-lint clean for all new code; `govulncheck` fails only on Go 1.25.0 stdlib advisories (known, PLAN status).

## Follow-ups
- F13-1: run Step 0 PoCs 1, 3, 7 and record them; add `FuzzParseLine` and the 1 h soak / pprof numbers (PLAN 13).
- F13-2: lint debt in the ported `parser`/`tailer` (gocognit/gocyclo/nestif/errorlint/errcheck/revive, 15 findings): refactor with the ported tests as the safety net.
- F13-3: raise `logger/internal/app` coverage to >= 85 % and `tracker` to >= 85 %.
- F13-4: real-window verification of `crimson-scout-ui` in the T15 build image (needs WebKitGTK; see T12 PoC 1) and the "Done when" run against T08 + T14.
- F13-5: `TestNoLocalWebsite` is only the `check.sh` grep so far; add the `go list -deps` assertion.
- F13-6: decide whether charts should carry real world bounds (see deviation above).
