# T10: AWS Lambda handler, admin Lambda, SAM template, deploy script

**Depends on:** T05, T06, T07, T09 · **Blocks:** T15, T18 · **PLAN.md sections:** [2 D4, D6](../PLAN.md#d4-aws-shape-lambda-arm64--function-url--dynamodb--ssm-deployed-by-aws-cloudformation-deploy), [7 security](../PLAN.md#7-security-baseline-applies-to-every-task-the-audit-phase-checks-it), [8 cost](../PLAN.md#8-cost-estimate-hobby-load-1-logger-1-3-viewers-15-s-polls-verify-prices-before-deploying), [10 workflow](../PLAN.md#10-mandatory-workflow-for-every-task-tdd-with-proof-of-concept-first)

## Goal
The same `apiserver` handler running in Lambda behind a Function URL, DynamoDB as the store, secrets in SSM, an IAM-only admin
Lambda for key management, and a one-command, least-privilege, cheapest-possible deployment with a teardown command.

## Outputs
```
api/cmd/crimson-api-lambda/main.go     public handler: cold-start init (SSM pepper, DynamoDB client), httpadapter wrapper
api/cmd/crimson-api-admin/main.go      admin handler: keys.create|list|revoke|rotate (event JSON in, JSON out)
internal/apiserver/dynamolimiter.go    Limiter on DynamoDB atomic counters (TTL), used only on Lambda
api/deploy/template.yaml               SAM-syntax CloudFormation
api/deploy/deploy.sh                   build -> package -> deploy -> print URL ; --destroy
api/deploy/README.md                   prerequisites, costs, rollback, teardown
scripts/cost-lint.sh                   (T21 extends) fails on forbidden resources
```
Libraries: `github.com/aws/aws-lambda-go` (`lambda`, `events`), `github.com/awslabs/aws-lambda-go-api-proxy/httpadapter`, `aws-sdk-go-v2/service/ssm`.

## Template (cheapest viable; every property is deliberate)
```yaml
AWSTemplateFormatVersion: '2010-09-09'
Transform: AWS::Serverless-2016-10-31
Parameters:
  ReservedConcurrency: {Type: Number, Default: 5}        # hard cap on parallel executions = cost + abuse ceiling
  TableBilling:        {Type: String, Default: PROVISIONED, AllowedValues: [PROVISIONED, PAY_PER_REQUEST]}
  TableWcu: {Type: Number, Default: 10}
  TableRcu: {Type: Number, Default: 10}
  GsiWcu: {Type: Number, Default: 5}
  GsiRcu: {Type: Number, Default: 5}
  RetentionDays: {Type: Number, Default: 30}
  BudgetEmail: {Type: String}
Resources:
  Table:                       # AWS::DynamoDB::Table, TimeToLiveSpecification ttl, no PITR, no streams, SSE with the AWS-owned key (free)
  PublicFn:                    # AWS::Serverless::Function  Runtime provided.al2023, Architectures [arm64], MemorySize 128, Timeout 10,
                               # Handler bootstrap, ReservedConcurrentExecutions !Ref ReservedConcurrency, Tracing Disabled,
                               # no VpcConfig, FunctionUrlConfig {AuthType: NONE}  (auth is our key check), Environment {TABLE, PEPPER_PARAM, RETENTION_DAYS}
  PublicFnLogs:                # AWS::Logs::LogGroup RetentionInDays 14
  AdminFn:                     # same runtime/arch, NO FunctionUrlConfig, Timeout 10, ReservedConcurrentExecutions 1
  Budget:                      # AWS::Budgets::Budget monthly $5, 80% actual + 100% forecast -> BudgetEmail
```
Public function IAM (inline policy, nothing else): `dynamodb:GetItem, PutItem, UpdateItem, Query, BatchWriteItem, TransactWriteItems` on the table ARN
and `table/index/GSI1`; `ssm:GetParameter` on the one pepper parameter; CloudWatch Logs write to its own log group. **No** `Scan`, no `DeleteItem`, no wildcards.
Admin function IAM: `GetItem, PutItem, UpdateItem, Query` on `KEY#` items only (`dynamodb:LeadingKeys` condition `KEY#*`). It has no `DeleteItem`: keys are disabled, never deleted.
Pepper: created by `deploy.sh` (`aws ssm put-parameter --type SecureString --tier Standard`, default `aws/ssm` key = free) only if missing; never rotated by the template.
Artifacts bucket `crimson-ledger-artifacts-<account>-<region>` created by `deploy.sh`: block public access, SSE-S3, lifecycle expire 30 days.

## Step 0: PoC (`scripts/poc/t10/`)
1. **Adapter works for Function URL events.** Build a tiny handler with `httpadapter.NewV2(mux)`, feed a recorded Function URL event JSON
   (`testdata/furl_get.json`, `furl_post_gzip_base64.json`) via `lambda`'s local invoke or by calling `Handler.ProxyWithContext`; PASS = status 200 and a gzip POST body decoded
   from `isBase64Encoded: true` correctly. Record the exact event field used for client IP (`requestContext.http.sourceIp`) and confirm `r.RemoteAddr` gets it.
2. **Cold start.** Deploy the PoC (empty template, same runtime settings) and invoke 20 times with a forced cold start (update env var between runs); record init duration from the `REPORT` line (`Init Duration`). PASS = < 400 ms including an SSM read. Compare 128 MB vs 256 MB (more memory = more CPU; the cheapest *per request* may be 256 MB if duration halves). Record both and pick by GB-s cost.
3. **Binary size and flags**: `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags='-s -w' -trimpath -tags lambda.norpc -o bootstrap` < 15 MB zipped. PASS prints size.
4. **Template validity**: `aws cloudformation validate-template --template-body file://template.yaml` passes, and `cfn-lint` (pip, in Docker) passes with zero errors. PASS prints both.
5. **Function URL abuse ceiling**: with `ReservedConcurrentExecutions=5`, fire 200 parallel requests; some get 429 from Lambda (throttled), account is unaffected. PASS records the throttle behaviour (clients treat 429 as retryable with backoff).
6. **IAM**: `aws iam simulate-principal-policy` for the role: allowed = the listed actions only, `dynamodb:Scan` and `dynamodb:DeleteItem` denied. PASS prints decisions.
7. **Cost sanity**: run a 10-minute load (T21 harness) and read `get-metric-statistics` (Invocations, Duration) to extrapolate the monthly bill against free tier. Record.

## Step 1: RED
- `TestPublicHandler_EventTable` using recorded events: GET health, POST ping plain, POST ping gzip base64, bad JSON, missing auth, oversize body (Function URL payload limit is 6 MB; our 256 KiB limit applies first).
- `TestColdStart_FailsClosedWithoutPepper` (SSM error -> every request 503, never "auth disabled").
- `TestAdmin_Events`: create returns token once and never logs it (capture log output), list hides hashes, revoke disables, unknown cmd 400, extra fields rejected.
- `TestDynamoLimiter_FixedWindow` against DynamoDB Local (atomic `ADD`, window rollover, TTL attribute present, a limiter/store error **fails closed** with 503 + `Retry-After`, so an outage cannot become a free-abuse window; decision recorded).
- `TestTemplate_*` (Go test parsing `template.yaml`): arch is arm64, memory <= 256, ReservedConcurrency present, no `VpcConfig`, Function URL only on PublicFn, log retention <= 14, no `Scan` in policies, no `*` actions/resources except `logs:*` on its own group.
- `deploy.sh` is tested with `shellcheck` and a `--dry-run` that prints the commands.

## Step 2: GREEN (key code)
```go
var handler = sync.OnceValues(func() (lambda.Handler, error) {   // cold-start init once per container
	cfg, err := awsconfig.LoadDefaultConfig(context.Background())
	if err != nil { return nil, err }
	pepper, err := loadPepper(ctx, ssm.NewFromConfig(cfg), os.Getenv("PEPPER_PARAM"))
	if err != nil { return nil, fmt.Errorf("load pepper: %w", err) }      // fail closed
	st := dynamostore.New(dynamodb.NewFromConfig(cfg), os.Getenv("TABLE"), retentionFromEnv())
	h := apiserver.New(apiserver.Deps{Store: st, Pepper: pepper, Limiter: apiserver.NewDynamoLimiter(st.Client(), os.Getenv("TABLE")),
		Now: time.Now, Log: slog.New(slog.NewJSONHandler(os.Stdout, nil)), Opts: lambdaOptions()})
	return lambda.NewHandler(httpadapter.NewV2(h).ProxyWithContext), nil
})
```
`deploy.sh` flow: check `aws sts get-caller-identity` and print account/region and **ask for confirmation** -> build `bootstrap` in Docker (`docker compose run --rm lambda-package`)
-> `aws cloudformation package` -> `aws cloudformation deploy --capabilities CAPABILITY_IAM --parameter-overrides ...` -> read `FunctionUrl` output ->
create the first keys via `aws lambda invoke` on the admin function (prints tokens once to the terminal only; never to CloudWatch because the admin handler's
response is not logged) -> print the `server_url` to paste into the apps. `--destroy` deletes the stack, the artifacts bucket and the SSM parameter after confirmation.

## Step 3: REFACTOR checklist
- [ ] `main.go` files <= 40 lines; wiring lives in `internal/awswire` (testable with fakes).
- [ ] Template parameters have descriptions, `AllowedValues`/`MinValue` constraints; no unused parameters.
- [ ] `deploy.sh` uses `set -euo pipefail`, quotes everything, passes `shellcheck`, has `--help`, never echoes secrets.
- [ ] README states costs, what is created, how to roll back and how to delete everything.

## Step 4: AUDIT
- check.sh green; `shellcheck`, `cfn-lint`, `scripts/cost-lint.sh` green.
- Deploy to a scratch stack, run T18 `aws` target smoke subset, then `--destroy`; confirm via `aws cloudformation list-stacks` and `aws dynamodb list-tables` that nothing remains.
- Security: no `AuthType NONE` on anything but PublicFn; admin function has no URL and no resource policy granting public invoke (`aws lambda get-policy` returns ResourceNotFound); SSM parameter is SecureString; S3 bucket public access block verified with `aws s3api get-public-access-block`; log group has retention; CloudTrail not required.
- Cost: Cost Explorer/`aws ce` check after 24 h of the scratch deploy shows $0.00 or near (report in T21).

## Done when
- [ ] One command deploys; one command removes everything; T18 passes against the deployed URL.
- [ ] check.sh and template tests green.

## PoC log
No AWS credentials were available when T10 was built, so PoCs 2, 5, 6 and 7 (cold start, concurrency throttle, IAM simulation, cost) are **not run**. Done offline:
* PoC 1: `httpadapter.NewV2` handles Function URL events (payload format 2.0) incl. gzip + `isBase64Encoded`, and `requestContext.http.sourceIp` becomes the client IP (`TestPublicHandler_EventTable`, `TestPublicHandler_ClientIPComesFromSourceIP`). Events are built in code, not recorded JSON.
* PoC 3: `scripts/build-lambda.sh`: public 11.2 MB, admin 10.7 MB uncompressed (zip is smaller), limit 15 MB.
* PoC 4: structural template tests pass; `aws cloudformation validate-template` and `cfn-lint` not run (no credentials, not installed).

## Follow-ups
* F10-1 Run PoCs 2, 5, 6, 7 and the Step 4 scratch deploy/destroy on a real account; run `cfn-lint` and `shellcheck` (the shellcheck test skips when it is missing).
* F10-2 `TestDynamoLimiter_FixedWindow` uses a fake counter, not DynamoDB Local. Add a DynamoDB Local case next to the dynamostore tests.
* F10-3 `RETENTION_DAYS` and the `RetentionDays` parameter were dropped: retention is the `dynamostore.Retention` constant (24 h). Make it configurable only if needed.
* F10-4 Template defaults are table 15/15 and GSI 10/10 (T09 capacity decision), not the 10/10 and 5/5 first sketched here. `BatchGetItem` is in the public policy because the store uses it.
* F10-5 Explicit IAM roles replace SAM `Policies` (SAM would add a wildcard managed policy). The admin `Query` on GSI1 is conditioned on leading key `KEYS`.
* F10-6 Limiter or store outage on the rate counter answers 503 + Retry-After (`limiterFailed`); a failed cold start is retried on the next request instead of cached.
* F10-7 `lambda-package` compose service uses `golang:1.25-bookworm` unpinned; T15 pins digests.
* F10-8 `main.go` of crimson-api-admin is 33 lines; wiring is in `internal/awswire`.
