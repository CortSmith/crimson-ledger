# T05: API core: router, auth, keys, rate limits, headers

**Depends on:** T01, T04 · **Blocks:** T06, T07, T08, T10 · **PLAN.md sections:** [2 D1 + D6](../PLAN.md#d1-endpoint-style-between-apps-and-api-rest--json-over-https-polled), [5.1 + 5.4](../PLAN.md#51-endpoints), [7 security](../PLAN.md#7-security-baseline-applies-to-every-task-the-audit-phase-checks-it), [10 workflow](../PLAN.md#10-mandatory-workflow-for-every-task-tdd-with-proof-of-concept-first)

## Goal
Everything of the API except the business handlers: an `http.Handler` with a fixed middleware chain, scoped API-key
authentication, key generation/verification, rate limiting, error shape, request IDs and security headers.
It must work unchanged under `net/http` (local) and the Lambda adapter (T10).

## Outputs
```
internal/apiserver/server.go        New(Deps) http.Handler ; Deps{Store, Keys, Limiter, Clock, Log, Config}
internal/apiserver/routes.go        route table: method+pattern -> scope -> handler
internal/apiserver/middleware.go    chain: requestID, recover, headers, clientIP, preauthLimit, auth, bodyLimit
internal/apiserver/errors.go        writeError(w, r, status, code, msg) ; apiError
internal/apiserver/auth.go          authenticate(r) (Principal, error) ; scope check
internal/apiserver/keys.go          GenerateKey, ParseKey, Verify, Pepper type
internal/apiserver/ratelimit.go     Limiter interface + MemoryLimiter (token bucket) + FailedAuthTracker
internal/apiserver/health.go        GET /v1/health
internal/apiserver/*_test.go
```

## Interfaces
```go
type Scope string // "ingest" | "view"

type Deps struct {
	Store   store.Store
	Pepper  []byte          // >= 32 random bytes; from api.json secret file locally, SSM on AWS
	Limiter Limiter
	Now     func() time.Time
	Log     *slog.Logger    // handlers log request id, route, status, duration; never keys or bodies
	Opts    Options         // MaxBodyBytes, TrustProxyHeaders, RateLimitPerMin, Version string
}

type Limiter interface {
	// Allow reports whether one more request for key is allowed now, else how long to wait.
	Allow(ctx context.Context, key string, limit int, per time.Duration) (ok bool, retryAfter time.Duration, err error)
}

type Principal struct{ KeyID string; Scope Scope; Label string }

// Key format: clk_<i|v>_<id 8 chars [a-z0-9]>_<secret 43 chars base64url (32 random bytes)>
func GenerateKey(scope Scope, pepper []byte, rnd io.Reader, now time.Time, ttl time.Duration) (token string, rec store.KeyRecord, err error)
func ParseKey(token string) (scope Scope, id, secret string, err error)
func HashSecret(pepper []byte, secret string) []byte            // HMAC-SHA256
func VerifyKey(rec store.KeyRecord, scope Scope, pepper []byte, secret string, now time.Time) error // ErrKeyDisabled|ErrKeyExpired|ErrBadSecret|ErrWrongScope
```
Middleware order (outermost first): `requestID` -> `recoverPanics` -> `securityHeaders` -> `clientIP` -> `preAuthLimit` (per-IP cheap
bucket + failed-auth lockout) -> `route match` -> `authenticate(scope)` -> `keyLimit` (per key) -> `bodyLimit` -> handler.
`/v1/health` skips auth and keyLimit but not the IP limit.

Response rules: errors exactly as PLAN 5.4 (`writeError`); `401` body is identical for "no header", "malformed", "unknown id",
"wrong secret" (avoids a key-existence oracle); `403 key_disabled` is only returned **after** the secret verified.

## Step 0: PoC (`scripts/poc/t05/`)
1. **Go 1.22+ `ServeMux` patterns** handle `"GET /v1/players/{guid}/trail"`, return 405 with `Allow` for wrong methods, 404 otherwise, and
   `r.PathValue("guid")` works. PASS prints the three outcomes.
2. **No timing oracle.** Verify two request paths (unknown id vs known id + wrong secret) take statistically the same time over 20 000 runs
   each (Mann-Whitney not needed: compare medians within 20%). The trick: on unknown id, still compute `HashSecret` and `ConstantTimeCompare`
   against a fixed dummy hash. PASS = medians within 20% (record numbers).
3. **Key entropy and format.** Generate 100 000 keys; all unique, all match the regex, `ParseKey` round-trips.
4. **Token bucket** math: limit 120/min, burst 20: 25 immediate calls -> 20 pass, 5 denied with `retryAfter` ~0.5 s; after advancing a fake clock 1 s two more pass.
5. **Panic safety.** A handler that panics yields a 500 JSON error with a request id and the server keeps serving. PASS prints both.
6. **Header order trap.** Show that `http.MaxBytesReader` returns `*http.MaxBytesError` and that the handler can map it to 413. PASS prints `413`.

## Step 1: RED
- `TestAuth` table: no header; `Basic x`; `Bearer` + garbage; unknown id; wrong secret; right key wrong scope (403 `forbidden`); disabled (403 `key_disabled`); expired; valid ingest on ingest route; valid view on view route. Assert status, `code`, and that **all 401 bodies are byte-identical except requestId**.
- `TestKey_RoundTrip`, `TestKey_ParseRejectsMalformed` (length, charset, scope letter), `FuzzParseKey`.
- `TestRateLimit_PerIP`, `_PerKey`, `TestFailedAuthLockout` (10 failures in 5 min -> 429 even for a *correct* key from that IP, with `Retry-After`; other IPs unaffected).
- `TestClientIP` rows: `RemoteAddr` only (default); with `TrustProxyHeaders` uses the **last** `X-Forwarded-For` entry; spoofed header ignored when untrusted.
- `TestHeaders_OnEveryResponse` (including errors and 404): `X-Request-Id`, `X-Content-Type-Options: nosniff`, `Cache-Control: no-store`, `Content-Type: application/json; charset=utf-8`, no `Server`/`X-Powered-By`.
- `TestPanicBecomes500`, `TestBodyTooLarge413`, `TestHealth_NoAuth_NoStoreAccess` (store is a fake that fails the test if called).
- `TestLogs_NeverContainKey`: capture slog output over a failing-auth request; assert the secret substring and the whole token are absent.

## Step 2: GREEN (key code)
```go
func (s *server) authenticate(scope Scope, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearer(r.Header.Get("Authorization"))
		p, err := s.principal(r.Context(), token, ok, scope)
		switch {
		case errors.Is(err, errUnauthorized):
			s.failedAuth(r) // counts toward lockout
			writeError(w, r, 401, "unauthorized", "missing or invalid API key")
		case errors.Is(err, ErrWrongScope):
			writeError(w, r, 403, "forbidden", "this key cannot use this endpoint")
		case errors.Is(err, ErrKeyDisabled), errors.Is(err, ErrKeyExpired):
			writeError(w, r, 403, "key_disabled", "this key is disabled or expired")
		case err != nil:
			s.internal(w, r, err)
		default:
			next.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), p)))
		}
	})
}

func (s *server) principal(ctx context.Context, token string, present bool, want Scope) (Principal, error) {
	_, id, secret, perr := ParseKey(token)
	rec, gerr := s.keys.Get(ctx, id)                 // id is "" or junk on parse error; Get returns ErrNotFound
	hash := rec.Hash
	if present && perr == nil && gerr == nil { /* real record */ } else { hash = dummyHash[:] }
	good := subtle.ConstantTimeCompare(HashSecret(s.pepper, secret), hash) == 1 // always computed
	if !present || perr != nil || gerr != nil || !good { return Principal{}, errUnauthorized }
	if err := VerifyKey(rec, want, s.pepper, secret, s.now()); err != nil { return Principal{}, err }
	s.touch(ctx, rec.ID) // best effort, throttled to once/10 min per key (in memory)
	return Principal{KeyID: rec.ID, Scope: Scope(rec.Scope), Label: rec.Label}, nil
}
```
`Get` for key lookup is cached in memory for 30 s (map + mutex, bounded 1000 entries) so polling does not hit the database
per request; revocation therefore takes up to 30 s (documented, configurable, 0 disables the cache). On Lambda the cache lives per container.
`writeError` always sets headers before the body and never writes a Go error string to the client.
`slog` handler wraps values through `secret.Redact`.

## Step 3: REFACTOR checklist
- [x] `middleware.go` has one function per middleware, each <= 25 lines, each doing one thing.
- [x] Route table is data (`[]route`), registration is a 10-line loop; adding an endpoint is one table row (+ handler).
- [x] No `interface{}`/`any` in public signatures; context keys are unexported typed values.
- [x] Error sentinel names are the vocabulary of the 4xx table in PLAN 5.4 (grep the codes: each appears once in `errors.go`).
- [x] `Options` has a `Validate()`; defaults are named constants.

## Step 4: AUDIT
- `scripts/check.sh ./internal/apiserver/...` coverage >= 85%.
- Fuzz: `FuzzParseKey` and a `FuzzAuthHeader` for 60 s each.
- Security review checklist (tick each with the test name that proves it):
  - [x] No endpoint other than `/v1/health` is reachable without a key.
  - [x] Auth runs before the body is read (test: send a 100 MB body with a bad key; assert the handler read < 1 KB).
  - [x] Secrets are absent from logs, errors, panics, `/v1/health`.
  - [x] Lockout cannot be bypassed by changing `X-Forwarded-For` when `TrustProxyHeaders=false`.
  - [x] Unknown JSON route methods return 405/404 in the standard error shape, not Go's plain text.
  - [x] `HEAD` and `OPTIONS` behave (405 with `Allow`; no CORS headers are ever sent: no browser origin is supported).

## Done when
- [x] A fake-store integration test builds `New(Deps)` and exercises `/v1/health` and a stub authenticated route through real `net/http/httptest`.
- [x] check.sh green; all PoC assertions converted into tests.

## PoC log
Done as in-package tests/benchmarks instead of a throwaway program (the PoC points are properties of our own code):
- Mux 405/404/PathValue: `TestRouting_ErrorsUseTheStandardShape`. Because `ServeMux` writes plain text for 404/405, `dispatch` probes the mux itself and answers in the JSON shape.
- Timing: `go test -bench Principal -count=3` -> WrongSecret 719-841 ns/op, UnknownID 747-797 ns/op (same within noise; auth budget is 50 us).
- Key format/entropy: `TestKey_GeneratedKeysAreUnique` (20 000 keys), `FuzzParseKey`.
- Token bucket 120/min burst 20: `TestMemoryLimiter_TokenBucket`. Panic: `TestPanicBecomes500`. 413: `TestBodyTooLarge413`.

RED: `go test ./internal/apiserver/` -> `undefined: Options`, `undefined: GenerateKey`, ... (build failed).
GREEN: `go test -race -count=1 ./internal/apiserver/` ok, first run. Coverage 90.7% (target 85).
Fuzz 30 s each (task says 60): `FuzzParseKey` 7.2 M execs, `FuzzAuthHeader` 388 k execs, no failures.
`go vet` clean for linux, windows, darwin; `archtest` ok.
AUDIT: `scripts/check.sh` could NOT run: staticcheck, golangci-lint, govulncheck not installed (same as T03/T04).

Security checklist proofs: only health open `TestAuth_NoEndpointOtherThanHealthIsOpen`; auth before body `TestAuth_RunsBeforeBodyIsRead`;
no secrets in logs `TestLogs_NeverContainKey` + `TestRedactingHandler_RedactsMessageAndAttrs` + `TestPanicBecomes500`;
lockout vs X-Forwarded-For `TestFailedAuthLockout_NotBypassedByForwardedFor`; JSON 404/405 + HEAD/OPTIONS + no CORS `TestRouting_ErrorsUseTheStandardShape`, `TestHeaders_OnEveryResponse`.

## Follow-ups
- Run `scripts/check.sh ./internal/apiserver/...` when the lint tools are installed; run the fuzzers for the full 60 s.
- Deviation: `New(Deps)` returns `(http.Handler, error)` so weak config (short pepper, bad options, no store) is refused, not panicked on.
- Routes are `route{pattern, scope, handler}` rows in `routes.go`; T06/T07 add rows. The failed-auth lockout is in-memory only; T10 needs a Dynamo-backed version next to `DynamoLimiter`.
- `Deps.Pepper` loading (secret file locally, SSM on AWS) belongs to T08/T10.
- `DynamoLimiter` implementation of `Limiter` is T10.
