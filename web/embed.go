// Package web embeds the built React Playground (web/dist) into the Go
// binary, so a single `forgeai` binary serves the UI with no separate
// static file deployment. Run `npm run build` in web/ after any UI change
// — the built dist/ is committed so `go build` works without Node
// installed (docs/V0.1_SPEC.md §1: single binary distribution).
package web

import "embed"

//go:embed all:dist
var DistFS embed.FS
