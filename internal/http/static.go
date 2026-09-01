package http

import (
	"io/fs"
	"net/http"

	"github.com/sibukixxx/rag-poc/web"
)

// staticHandler serves the embedded React Playground build. web.DistFS
// always contains a "dist" subdirectory because it's committed to the
// repo (see web/embed.go); fs.Sub only fails if that invariant breaks at
// build time, so a panic here is a build-time bug, not a runtime one.
func staticHandler() http.Handler {
	sub, err := fs.Sub(web.DistFS, "dist")
	if err != nil {
		panic("http: web/dist missing from embedded filesystem: " + err.Error())
	}
	return http.FileServer(http.FS(sub))
}
