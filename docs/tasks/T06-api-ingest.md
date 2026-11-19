# T06: `POST /v1/pings` (ingest)

**Depends on:** T02, T04, T05 · **Blocks:** T08, T10 · **PLAN.md sections:** [5.1, 5.2, 5.4](../PLAN.md#51-endpoints), [7 security](../PLAN.md#7-security-baseline-applies-to-every-task-the-audit-phase-checks-it), [13 perf budgets](../PLAN.md#13-performance-budgets-measured-in-t21-every-task-logs-its-benchmarks-against-these), [10 workflow](../PLAN.md#10-mandatory-workflow-for-every-task-tdd-with-proof-of-concept-first)

## Goal
The only write endpoint. Accept one gzip-or-plain JSON ping from an `ingest` key, validate it completely, hand it to
`store.ApplyPing`, answer with counts. Treat the body as hostile.

## Outputs
```
internal/apiserver/ingest.go        handlePing, readBody (gzip + size limit), response type
internal/apiserver/ingest_test.go
internal/apiserver/routes.go        + one row: POST /v1/pings, ScopeIngest
```

## Handler flow (order matters; each step can only reject, never mutate)
1. Auth (T05 middleware) must already have passed; scope `ingest`.
2. `Content-Type` must be `application/json` (parameters like charset ignored) else 415.
3. `Content-Length` > `MaxBodyBytes` (compressed) -> 413 before reading. Chunked bodies are limited by `http.MaxBytesReader`.
4. `Content-Encoding`: empty or `gzip` only (anything else 415). Gzip is decoded through `io.LimitReader(gz, MaxBodyBytes+1)`; reading
   past the limit is 413 (decompression-bomb guard). Concatenated/multi-member gzip is rejected.
5. `domain.DecodePing` (strict) then `domain.ValidatePing(p, now)`; failure -> 400 `invalid_ping` with the validation path only.
6. `store.ApplyPing(ctx, p, now)`; error -> 500 (logged with request id; client sees nothing internal). `store` timeout via `context.WithTimeout(r.Context(), 10s)`.
7. 200 `{"accepted":true,"contacts":N,"fixes":N,"kills":N}` or `{"accepted":false,"duplicate":true}`.
Identity: the client-supplied `source` has no authority and is never used for any decision. The authenticated `principal.KeyID` is written to the log line
for audit (the batch marker stores only `batch_id` and `received_at`).

Policy table for fields a rogue ingest key could abuse
| Field | Policy |
|---|---|
| `charts` | first write wins per `mapId` (store contract rule 5); changes logged at WARN with key id |
| `contacts[].reaction` | accepted as sent (cannot be verified server side); the viewer labels data as reported by scouts |
| `kills` | capped per ping (200) and per key per hour (`MaxKillsPerKeyHour=2000`, 429 `rate_limited` after) |
| distinct contacts | capped per key per hour (`MaxContactsPerKeyHour=5000`, 429 after) so a leaked key cannot flood the sweep with fake contacts (T19 X404) |
| `lastSeen`/`t` | within [now-24h, now+5min] (T02) so data cannot be pushed into the far future to pin a contact |

## Step 0: PoC (`scripts/poc/t06/`)
1. **Gzip bomb guard.** 10 KB compressed -> 1 GB zeros through `LimitReader(gz, 262145)`: returns 413 path in < 100 ms, < 10 MB alloc. PASS prints both numbers.
2. **Multi-member gzip** (`gzip.Reader.Multistream(false)`) with trailing garbage after the first member is rejected. PASS prints `rejected`.
3. **Chunked body without Content-Length** over the limit: `MaxBytesReader` trips at the limit, connection stays usable. PASS prints `413`.
4. **Latency.** `DecodePing`+`ValidatePing` for the max ping (200x50) measured with `testing.Benchmark`; PASS = under budget (5 ms) and records allocs/op.
5. **`Content-Type` parsing** with `mime.ParseMediaType` for `application/json; charset=UTF-8`, `Application/JSON`, `text/json`, empty. Record which pass.

## Step 1: RED
Table `TestPing_Responses` (all via `httptest` + fake store):
valid plain; valid gzip; duplicate batch; wrong content type (415); `Content-Encoding: br` (415); body at limit (200) and limit+1 (413);
gzip bomb (413); unknown field (400, path in message); NaN-like coordinates; 201 contacts; view key (403); no key (401);
store error (500, body contains no error text); store slow (context deadline -> 503 `unavailable` + `Retry-After: 2`).
`TestPing_LogsNoBody`, `TestPing_KillRateLimitPerKey`, `TestPing_ChartPoisoningIgnored` (store fake records calls; asserts chart conflict logged).
`FuzzIngestBody` (random bytes + random gzip wrap; never panics, status always in {200,400,413,415}).

## Step 2: GREEN (key code)
```go
func (s *server) handlePing(w http.ResponseWriter, r *http.Request) {
	if !isJSON(r.Header.Get("Content-Type")) { writeError(w, r, 415, "unsupported_media_type", "send application/json"); return }
	body, err := s.readBody(w, r)
	if err != nil { s.bodyError(w, r, err); return }
	p, err := domain.DecodePing(body, s.opts.MaxBodyBytes)
	if err == nil { err = domain.ValidatePing(p, s.now()) }
	if err != nil { writeError(w, r, 400, "invalid_ping", validationMessage(err)); return }
	ctx, cancel := context.WithTimeout(r.Context(), storeTimeout)
	defer cancel()
	res, err := s.store.ApplyPing(ctx, p, s.now())
	if err != nil { s.storeError(w, r, err); return }
	writeJSON(w, http.StatusOK, newPingResponse(res))
}
```
`validationMessage` returns only `*domain.ValidationError` text; for other errors a fixed string. Responses use a pre-sized `bytes.Buffer`
(`json.NewEncoder`, no pretty print).

## Step 3: REFACTOR checklist
- [ ] `readBody` is the single place that knows about gzip and limits; handler contains no `io` plumbing.
- [ ] Status mapping lives in `bodyError`/`storeError` (two small switch statements), not scattered.
- [ ] Constants: `storeTimeout`, `MaxKillsPerKeyHour` with comments on origin.
- [ ] The doc comment of `handlePing` lists the step order above.

## Step 4: AUDIT
- check.sh green, coverage >= 90% for `ingest.go`.
- Fuzz `FuzzIngestBody` 90 s.
- Benchmarks: `BenchmarkHandlePing_Max` end-to-end through `httptest` with the SQLite store; budget total < 160 ms; record allocs; profile once with `-cpuprofile` and note the top 3 functions in the log (do not optimise below budget).
- Security: confirm nothing from the body reaches a log line, a SQL string, or a response except counts; the only client-controlled strings stored are validated names/raw/zone.

## Done when
- [ ] Every row of PLAN 5.4 that can occur on this route has a test.
- [ ] check.sh green and benchmark within budget.

## Progress log (2026-11-17)
Built test-first against the handler flow above; the PoC step was folded into the tests (gzip bomb, multi-member, trailing garbage,
truncated gzip and the content-type table are rows of `TestPing_Responses`).
- `go test -race ./internal/apiserver` green; coverage of `ingest.go`/`quota.go` >= 89%/100%.
- `FuzzIngestBody` 30 s, 346k execs, no failure (statuses only 200/400/413/415).
- `BenchmarkHandlePing_Max` (200 contacts x 15 fixes = the largest ping 256 KiB admits, real SQLite): 20 ms/op, 4.9 MB, 81k allocs. Budget 160 ms.
- `scripts/check.sh ./internal/apiserver/...`: gofmt, vet, staticcheck, golangci-lint 0 issues. govulncheck fails only on Go stdlib
  advisories fixed in later 1.25.x patches (local toolchain is go1.25.0): upgrade the toolchain, no code change.
- T05 lint debt fixed on the way: `principal` split (`lookupKey`), two revive nits.

## PoC log
_(folded into tests, see Progress log)_

## Follow-ups
- F1: the hourly kill/contact quotas are in memory; on Lambda each container counts alone. T10 may move them to the DynamoDB counter used by the rate limiter.
- F2: `warnOnChartChanges` costs one `Charts` read for pings that carry charts; T21 should confirm that is rare enough.
- F3: raise the Go toolchain to the latest 1.25.x so govulncheck is clean.
