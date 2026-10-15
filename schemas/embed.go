// Package schemas embeds the JSON Schema contracts (*.schema.json) so the Go
// validators check against exactly the files in this directory, never a copy.
package schemas

import "embed"

// FS holds every *.schema.json in this directory.
//
//go:embed *.schema.json
var FS embed.FS
