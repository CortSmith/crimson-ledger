# T09: DynamoDB store (same contract suite)

**Depends on:** T04 · **Blocks:** T10 · **PLAN.md sections:** [2 D3, D4](../PLAN.md#d3-database-one-store-interface-two-implementations), [7 security](../PLAN.md#7-security-baseline-applies-to-every-task-the-audit-phase-checks-it), [8 cost](../PLAN.md#8-cost-estimate-hobby-load-1-logger-1-3-viewers-15-s-polls-verify-prices-before-deploying), [13 perf](../PLAN.md#13-performance-budgets-measured-in-t21-every-task-logs-its-benchmarks-against-these), [10 workflow](../PLAN.md#10-mandatory-workflow-for-every-task-tdd-with-proof-of-concept-first)

## Goal
`internal/store/dynamostore` implements `store.Store` on one DynamoDB table and passes `storetest.Run` unchanged
(the only allowed difference is the declared `Prune` capability). Designed so a hobby load fits the always-free
provisioned capacity (<= 25 WCU / 25 RCU total) and so reads cost one small query.

## Outputs
```
database/dynamodb/table.json           CreateTable input (used by tests and docs)
database/dynamodb/README.md            key design table below + capacity maths
internal/store/dynamostore/*.go        store.go keys.go apply.go read.go keystore.go
internal/store/dynamostore/dynamostore_test.go   runs storetest.Run against DynamoDB Local
docker-compose.yml (service dynamodb-local, profile "test")  amazon/dynamodb-local, pinned digest, -inMemory
```
Libraries: `github.com/aws/aws-sdk-go-v2` (`config`, `service/dynamodb`, `feature/dynamodb/attributevalue`). Test endpoint override via env `DYNAMODB_ENDPOINT`.

## Table design (single table `crimson-ledger`, keys `PK` S, `SK` S; TTL attribute `ttl` N; one GSI `GSI1`)
| Entity | PK | SK | Attributes / notes |
|---|---|---|---|
| Contact | `C#<rs>#<guid>` | `META` | name, raw, reaction, firstSeen, lastSeen, map, x, y, kind; `GSI1PK=R#<rs>`, `GSI1SK=<lastSeen, 13 digits>` |
| Fix | `F#<rs>#<guid>` | `<t 13 digits>#<mapId 6 digits>` | x, y, kind, `ttl = t + retention` |
| Name word | `W#<rs>#<first 2 chars of word>` | `<word>#<guid>` | prefix search: `begins_with(SK, q)` (q >= 2 chars) |
| Kill | `K#<rs>#<killer>` | `<t>#<victim>` | mapId, `ttl` |
| Bounty total | `B#<rs>` | `<killer>` | kills (N), lastKill |
| Chart | `CHART` | `<mapId 6 digits>` | zone, x0,y0,x1,y1 (first write wins: condition `attribute_not_exists(PK)`) |
| Batch marker | `BATCH#<batchId>` | `-` | `ttl = now + 24h` |
| Version | `V#<rs>` | `-` | `v` (N), atomic `ADD v 1` |
| API key | `KEY#<id>` | `-` | scope, label, hash (B), createdAt, expiresAt, disabled, lastUsedAt |
| Rate counter (T10) | `RL#<bucket>` | `-` | count, `ttl` |
Sweep = `Query GSI1 PK=R#<rs> AND GSI1SK >= <now-maxAge>` (descending); map/guid filters applied in memory (a 15-minute window is small),
a single guid uses `GetItem`. Only one GSI (each GSI doubles the write cost of the item). Bounty = `Query PK=B#<rs>` then sort in memory
(bounded: `MaxBountyKillers = 5000`; documented).

Idempotency without transactions where possible:
* Contacts/fixes/words/charts: deterministic keys + `UpdateItem`/`PutItem`, so re-applying a ping is harmless. Contact update has
  `ConditionExpression: attribute_not_exists(lastSeen) OR lastSeen <= :t` for position fields; `firstSeen` lowers via a separate `SET firstSeen = if_not_exists(...)`+`min` done as a second conditional update.
* Kills: `TransactWriteItems` of (`Put` kill with `attribute_not_exists(PK)`, `Update` total `ADD kills 1`) so a retry cannot double count (transaction = 2x WCU, kills are rare).
* Batch marker is written **last**; checked first (`GetItem`, eventually consistent is fine) to short-circuit duplicates.
* Batching: `BatchWriteItem` (25/request) for fixes and words; handle `UnprocessedItems` with capped exponential backoff + jitter (max 5 tries) -> `ErrUnavailable`.
* Throttling (`ProvisionedThroughputExceededException`) maps to `store.ErrUnavailable` (API answers 503 + `Retry-After: 2`; clients back off).

## Capacity maths (record the real numbers in the PoC log)
Max ping = 200 contacts x ~1 KB item (1 WCU each) + fixes 50 x 200 x 0.2 KB (~1 WCU per 5 fixes) -> ~2 200 WCU of work if every field
were new, **per 2 s flush at worst**. Typical live play: 20-60 contacts changing per flush, 1-3 fixes each: ~100-250 WCU per flush. Provisioned 10 WCU
cannot absorb that burst (burst bucket = 300 s of unused capacity = 3 000 WCU). The logger therefore **coalesces** (T13: send only contacts whose
position changed by > 0.2 % or reaction changed, at most one fix per contact per flush) and the PoC measures sustained WCU/minute for a recorded real session.
Decision rule: if sustained average > 8 WCU/s over a 1-hour simulated session, raise provisioned WCU to 25 (still free) and report; if it still
exceeds, switch the template parameter to on-demand and record the cost estimate. The decision and numbers go in the T21 report.

## Step 0: PoC (`scripts/poc/t09/`) against DynamoDB Local (`docker run -p 8000:8000 amazon/dynamodb-local -inMemory`)
1. **CreateTable from `table.json`**, TTL enable, GSI present. PASS = `DescribeTable` shows ACTIVE (DynamoDB Local ignores TTL deletion, note it).
2. **Prefix word search**: `Query PK=W#rs#mc, SK begins_with "mc"` returns guids; q of exactly 2 chars works; accent folding done before key build. PASS prints results.
3. **Condition `lastSeen <= :t`**: older write fails with `ConditionalCheckFailedException` and is treated as success-no-op by the code. PASS prints both.
4. **Kill transaction retry**: run the same transaction twice; total stays 1 (second fails `TransactionCanceledException` with a conditional check reason -> treated as duplicate). PASS prints total.
5. **Consumed capacity**: call with `ReturnConsumedCapacity=TOTAL` for (a) one max contact upsert, (b) a 50-fix batch, (c) a sweep of 100 contacts, (d) the 304 path (`GetItem V#rs`). Record WCU/RCU numbers; these feed T21.
6. **Item size**: a contact item serialises < 1 KB (1 WCU). Print size.
7. **UnprocessedItems loop**: simulate by throttling a tiny table (1 WCU) and show the retry loop finishes or returns `ErrUnavailable`. PASS prints the outcome.

## Step 1: RED
`dynamostore_test.go`: `storetest.Run(t, newDynamoStore)` where `newDynamoStore` creates a uniquely named table per test (`it-<random>`) on DynamoDB Local,
skipped with a clear message if `DYNAMODB_ENDPOINT` is unset. Run: all subtests fail `not implemented`. Plus store-specific tests:
`TestKeyBuilders` (13-digit padding sorts numerically, `#` cannot appear in guids/rulesets/words so keys cannot collide: assert on hostile inputs),
`TestBatchRetriesUnprocessed`, `TestThrottleMapsToErrUnavailable` (fake client interface).

## Step 2: GREEN
Depend on a small consumer-side interface, not the concrete SDK client, so tests can fake throttling:
```go
type api interface {
	GetItem(ctx context.Context, in *dynamodb.GetItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	Query(ctx context.Context, in *dynamodb.QueryInput, opts ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
	UpdateItem(...); PutItem(...); BatchWriteItem(...); TransactWriteItems(...)
}
```
Key builders are pure functions (`contactKey(rs, guid)`, `fixSK(t, mapID)`) with the padding and separators in one file. Expression strings are constants;
values go through `ExpressionAttributeValues` only (no string splicing).

## Step 3: REFACTOR checklist
- [ ] `keys.go` is the only place that knows `#`-formats; `README.md` table equals it (a test parses the README table and asserts each prefix appears in `keys.go`).
- [ ] No function > 40 lines; the apply pipeline mirrors SQLite's step names (`upsertContacts`, `putFixes`, `putWords`, `applyKills`, `putCharts`, `bumpVersion`, `markBatch`) for easy comparison.
- [ ] Error mapping in one function `mapError(err) error`.
- [ ] Capacity numbers and the decision rule are in `database/dynamodb/README.md`.

## Step 4: AUDIT
- check.sh green; `docker compose --profile test run --rm test-dynamo` runs the suite against DynamoDB Local.
- `go test -race -count=5` on the store.
- Run the suite **once against real DynamoDB** in a throwaway table in your AWS account (`CL_REAL_DYNAMO=1`, region from env, table deleted after): records that Local and real agree. Log the result.
- Security: no `Scan` calls anywhere (`grep -rn "\.Scan(" internal/store/dynamostore` empty); the IAM policy in T10 lists exactly the actions used here (the test prints the set of SDK operations called; compare).
- Performance: `BenchmarkApplyPing_Max_DynamoLocal` and `BenchmarkSweep_DynamoLocal`; round trips per ping counted (target <= 12 requests for a max ping thanks to batching).

## Done when
- [ ] `storetest.Run` green on DynamoDB Local and on real DynamoDB (one logged run).
- [ ] Capacity decision recorded.
- [ ] check.sh green.

## PoC log
`go run ./scripts/poc/t09` against `amazon/dynamodb-local` (-inMemory -sharedDb):
```
PASS 1 table ACTIVE gsis 1
PASS 2 prefix hits 2
PASS 3 older write rejected, old item returned: 200
PASS 4 total after retry: 1
PASS 5 contact put WCU table=1.0 total=2.0
PASS 6 contact item bytes ~ 185 (< 1024)
```
Findings: a contact write costs 2 WCU (table + GSI1). `ReturnValuesOnConditionCheckFailure=ALL_OLD` hands back the old item, so
the upsert needs no separate read. `raw` is a reserved word (attribute renamed `rw`). PoC items 5 (sweep / 304 / 50-fix batch capacity),
and 7 (throttled tiny table) are not run yet; item 7 is covered by `TestBatchWrite_*` with a fake client.

## Audit log
* Contract: `storetest.Run(..., NoPrune())` green on DynamoDB Local; `go test -race -count=5` green; coverage 84.6 %.
* staticcheck and golangci-lint: 0 issues. govulncheck fails only on Go 1.25.0 stdlib advisories (known, see PLAN status).
* No `Scan` (`TestSource_NeverCallsScan`); key formats checked against README (`TestKeyFormats_AreDocumentedInReadme`).
* Deviation: contacts are upserted one `UpdateItem` each (conditional, cannot be batched), so a 200-contact ping is ~200+ requests, not <= 12.

## Follow-ups
* NOT DONE: run the suite once against real DynamoDB (`CL_REAL_DYNAMO=1`); `BenchmarkApplyPing_Max_DynamoLocal`, `BenchmarkSweep_DynamoLocal`; real capacity numbers for the decision rule. Needs AWS credentials.
* `docker compose --profile test run --rm test-dynamo` is not wired (T15 owns the compose test runner); the `dynamodb-local` service exists.
* Perf: parallelise contact upserts (bounded workers) if T21 shows the 200-request ping is too slow; trail reads every fix of a contact (cost grows with 24 h of fixes).
- Provisioned vs on-demand final choice goes to T21 report.
