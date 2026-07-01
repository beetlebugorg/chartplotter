//go:build !tile57

package server

import "net/http"

// serveTile57Style / serveTile57StyleDiff are stubs in the default CGO-free build:
// tile57 style generation needs the native engine, only linked under -tags tile57.
func (s *Server) serveTile57Style(w http.ResponseWriter, r *http.Request) {
	apiErr(w, http.StatusNotImplemented, "style generation requires a -tags tile57 build (run `make build-tile57`)")
}

func (s *Server) serveTile57StyleDiff(w http.ResponseWriter, r *http.Request) {
	apiErr(w, http.StatusNotImplemented, "style diff requires a -tags tile57 build (run `make build-tile57`)")
}
