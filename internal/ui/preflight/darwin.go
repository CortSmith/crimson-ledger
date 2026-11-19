//go:build darwin

package preflight

import (
	"os/exec"
	"strings"
)

// Check returns the missing requirements for app (empty when the UI can start).
func Check(app string) []Missing {
	_ = app
	out, err := exec.Command("/usr/bin/sw_vers", "-productVersion").Output()
	if err == nil && macVersionOK(string(out)) {
		return nil
	}
	return []Missing{{Name: "macOS 11 or newer", Why: "web view engine (WKWebView)", Install: map[string]string{
		"macos": "update macOS in System Settings > General > Software Update"}}}
}

// Family is unused on macOS.
func Family() string { return "macos" }

// Dialog shows the message in a native dialog via osascript.
func Dialog(app string, m []Missing) {
	var names []string
	for _, x := range m {
		names = append(names, x.Name)
	}
	msg := strings.ReplaceAll(app+" cannot start: needs "+strings.Join(names, ", "), `"`, `'`)
	_ = exec.Command("/usr/bin/osascript", "-e", `display dialog "`+msg+`" buttons {"OK"}`).Run() // #nosec G204 -- quotes stripped
}
