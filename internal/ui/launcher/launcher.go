// Package launcher starts a UI binary only when the preflight check passes, so a missing web view engine
// produces a readable message instead of a raw loader error.
package launcher

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"crimsonledger/internal/ui/preflight"
)

// Exit codes of the launcher.
const (
	ExitOK                  = 0
	ExitMissingLibs         = 78
	ExitBuiltWithoutWebview = 70
	ExitStartFailed         = 1
)

var errNoUI = errors.New("the UI program is missing")

type deps struct {
	check   func(string) []preflight.Missing
	family  func() string
	dialog  func(string, []preflight.Missing)
	exec    func(path string, args []string) int
	resolve func(ui string) (string, error)
	stderr  io.Writer
}

// Run checks the system, then runs <dir of this executable>/lib/<uiBinary> with args and returns its exit code.
func Run(app, uiBinary string, args []string) int {
	return run(app, uiBinary, args, deps{
		check: preflight.Check, family: preflight.Family, dialog: preflight.Dialog,
		exec: execUI, resolve: resolveNextToSelf, stderr: os.Stderr,
	})
}

func run(app, ui string, args []string, d deps) int {
	if missing := d.check(app); len(missing) > 0 {
		preflight.Report(d.stderr, app, missing, d.family())
		d.dialog(app, missing)
		return ExitMissingLibs
	}
	path, err := d.resolve(ui)
	if err != nil {
		_, _ = fmt.Fprintf(d.stderr, "%s cannot start: %v. Please reinstall %s.\n", app, err, app)
		return ExitStartFailed
	}
	return d.exec(path, args)
}

func resolveNextToSelf(ui string) (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	if self, err = filepath.EvalSymlinks(self); err != nil {
		return "", err
	}
	return resolveIn(filepath.Dir(self), ui)
}

// uiFile is the file name of a UI binary on goos: Windows executables carry the .exe suffix.
func uiFile(name, goos string) string {
	if goos == "windows" && !strings.HasSuffix(name, ".exe") {
		return name + ".exe"
	}
	return name
}

// resolveIn returns <dir>/lib/<name> and refuses any name that escapes that directory or does not exist.
func resolveIn(dir, name string) (string, error) {
	libDir := filepath.Join(dir, "lib")
	p := filepath.Join(libDir, uiFile(name, runtime.GOOS))
	if filepath.IsAbs(name) || !strings.HasPrefix(p, libDir+string(filepath.Separator)) {
		return "", errors.New("the UI program path is outside the install folder")
	}
	return p, nil
}

func execUI(path string, args []string) int {
	if _, err := os.Stat(path); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "the UI program is missing (%s). Please reinstall.\n", filepath.Base(path))
		return ExitStartFailed
	}
	cmd := exec.Command(path, args...) // #nosec G204 -- path pinned to <install dir>/lib by resolveIn
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		_, _ = fmt.Fprintf(os.Stderr, "could not start the UI program: %v\n", err)
		return ExitStartFailed
	}
	return ExitOK
}
