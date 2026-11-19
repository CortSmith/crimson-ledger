// Package vmaps exists so the viewer's zone images and index.json can be embedded; see viewer/internal/maps.
package vmaps

import "embed"

// FS holds index.json and the zone images (*.webp or *.png), converted by scripts/maps-to-webp.sh.
//
//go:embed *
var FS embed.FS
