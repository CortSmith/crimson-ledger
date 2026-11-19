#!/usr/bin/env bash
# One-command deploy of the Crimson Ledger API to AWS, and one-command removal.
#   api/deploy/deploy.sh --budget-email you@example.com        deploy (asks first)
#   api/deploy/deploy.sh --budget-email you@example.com --domain api.example.com --certificate-arn ARN [--hosted-zone-id Z...]
#                                                               also serve the API on your own domain (see api/deploy/README.md)
#   api/deploy/deploy.sh --destroy                              delete everything (asks first)
#   api/deploy/deploy.sh --help
set -euo pipefail

STACK="crimson-ledger"
PEPPER_PARAM="/crimson-ledger/pepper"
BUDGET_EMAIL=""
DOMAIN=""
CERT_ARN=""
ZONE_ID=""
REMOVE_DOMAIN=0
DRY_RUN=0
DESTROY=0
ASSUME_YES=0
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

usage() {
  cat <<'USAGE'
usage: deploy.sh [--budget-email ADDRESS] [--domain NAME --certificate-arn ARN [--hosted-zone-id ID]] [--stack NAME] [--destroy] [--dry-run] [--yes]

  --budget-email   where the 5 USD monthly budget alerts go (required to deploy)
  --domain         serve the API on this host name (api.example.com) on API Gateway; needs --certificate-arn
  --certificate-arn  ACM certificate for --domain, in the SAME region as the stack (the script checks)
  --hosted-zone-id Route 53 hosted zone of --domain; the DNS records are then created for you (otherwise you add one CNAME)
  --remove-domain  take the custom domain (and its DNS record) away again; the default API address comes back
  --stack NAME     CloudFormation stack name (default crimson-ledger)
  --destroy        delete the stack, the artifacts bucket and the pepper parameter
  --dry-run        print the commands instead of running them
  --yes            do not ask for confirmation (for CI scratch stacks)

Needs: aws CLI v2 with credentials, docker, jq. Keys are printed once, to this terminal only.
USAGE
}

while [ $# -gt 0 ]; do
  case "$1" in
    --budget-email) BUDGET_EMAIL="${2:?--budget-email needs a value}"; shift 2 ;;
    --domain) DOMAIN="${2:?--domain needs a value}"; shift 2 ;;
    --certificate-arn) CERT_ARN="${2:?--certificate-arn needs a value}"; shift 2 ;;
    --hosted-zone-id) ZONE_ID="${2:?--hosted-zone-id needs a value}"; shift 2 ;;
    --remove-domain) REMOVE_DOMAIN=1; shift ;;
    --stack) STACK="${2:?--stack needs a value}"; shift 2 ;;
    --destroy) DESTROY=1; shift ;;
    --dry-run) DRY_RUN=1; shift ;;
    --yes) ASSUME_YES=1; shift ;;
    --help|-h) usage; exit 0 ;;
    *) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
done

# run prints the command in dry-run mode and runs it otherwise.
run() {
  if [ "$DRY_RUN" -eq 1 ]; then
    printf '+'; printf ' %q' "$@"; printf '\n'
  else
    "$@"
  fi
}

# query runs a read-only command; in dry-run mode it prints it and yields a placeholder.
query() {
  if [ "$DRY_RUN" -eq 1 ]; then
    { printf '+'; printf ' %q' "$@"; printf '\n'; } >&2
    echo "<dry-run>"
  else
    "$@"
  fi
}

confirm() { # confirm <what to type>
  [ "$ASSUME_YES" -eq 1 ] && return 0
  local answer
  read -r -p "Type '$1' to continue: " answer
  [ "$answer" = "$1" ] || { echo "aborted"; exit 1; }
}

if [ "$DRY_RUN" -eq 1 ]; then
  ACCOUNT="000000000000"; REGION="${AWS_REGION:-${AWS_DEFAULT_REGION:-us-east-1}}"
else
  command -v aws >/dev/null || { echo "aws CLI not found" >&2; exit 1; }
  ACCOUNT="$(aws sts get-caller-identity --query Account --output text)"
  REGION="$(aws configure get region || true)"
  REGION="${AWS_REGION:-${AWS_DEFAULT_REGION:-${REGION:-}}}"
  [ -n "$REGION" ] || { echo "no AWS region configured (aws configure, or set AWS_REGION)" >&2; exit 1; }
