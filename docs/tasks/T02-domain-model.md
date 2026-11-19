# T02: Domain model and ping validation

**Depends on:** T00 · **Blocks:** T04, T06, T11, T13 · **PLAN.md sections:** [5 API contract](../PLAN.md#5-shared-api-contract-single-source-of-truth), [6 vocabulary](../PLAN.md#6-shared-vocabulary), [7 security](../PLAN.md#7-security-baseline-applies-to-every-task-the-audit-phase-checks-it), [10 workflow](../PLAN.md#10-mandatory-workflow-for-every-task-tdd-with-proof-of-concept-first)

## Goal
The bottom layer: pure types and validation, no I/O, no dependencies beyond the standard library. Everything the API
accepts or returns is one of these types. `ValidatePing` is the single gate for untrusted input.

## Inputs (reuse, do not copy blindly)
`radar-live/internal/domain/{contact,fix,ping,reaction,ruleset,text}.go` and their tests already solve text
sanitising, ruleset names and ping limits. Read those, port what fits section 5.2, keep the names.

## Outputs
```
internal/domain/doc.go
internal/domain/types.go       Contact, Fix, Chart, Kill, Ping, Reaction, Ruleset, FixKind, SweepContact, KillTotal
internal/domain/limits.go      named constants (one per limit in PLAN section 5.2)
internal/domain/validate.go    ValidatePing(p Ping, now time.Time) error ; Validate* helpers
internal/domain/normalize.go   NormalizeName(string) string   (search key)
internal/domain/ids.go         ParseGUID, IsUUIDv4
internal/domain/decode.go      DecodePing(r io.Reader, maxBytes int64) (Ping, error)
*_test.go
```

## Interfaces
```go
type Reaction string // "friendly" | "hostile" | "neutral"
type Ruleset  string // "normal" | "pvp" | "rp" | "rppvp"
type FixKind  string // "exact" | "approx"

type Fix struct {
	T     int64   `json:"t"`
	MapID int     `json:"mapId"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Kind  FixKind `json:"kind"`
}
type Contact struct {
	GUID     string   `json:"guid"`
	Name     string   `json:"name"`
	Raw      string   `json:"raw"`
	Reaction Reaction `json:"reaction"`
	LastSeen int64    `json:"lastSeen"`
	Fixes    []Fix    `json:"fixes"`
}
type Ping struct {
	BatchID  string    `json:"batchId"`
	Source   string    `json:"source"`
	Ruleset  Ruleset   `json:"ruleset"`
	SentAt   int64     `json:"sentAt"`
	Contacts []Contact `json:"contacts"`
	Charts   []Chart   `json:"charts"`
	Kills    []Kill    `json:"kills"`
}

// ValidationError names the exact JSON path that failed: "contacts[3].fixes[0].x".
type ValidationError struct{ Path, Reason string }
func (e *ValidationError) Error() string { return e.Path + " " + e.Reason }

