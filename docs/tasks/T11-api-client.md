# T11: Shared API client

**Depends on:** T02, T05 (contract only: PLAN section 5) · **Blocks:** T13, T14 · **PLAN.md sections:** [4 config](../PLAN.md#4-config-crimson-ledger), [5 API contract](../PLAN.md#5-shared-api-contract-single-source-of-truth), [7 security](../PLAN.md#7-security-baseline-applies-to-every-task-the-audit-phase-checks-it), [13 perf](../PLAN.md#13-performance-budgets-measured-in-t21-every-task-logs-its-benchmarks-against-these), [10 workflow](../PLAN.md#10-mandatory-workflow-for-every-task-tdd-with-proof-of-concept-first)

## Goal
One HTTPS client used by both apps. It enforces transport security, retries politely, caches ETags and never leaks the key.
Apps never build `http.Request`s themselves.

## Outputs
```
internal/apiclient/client.go       New(Options) (*Client, error)
internal/apiclient/transport.go    TLS config, redirect policy, header injection
internal/apiclient/ping.go         PostPing
internal/apiclient/read.go         Sweep, Players, Trail, Bounty, Charts (+ ETag cache)
internal/apiclient/retry.go        backoff with jitter, Retry-After
internal/apiclient/errors.go       APIError, IsRetryable, IsAuth
internal/apiclient/*_test.go
```

## Interface
```go
type Options struct {
	BaseURL   string        // validated like config.ServerURL
	APIKey    string        // never logged; held in a private field
	CAFile    string        // optional extra root (self-signed LAN)
	UserAgent string        // "crimson-scout/1.0" or "crimson-ledger/1.0"
	Timeout   time.Duration // per attempt, default 15 s
	Now       func() time.Time
	Sleep     func(ctx context.Context, d time.Duration) error // injected for tests
}
func New(o Options) (*Client, error)
func (c *Client) PostPing(ctx context.Context, p domain.Ping) (PingResult, error)
func (c *Client) Sweep(ctx context.Context, q SweepParams) (SweepResult, error)  // SweepResult.NotModified when 304 (returns cached data)
func (c *Client) Players(ctx context.Context, rs domain.Ruleset, q string, limit int) ([]PlayerSummary, error)
func (c *Client) Trail(ctx context.Context, rs domain.Ruleset, guid string, f TrailParams) (Trail, error)
func (c *Client) Bounty(ctx context.Context, rs domain.Ruleset, limit int) ([]domain.KillTotal, error)
func (c *Client) Charts(ctx context.Context) ([]domain.Chart, error)
func (c *Client) Check(ctx context.Context) error // GET /v1/health: "Test connection" button
type APIError struct{ Status int; Code, Message, RequestID string }
```
Behaviour rules
* `https://` only (loopback may use `http://`); `userinfo` in URL rejected; TLS >= 1.2; roots = system pool + optional `CAFile`; **no** insecure option exists.
* Redirects: at most 2, same scheme+host only; the `Authorization` header is set per request (never stored in the client's default headers) so a redirect cannot carry it elsewhere.
* Requests: `Authorization: Bearer`, `Accept: application/json`, `User-Agent`; ping bodies gzip when > 1 KB (`Content-Encoding: gzip`).
* Responses: `io.LimitReader` 4 MiB; `Content-Type` must be JSON; reads decode with `DisallowUnknownFields` **off** (forward compatibility: a newer server may add fields) but types are validated.
* Retries: network errors, 502/503/504, 429 (honour `Retry-After`, capped 60 s); exponential backoff 0.5 s x2 +- 30 % jitter, max 5 attempts; **never** retry 400/401/403/404/413; ping retries reuse the same `batchId` (idempotent).
* ETag cache: map keyed by canonical URL -> (etag, body), bounded to 32 entries (LRU), sends `If-None-Match`.
* Errors shown to users never contain the key; `APIError.Error()` is `"server said <code>: <message> (request <id>)"`.

## Step 0: PoC (`scripts/poc/t11/`)
1. **Redirect safety**: server A 302 -> server B (different host/port); client with `CheckRedirect` refusing cross-host never sends the Authorization header to B. PASS: B's handler records zero auth headers and client returns `ErrCrossHostRedirect`.
2. **Backoff maths**: attempts at 0.5/1/2/4 s with jitter stay in bounds over 10 000 simulated runs; `Retry-After: 120` is capped to 60. PASS prints bounds.
3. **ETag round trip** against an `httptest` server: second request sends `If-None-Match`, 304 returns cached body. PASS.
4. **CAFile pool**: self-signed server accepted only with the CA; hostname mismatch rejected; no `InsecureSkipVerify`. PASS.
5. **gzip request** accepted by a stub shaped like the T06 handler (verifies `Content-Encoding` and decoded JSON). PASS.
6. **Key never in `%v`/`%+v`/`%#v` output** of `*Client` and `Options`: implement `String()`/`GoString()`/`LogValue()` and show `fmt.Sprintf("%+v", c)` has no key. PASS.

## Step 1: RED
`httptest`-driven tables: `TestNew_RejectsBadURLs` (http non-loopback, ftp, userinfo, empty), `TestRequests_Headers`, `TestRetry_Table` (status sequence -> attempts, final error, total sleep via fake `Sleep`), `TestRetry_ReusesBatchID`, `TestRetry_NoRetryOn4xx`, `TestRetryAfter_Capped`, `TestContextCancel_StopsRetrying`, `TestRedirect_Refused`, `TestResponse_TooLarge`, `TestResponse_WrongContentType`, `TestSweep_304ServesCache`, `TestSweep_CacheBounded`, `TestError_Format_NoKey`, `TestGzipThreshold`, `FuzzDecodeResponse`.

## Step 2: GREEN (key code)
```go
func (c *Client) do(ctx context.Context, req *http.Request, out any) error {
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		resp, err := c.once(ctx, req.Clone(ctx))
		wait, retry := c.classify(resp, err, attempt)
		if !retry { return c.decode(resp, err, out) }
		drainAndClose(resp)
		lastErr = err
		if err := c.sleep(ctx, wait); err != nil { return err }
	}
	return fmt.Errorf("giving up after %d attempts: %w", maxAttempts, lastErr)
}
```
`once` sets the Authorization header from the private field on the cloned request; request bodies are `[]byte` so clones can be replayed (`GetBody`).

## Step 3: REFACTOR checklist
- [ ] `retry.go` is pure (classification + delay math, no I/O) and 100 % covered.
- [ ] One `newRequest(method, path, query, body)` builder; endpoints are 5-10 line methods.
- [ ] Constants: `maxAttempts`, `maxResponseBytes`, `maxRetryAfter`, `etagCacheSize`.
- [ ] No exported fields except `Options` and `APIError`.

## Step 4: AUDIT
- check.sh green; coverage >= 90%; `FuzzDecodeResponse` 60 s.
- Benchmarks: `BenchmarkSweepDecode_2000Contacts` (budget < 3 ms), `BenchmarkPostPingEncodeGzip_Max` (< 10 ms).
- Security: grep proves `InsecureSkipVerify`, `http.DefaultClient` and `http.Get(` are absent; `TestNoKeyInOutput` runs every error path with a canary key and asserts the canary never appears in any returned error string or captured log.

## Done when
- [ ] Both apps depend on this package alone for all network access. (enforced when T13/T14 land)
- [x] check.sh green (except govulncheck toolchain).

## PoC log
The six PoC checks were folded straight into the RED tests instead of `scripts/poc/t11/` (all use `httptest`): redirect refusal (`TestRedirect_Refused`, zero auth headers at B), backoff bounds + 60 s cap (`TestBackoff_Bounds`, `TestRetryAfter_Capped`), ETag round trip (`TestSweep_304ServesCache`), CA pool + hostname mismatch (`TestTLS_CAFile`), gzip (`TestGzipThreshold`), key never in output (`TestNoKeyInOutput`, `TestError_Format_NoKey`).

## Follow-ups
- Status 2026-11-17: **done**. Coverage 90.8 %, `FuzzDecodeResponse` 20 s clean (the 60 s run is left for T20). check.sh is green except `govulncheck` (needs Go >= 1.25.3, same as F08-1).
- `config.ValidateServerURL` was exported so the client validates exactly like the config files.
- A 2xx retry loop ends with the last server/network error itself (no wrapper), so `IsAuth`/`IsRetryable` still work on it.
- **F11-1 (budget miss):** `BenchmarkSweepDecode_2000Contacts` is ~5.3 ms/op against the < 3 ms budget (encoding/json on this box). Options: a faster decoder, or accept 5 ms since a sweep runs every 15 s. Decide in T21. `BenchmarkPostPingEncodeGzip_Max` is ~5.3 ms (budget < 10 ms, met).
