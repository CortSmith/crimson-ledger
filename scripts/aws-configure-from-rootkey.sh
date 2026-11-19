#!/usr/bin/env bash
# Configure the AWS CLI from an AWS rootkey.csv download.
# Usage: aws-configure-from-rootkey.sh [path/to/rootkey.csv]
set -euo pipefail

CSV="${1:-$HOME/Documents/rootkey.csv}"
REGION="us-east-1"
OUTPUT="json"

command -v aws >/dev/null || { echo "aws CLI not found in PATH" >&2; exit 1; }
[[ -r "$CSV" ]] || { echo "Cannot read $CSV" >&2; exit 1; }

# Strip CR (Windows line endings), UTF-8 BOM, and double quotes.
clean() { tr -d '\r"' | sed '1s/^\xEF\xBB\xBF//'; }

AWS_ACCESS_KEY_ID=""
AWS_SECRET_ACCESS_KEY=""

if grep -q '^AWSAccessKeyId=' "$CSV"; then
  # Legacy root key format:
  #   AWSAccessKeyId=AKIA...
  #   AWSSecretKey=...
  AWS_ACCESS_KEY_ID=$(clean < "$CSV" | sed -n 's/^AWSAccessKeyId=//p' | head -n1)
  AWS_SECRET_ACCESS_KEY=$(clean < "$CSV" | sed -n 's/^AWSSecretKey=//p' | head -n1)
else
  # Console CSV format: header row, then "Access key ID,Secret access key"
  IFS=',' read -r AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY < <(clean < "$CSV" | sed -n '2p')
fi

if [[ -z "$AWS_ACCESS_KEY_ID" || -z "$AWS_SECRET_ACCESS_KEY" ]]; then
  echo "Could not parse access key and secret from $CSV" >&2
  exit 1
fi

# Feed the four `aws configure` prompts via stdin so the secret never
# appears in the process list (unlike `aws configure set ... <secret>`).
printf '%s\n%s\n%s\n%s\n' \
  "$AWS_ACCESS_KEY_ID" \
  "$AWS_SECRET_ACCESS_KEY" \
  "$REGION" \
  "$OUTPUT" | aws configure

unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY
echo "AWS CLI configured (region=$REGION, output=$OUTPUT)."
