package server

import "io/fs"

// Option configures optional server behaviors without breaking existing call sites.
type Option func(*Server)

// WithWebFS injects an optional static web filesystem (typically embedded web/dist).
func WithWebFS(webFS fs.FS) Option {
	return func(s *Server) {
		s.webFS = webFS
	}
}
