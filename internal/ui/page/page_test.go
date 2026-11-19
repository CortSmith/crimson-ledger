package page

import (
	"regexp"
	"strings"
	"testing"
)

var assets = Assets{CSS: "body{color:#000}", JS: `"use strict";window.__app={};`, Body: "<main id=app></main>"}

func TestBuild_InjectsNonceEverywhere(t *testing.T) {
	html, nonce := Build(assets)
	if n := strings.Count(html, `nonce="`+nonce+`"`); n != 4 {
		t.Fatalf("want 4 nonce attributes (2 style, 2 script), got %d", n)
	}
	if strings.Count(html, "<script") != 2 || strings.Count(html, "<style") != 2 {
		t.Fatalf("unexpected tag counts (shared keyfield + app each)")
	}
}

func TestBuild_CSPExact(t *testing.T) {
	html, n := Build(assets)
	want := "default-src 'none'; script-src 'nonce-" + n + "'; style-src 'nonce-" + n +
		"'; img-src data:; connect-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
	if !strings.Contains(html, `content="`+want+`"`) {
		t.Fatalf("CSP not exact:\n%s", html)
	}
	for _, bad := range []string{"unsafe-inline", "unsafe-eval"} {
		if strings.Contains(html, bad) {
			t.Fatalf("%s present", bad)
		}
	}
}

func TestBuild_NoExternalURLs(t *testing.T) {
	html, _ := Build(assets)
	if re := regexp.MustCompile(`https?://|//cdn`); re.MatchString(html) {
		t.Fatalf("external URL in page: %q", re.FindString(html))
	}
}

func TestBuild_NonceDiffersPerBuild(t *testing.T) {
	_, a := Build(assets)
	_, b := Build(assets)
	if a == b || len(a) < 20 {
		t.Fatalf("nonces %q %q", a, b)
	}
}

func TestBuild_EscapesClosingTagsInAssets(t *testing.T) {
	html, _ := Build(Assets{JS: "var x='</script><img src=x>';", CSS: "/*</style>*/"})
	if strings.Count(html, "</script>") != 2 || strings.Count(html, "</style>") != 2 {
		t.Fatalf("asset closed its own tag: %s", html)
	}
}
