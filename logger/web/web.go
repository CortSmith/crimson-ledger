// Package web holds the scout's page: one HTML body, one stylesheet, one script, embedded in the binary.
package web

import (
	_ "embed" // the page assets

	"crimsonledger/internal/ui/page"
)

//go:embed index.html
var body string

//go:embed app.css
var css string

//go:embed app.js
var js string

// Assets returns the page parts for page.Build.
func Assets() page.Assets { return page.Assets{Body: body, CSS: css, JS: js} }
