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

// TestTestkitIsImportedOnlyFromTests: shipping code must never depend on the test harnesses (T17).
func TestTestkitIsImportedOnlyFromTests(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", "crimsonledger/api/...", "crimsonledger/viewer/...", "crimsonledger/logger/...").CombinedOutput()
	if err != nil {
		t.Skipf("no shipping packages to inspect: %s", out)
	}
	for _, d := range strings.Fields(string(out)) {
		if strings.HasPrefix(d, "crimsonledger/internal/testkit") {
			t.Errorf("shipping binary depends on %s", d)
		}
	}
}

func TestImportRules(t *testing.T) {
	for _, r := range rules {
		t.Run(r.from, func(t *testing.T) { // subtest: a skip for one rule must not hide the others
			deps := listDeps(t, r.from)
			for _, bad := range r.forbidden {
				for _, d := range deps {
					if d == bad || strings.HasPrefix(d, bad+"/") {
						t.Errorf("%s imports %s: %s", r.from, d, r.why)
					}
				}
			}
		})
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
