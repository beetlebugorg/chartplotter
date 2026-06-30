//go:build !tile57

package server

import "net/http"

// serveTile57Style is a stub in the default CGO-free build: tile57 style
// generation needs the native engine, which is only linked under -tags tile57.
func (s *Server) serveTile57Style(w http.ResponseWriter, r *http.Request) {
	apiErr(w, http.StatusNotImplemented, "style generation requires a -tags tile57 build (run `make build-tile57`)")
}
