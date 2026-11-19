# T08: Local hosting: `crimson-api serve`, key CLI, TLS

**Depends on:** T05, T06, T07 · **Blocks:** T15, T18 · **PLAN.md sections:** [2 D3, D6, D7](../PLAN.md#d6-api-keys-scoped-hashed-no-accounts), [4 config](../PLAN.md#4-config-crimson-ledger), [7 security](../PLAN.md#7-security-baseline-applies-to-every-task-the-audit-phase-checks-it), [10 workflow](../PLAN.md#10-mandatory-workflow-for-every-task-tdd-with-proof-of-concept-first)

## Goal
Run the API on your own machine with one command, config in `~/.crimson-ledger/`, SQLite store, safe defaults, key
management from the terminal, optional TLS for a LAN.

## Outputs
```
api/cmd/crimson-api/main.go        run(args, stdout, stderr) int
api/cmd/crimson-api/serve.go       serve flags, wiring, graceful shutdown, prune ticker
api/cmd/crimson-api/keys.go        keys create|list|revoke|rotate
api/cmd/crimson-api/tls.go         tls-selfsign
api/cmd/crimson-api/*_test.go
internal/config/pepper.go          LoadOrCreatePepper() -> ~/.crimson-ledger/api.pepper (0600, 32 random bytes)
```

## Commands
```
crimson-api serve [--listen 127.0.0.1:8443] [--insecure-lan]
crimson-api keys create --scope ingest|view --label "my-pc" [--expires 90d]    # prints the token ONCE, then never again
crimson-api keys list                                                          # id, scope, label, created, expires, disabled, last used
crimson-api keys revoke <id>
crimson-api keys rotate <id>                                                   # create replacement (same scope/label), disable old
crimson-api tls-selfsign --host 192.168.1.20 [--host my-pc.local]              # writes ~/.crimson-ledger/tls/server.{crt,key}
crimson-api version
```
Behaviour: first `serve` creates `api.json` defaults, runs migrations (`database.Migrate`), creates the pepper file, prints the
listen address, and, if no keys exist, prints the exact command to create one (never auto-creates a key). Exit codes: 0, 1 runtime, 2 usage.
Binding rules (PLAN 7): non-loopback address without `tls_cert`/`tls_key` is refused unless `--insecure-lan`, which logs a WARN every start.
Plain `http://` on loopback is allowed. Graceful shutdown on SIGINT/SIGTERM with a 10 s drain. Background goroutine prunes
(`store.Prune(now - retention)`) hourly and on start.

## Step 0: PoC (`scripts/poc/t08/`)
1. **Graceful shutdown**: start `http.Server`, send SIGTERM to self, in-flight 2 s request completes, `Shutdown` returns nil < 3 s. PASS prints the timing.
2. **Self-signed cert accepted via `RootCAs` only.** Generate ECDSA P-256 cert with SANs (IP + DNS), serve with `ListenAndServeTLS`, client with `RootCAs` containing only that cert and **no** `InsecureSkipVerify` succeeds; same client without the CA fails with `x509: certificate signed by unknown authority`. PASS prints both. Also assert hostname mismatch fails.
3. **Key printed once**: `keys create` stores only the HMAC; running `keys list` never reveals the token; DB dump (`sqlite3 .dump`) contains no substring of the secret. PASS prints `ok`.
4. **Pepper file**: created 0600 with `crypto/rand`; second run reads the same bytes; wrong-size file is refused.
5. **Loopback detection** with `netip.ParseAddrPort` for `127.0.0.1:1`, `[::1]:1`, `localhost:1`, `0.0.0.0:1`, `:8443` (empty host = all interfaces = **not** loopback). PASS = table.
6. **Port in use** yields a readable error, not a stack trace.

## Step 1: RED
- `TestRun_Usage` (exit 2 + help text), `TestServe_RefusesPublicBindWithoutTLS`, `TestServe_AllowsInsecureLANWithWarning` (log captured), `TestServe_CreatesDefaultsAndMigrates`.
- `TestKeys_CreatePrintsOnce`, `_ListHidesSecrets`, `_RevokeBlocksAuth` (use a live `httptest` server + `http.Client`: create key, call, revoke, call -> 403 within the cache TTL; with `--no-cache` test option -> immediate), `_RotateKeepsScope`, `_ExpiryParse` (`90d`, `12h`, bad values).
- `TestTLS_SelfsignWritesFiles0600`, `TestTLS_ClientTrustsOnlyWithCA`.
- `TestServer_Timeouts` (slowloris header trickle is cut at `ReadHeaderTimeout`), `TestServer_MaxHeaderBytes`.
- `TestGracefulShutdown` (in-flight request completes).
- Smoke script `scripts/smoke-local.sh`: build, `serve` in background on a random port, create keys, POST a sample ping, GET sweep, assert JSON; exits non-zero on failure.

## Step 2: GREEN (key wiring)
```go
func serve(ctx context.Context, cfg config.APIConfig, log *slog.Logger) error {
	db, err := database.Open(cfg.DBPathExpanded())
	if err != nil { return fmt.Errorf("open database: %w", err) }
	defer db.Close()
	if err := database.Migrate(ctx, db); err != nil { return fmt.Errorf("migrate: %w", err) }
	pepper, err := config.LoadOrCreatePepper()
	if err != nil { return err }
	h := apiserver.New(apiserver.Deps{Store: sqlitestore.New(db), Pepper: pepper,
		Limiter: apiserver.NewMemoryLimiter(), Now: time.Now, Log: log, Opts: cfg.ServerOptions()})
	srv := &http.Server{Addr: cfg.Listen, Handler: h, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	return runUntilDone(ctx, srv, cfg, log)       // ListenAndServe[TLS] + Shutdown on ctx.Done()
}
```
`keys create` uses `apiserver.GenerateKey` + `store.Keys().Put`; prints `token` to stdout only (nothing else on stdout so it can be piped),
guidance goes to stderr. This file is the **only** place in the repo allowed to call `ListenAndServe` (check.sh enforces).

## Step 3: REFACTOR checklist
- [x] `main.go` only dispatches; each subcommand is its own file with `func runX(args []string, io IO) int`.
- [x] `IO{Stdout, Stderr io.Writer; Env func(string) string; Now func() time.Time}` injected so tests need no globals.
- [x] Flag definitions via `flag.NewFlagSet` per command (no global `flag`), `-h` text reviewed for clarity.
- [ ] Log fields: `time, level, msg, request_id, route, status, dur_ms`; no query strings (can contain `q`), no auth header.
- [x] Startup banner states mode (loopback / TLS / insecure-lan) in one plain line.

## Step 4: AUDIT
- check.sh green; `CGO_ENABLED=0 go build` for linux/windows/darwin (amd64+arm64) logged.
- `scripts/smoke-local.sh` passes; run `nmap`-style check: `ss -ltnp | grep crimson-api` shows only the intended address.
- Security: pepper and DB files 0600 in a 0700 dir; `crimson-api` refuses to start if `api.json` is group-readable; token never appears in shell history instructions (docs tell users to copy it directly into the app UI); no secret in `ps` output (nothing secret is a flag).

## Done when
- [x] From a clean machine: `crimson-api serve`, `keys create`, `curl` with the token works exactly as `smoke-local.sh` expects.
- [x] check.sh green for T08 packages (repo-wide run blocked by F08-1).

## PoC log
The throwaway PoC programs were folded straight into the real tests (each PoC assertion has a test of the same name):
shutdown (`TestGracefulShutdown_LetsInFlightRequestFinish`), self-signed cert via `RootCAs` only plus hostname mismatch
(`TestTLS_ClientTrustsOnlyWithCA`), key printed once and no secret in the database (`TestKeys_CreatePrintsOnce`,
`TestKeys_DatabaseHoldsNoSecret`), pepper (`internal/config/pepper_test.go`), loopback table (`TestIsLoopbackAddr`),
port in use (`TestServe_WhenPortInUse_SaysSo`).

## Result (2026-11-17)
- `scripts/smoke-local.sh` prints `SMOKE OK`; `go test -race ./...` green; coverage of `api/cmd/crimson-api` 85.5 %.
- `golangci-lint run ./api/... ./internal/config/...` 0 issues; `CGO_ENABLED=0 go build` ok for linux/windows/darwin x amd64/arm64.
- Deviations: `serve` uses `net.Listen` + `Serve` (not `ListenAndServe`) so the port-in-use error is ours; `tls-selfsign`
  refuses to overwrite without `--force`; `keys revoke` takes up to the 30 s key cache TTL on a running server.

## Follow-ups
- F08-1: `scripts/check.sh ./...` still fails on lint in code from T03/T04 (not T08): `database/cmd/crimson-db/run.go` and
  `database/maintenance.go` (errcheck, errorlint, gosec G202), `internal/store/storetest` (gocyclo x3, funlen, unparam).
  Fix before T09 reuses the contract suite. govulncheck is still blocked on a newer Go 1.25.x patch.
- F08-2: default `db_path` is `~/.crimson-ledger/data/ledger.db`, which ignores `CRIMSON_LEDGER_HOME`; T15 (Docker) must write an `api.json` with an explicit path.