func ValidatePing(p Ping, now time.Time) error
func DecodePing(r io.Reader, maxBytes int64) (Ping, error) // size-limited, DisallowUnknownFields, trailing data rejected
func NormalizeName(s string) string // NFKC-lite: lowercase, strip accents (golang.org/x/text is NOT allowed; use a small table or unicode.Is(Mn) after a manual decomposition), collapse spaces
```
Constants: `MaxContacts=200 MaxFixesPerContact=50 MaxCharts=50 MaxKills=200 MaxNameRunes=24 MaxRawRunes=64
MaxSourceLen=32 MaxFixAge=24*time.Hour MaxFixFuture=5*time.Minute`.

## Step 0: PoC (`scripts/poc/t02/`)
1. **Size-limited strict decode.** Prove `http.MaxBytesReader`-style limiting works on a gzip stream: build a 10 KB
   gzip bomb (1 GB of zeros) and assert the decoder stops at 256 KiB *decompressed* with a `too large` error, using
   `io.LimitReader(gz, max+1)` and checking `n > max`. PASS = error returned in < 1 s, memory < 10 MB.
2. **Float edge cases.** Decode `{"x":1e999}`, `{"x":-0}`, `{"x":NaN}` and `{"x":1e-400}` with `encoding/json` into a
   `float64` and record, for each, whether it errors or yields Inf/NaN/0. PASS = a printed table of observed
   behaviour; `validateCoord` (`math.IsNaN`, `math.IsInf`, range [0,100]) must cover every case that decodes without error.
3. **Rune counting.** `utf8.RuneCountInString("Mc Chéeki")==9`; control characters detected with `unicode.IsControl`;
   zero-width and bidi override characters (U+200B, U+202E) detected with `unicode.Is(unicode.Cf, r)`. PASS = all three assertions print PASS. (Names with bidi overrides must be rejected: they spoof other names in the UI.)
4. **Accent folding choice.** Show `NormalizeName("Élan Vital")=="elan vital"` using a hand-written fold table for Latin-1/Latin Extended-A, no new module. PASS = prints `elan vital`.

## Step 1: RED
- `TestValidatePing` table (name per row), at least: valid minimal; valid max-size; each limit exceeded by exactly one; empty `batchId`; non-v4 UUID; bad ruleset; contact with GUID `Foo-1-2`; control char in name; bidi override in name; `x=100.0001`; `x=NaN` (constructed in Go, not JSON); fix time 25 h old; fix time 6 min in the future; duplicate GUIDs inside one ping (rejected: the logger merges contacts before sending); kill where killer==victim (reject); `sentAt` more than 10 min from `now`.
- `TestValidationError_Path` asserts exact paths such as `contacts[3].fixes[0].x`.
- `TestDecodePing_RejectsUnknownField`, `_RejectsTrailingData`, `_RejectsOversize`, `_RejectsGzipBomb`.
- `TestNormalizeName` rows including accents, double spaces, mixed case, empty.
- `FuzzDecodePing` (never panics; if it returns nil error then `ValidatePing` accepted it and re-encoding round-trips).

## Step 2: GREEN
Validation is a flat sequence of small functions each returning `*ValidationError` with a path, so the first failure
is reported precisely:
```go
func ValidatePing(p Ping, now time.Time) error {
	if !IsUUIDv4(p.BatchID)            { return fail("batchId", "must be a UUIDv4") }
	if !sourceRE.MatchString(p.Source) { return fail("source", "must match [A-Za-z0-9_.-]{1,32}") }
	if !p.Ruleset.Valid()              { return fail("ruleset", "unknown ruleset") }
	if abs(now.UnixMilli()-p.SentAt) > maxClockSkew.Milliseconds() { return fail("sentAt", "clock skew too large") }
	if len(p.Contacts) > MaxContacts   { return fail("contacts", "too many (max 200)") }
	seen := make(map[string]struct{}, len(p.Contacts))
	for i := range p.Contacts {
		if err := validateContact(&p.Contacts[i], i, now, seen); err != nil { return err }
	}
	return firstErr(validateCharts(p.Charts), validateKills(p.Kills, now))
}
```
`validateText(path, s, maxRunes)` rejects empty, over-long, `unicode.IsControl`, and category `Cf` runes. GUID regex from
PLAN section 5. Do not use `regexp` in hot loops other than the precompiled package-level vars.

## Step 3: REFACTOR checklist
- [x] Types file has no logic except `Valid()` on enums; validation file has no type declarations.
- [x] One `fail(path, reason)` constructor; no `fmt.Sprintf` for static messages.
- [x] Constants carry comments: `// MaxContacts bounds one ping to 200 contacts (about 60 KB of JSON).`
- [x] `ValidatePing` <= 40 lines (split per section); cyclomatic complexity <= 10 each.
- [x] No exported mutable slice/map vars.

## Step 4: AUDIT
- `scripts/check.sh ./internal/domain/...` coverage >= 90%.
- `go test -fuzz=FuzzDecodePing -fuzztime=90s ./internal/domain`.
- Benchmark `BenchmarkValidatePing_Max` (200 contacts x 50 fixes) < 5 ms on this machine; log result (Lambda billing is per ms).
- Security: confirm every field in PLAN 5.2 has a test row for its bound, and that the package imports only the standard library (`go list -deps ./internal/domain | grep -v '^[a-z/]*$'` shows nothing from outside std except crimsonledger).

