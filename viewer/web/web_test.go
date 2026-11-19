package web

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"crimsonledger/internal/ui/page"
)

func TestAssets_EveryElementTheScriptUsesExists(t *testing.T) {
	ids := map[string]bool{"radar": true}
	for _, m := range regexp.MustCompile(`id="([^"]+)"`).FindAllStringSubmatch(body, -1) {
		ids[m[1]] = true
	}
	for _, m := range regexp.MustCompile(`\$\("([^"]+)"\)`).FindAllStringSubmatch(appJS, -1) {
		if !ids[m[1]] {
			t.Errorf("app.js uses #%s, which index.html does not have", m[1])
		}
	}
}

func TestAssets_BoundFunctionsAreTheOnesTheAppBinds(t *testing.T) {
	for _, fn := range []string{"loadSettings", "saveSettings", "revealKey", "connect", "setVisible", "setRuleset", "getCharts", "getMap",
		"search", "getTrail", "getBounty", "copyTarget", "getView", "saveView", "testViewer", "getAppearance", "saveAppearance", "resetAppearance"} {
		if !strings.Contains(appJS, "window."+fn+"(") {
			t.Errorf("page never calls %s", fn)
		}
	}
}

func TestAssets_TheAppearanceTabHasNoColourTableOfItsOwn(t *testing.T) {
	// Presets live in Go (config.Presets) and reach the page through getAppearance; a second copy in JS would drift.
	for name, src := range map[string]string{"style.js": styleJS, "appearance.js": appearanceJS, "app.js": appJS} {
		for _, hex := range []string{"#3b82f6", "#ef4444", "#0072b2", "#00ffff"} {
			if strings.Contains(src, hex) {
				t.Errorf("%s hard-codes preset colour %s", name, hex)
			}
		}
	}
}

func TestAssets_NoLocalWebsiteNoUnsafeCode(t *testing.T) {
	html, _ := page.Build(Assets())
	clean := strings.ReplaceAll(html, "http"+"://www.w3.org/2000/svg", "")
	for _, bad := range []string{"http://", "https://", "innerHTML", "eval(", "new Function", "fetch(", "EventSource", "127.0.0.1", "src=\"", "href=\"", "target=\"_blank"} {
		if strings.Contains(clean, bad) {
			t.Errorf("page contains %q", bad)
		}
	}
}

func TestAssets_PageScriptParses(t *testing.T) {
	// A duplicate const or any syntax error in the joined script stops every handler from attaching.
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	html, _ := page.Build(Assets())
	m := regexp.MustCompile(`(?s)<script[^>]*>(.*?)</script>`).FindAllStringSubmatch(html, -1)
	dir := t.TempDir()
	for i, s := range m {
		f := filepath.Join(dir, fmt.Sprintf("s%d.js", i))
		if err := os.WriteFile(f, []byte("(function(){"+s[1]+"\n})"), 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("node", "--check", f).CombinedOutput(); err != nil {
			t.Errorf("script %d does not parse: %s", i, out)
		}
	}
}

// The *.test.mjs files (pure logic and the page driven through a fake DOM) are not run by anything else, so run them here.
func TestJSSuites(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	for _, dir := range []string{".", "../../internal/ui/page"} {
		cmd := exec.Command("node", "--test")
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("node --test in %s failed:\n%s", dir, out)
		}
	}
}
