# T07: Read endpoints (sweep, players, trail, bounty, charts)

**Depends on:** T04, T05 · **Blocks:** T08, T10 · **PLAN.md sections:** [5.1, 5.3, 5.4](../PLAN.md#51-endpoints), [7 security](../PLAN.md#7-security-baseline-applies-to-every-task-the-audit-phase-checks-it), [13 perf budgets](../PLAN.md#13-performance-budgets-measured-in-t21-every-task-logs-its-benchmarks-against-these), [10 workflow](../PLAN.md#10-mandatory-workflow-for-every-task-tdd-with-proof-of-concept-first)

## Goal
Five `GET` endpoints for `view` keys with strict query parsing, ETag/304 support, bounded results and no way to read more than
the contract allows (no listing of all data, no cross-ruleset reads, no raw rows).

## Outputs
```
internal/apiserver/query.go         typed query parsers: parseRuleset, parseLimit, parseGUID, parseSince, parseMapID, rejectUnknownParams
internal/apiserver/etag.go          etagFor(ruleset, version, canonicalQuery) ; matches(If-None-Match)
internal/apiserver/read.go          handleSweep, handlePlayers, handleTrail, handleBounty, handleCharts
internal/apiserver/read_test.go, query_test.go, etag_test.go
```

## Rules
* **Unknown query parameters are a 400** (`invalid_request`, names the parameter). Repeated parameters are a 400. This stops parameter-pollution tricks.
* `ruleset` is **required** on sweep, players, trail, bounty (no default "all"); must be one of the four values. There is no endpoint that spans rulesets.
* Limits: `players.limit` default 20 max 50; `trail.limit` default 500 max 2000; `bounty.limit` default 25 max 100; sweep returns <= 2000 rows;
  `q` 2-24 runes after normalisation, no control chars. A larger value is rejected with 400, never silently clamped (clients learn the real maximum).
* Pagination: trail uses `since` (epoch ms, exclusive lower bound) plus `limit`; there is no offset (offsets enable cheap full scans).
* `guid` path value validated by `domain.ParseGUID` before touching the store; failure is 400, an unknown valid guid is 404 (same shape whether the
  guid never existed or has expired).
* **ETag**: `W/"<ruleset>.<version>.<fnv64a of canonical query>"` from `store.Version` (one cheap read). If `If-None-Match` matches -> `304`, empty body,
  no data query executed. `Cache-Control: private, max-age=0, must-revalidate`. `charts` additionally `max-age=3600` and an ETag from a hash of the body.
* Time-based sweeps (`maxAgeSec`) would make ETags stale as time passes without writes: the ETag therefore includes `floor(now / 5s)` bucket, so a 304 is valid for
  at most 5 s without a ping (cheap and correct enough for a 5-30 s poll).
* Responses never include internal fields (`first_seen` only where the contract says, no key ids, no row ids).

## Step 0: PoC (`scripts/poc/t07/`)
1. **304 path cost.** In-process: handler returning 304 after one `Version` call is < 1 ms p95 and does zero allocations beyond the response writer. PASS prints p95 + allocs (budget 1 ms).
2. **Query strictness.** `r.URL.Query()` drops nothing but cannot tell you about duplicates: prove `len(vals[k]) > 1` detection and unknown-key detection against a table (`?ruleset=pvp&ruleset=rp`, `?ruleset=pvp&x=1`, `?ruleset=pvp%00`, `?q=%C3%A9` etc.). PASS = each classified as expected.
3. **Weak ETag round trip** with `If-None-Match: W/"a", W/"b"` lists and `*`. PASS prints matches/no-match per case.
4. **Sweep at scale.** 10 000 contacts in SQLite (T04 store): `Sweep` + JSON encode < 40 ms; record bytes. If over, measure encode vs query separately and decide (budget: Sweep 20 ms).
5. **Reject, not clamp** (decided above): PoC proves the 400 message includes the maximum so clients can correct themselves.

## Step 1: RED
- `TestSweep_Table`: empty; one contact; filter by `zone`; by `guid`; `maxAgeSec` boundary (exactly at limit included); ruleset isolation; limit > max rejected.
- `TestQueryStrictness`: unknown param, duplicate param, missing ruleset, bad ruleset, `limit=0`, `limit=-1`, `limit=abc`, `limit=999999999999999999999`, control chars, 1 KB `q`.
- `TestPlayers_PrefixAndAccents`, `TestPlayers_ShortQueryIs400`.
- `TestTrail_404ForUnknownGUID`, `_400ForMalformedGUID`, `_SinceCursorPagination` (loop until empty, no duplicates/gaps).
- `TestETag_304_SkipsStoreQuery` (fake store counts calls: only `Version`), `_ChangesAfterPing`, `_ChangesWithQuery`, `_ChangesAcrossBucket`.
- `TestIngestKeyCannotRead` (403) and `TestViewKeyCannotWrite` (done in T06 too).
- `TestCharts_LongCache`, `TestBounty_StableOrdering`.
- `FuzzQuery` (random raw query strings; never panics; status in {200,304,400}).

## Step 2: GREEN (key code)
```go
type paramSpec map[string]func(string) error // allowed names -> validators

func strictParams(r *http.Request, spec paramSpec) (url.Values, error) {
	v := r.URL.Query()
	for name, vals := range v {
		check, ok := spec[name]
		switch {
		case !ok:           return nil, badParam(name, "unknown parameter")
		case len(vals) != 1: return nil, badParam(name, "must appear once")
		}
		if err := check(vals[0]); err != nil { return nil, badParam(name, err.Error()) }
	}
	return v, nil
}

func (s *server) handleSweep(w http.ResponseWriter, r *http.Request) {
	q, err := parseSweep(r)                       // strictParams + typed SweepQuery
	if err != nil { writeBadRequest(w, r, err); return }
	ver, err := s.store.Version(r.Context(), q.Ruleset)
	if err != nil { s.internal(w, r, err); return }
	tag := etagFor(q.Ruleset, ver, q.canonical(), s.now())
	if notModified(w, r, tag) { return }          // sets ETag + 304
	rows, err := s.store.Sweep(r.Context(), q)
	if err != nil { s.internal(w, r, err); return }
	writeJSONTagged(w, tag, sweepResponse{ServerTime: s.now().UnixMilli(), Ruleset: q.Ruleset, Contacts: rows})
}
```
`writeJSONTagged` encodes into a pooled `bytes.Buffer` (pool only after the benchmark proves a gain), sets `Content-Length`, optional gzip when
`Accept-Encoding: gzip` and body > 1 KB (Lambda Function URL returns bytes as-is, so compress in the handler; local server too).

## Step 3: REFACTOR checklist
- [ ] Five handlers share one skeleton (`parse -> version -> etag -> query -> write`); no copy-pasted blocks.
- [ ] Parsers return typed query structs with a `canonical() string` used for ETags and logs.
- [ ] Response structs live in `read.go` top, match PLAN 5.3 field names exactly (golden JSON tests in `testdata/`).
- [ ] Validators small and named (`validRuleset`, `validLimit(max int)`).

## Step 4: AUDIT
- check.sh green, coverage >= 90% for read/query/etag.
- `FuzzQuery` 90 s.
- Benchmarks logged: `BenchmarkSweep304`, `BenchmarkSweep10k`, `BenchmarkPlayers`, `BenchmarkTrail2000` vs budgets in PLAN 13.
- Security: confirm by test that *no* parameter combination returns data from another ruleset, returns more than the max rows, or exposes key/batch tables; enumeration check: response for unknown vs expired guid is byte-identical.

## Done when
- [ ] Golden JSON files for every endpoint equal PLAN 5.3 shapes.
- [ ] check.sh green; budgets met or a justified deviation in Follow-ups.

## PoC log
PoC step 0 was folded into the real tests (strictness table, ETag list/`*` cases, 304 skips the store, `FuzzQuery` 20 s: 656k execs, no failures).
No separate `scripts/poc/t07/` was written.

## Result
`query.go`, `etag.go`, `read.go` + `read_test.go` implemented; routes registered for the `view` scope. `scripts/check.sh` is green except
govulncheck (stdlib advisories fixed in Go 1.25.3+, toolchain issue, same as T00-T06). Coverage of `internal/apiserver`: 92.9%.

## Follow-ups
- Not done: `BenchmarkSweep304/Sweep10k/Players/Trail2000`, the 90 s fuzz run, and testdata golden files (goldens are inline strings in `TestReadGoldenShapes`).
- Trail `since` is an exclusive lower bound but the store orders newest first and cuts at `limit`, so `since` only answers "fixes newer than X (newest N)".
  It cannot page backwards through history. If the viewer needs that, add a `before` cursor to `TrailFilter` (T14 decides).
- Time-bound ETag bucket applies to sweep only; players/trail/bounty ETags depend on `store.Version` alone.