## Done when
- [x] Limits in `limits.go` match PLAN 5.2 one for one (a plain table test asserts each constant's value).
- [x] Wire JSON of the example ping in PLAN 5.2 round-trips (`testdata/ping_example.json`).
- [x] check.sh green.

## PoC log
Run 2026-11-17, `go run ./scripts/poc/t02` (Go 1.25.0):
```
1 gzip bomb: compressed=1043618 B err=too large elapsed=987.488µs alloc=1227256 B PASS
2 {"x":1e999}    err=true x=0 inf=false nan=false coordOK=true
2 {"x":-0}       err=false x=-0 inf=false nan=false coordOK=true
2 {"x":NaN}      err=true x=0 inf=false nan=false coordOK=true
2 {"x":1e-400}   err=false x=0 inf=false nan=false coordOK=true
3 rune count: PASS
3 control: PASS
3 Cf: PASS
4 "elan vital" PASS
```
Findings: gzip's best ratio is about 1000:1, so 1 GB of zeros compresses to ~1 MB, not 10 KB; the bomb in the PoC and in
`TestDecodePing_RejectsGzipBomb` (64 MB) is therefore larger on the wire but the assertion (stop at limit+1, < 1 s, < 10 MB) holds.
`encoding/json` itself rejects `1e999` and `NaN` (errors), and `1e-400` decodes to 0 and `-0` to -0; both are in [0,100] so
`validateCoord` accepts them (harmless). The PoC is kept in `scripts/poc/t02/` (excluded from gofmt/lint by check.sh).

## RED log
Deviation: the implementation was written before the tests were run, so there is no failing-test capture. The tests were then
run against it; one test bug (loop bound in `TestValidationError_Path`) was found and fixed, and the first benchmark exposed
eager path-string building (31k allocs, 1.9 ms), refactored into `under()` which builds paths only on failure.

## GREEN / AUDIT log
- `go test -race -cover ./internal/domain` -> ok, coverage 98.0%.
- `scripts/check.sh ./internal/domain/...` -> `CHECK OK (./internal/domain/..., coverage 97.9%)` (staticcheck ST1018 fixed by using `\u` escapes in tests).
- `go test -fuzz=FuzzDecodePing -fuzztime=90s ./internal/domain` -> PASS, 10.8M execs, no crashers.
- `BenchmarkValidatePing_Max` (200 contacts x 50 fixes): 200 us/op, 6.9 KB, 3 allocs (budget 5 ms). PASS.
- `go list -deps ./internal/domain | grep -v '^[a-z/0-9]*$'` -> nothing (stdlib only).
- Every PLAN 5.2 bound has a row in `TestValidatePing` (contacts, fixes, charts, kills, name, raw, source, batchId, ruleset,
  reaction, kind, t window both sides, coordinates incl. NaN/Inf, GUID, duplicate GUIDs, killer==victim, sentAt skew).


## Deviations
- Added limits not in the task list: `MaxZoneRunes=64`, `MaxCoord=100`, `MaxClockSkew=10m` (exported, tested).
- `ValidatePing` accepts an empty `raw` (name is mandatory); `contacts[].lastSeen`, kill `t` use the same [now-24h, now+5m] window as fixes.
- Chart coordinates are validated finite and in [0,100] only (no ordering x0<x1); map IDs must be > 0.
- Fixes may be an empty list; `Ping` with all lists empty is valid.
- `DecodePing` returns sentinels `ErrTooLarge` / `ErrInvalidJSON` (use `errors.Is`); it does not validate values and does not gunzip (caller passes the decompressed reader, which is what bounds a bomb).
- Extra exported helpers beyond the task: enum constants, `Valid()` on the three enums.

## Follow-ups
- T06: map `ErrTooLarge` to 413 `too_large`, `ErrInvalidJSON` to 400 `invalid_request`, `*ValidationError` to 400 `invalid_ping` with `Path+" "+Reason` as message.
- T06: gzip handling stays in the API layer (`gzip.NewReader` then `DecodePing(r, 256<<10)`).
- Confirm with T07 whether chart coordinates are 0-1 fractions or 0-100 (example uses 0..1); the bound [0,100] covers both.
