//go:build windows

package preflight

import (
	"os/exec"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const webView2GUID = `{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`

func registryVersion() string {
	paths := []struct {
		root registry.Key
		path string
	}{
		{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\` + webView2GUID},
		{registry.CURRENT_USER, `Software\Microsoft\EdgeUpdate\Clients\` + webView2GUID},
	}
	for _, p := range paths {
		k, err := registry.OpenKey(p.root, p.path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		v, _, err := k.GetStringValue("pv")
		_ = k.Close()
		if err == nil && webView2VersionOK(v) {
			return v
		}
	}
	return ""
}

// Check returns the missing requirements for app (empty when the UI can start).
func Check(app string) []Missing {
	_ = app
	if registryVersion() != "" {
		return nil
	}
	return []Missing{{Name: "Microsoft Edge WebView2 Runtime", Why: "web view engine", Install: map[string]string{
		"windows": "download the Evergreen Standalone installer from https://developer.microsoft.com/microsoft-edge/webview2/"}}}
}

// Family is unused on Windows.
func Family() string { return "windows" }

// Dialog shows the message in a message box (via PowerShell, so no cgo or user32 binding is needed).
func Dialog(app string, m []Missing) {
	var names []string
	for _, x := range m {
		names = append(names, x.Name)
	}
	msg := app + " cannot start: missing " + strings.Join(names, ", ") + ". Install the WebView2 Runtime and start again."
	script := "Add-Type -AssemblyName PresentationFramework; [System.Windows.MessageBox]::Show($args[0], $args[1]) | Out-Null"
	_ = exec.Command("powershell", "-NoProfile", "-Command", script, msg, app).Run() // #nosec G204 -- fixed script, text passed as arguments
}
