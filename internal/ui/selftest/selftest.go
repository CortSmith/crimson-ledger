// Package selftest implements the --selftest flag of both UI binaries: load the page in the real window, call
// loadSettings through it, print "SELFTEST OK" and exit 0. It proves the system web view and the bindings work
// (used by the packaging smoke tests). It opens no network connection: the page's CSP forbids it and loadSettings
// only reads the local config.
package selftest

import (
	"fmt"
	"io"
	"strings"
	"time"

	"crimsonledger/internal/ui/window"
)

// DoneBinding is the page-callable function the injected script reports through.
const DoneBinding = "selftestDone"

// Wanted reports whether args ask for a self-test run.
func Wanted(args []string) bool {
	for _, a := range args {
		if a == "--selftest" {
			return true
		}
	}
	return false
}

// Inject appends a script (carrying the page's CSP nonce) that waits for the page load, calls loadSettings and
// reports the outcome through DoneBinding.
func Inject(html, nonce string) string {
	js := `window.addEventListener('load',function(){` +
		`window.loadSettings().then(function(){window.` + DoneBinding + `(true)},` +
		`function(){window.` + DoneBinding + `(false)})});`
	script := `<script nonce="` + nonce + `">` + js + `</script>`
	if i := strings.LastIndex(html, "</body>"); i >= 0 {
		return html[:i] + script + html[i:]
	}
	return html + script
}

// Bind registers DoneBinding on w. When the page reports, it prints the verdict to out, closes the window and
// calls exit with the process exit code (0 on success, 1 on failure).
func Bind(w window.Window, out io.Writer, exit func(code int)) error {
	return w.Bind(DoneBinding, func(ok bool) (bool, error) {
		code := 0
		if ok {
			_, _ = fmt.Fprintln(out, "SELFTEST OK")
		} else {
			_, _ = fmt.Fprintln(out, "SELFTEST FAILED: loadSettings did not answer")
			code = 1
		}
		w.Terminate()
		exit(code)
		return ok, nil
	})
}

// Timeout is how long the page gets to report before the self-test fails.
const Timeout = 20 * time.Second

// Watchdog fails the run if the page never reports (blank window, script blocked).
func Watchdog(out io.Writer, exit func(code int)) {
	time.AfterFunc(Timeout, func() {
		_, _ = fmt.Fprintln(out, "SELFTEST FAILED: the page did not report within", Timeout)
		exit(1)
	})
}