fi
BUCKET="crimson-ledger-artifacts-${ACCOUNT}-${REGION}"
echo "AWS account ${ACCOUNT}, region ${REGION}, stack ${STACK}"

ensure_bucket() {
  if [ "$DRY_RUN" -eq 0 ] && aws s3api head-bucket --bucket "$BUCKET" 2>/dev/null; then return 0; fi
  if [ "$REGION" = "us-east-1" ]; then
    run aws s3api create-bucket --bucket "$BUCKET" --region "$REGION"
  else
    run aws s3api create-bucket --bucket "$BUCKET" --region "$REGION" \
      --create-bucket-configuration "LocationConstraint=${REGION}"
  fi
  run aws s3api put-public-access-block --bucket "$BUCKET" \
    --public-access-block-configuration BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true
  run aws s3api put-bucket-encryption --bucket "$BUCKET" \
    --server-side-encryption-configuration '{"Rules":[{"ApplyServerSideEncryptionByDefault":{"SSEAlgorithm":"AES256"}}]}'
  run aws s3api put-bucket-lifecycle-configuration --bucket "$BUCKET" \
    --lifecycle-configuration '{"Rules":[{"ID":"expire-30d","Status":"Enabled","Filter":{"Prefix":""},"Expiration":{"Days":30}}]}'
}

# ensure_pepper creates the key-hashing secret once. It is never rotated: a new pepper invalidates every key.
ensure_pepper() {
  if [ "$DRY_RUN" -eq 0 ] && aws ssm get-parameter --name "$PEPPER_PARAM" >/dev/null 2>&1; then
    echo "pepper parameter exists, keeping it"
    return 0
  fi
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "+ aws ssm put-parameter --name $PEPPER_PARAM --type SecureString --tier Standard --cli-input-json file://<private temp file>"
    return 0
  fi
  local tmp
  tmp="$(mktemp)"
  trap 'rm -f "$tmp"' RETURN
  chmod 600 "$tmp"
  jq -n --arg name "$PEPPER_PARAM" --arg value "$(head -c 32 /dev/urandom | base64)" \
    '{Name: $name, Value: $value, Type: "SecureString", Tier: "Standard"}' >"$tmp"
  aws ssm put-parameter --cli-input-json "file://${tmp}" >/dev/null
  echo "created pepper parameter ${PEPPER_PARAM}"
}

# create_key <scope> <label>: prints the token to the terminal only.
create_key() {
  local fn="$1" scope="$2" label="$3" out
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "+ aws lambda invoke --function-name <AdminFunctionName> (keys.create ${scope} ${label}); token printed once to this terminal"
    return 0
  fi
  out="$(mktemp)"
  chmod 600 "$out"
  aws lambda invoke --function-name "$fn" --cli-binary-format raw-in-base64-out \
    --payload "{\"cmd\":\"keys.create\",\"scope\":\"${scope}\",\"label\":\"${label}\"}" "$out" >/dev/null
  if [ "$(jq -r .status "$out")" != "200" ]; then
    echo "creating the ${scope} key failed: $(jq -r .error "$out")" >&2
    rm -f "$out"
    return 1
  fi
  echo "${scope} key (shown once, paste it into the app, not into a chat): $(jq -r .token "$out")"
  rm -f "$out"
}

# key_count prints how many keys the admin function already knows.
key_count() {
  local out
  out="$(mktemp)"
  aws lambda invoke --function-name "$1" --cli-binary-format raw-in-base64-out \
    --payload '{"cmd":"keys.list"}' "$out" >/dev/null
  jq -r '(.keys // []) | length' "$out"
  rm -f "$out"
}

# domain_overrides prints the CloudFormation parameter overrides for the custom domain, one per line. With no domain
# flag it prints nothing, so a re-deploy keeps whatever the stack already has.
domain_overrides() {
  if [ "$REMOVE_DOMAIN" -eq 1 ]; then
    printf '%s\n' "DomainName=" "CertificateArn=" "HostedZoneId="
    return 0
  fi
  [ -n "$DOMAIN" ] || return 0
  printf '%s\n' "DomainName=${DOMAIN}" "CertificateArn=${CERT_ARN}" "HostedZoneId=${ZONE_ID}"
}

