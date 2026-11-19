# T21: Performance review and cheapest-cloud verification

**Depends on:** T10, T18 (and all build tasks for the code review) · **Blocks:** release · **PLAN.md sections:** [8 cost](../PLAN.md#8-cost-estimate-hobby-load-1-logger-1-3-viewers-15-s-polls-verify-prices-before-deploying), [13 performance budgets](../PLAN.md#13-performance-budgets-measured-in-t21-every-task-logs-its-benchmarks-against-these), [2 D3/D4](../PLAN.md#d3-database-one-store-interface-two-implementations), [7 security](../PLAN.md#7-security-baseline-applies-to-every-task-the-audit-phase-checks-it), [10 workflow](../PLAN.md#10-mandatory-workflow-for-every-task-tdd-with-proof-of-concept-first)

## Goal
(1) Prove every cloud resource is the cheapest option that still meets the security baseline, mechanically and with a real bill check.
(2) Review and test all code for performance so it is **as fast as possible without adding security risk**.

Rule for every optimisation: **measure -> change -> measure -> run T19 + check.sh -> keep only if the benchmark improves >= 10 % (hot paths) and no security test regresses.**
Forbidden speed-ups: removing/relaxing validation, auth, limits, timeouts, TLS verification; longer key caches; `unsafe`; custom crypto; disabling the race detector in CI.

## Outputs
```
perf/baseline/*.txt                 committed `go test -bench` outputs per package (benchstat baselines, per machine class noted)
scripts/bench.sh                    runs all benchmarks (-count=10 -benchmem), compares to baseline with benchstat, fails on >10 % regression or >20 % alloc growth
scripts/cost-lint.sh                parses template.yaml, fails on any forbidden or non-cheapest setting (below)
scripts/loadtest/                   scenarios (Go `vegeta` or k6 in Docker): steady, burst, spike, soak
scripts/aws-cost-report.sh          CloudWatch + Cost Explorer queries -> docs/perf/cost-report.md
docs/perf/perf-report.md            results vs PLAN 13 budgets, profiles, decisions
docs/perf/cost-report.md            resources, price assumptions, measured usage, projected monthly bill
Makefile: bench, bench-compare, loadtest-local, loadtest-aws, cost-lint
```

## Part 1: Cheapest cloud resources (verification checklist; `cost-lint.sh` automates the template half)
| Resource | Cheapest choice | Lint rule |
|---|---|---|
| Compute | Lambda `provided.al2023`, **arm64** (about 20 % cheaper per GB-s than x86) | `Architectures: [arm64]` required |
| Memory | measured: pick the size with the lowest cost per request (duration x memory), among 128/192/256/512 MB | `MemorySize <= 512`; chosen value matches the report |
| Timeout | 10 s (limits worst-case billing) | `Timeout <= 10` |
| Front door | Lambda Function URL (free) | no `AWS::ApiGateway*`, `AWS::ApiGatewayV2*`, `AWS::CloudFront*`, `AWS::ElasticLoadBalancing*` |
| Abuse ceiling | reserved concurrency (free) | `ReservedConcurrentExecutions` present and <= 10; **no** provisioned concurrency |
| Database | DynamoDB single table, **provisioned inside the free tier** (<= 25 WCU/RCU incl. GSI) unless measurement forces on-demand; no PITR, no streams, no DAX, no global tables, standard table class | `BillingMode`/capacity checked; sum of table + GSI capacity <= 25/25; forbidden properties absent |
| Encryption | AWS-owned key (free); **no** customer KMS key | no `AWS::KMS::Key`; SSM uses `aws/ssm` default key |
| Secrets | SSM Parameter Store Standard (free) not Secrets Manager ($0.40/secret/month) | no `AWS::SecretsManager::*` |
| Network | none: Lambda outside any VPC; no NAT, no interface endpoints | no `VpcConfig`, no `AWS::EC2::*` |
| Logs | CloudWatch Logs, 14-day retention, JSON, **no custom metrics, no EMF, no X-Ray, no Insights subscriptions** | `RetentionInDays <= 14`; `Tracing` absent/Disabled |
| Alarms | Budgets (first 2 budgets free) + <= 10 CloudWatch alarms (free tier) | at most 2 `Budget`, <= 10 `Alarm` |
| Storage | artifact S3 bucket with 30-day lifecycle, delete after deploy optional | lifecycle present |
| WAF / Shield / Cognito / ELB / EventBridge rules | none | forbidden types |
| Data transfer | responses small (gzip), 304s | measured bytes/request in report |
`cost-lint.sh` is also run on the template in CI (T16) so a future change cannot silently add cost. Prices are looked up at review time from the
official AWS pricing pages/API (`aws pricing get-products`) and stored in `cost-report.md` with the retrieval date; the plan's numbers are estimates until then.

### Cheapest-config experiments (all logged)
1. **Lambda memory sweep** (128/192/256/512 MB): same 5-minute steady load against each; record `Duration`, `Billed Duration`, cold-start `Init Duration`; compute GB-s per 1 000 requests and pick the minimum. Expect 256 MB may beat 128 MB if duration more than halves. PASS = table + decision.
2. **DynamoDB provisioned vs on-demand**: replay a recorded 1-hour session through the real logger -> API at 2x speed on both billing modes (scratch tables); record consumed WCU/RCU, throttles, p95 latency. Decision rule from T09: stay provisioned if throttles = 0 and sustained average < 80 % of provisioned; otherwise raise within free 25 units; otherwise on-demand with estimate.
3. **Read cost minimisation**: confirm the viewer's steady state is dominated by 304s (1 `GetItem` on `V#<rs>`, <= 0.5 RCU eventually consistent) and measure RCU per poll; try poll interval 15 s vs 30 s and show cost delta; keep default 15 s only if free-tier-safe.
4. **Write cost minimisation**: logger coalescing parameters (0.2 % threshold, 2 s flush) tuned against WCU/hour; show cost vs freshness trade-off table; keep the cheapest setting that preserves radar usefulness.
5. **Response size**: gzip level 1 vs 5 vs 9 vs off on the sweep body: bytes, CPU ms; pick the best total (usually level 1-5); data transfer out is free for the first 100 GB/month, so CPU time dominates: record.
6. **Cold start**: binary size/flags (`-ldflags="-s -w"`, `-trimpath`), lazy init of SSM, `GOMAXPROCS`/`GOGC` tuning (`GOGC=200` may cut GC work in a 128-256 MB container; test, record), `lambda.norpc` tag.
7. **Provisioned-free alternatives considered and rejected** (documented with prices): API Gateway HTTP API ($1/M requests), RDS/Aurora, Lightsail ($3.50-5/month fixed, simpler but not free), EC2 t4g.nano (about $3/month): kept for the report so the choice is auditable.

### Real-bill verification
Deploy the scratch stack for 7 days with a synthetic steady load (1 logger at 2 s + 3 viewers at 15 s, 8 h/day) or use the real stack; run `scripts/aws-cost-report.sh`
(`aws ce get-cost-and-usage --granularity DAILY --group-by SERVICE`, plus CloudWatch `Invocations`, `Duration`, `ConsumedRead/WriteCapacityUnits`, `Throttles`).
PASS = projected monthly cost <= $1.00 and within 25 % of the model in PLAN 8; any service with a non-zero charge is explained or removed.

## Part 2: Code performance review

### Benchmarks (written test-first in each component task, consolidated here)
Required benchmarks (names fixed so `benchstat` baselines line up):
`BenchmarkValidatePing_Max`, `BenchmarkDecodePing_Max`, `BenchmarkApplyPing_Max_SQLite`, `BenchmarkApplyPing_Max_DynamoLocal`, `BenchmarkSweep_10k_SQLite`, `BenchmarkSweep304`,
`BenchmarkPlayers_Prefix`, `BenchmarkTrail_2000`, `BenchmarkAuth_CachedKey`, `BenchmarkAuth_ColdKey`, `BenchmarkHandler_EndToEnd_Ping`, `BenchmarkClientSweepDecode_2000`,
`BenchmarkClientPostPingGzip_Max`, `BenchmarkParseLines_1M`, `BenchmarkTracker_Update`, `BenchmarkBatcher_Flush`, `BenchmarkNormalizeName`.
Gate (scripts/bench.sh): budgets from PLAN 13, plus no regression > 10 % time or > 20 % allocs vs committed baseline; baselines updated only in a commit that explains why.

### Profiling procedure (for every hot path above budget or in the top 3 of a benchmark)
1. `go test -bench X -cpuprofile cpu.out -memprofile mem.out -benchtime 3s`; `go tool pprof -top -cum`, `-list`; record top 5.
2. Look for: allocations in loops, `fmt.Sprintf` on hot paths, repeated JSON marshal, regexp compile in functions, `append` without capacity, string<->[]byte copies, interface boxing, `defer` in tight loops, map growth without size hint, reflection (`encoding/json`) on 200x50 structures.
3. SQL: `EXPLAIN QUERY PLAN` for every statement (must use an index/PK; no `SCAN`), batched multi-row inserts, prepared statements per transaction, `WITHOUT ROWID` where used, `ANALYZE`, WAL checkpoint settings, page size/cache size tested (`cache_size`, `mmap_size`: safe, read-only settings), no `synchronous=OFF` (durability is a security/integrity property: allowed `NORMAL` with WAL only).
4. DynamoDB: round trips per ping (<= 12), batch sizes (25), parallel `BatchWriteItem` with bounded concurrency (4) after proving benefit, projection expressions to read fewer bytes, `ConsistentRead=false` where staleness is harmless, no per-request `DescribeTable`.
5. Concurrency: goroutine counts stable (`pprof goroutine`), no unbounded fan-out, channel sizes justified, `sync.Pool` only for buffers proven by benchmark, lock contention via `-mutexprofile`/`-blockprofile`.
6. Allowed micro-optimisations (each needs a benchmark delta): `json.Encoder` into reused buffer, pre-sized slices, avoiding intermediate structs, faster hand-written encoding of the **sweep response only if >= 30 % faster** and verified identical by golden + fuzz differential test (still produces escaped output; security-neutral), `strings.Builder`, `bytes.Buffer` pools, `sort.Slice` -> `slices.SortFunc`.
7. Every accepted optimisation gets: before/after `benchstat`, a comment saying why (`// perf: ...`), the security tests green (T19 subset `X3xx X4xx` + unit), and a line in `perf-report.md`.

### Load tests (both targets; AWS capped by T19 safety rules)
| Scenario | Shape | Pass criteria |
|---|---|---|
| steady | 1 logger @2 s (60 contacts) + 3 viewers @15 s for 30 min | 0 errors; p95 server time <= 25 ms warm; cost projection per report |
| burst | 10 viewers @5 s + logger max pings for 5 min | no 5xx; throttles = 0 (DynamoDB) or retried successfully; p99 < 500 ms |
| spike | 0 -> 200 req/s for 30 s (local) / 50 req/s (aws) | limiter returns 429, no 5xx, recovery < 10 s |
| soak | 1 logger + 3 viewers for 8 h (local) | RSS flat (< 5 % drift), no FD/goroutine growth, SQLite file growth matches data, `crimson-db check` clean |
| cold-start storm (aws) | scale to zero, 20 concurrent first requests | p95 first byte < 1 s, init < 400 ms |
Record: latency histograms, error rates, CPU/RSS (local `docker stats`/`pprof`), Lambda `Duration`/`MaxMemoryUsed` (right-size memory to measured max + 30 % headroom if cheaper), DynamoDB consumed capacity.

### Client performance
* Logger: parse 1 M lines < 3 s and < 100 MB RSS; steady CPU < 1 % idle and < 5 % during active play on a mid-range PC; upload goroutine never blocks the tailer (bounded channels) — test with a hung server for 10 min: tailer keeps up, memory bounded.
* Viewer: first paint < 300 ms after data, 2 000 blips at 60 fps (frame p95 < 16 ms), poll 304 handling < 5 ms in Go, idle CPU < 1 %, memory flat in 1 h soak, startup to window < 1.5 s on the reference machine, binary size reported (maps WebP quality tuned: size vs visual diff).
* Launcher: < 50 ms to exec the UI binary (T12).
* Startup of API locally: container healthy < 2 s; migrations on an empty DB < 200 ms.

## Step 0: PoC (`scripts/poc/t21/`)
1. **benchstat gate**: two benchmark runs with an injected 15 % slowdown -> `bench.sh` exits non-zero; with 5 % noise -> passes (use `-count=10`; show variance handling). PASS prints both.
2. **cost-lint catches violations**: feed templates containing an x86 function, an API Gateway, a NAT gateway, a KMS key, provisioned concurrency, `Tracing: Active`, 30-day logs; each is flagged by a distinct rule. PASS prints the 7 messages; the real template passes.
3. **Pricing lookup** via `aws pricing get-products --service-code AWSLambda` / `AmazonDynamoDB` works with the CLI (region `us-east-1` endpoint) and yields arm64 GB-s and request prices to feed the model; record date. PASS prints numbers.
4. **Load generator** can drive the local API at 200 req/s from the build image and reports histograms; PASS prints p50/p95/p99.
5. **Memory sweep harness** can redeploy the function with different `MemorySize` and run a 2-minute load each (script with `aws lambda update-function-configuration`); PASS prints cost-per-1k-request for each size from `REPORT` lines.
6. **`EXPLAIN QUERY PLAN` audit script** runs every SQL constant in the repo (extracted by a test that lists them) against a migrated DB and fails on `SCAN TABLE` for tables > 1 000 rows. PASS prints plans.
7. **pprof baseline** of the real API under steady load captured and the top functions listed (so later optimisations are evidence-based).

## Step 1: RED
Commit the budgets as failing gates first: `bench.sh` with PLAN 13 budgets (fails where code is slower), `cost-lint.sh` (fails if any rule violated), load-test pass criteria as assertions. The initial failures list is the work queue.

## Step 2: GREEN
Work the queue in order of cost/benefit; for each item follow the optimisation rule at the top. Choose and write down: Lambda memory size, DynamoDB billing mode and capacity, gzip level, GOGC, poll interval default, coalescing parameters; update the template defaults, `api.json`/`logger.json`/`viewer.json` defaults and PLAN 8/13 with measured numbers (keep PLAN as source of truth).

## Step 3: REFACTOR checklist
- [ ] Performance code stays readable: each optimisation isolated in a small function with a `// perf:` comment, a benchmark, and a test proving identical output to the simple version (differential test).
- [ ] No optimisation duplicates logic that can drift (single source for encoding, validation, key building).
- [ ] Baselines and reports regenerated by scripts, not hand-edited.
- [ ] Config defaults and the template reflect measured choices, with the measurement referenced in a comment.

## Step 4: AUDIT
- `scripts/bench.sh` green vs budgets and baseline; `cost-lint.sh` green; `check.sh` green; **T19 re-run green** after the last optimisation (proof that speed did not cost security); `govulncheck` unchanged.
- Security review of the diff of all performance changes by the T20 reviewer: list each changed validation/limit/cache (expected: none removed, none loosened) and attach.
- Cost: 7-day bill report within projection (<= $1.00/month); budget alarm verified by `aws budgets describe-budgets`.

## Done when
- [ ] Every PLAN 13 budget met or a justified, owner-approved deviation recorded in `perf-report.md`.
- [ ] Cheapest-resource table above fully satisfied and enforced by `cost-lint.sh` in CI.
- [ ] Final numbers copied into PLAN 8 and 13.

## PoC log
_(paste)_

## Follow-ups
- Re-measure after dependency or runtime upgrades (Go minor, AWS price changes).
