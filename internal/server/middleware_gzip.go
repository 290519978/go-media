package server

import (
	"compress/gzip"
	"strings"

	"github.com/gin-gonic/gin"
)

type gzipResponseWriter struct {
	gin.ResponseWriter
	writer *gzip.Writer
}

func (w *gzipResponseWriter) Write(data []byte) (int, error) {
	return w.writer.Write(data)
}

func (w *gzipResponseWriter) WriteString(s string) (int, error) {
	return w.writer.Write([]byte(s))
}

func (s *Server) gzipEventsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := strings.TrimSpace(c.Request.URL.Path)
		if !strings.HasPrefix(path, "/api/v1/events") || strings.HasPrefix(path, "/api/v1/events/image/") {
			c.Next()
			return
		}
		acceptEncoding := strings.ToLower(strings.TrimSpace(c.GetHeader("Accept-Encoding")))
		if !strings.Contains(acceptEncoding, "gzip") {
			c.Next()
			return
		}

		c.Header("Vary", "Accept-Encoding")
		c.Header("Content-Encoding", "gzip")
		c.Header("Content-Length", "")

		gz := gzip.NewWriter(c.Writer)
		defer func() {
			_ = gz.Close()
		}()

		c.Writer = &gzipResponseWriter{
			ResponseWriter: c.Writer,
			writer:         gz,
		}
		c.Next()
	}
}
