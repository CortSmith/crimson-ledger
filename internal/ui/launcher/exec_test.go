package launcher

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain lets the test binary double as a child process that exits with the code in CHILD_EXIT.
func TestMain(m *testing.M) {
	if c := os.Getenv("CHILD_EXIT"); c != "" {
		code := 0
		for _, d := range c {
			code = code*10 + int(d-'0')
		}
		os.Exit(code)
	}
	os.Exit(m.Run())
}

func TestExecUI_ExitCodes(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CHILD_EXIT", "0")
	if got := execUI(self, nil); got != ExitOK {
		t.Fatalf("got %d", got)
	}
	t.Setenv("CHILD_EXIT", "42")
	if got := execUI(self, nil); got != 42 {
		t.Fatalf("got %d", got)
	}
}

func TestExecUI_Missing(t *testing.T) {
	if got := execUI(filepath.Join(t.TempDir(), "nope"), nil); got != ExitStartFailed {
		t.Fatalf("got %d", got)
	}
}

func TestExecUI_NotExecutable(t *testing.T) {
	p := filepath.Join(t.TempDir(), "plain")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := execUI(p, nil); got != ExitStartFailed {
		t.Fatalf("got %d", got)
	}
}

func TestResolveIn_OK(t *testing.T) {
	got, err := resolveIn("/opt/app", "scout-ui")
	if err != nil || got != filepath.Join("/opt/app", "lib", "scout-ui") {
		t.Fatalf("%q %v", got, err)
	}
}

func TestResolveNextToSelf(t *testing.T) {
	if p, err := resolveNextToSelf("x-ui"); err != nil || filepath.Base(p) != "x-ui" {
		t.Fatalf("%q %v", p, err)
	}
}

func TestRun_Public_MissingUI(t *testing.T) {
	// The test binary has no lib/ sibling; preflight passes only where WebKit exists, otherwise 78. Either is a refusal.
	if code := Run("A", "x-ui", nil); code == ExitOK {
		t.Fatalf("code %d", code)
	}
}
