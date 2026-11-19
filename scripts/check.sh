#!/usr/bin/env bash
# AUDIT gate. Usage: scripts/check.sh [package pattern]   (default ./...)
set -euo pipefail
cd "$(dirname "$0")/.."
export PATH="$PATH:$(go env GOPATH)/bin"
# shellcheck source=tools.env
source scripts/tools.env
PKG="${1:-./...}"
MIN_COVER="${MIN_COVER:-70}"
fail() { echo "CHECK FAILED: $*" >&2; exit 1; }
step() { echo; echo "== $*"; }

need() { # need <binary> <go install path>
  command -v "$1" >/dev/null || fail "$1 is missing. Install: go install $2"
}
need staticcheck "honnef.co/go/tools/cmd/staticcheck@${STATICCHECK_VERSION}"
need golangci-lint "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCI_LINT_VERSION}"
need govulncheck "golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION}"

# Non-test, non-PoC Go files: the scope of the grep guards below.
prod_files() { grep -rl --include='*.go' "$1" . 2>/dev/null | grep -v -e '_test\.go' -e '^\./scripts/poc/' || true; }

step gofmt
bad="$(gofmt -l . | grep -v '^scripts/poc/' || true)"
[ -z "$bad" ] || fail "gofmt needed (run gofmt -w): $bad"

step "go vet (host + windows + darwin)"
go vet "$PKG"
GOOS=windows CGO_ENABLED=0 go vet "$PKG"
GOOS=darwin CGO_ENABLED=0 go vet "$PKG"

step staticcheck;   staticcheck "$PKG"
step golangci-lint; golangci-lint run "$PKG"   # includes gosec, errcheck, gocyclo, revive
step govulncheck;   govulncheck "$PKG"

step "tests -race + coverage >= ${MIN_COVER}%"
go test -race -count=1 -coverprofile=cover.out "$PKG"
if [ "$(wc -l < cover.out)" -le 1 ]; then
  pct=100 # cover.out holds only its mode line: no statements to cover yet
else
  pct="$(go tool cover -func=cover.out | awk '/^total:/ {gsub("%","",$3); print $3}')"
fi
awk -v p="$pct" -v m="$MIN_COVER" 'BEGIN{exit !(p+0 >= m+0)}' || fail "coverage $pct% < $MIN_COVER%"

step "secret and TLS greps"
hits="$(prod_files InsecureSkipVerify)"
[ -z "$hits" ] || fail "InsecureSkipVerify is forbidden: $hits"
hits="$(grep -rniE --include='*.go' '(log|fmt)\.(Printf|Println|Sprintf).*(apikey|api_key|secret|bearer)' . \
  | grep -v -e '_test\.go' -e 'scripts/poc/' || true)"
[ -z "$hits" ] || fail "possible secret in a log/format call; use secret.Redact: $hits"
hits="$(grep -rn --include='*.go' 'ListenAndServe(' . \
  | grep -v -e '_test\.go' -e 'scripts/poc/' -e 'api/cmd/crimson-api/' || true)"
[ -z "$hits" ] || fail "only crimson-api may listen; viewer/logger must not host a site: $hits"

step "no local website in UI apps"
hits="$(grep -rnE --include='*.go' 'net/http/httptest|http\.ListenAndServe|net\.Listen' viewer logger internal/ui 2>/dev/null \
  | grep -v '_test\.go' || true)"
[ -z "$hits" ] || fail "viewer/logger/ui must not open a listening socket: $hits"

echo; echo "CHECK OK ($PKG, coverage ${pct}%)"
