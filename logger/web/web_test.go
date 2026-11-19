package web

import (
	"regexp"
	"strings"
	"testing"

	"crimsonledger/internal/ui/page"
)

func TestAssets_EveryElementTheScriptUsesExists(t *testing.T) {
	ids := map[string]bool{}
	for _, m := range regexp.MustCompile(`id="([^"]+)"`).FindAllStringSubmatch(body, -1) {
		ids[m[1]] = true
	}
	for _, m := range regexp.MustCompile(`\$\("([^"]+)"\)`).FindAllStringSubmatch(js, -1) {
		if !ids[m[1]] {
			t.Errorf("app.js uses #%s, which index.html does not have", m[1])
		}
	}
}

func TestAssets_BoundFunctionsAreTheOnesTheAppBinds(t *testing.T) {
	for _, fn := range []string{"loadSettings", "saveSettings", "revealKey", "testConnection", "start", "stop", "getStatus", "pickFolder", "defaultLogsDir"} {
		if !strings.Contains(js, "window."+fn+"(") {
			t.Errorf("page never calls %s", fn)
		}
	}
}

func TestAssets_PageHasNoExternalUrlsOrInnerHTML(t *testing.T) {
	html, _ := page.Build(Assets())
	for _, bad := range []string{"http://", "https://", "innerHTML", "eval(", "src=\"", "href=\""} {
		if strings.Contains(strings.ReplaceAll(html, "http"+"://www.w3.org/2000/svg", ""), bad) {
			t.Errorf("page contains %q", bad)
		}
	}
}
