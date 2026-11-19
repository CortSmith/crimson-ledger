// Package migrations embeds the forward-only SQL migration files.
package migrations

import "embed"

// FS holds every NNNN_name.sql migration file at its root.
//
//go:embed *.sql
var FS embed.FS
