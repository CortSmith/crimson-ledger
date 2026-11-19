#!/usr/bin/env bash
# Fails when the SAM template contains a resource or setting that costs money by default (T10; T21 extends this).
set -euo pipefail
cd "$(dirname "$0")/.."
T="${1:-api/deploy/template.yaml}"
fail() { echo "COST-LINT FAILED: $*" >&2; exit 1; }

allowed='AWS::DynamoDB::Table|AWS::Serverless::Function|AWS::Logs::LogGroup|AWS::IAM::Role|AWS::Budgets::Budget|AWS::ApiGatewayV2::Api|AWS::ApiGatewayV2::Integration|AWS::ApiGatewayV2::Route|AWS::ApiGatewayV2::Stage|AWS::ApiGatewayV2::DomainName|AWS::ApiGatewayV2::ApiMapping|AWS::Lambda::Permission|AWS::Route53::RecordSet'
types="$(grep -E '^\s+Type: AWS::' "$T" | sed -E 's/.*Type: *//' | sort -u)"
while IFS= read -r ty; do
  [ -z "$ty" ] && continue
  echo "$ty" | grep -Eq "^(${allowed})$" || fail "resource type $ty is not on the allow-list (NAT, VPC endpoints, KMS keys, REST API Gateway, WAF, load balancers, ... cost money)"
done <<<"$types"

for forbidden in 'VpcConfig' 'AWS::KMS' 'KmsKeyId' 'AWS::ApiGateway::' 'AWS::CloudFront' 'AWS::ElasticLoadBalancing' 'AWS::ApiGatewayV2::VpcLink' 'AWS::EC2' 'AWS::WAF' 'AWS::SecretsManager' \
                 'StreamSpecification' 'PointInTimeRecoveryEnabled: true' 'Tracing: Active' 'ProvisionedConcurrencyConfig' 'x86_64'; do
  if grep -q "$forbidden" "$T"; then fail "'$forbidden' found in $T"; fi
done
grep -q 'ReservedConcurrentExecutions' "$T" || fail "ReservedConcurrentExecutions missing (cost and abuse ceiling)"
grep -Eq 'RetentionInDays: (1|3|5|7|14)$' "$T" || fail "log groups must set RetentionInDays <= 14"
echo "COST-LINT OK ($T)"