check_domain_flags() {
  if [ "$REMOVE_DOMAIN" -eq 1 ] && [ -n "$DOMAIN$CERT_ARN$ZONE_ID" ]; then
    echo "--remove-domain cannot be combined with --domain, --certificate-arn or --hosted-zone-id" >&2; exit 2
  fi
  if [ -n "$DOMAIN" ] || [ -n "$CERT_ARN" ] || [ -n "$ZONE_ID" ]; then
    [ -n "$DOMAIN" ] && [ -n "$CERT_ARN" ] || { echo "--domain and --certificate-arn go together" >&2; exit 2; }
    case "$DOMAIN" in *[A-Z]*|*" "*|*/*|http*) echo "--domain must be a lower-case host name such as api.example.com (no https://, no path)" >&2; exit 2 ;; esac
    case "$CERT_ARN" in arn:aws*:acm:"${REGION}":*:certificate/*) ;; *) echo "--certificate-arn must be an ACM certificate in ${REGION}, the region of this stack" >&2; exit 2 ;; esac
  fi
}

deploy() {
  [ -n "$BUDGET_EMAIL" ] || { echo "--budget-email is required" >&2; exit 2; }
  check_domain_flags
  [ "$DRY_RUN" -eq 1 ] || command -v jq >/dev/null || { echo "jq not found" >&2; exit 1; }
  echo "This creates: a DynamoDB table, 2 Lambda functions, an API Gateway HTTP API in front of the public one, 2 roles, 2 log groups, a 5 USD budget, an artifacts bucket."
  confirm "$ACCOUNT"
  ensure_pepper
  ensure_bucket
  (cd "$ROOT" && run docker compose --profile build run --rm lambda-package)
  run aws cloudformation package --template-file "$ROOT/api/deploy/template.yaml" \
    --s3-bucket "$BUCKET" --output-template-file "$ROOT/api/build/packaged.yaml"
  local overrides=()
  while IFS= read -r line; do [ -n "$line" ] && overrides+=("$line"); done < <(domain_overrides)
  run aws cloudformation deploy --template-file "$ROOT/api/build/packaged.yaml" --stack-name "$STACK" \
    --capabilities CAPABILITY_IAM \
    --parameter-overrides "BudgetEmail=${BUDGET_EMAIL}" "PepperParameterName=${PEPPER_PARAM}" ${overrides[@]+"${overrides[@]}"}
  local url fn dns
  url="$(query aws cloudformation describe-stacks --stack-name "$STACK" --query "Stacks[0].Outputs[?OutputKey=='ApiUrl'].OutputValue" --output text)"
  dns="$(query aws cloudformation describe-stacks --stack-name "$STACK" --query "Stacks[0].Outputs[?OutputKey=='DnsTarget'].OutputValue" --output text)"
  fn="$(query aws cloudformation describe-stacks --stack-name "$STACK" --query "Stacks[0].Outputs[?OutputKey=='AdminFunctionName'].OutputValue" --output text)"
  if [ "$DRY_RUN" -eq 0 ] && [ "$(key_count "$fn")" != "0" ]; then
    echo "keys already exist; not creating new ones (use the admin function to add or rotate)"
  else
    create_key "$fn" ingest "scout"
    create_key "$fn" view "viewer"
  fi
  echo
  echo "Deployed. server_url for the scout and the viewer: ${url}"
  if [ -n "$DOMAIN" ] && [ -z "$ZONE_ID" ]; then
    echo "DNS step left for you: create a CNAME record  ${DOMAIN}  ->  ${dns}  at your DNS provider."
    echo "Until it resolves the API is unreachable: with a custom name the default execute-api address is switched off (see api/deploy/README.md)."
  fi
  echo "Manage keys later: aws lambda invoke --function-name ${fn} --cli-binary-format raw-in-base64-out --payload '{\"cmd\":\"keys.list\"}' keys.json"
}

destroy() {
  echo "This DELETES the stack ${STACK} (table and all data), the bucket ${BUCKET} and the pepper parameter ${PEPPER_PARAM}."
  echo "Every API key stops working for good."
  confirm "$STACK"
  run aws cloudformation delete-stack --stack-name "$STACK"
  run aws cloudformation wait stack-delete-complete --stack-name "$STACK"
  run aws s3 rm "s3://${BUCKET}" --recursive
  run aws s3api delete-bucket --bucket "$BUCKET"
  run aws ssm delete-parameter --name "$PEPPER_PARAM"
  echo "Removed. Check: aws cloudformation list-stacks, aws dynamodb list-tables."
}

if [ "$DESTROY" -eq 1 ]; then destroy; else deploy; fi
