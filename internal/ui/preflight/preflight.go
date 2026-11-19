// Package preflight detects missing system libraries before a UI binary starts and explains how to install them.
// It is pure Go (CGO off) so it runs even where the web view engine is absent.
package preflight

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Missing is one absent requirement. Install maps a distro family (debian, fedora, arch, suse) or an OS name to a command.
type Missing struct {
	Name    string
	Why     string
	Install map[string]string
}

type lib struct {
	Name    string
	Alt     []string
	Why     string
	Install map[string]string
}

// hintOrder is the display order and label of every install-hint family.
var hintOrder = []hint{
	{"debian", "Debian/Ubuntu:"},
	{"fedora", "Fedora:       "},
	{"arch", "Arch:         "},
	{"suse", "openSUSE:     "},
	{"windows", "Windows:      "},
	{"macos", "macOS:        "},
}

func checkLibs(libs []lib, open func(string) bool) []Missing {
	var out []Missing
	for _, l := range libs {
		ok := open(l.Name)
		for _, a := range l.Alt {
			ok = ok || open(a)
		}
		if !ok {
			out = append(out, Missing{Name: l.Name, Why: l.Why, Install: l.Install})
		}
	}
	return out
}

// Report writes the exact user-facing message. family (from Family) is listed first. Nothing is written when m is empty.
func Report(w io.Writer, app string, m []Missing, family string) {
	if len(m) == 0 {
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s cannot start: missing required libraries.\n\n", app)
	for _, x := range m {
		fmt.Fprintf(&b, "  - %s  (%s)\n", x.Name, x.Why)
		for _, h := range orderedHints(family) {
			if cmd, ok := x.Install[h.Key]; ok {
				fmt.Fprintf(&b, "      %s %s\n", h.Label, cmd)
			}
		}
	}
	fmt.Fprintf(&b, "\nInstall the libraries above and start %s again. (It does not fall back to a browser or a local web page.)\n", app)
	_, _ = io.WriteString(w, b.String()) // best effort: stderr may be closed
}

type hint = struct{ Key, Label string }

// orderedHints returns hintOrder with the matching family first.
func orderedHints(family string) []hint {
	out := make([]hint, 0, len(hintOrder))
	for _, h := range hintOrder {
		if h.Key == family {
			out = append(out, h)
		}
	}
	for _, h := range hintOrder {
		if h.Key != family {
			out = append(out, h)
		}
	}
	return out
}

// detectFamily reads os-release text and returns debian, fedora, arch, suse or "".
func detectFamily(osRelease string) string {
	var ids []string
	for _, line := range strings.Split(osRelease, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || (k != "ID" && k != "ID_LIKE") {
			continue
		}
		ids = append(ids, strings.Fields(strings.ToLower(strings.Trim(v, `"'`)))...)
	}
	for _, id := range ids {
		switch id {
		case "debian", "ubuntu":
			return "debian"
		case "fedora", "rhel", "centos":
			return "fedora"
		case "arch":
			return "arch"
		case "suse", "opensuse", "opensuse-leap", "opensuse-tumbleweed":
			return "suse"
		}
	}
	return ""
}

func webView2VersionOK(v string) bool { return v != "" && v != "0.0.0.0" }

// macVersionOK reports whether a sw_vers product version is 11.0 or newer.
func macVersionOK(v string) bool {
	major, _, _ := strings.Cut(strings.TrimSpace(v), ".")
	n, err := strconv.Atoi(major)
	return err == nil && n >= 11
}
