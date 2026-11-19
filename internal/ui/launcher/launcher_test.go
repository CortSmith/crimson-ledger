package launcher

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"

	"crimsonledger/internal/ui/preflight"
)

type spy struct {
	execCalled bool
	execPath   string
	execArgs   []string
	execCode   int
	dialogs    int
	missing    []preflight.Missing
	stderr     bytes.Buffer
}

func (s *spy) deps() deps {
	return deps{
		check:   func(string) []preflight.Missing { return s.missing },
		family:  func() string { return "debian" },
		dialog:  func(string, []preflight.Missing) { s.dialogs++ },
		exec:    func(p string, a []string) int { s.execCalled, s.execPath, s.execArgs = true, p, a; return s.execCode },
		stderr:  &s.stderr,
		resolve: func(ui string) (string, error) { return "/opt/app/lib/" + ui, nil },
	}
}

func TestRun_MissingLibs_Exit78_DoesNotExecUI(t *testing.T) {
	s := &spy{missing: []preflight.Missing{{Name: "libgtk-3.so.0", Why: "window toolkit"}}}
	if code := run("Crimson Ledger", "x-ui", nil, s.deps()); code != ExitMissingLibs {
		t.Fatalf("code %d", code)
	}
	if s.execCalled || s.dialogs != 1 || !strings.Contains(s.stderr.String(), "libgtk-3.so.0") {
		t.Fatalf("exec=%v dialogs=%d stderr=%q", s.execCalled, s.dialogs, s.stderr.String())
	}
}

func TestRun_OK_ExecsUIWithArgs(t *testing.T) {
	s := &spy{}
	run("A", "x-ui", []string{"--a", "b"}, s.deps())
	if !s.execCalled || s.execPath != "/opt/app/lib/x-ui" || len(s.execArgs) != 2 {
		t.Fatalf("%+v", s)
	}
}

func TestRun_UIBinaryMissing_Message(t *testing.T) {
	s := &spy{}
	d := s.deps()
	d.resolve = func(string) (string, error) { return "", errNoUI }
	if code := run("A", "x-ui", nil, d); code != ExitStartFailed || !strings.Contains(s.stderr.String(), "reinstall") {
		t.Fatalf("code %d stderr %q", code, s.stderr.String())
	}
}

func TestRun_PassesThroughExitCode(t *testing.T) {
	s := &spy{execCode: 42}
	if code := run("A", "x-ui", nil, s.deps()); code != 42 {
		t.Fatalf("code %d", code)
	}
}

func TestResolve_RefusesOutsideDir(t *testing.T) {
	if _, err := resolveIn("/opt/app", "../evil"); err == nil {
		t.Fatal("expected refusal")
	}
	if _, err := resolveIn("/opt/app", "/etc/passwd"); err == nil {
		t.Fatal("expected refusal")
	}
}

func TestLauncher_NoNetworkImports(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range strings.Fields(string(out)) {
		if p == "net/http" {
			t.Fatal("launcher must not import net/http")
		}
	}
}
