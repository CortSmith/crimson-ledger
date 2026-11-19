package preflight

import (
	"bytes"
	"strings"
	"testing"
)

func TestCheck_ReportsMissing(t *testing.T) {
	open := func(n string) bool { return n != "libdefinitely-missing.so.9" }
	got := checkLibs([]lib{{Name: "libdefinitely-missing.so.9", Why: "nothing"}, {Name: "libok.so.1", Why: "ok"}}, open)
	if len(got) != 1 || got[0].Name != "libdefinitely-missing.so.9" {
		t.Fatalf("got %+v", got)
	}
}

func TestCheck_AlternativesSatisfy(t *testing.T) {
	open := func(n string) bool { return n == "libb.so" }
	got := checkLibs([]lib{{Name: "liba.so", Alt: []string{"libb.so"}, Why: "x"}}, open)
	if len(got) != 0 {
		t.Fatalf("alternative should satisfy: %+v", got)
	}
}

func TestDistroDetection(t *testing.T) {
	cases := map[string]string{
		"ID=ubuntu\nID_LIKE=debian\n": "debian",
		"ID=debian\n":                 "debian",
		"ID=fedora\n":                 "fedora",
		"ID=arch\n":                   "arch",
		"ID=manjaro\nID_LIKE=arch\n":  "arch",
		"ID=opensuse-leap\nID_LIKE=\"suse opensuse\"\n": "suse",
		"ID=plan9\n": "",
		"":           "",
	}
	for in, want := range cases {
		if got := detectFamily(in); got != want {
			t.Errorf("detectFamily(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReport_ExactText(t *testing.T) {
	m := []Missing{{Name: "libgtk-3.so.0", Why: "window toolkit", Install: map[string]string{
		"debian": "sudo apt install libgtk-3-0", "fedora": "sudo dnf install gtk3"}}}
	var b bytes.Buffer
	Report(&b, "Crimson Ledger", m, "fedora")
	want := "Crimson Ledger cannot start: missing required libraries.\n\n" +
		"  - libgtk-3.so.0  (window toolkit)\n" +
		"      Fedora:        sudo dnf install gtk3\n" +
		"      Debian/Ubuntu: sudo apt install libgtk-3-0\n\n" +
		"Install the libraries above and start Crimson Ledger again. (It does not fall back to a browser or a local web page.)\n"
	if b.String() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", b.String(), want)
	}
}

func TestWindowsRegistry_Table(t *testing.T) {
	for v, want := range map[string]bool{"": false, "0.0.0.0": false, "120.0.2210.91": true} {
		if got := webView2VersionOK(v); got != want {
			t.Errorf("webView2VersionOK(%q) = %v", v, got)
		}
	}
}

func TestMacVersion_Table(t *testing.T) {
	for v, want := range map[string]bool{"10.15": false, "10.15.7": false, "11.0": true, "14.5": true, "11": true, "junk": false, "": false} {
		if got := macVersionOK(v); got != want {
			t.Errorf("macVersionOK(%q) = %v", v, got)
		}
	}
}

func TestReport_NoMissingWritesNothing(t *testing.T) {
	var b bytes.Buffer
	Report(&b, "X", nil, "")
	if strings.TrimSpace(b.String()) != "" {
		t.Fatal("expected no output")
	}
}
