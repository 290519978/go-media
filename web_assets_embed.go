//go:build embed_web

package main

import (
	"embed"
	"fmt"
	"io/fs"
)

//go:embed all:web/dist
var embeddedWebDist embed.FS

func init() {
	loadEmbeddedWebFSHook = func() fs.FS {
		webFS, err := fs.Sub(embeddedWebDist, "web/dist")
		if err != nil {
			panic(fmt.Sprintf("load embedded web dist failed: %v", err))
		}
		return webFS
	}
}
