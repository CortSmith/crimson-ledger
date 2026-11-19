// Package web holds the viewer's page: HTML, one stylesheet and the scripts, embedded in the binary.
package web

import (
	_ "embed" // the page assets
	"strings"

	"crimsonledger/internal/ui/page"
)

//go:embed index.html
var body string

//go:embed app.css
var css string

//go:embed style.js
var styleJS string

//go:embed tracks.js
var tracksJS string

//go:embed appearance.js
var appearanceJS string

//go:embed radar.js
var radarJS string

//go:embed list.js
var listJS string

//go:embed panels.js
var panelsJS string

//go:embed world.js
var worldJS string

//go:embed app.js
var appJS string

// Assets returns the page parts for page.Build. Modules load before app.js, which wires them together.
func Assets() page.Assets {
	return page.Assets{Body: body, CSS: css, JS: strings.Join([]string{styleJS, tracksJS, radarJS, listJS, panelsJS, worldJS, appearanceJS, appJS}, "\n")}
}
