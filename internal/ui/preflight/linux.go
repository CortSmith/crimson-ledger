//go:build linux

package preflight

import (
	"os"

	"github.com/ebitengine/purego"
)

var libs = []lib{
	{Name: "libwebkit2gtk-4.1.so.0", Alt: []string{"libwebkit2gtk-4.0.so.37"}, Why: "web view engine", Install: map[string]string{
		"debian": "sudo apt install libwebkit2gtk-4.1-0", "fedora": "sudo dnf install webkit2gtk4.1", "arch": "sudo pacman -S webkit2gtk-4.1",
		"suse": "sudo zypper install libwebkit2gtk-4_1-0"}},
	{Name: "libgtk-3.so.0", Why: "window toolkit", Install: map[string]string{
		"debian": "sudo apt install libgtk-3-0", "fedora": "sudo dnf install gtk3", "arch": "sudo pacman -S gtk3",
		"suse": "sudo zypper install libgtk-3-0"}},
}

func dlopenOK(name string) bool {
	h, err := purego.Dlopen(name, purego.RTLD_LAZY|purego.RTLD_LOCAL)
	if err != nil {
		return false
	}
	_ = purego.Dlclose(h)
	return true
}

// Check returns the missing requirements for app (empty when the UI can start).
func Check(app string) []Missing { _ = app; return checkLibs(libs, dlopenOK) }

// Family returns the distro family for ordering install hints.
func Family() string {
	b, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return ""
	}
	return detectFamily(string(b))
}

// Dialog is a no-op on Linux: the message goes to stderr, where the launching terminal shows it.
func Dialog(app string, m []Missing) { _, _ = app, m }
