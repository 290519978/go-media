package server

import (
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
)

// registerEmbeddedWebRoutes wires SPA static serving when web assets are embedded.
func (s *Server) registerEmbeddedWebRoutes(r *gin.Engine) {
	if s == nil || r == nil || s.webFS == nil {
		return
	}

	fileServer := http.FileServer(http.FS(s.webFS))
	r.NoRoute(func(c *gin.Context) {
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			c.Status(http.StatusNotFound)
			return
		}
		if isBackendOnlyPath(c.Request.URL.Path) {
			c.Status(http.StatusNotFound)
			return
		}

		cleanPath := strings.TrimPrefix(path.Clean("/"+c.Request.URL.Path), "/")
		if cleanPath == "." || cleanPath == "" {
			cleanPath = "index.html"
		}

		if webFileExists(s.webFS, cleanPath) {
			// Avoid FileServer's built-in "/index.html" -> "/" redirect loop.
			if cleanPath == "index.html" {
				c.Request.URL.Path = "/"
			} else {
				c.Request.URL.Path = "/" + cleanPath
			}
			fileServer.ServeHTTP(c.Writer, c.Request)
			return
		}

		c.Request.URL.Path = "/"
		fileServer.ServeHTTP(c.Writer, c.Request)
	})
}

func webFileExists(webFS fs.FS, name string) bool {
	if webFS == nil || strings.TrimSpace(name) == "" {
		return false
	}
	info, err := fs.Stat(webFS, name)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func isBackendOnlyPath(pathValue string) bool {
	return hasAPIPrefix(pathValue, "/api") ||
		hasAPIPrefix(pathValue, "/ai") ||
		hasAPIPrefix(pathValue, "/ws")
}

func hasAPIPrefix(pathValue, prefix string) bool {
	return pathValue == prefix || strings.HasPrefix(pathValue, prefix+"/")
}
