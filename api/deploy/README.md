# Deploying the API to AWS

One command deploys, one command removes everything.

```
api/deploy/deploy.sh --budget-email you@example.com     # asks you to type the AWS account id first
api/deploy/deploy.sh --destroy                          # asks you to type the stack name first
api/deploy/deploy.sh --dry-run --yes --budget-email x@y.z   # prints every command, runs none
```

Needs: AWS CLI v2 with credentials and a default region, Docker (builds the arm64 binaries), `jq`.

## What gets created

| Resource | Why | Cost |
|---|---|---|
| DynamoDB table `Table` + GSI1 | all data; TTL removes fixes and kills after 24 h | provisioned 15+10 WCU/RCU stays inside the always-free 25/25 |
| `PublicFn` (Lambda arm64, 128 MB) | the API; reserved concurrency 5 is the cost and abuse ceiling | free tier covers hobby load |
| API Gateway **HTTP API** (one `$default` route, stage throttle 20 req/s, burst 40) | the public entry point; the Lambda does its own routing and key checks | **1 USD per million requests** (first million a month free for the first 12 months) |
| `AdminFn` (no URL, IAM-only, concurrency 1) | key create/list/revoke/rotate, invoked with the AWS CLI | ~0 |
| 2 log groups, 14 days retention | function logs | ~0 |
| SSM SecureString `/crimson-ledger/pepper` | key-hashing secret, default `aws/ssm` key (free) | free (Standard tier) |
| S3 bucket `crimson-ledger-artifacts-<account>-<region>` | deployment zips; public access blocked, SSE-S3, 30-day expiry | cents |
| Budget, 5 USD/month | mails at 80 % actual and 100 % forecast | free |
| *Only with `--domain`:* API Gateway regional custom domain + API mapping | serves the API on your own host name (`api.example.com`) and switches the default `execute-api` address off | no extra charge |
| *Only with `--hosted-zone-id`:* 1 Route 53 alias record (A) | points the name at API Gateway | the record is free; a hosted zone costs 0.50 USD/month |

No VPC, NAT, KMS key, REST API Gateway, CloudFront, load balancer, X-Ray, PITR or streams (`scripts/cost-lint.sh` fails if one appears). Verify current prices before you deploy: PLAN.md section 8.

## Use your own domain name

The API is served by an API Gateway HTTP API, which supports custom domains directly. Without `--domain` you get the default address
`https://<id>.execute-api.<region>.amazonaws.com` (printed as `ApiUrl`). With `--domain` that default address is **switched off**, so the API
is reachable only by your name. You need a domain you control and DNS either in Route 53 or at any provider.

### 1. Get a certificate (once, in the SAME region as the stack)

```
REGION=$(aws configure get region)
CERT=$(aws acm request-certificate --region "$REGION" --domain-name api.example.com \
        --validation-method DNS --query CertificateArn --output text)
aws acm describe-certificate --region "$REGION" --certificate-arn "$CERT" \
        --query 'Certificate.DomainValidationOptions[0].ResourceRecord'
```
Create the CNAME it prints (Name and Value) at your DNS provider, or in the Route 53 hosted zone, then wait:
```
aws acm wait certificate-validated --region "$REGION" --certificate-arn "$CERT"
```
Keep that CNAME: it lets AWS renew the certificate for free.

### 2. Deploy with the domain

```
api/deploy/deploy.sh --budget-email you@example.com --domain api.example.com --certificate-arn "$CERT"                       # DNS elsewhere
api/deploy/deploy.sh --budget-email you@example.com --domain api.example.com --certificate-arn "$CERT" --hosted-zone-id Z0123456789ABC   # Route 53 record created for you
```
Re-running on an existing stack adds the domain without touching the table or the keys.

### 3. Point the name at the API (only without `--hosted-zone-id`)

The script ends with `create a CNAME record api.example.com -> d-xxxx.execute-api.<region>.amazonaws.com`. Add it at your DNS provider.
Read it again any time: `aws cloudformation describe-stacks --stack-name crimson-ledger --query "Stacks[0].Outputs[?OutputKey=='DnsTarget'].OutputValue" --output text`.

### 4. Check it, then use it

```
curl -i https://api.example.com/v1/health      # 200
```
In the viewer and the scout set **Server address** to `https://api.example.com`. The keys stay the same. A browser at `/` shows an API error page: there is only the API under `/v1/`.

### Notes
* **Cost:** HTTP API is 1 USD per million requests. A scout uploading every 2 s while you play is about 1.3 million requests a month if it ran around the clock, plus a viewer polling every 15 s about 170 thousand. Expect roughly 1 to 2 USD a month for heavy use; raise `upload_interval_ms` in the scout to lower it. The 5 USD budget alarm watches everything.
* **Throttle:** the stage allows 20 requests/s (burst 40) in total, shared by every caller. Past that callers get 429 and the apps back off. Change it with the `ApiRateLimit` and `ApiBurstLimit` parameters.
* **Client addresses:** API Gateway reports the real caller address, so per-IP limits and lockouts work per user.
* **Remove the domain:** `deploy.sh --budget-email you@example.com --remove-domain` (the default address comes back). Delete your own CNAMEs yourself.
* **Not run against real AWS yet.** Do the first deploy on a scratch stack: `--stack crimson-ledger-test`.

## First keys and the apps

The deploy prints an ingest key (for the scout) and a view key (for the viewer) **once, to your terminal only**, then the `server_url`.
Paste them into the apps. Later: 
`aws lambda invoke --function-name <AdminFunctionName> --cli-binary-format raw-in-base64-out --payload '{"cmd":"keys.create","scope":"view","label":"laptop","expiresDays":90}' out.json`
(also `keys.list`, `keys.revoke` and `keys.rotate` with `"id"`). Read the token from `out.json` and delete the file; the function never logs it.
Revocation takes up to 30 s to reach running functions (key cache).

## Throttling

When the stage throttle or 5 running executions are exceeded, API Gateway or Lambda answers 429 itself. The apps treat 429 and 503 as retryable and back off.
If the shared rate counter or SSM is unavailable the API answers 503 with `Retry-After` (fail closed), never "open".

## Roll back and remove

* A failed update rolls back by itself (CloudFormation). Re-run the script with the previous commit checked out to go back.
* `deploy.sh --destroy` deletes the stack (the table and its data), the bucket and the pepper. Every key stops working for good.
  Afterwards `aws cloudformation list-stacks` and `aws dynamodb list-tables` must show nothing of ours.
* The pepper is never rotated by the template or by re-deploying. Losing it invalidates all keys.

## Known limits

* `TableBilling=PAY_PER_REQUEST` works but is not free tier.
* Data retention is fixed at 24 h in code (`dynamostore.Retention`), so there is no retention parameter.
* Not run against real AWS yet: see T10 Follow-ups in `docs/tasks/T10-aws-lambda-deploy.md`.
