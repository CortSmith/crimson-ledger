// Package page builds the single inline HTML document both apps show: one nonce, a strict CSP, no network.
package page

import (
	"crypto/rand"
	_ "embed" // shared masked-key component
	"encoding/base64"
	"strings"
)

//go:embed keyfield.js
var keyfieldJS string

//go:embed keyfield.css
var keyfieldCSS string

// Assets is what an app supplies; the shared key field is added automatically.
type Assets struct{ HTML, CSS, JS, Body string }

// Build returns the full document and the nonce used in it. A new nonce is generated every call.
func Build(a Assets) (html, nonce string) {
	nonce = randomNonce()
	csp := "default-src 'none'; script-src 'nonce-" + nonce + "'; style-src 'nonce-" + nonce +
		"'; img-src data:; connect-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
	var b strings.Builder
	b.WriteString(`<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="` + csp + `">`)
	for _, css := range []string{keyfieldCSS, a.CSS} {
		b.WriteString(`<style nonce="` + nonce + `">` + inert(css, "</style") + `</style>`)
	}
	b.WriteString(`</head><body>` + a.Body)
	for _, js := range []string{keyfieldJS, a.JS} {
		b.WriteString(`<script nonce="` + nonce + `">` + inert(js, "</script") + `</script>`)
	}
	b.WriteString(`</body></html>`)
	return b.String(), nonce
}

// inert stops an asset from closing its own tag by escaping the closing sequence (case-insensitively).
func inert(s, closing string) string {
	lower := strings.ToLower(s)
	var b strings.Builder
	for i := 0; i < len(s); {
		if strings.HasPrefix(lower[i:], closing) {
			b.WriteString(`<\/` + s[i+2:i+len(closing)])
			i += len(closing)
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func randomNonce() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic("crypto/rand failed: " + err.Error()) // no safe fallback exists for a CSP nonce
	}
	return base64.RawStdEncoding.EncodeToString(raw[:])
}
