# T00: Scaffold, module, tooling, AUDIT gate

**Depends on:** none · **Blocks:** everything · **PLAN.md sections to read:** [3 layout](../PLAN.md#3-repository-layout-all-under-app), [7 security baseline](../PLAN.md#7-security-baseline-applies-to-every-task-the-audit-phase-checks-it), [10 workflow](../PLAN.md#10-mandatory-workflow-for-every-task-tdd-with-proof-of-concept-first)

## Goal
Create `App/` with the Go module, empty package skeletons with package comments, the one-command AUDIT gate
(`scripts/check.sh`), and an architecture test that keeps apps independent. Nothing else.

## Outputs
```
App/go.mod                      module crimsonledger / go 1.25
App/Makefile                    targets: test lint check build-linux build-windows build-macos
App/scripts/check.sh            AUDIT gate used by every task
App/.golangci.yml
App/.gitignore                  dist/ cover.out *.exe scripts/poc/**/go.sum? (no: poc keeps its own go.mod, ignore only binaries)
App/internal/archtest/arch_test.go
App/internal/<pkg>/doc.go       for config secret domain store apiserver apiclient ui logparse (package comment only)
App/{api,database,logger,viewer}/doc.go
```

## Build tag convention (decided here, used by T12-T14)
Real WebView code lives behind build tag **`webview`** (`//go:build webview`). Without the tag the UI binaries
compile to a stub that prints `built without -tags webview` and exits 70. Why: `go vet`, `go test` and the AUDIT
gate then work on any machine with no GTK/WebKit headers; only release builds (`-tags webview`) need them.

## Step 0: PoC (run before writing anything real)
Directory `scripts/poc/t00/`.

1. **Tools install and run.** Prove each can be installed pinned and executed offline-after-install:
   ```sh
   go install honnef.co/go/tools/cmd/staticcheck@2025.1.1
   go install golang.org/x/vuln/cmd/govulncheck@latest
   go install github.com/securego/gosec/v2/cmd/gosec@latest
   go install github.com/golangci/golangci-lint/cmd/golangci-lint@v2.1.6   # confirm the newest v2 tag, record it
   staticcheck -version && govulncheck -version && gosec -version && golangci-lint version
   ```
   PASS = four version lines. Record the exact versions; pin them in `scripts/tools.env`.
2. **Arch test mechanism.** Prove `go list -deps` can list a package's transitive imports and that a forbidden import
   is detected:
   ```go
   // scripts/poc/t00/arch_poc_test.go
   func TestPoC_DepsDetectForbidden(t *testing.T) {
       out, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", "net/http").Output()
       if err != nil { t.Fatal(err) }
       if !strings.Contains(string(out), "crypto/tls") { t.Fatal("expected crypto/tls in net/http deps") }
   }
   ```
   PASS = test passes.
3. **Windows and macOS compile check without those machines.**
   `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go vet ./...` and `GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go vet ./...`
   on a package with `//go:build windows` code. PASS = exit 0 (this is how Windows/mac-only files are checked on Linux).

Log: paste outputs under **PoC log** at the bottom.

## Step 1: RED
Write `internal/archtest/arch_test.go` first:

```go
// Package archtest enforces the import rules from PLAN.md section 3.
package archtest

import (
	"os/exec"
	"strings"
	"testing"
)

// rule: packages matching "from" must not depend on any package in "forbidden".
type rule struct {
	from      string
	forbidden []string
	why       string
}

var rules = []rule{
	{"crimsonledger/api/...", []string{"crimsonledger/internal/ui", "crimsonledger/internal/logparse", "crimsonledger/viewer", "crimsonledger/logger"}, "api must stay headless"},
	{"crimsonledger/internal/domain", []string{"crimsonledger/internal/store", "crimsonledger/internal/apiserver", "crimsonledger/internal/ui"}, "domain is the bottom layer"},
	{"crimsonledger/viewer/...", []string{"crimsonledger/logger", "crimsonledger/api", "crimsonledger/internal/store", "crimsonledger/internal/logparse"}, "viewer talks to the API only"},
	{"crimsonledger/logger/...", []string{"crimsonledger/viewer", "crimsonledger/api", "crimsonledger/internal/store"}, "logger talks to the API only"},
	{"crimsonledger/internal/ui", []string{"crimsonledger/internal/store", "crimsonledger/internal/apiserver"}, "ui is presentation only"},
}

func TestImportRules(t *testing.T) {
	for _, r := range rules {
		deps := listDeps(t, r.from)
		for _, bad := range r.forbidden {
			for _, d := range deps {
				if d == bad || strings.HasPrefix(d, bad+"/") {
					t.Errorf("%s imports %s: %s", r.from, d, r.why)
				}
			}
		}
	}
}

func listDeps(t *testing.T, pattern string) []string {
	t.Helper()
	out, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", pattern).CombinedOutput()
	if err != nil {
		t.Skipf("pattern %s has no packages yet: %s", pattern, out) // skipped, not passed, until the package exists
	}
	return strings.Fields(string(out))
}
```
Run `go test ./internal/archtest/...`. RED log = it fails/skips because nothing exists yet. Then create the skeletons.

## Step 2: GREEN
`go.mod`:
```
module crimsonledger

go 1.25
```
`scripts/check.sh` (the gate; every task's AUDIT phase calls it with the package path):

```bash
#!/usr/bin/env bash
# AUDIT gate. Usage: scripts/check.sh [package pattern]   (default ./...)
set -euo pipefail
cd "$(dirname "$0")/.."
PKG="${1:-./...}"
MIN_COVER="${MIN_COVER:-80}"
fail() { echo "CHECK FAILED: $*" >&2; exit 1; }
step() { echo; echo "== $*"; }

step gofmt
bad="$(gofmt -l . | grep -v '^scripts/poc/' || true)"
[ -z "$bad" ] || fail "gofmt needed: $bad"

step "go vet (host + windows + darwin)"
go vet "$PKG"
GOOS=windows CGO_ENABLED=0 go vet "$PKG"
GOOS=darwin  CGO_ENABLED=0 go vet "$PKG"

step staticcheck;   staticcheck "$PKG"
step golangci-lint; golangci-lint run "$PKG"          # includes gosec, errcheck, gocyclo, revive
step govulncheck;   govulncheck "$PKG"

step "tests -race + coverage >= ${MIN_COVER}%"
go test -race -count=1 -coverprofile=cover.out "$PKG"
pct="$(go tool cover -func=cover.out | awk '/^total:/ {gsub("%","",$3); print $3}')"
awk -v p="$pct" -v m="$MIN_COVER" 'BEGIN{exit !(p+0 >= m+0)}' || fail "coverage $pct% < $MIN_COVER%"

step "secret and TLS greps"
! grep -rn --include='*.go' 'InsecureSkipVerify' . | grep -v '_test.go' | grep -v 'scripts/poc/' \
  || fail "InsecureSkipVerify is forbidden"
! grep -rniE --include='*.go' '(log|fmt)\.(Printf|Println|Sprintf).*(apikey|api_key|secret|bearer)' . \
  | grep -v '_test.go' | grep -v 'scripts/poc/' || fail "possible secret in a log/format call; use secret.Redact"
! grep -rn --include='*.go' 'ListenAndServe(' . | grep -v '_test.go' | grep -v 'scripts/poc/' \
  | grep -v 'api/cmd/crimson-api/' || fail "only crimson-api may listen; viewer/logger must not host a site"

step "no local website in UI apps"
! grep -rnE --include='*.go' 'net/http/httptest|http\.ListenAndServe|net\.Listen' viewer logger internal/ui 2>/dev/null \
  | grep -v '_test.go' || fail "viewer/logger/ui must not open a listening socket"
echo; echo "CHECK OK ($PKG, coverage ${pct}%)"
```
`.golangci.yml` enables: `gosec errcheck govet staticcheck revive gocyclo(min 12) gocognit(min 15) funlen(lines 40) nestif misspell unparam ineffassign errorlint bodyclose noctx`.
Makefile:
```make
check:        ; scripts/check.sh ./...
test:         ; go test -race ./...
build-linux:  ; CGO_ENABLED=1 go build -tags webview ./viewer/... ./logger/...   # run inside Dockerfile.build
build-windows:; GOOS=windows CGO_ENABLED=0 go build -tags webview ./viewer/... ./logger/...
build-macos:  ; GOOS=darwin CGO_ENABLED=1 go build -tags webview ./viewer/... ./logger/...   # run on a Mac only
```
`doc.go` per package, e.g. `// Package config resolves and reads ~/.crimson-ledger. See docs/tasks/T01.` + `package config`.

## Step 3: REFACTOR checklist
- [x] `check.sh` has no duplicated flags; every step prints a header; failure message says what to do.
- [x] Rules table in `arch_test.go` is the only place import rules live; each has a `why`.
- [x] `scripts/tools.env` pins tool versions; `check.sh` fails with install instructions when a tool is missing.

## Step 4: AUDIT
- `scripts/check.sh ./...` exits 0 (arch test may skip rules for packages that do not exist yet; it must not fail).
- Prove the gate can fail: add a temporary `import "crimsonledger/internal/ui"` to a file under `api/`, run
  `go test ./internal/archtest/...`, see FAIL, remove it. Paste output.
- Prove the grep guards fire: temporarily add `InsecureSkipVerify` in a non-test file; `check.sh` must fail.

## Done when
- [x] `App/` tree exists exactly as listed, `go build ./...` and `go test ./...` pass on an empty codebase.
- [x] `scripts/check.sh` works and is proven able to fail (two negative demos logged).
- [x] Tool versions pinned in `scripts/tools.env`.

## PoC log
Run 2026-11-17, Linux, host Go 1.24.2 (auto-switches to 1.25.0 because go.mod says `go 1.25`).
- PoC 1 tools: `staticcheck 2025.1.1`, `golangci-lint v2.1.6`, `govulncheck v1.10.0`, `gosec` all install and print versions. PASS.
  **Finding:** tools built with go1.24.2 refuse a `go 1.25` module ("module requires at least go1.25, but Staticcheck was built with go1.24.2").
  Fix: install them with `GOTOOLCHAIN=go1.25.0`. `govulncheck@latest` and `gosec@latest` now need Go >= 1.26, so the
  earlier go1.24-built binaries are kept for those two (govulncheck still scans the 1.25 module fine); gosec also runs inside golangci-lint.
  The import path for golangci-lint v2 is `github.com/golangci/golangci-lint/v2/cmd/golangci-lint` (the plan's path lacks `/v2`).
- PoC 2 `go list -deps` finds `crypto/tls` under `net/http`. PASS.
- PoC 3 `GOOS=windows|darwin CGO_ENABLED=0 go vet` on a build-tagged package exits 0. PASS.
- PoC deleted after the real arch test covered the same mechanism.

## RED log
`go test ./internal/archtest/...` before any package existed: every rule SKIPped (no packages). Plan bug found and fixed:
`t.Skipf` in the plan's single loop skipped *all* later rules after the first missing package; rules are now subtests.

## AUDIT log
- `scripts/check.sh ./...` -> `CHECK OK (./..., coverage 100%)` (no statements yet; the gate treats that as 100).
- Negative demo 1: `api/cmd/demo` importing `internal/ui` -> `arch_test.go:32: crimsonledger/api/... imports crimsonledger/internal/ui: api must stay headless`, FAIL.
- Negative demo 2: a non-test file with `InsecureSkipVerify` -> `check.sh` failed (gosec G402 inside golangci-lint fired before the grep guard; the grep guard is the second line of defence).

## Follow-ups
- `check.sh` needs `GOTOOLCHAIN=go1.25.0`-built linters until the host Go is >= 1.25; T16 (CI) should pin Go 1.25 for the tools.
