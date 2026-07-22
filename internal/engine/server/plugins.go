package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/beetlebugorg/chartplotter/internal/engine/nmea"
	"github.com/beetlebugorg/chartplotter/internal/engine/plugin"
)

// plugins.go wires the plugin engine (internal/engine/plugin) into the HTTP server:
// it constructs the Manager, provides the capability Host backed by the shared
// vessel/AIS/raw stores, and serves the /api/plugins management routes plus the
// per-plugin /plugins/<id>/{ui,serve}/* static surface (spec §11, Appendix A.2).

// maxPluginUpload caps an uploaded plugin archive.
const maxPluginUpload = 64 << 20

// initPlugins builds the Manager rooted at the data dir and starts enabled plugins.
func (s *Server) initPlugins() {
	s.pluginMgr = plugin.NewManager(context.Background(), plugin.ManagerOpts{
		DataDir: s.dataDir,
		Host:    &pluginHost{s: s},
		Logf:    log.Printf,
	})
}

// pluginHost implements plugin.Host over the server's shared stores. Plugin writes
// are attributed to the plugin id so provenance/priority can arbitrate against the
// built-in NMEA sources (spec §6, §9).
type pluginHost struct{ s *Server }

func (h *pluginHost) PublishVessel(source string, deltas []nmea.Delta) {
	h.s.vessel.PublishDeltas(source, deltas)
}
func (h *pluginHost) PublishAIS(source string, targets []nmea.AISTarget) {
	ais := h.s.nmeaMgr.AIS()
	for _, t := range targets {
		ais.Upsert(t, source)
	}
}
func (h *pluginHost) PublishRaw(source string, lines []string) {
	for _, line := range lines {
		h.s.rawHub.publish(source, line)
	}
}
func (h *pluginHost) EvictAIS(source string)                   { h.s.nmeaMgr.AIS().EvictSource(source) }
func (h *pluginHost) UpdateStatus(string, plugin.PluginStatus) {} // surfaced via the SSE poll
func (h *pluginHost) Log(id, level, msg string)                { log.Printf("[plugin %s] %s: %s", id, level, msg) }

// --- management API --------------------------------------------------------

// servePlugins handles GET /api/plugins — the installed list with manifest + status.
func (s *Server) servePlugins(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		apiErr(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	writeJSON(w, map[string]any{"ok": true, "plugins": s.pluginMgr.List()})
}

// servePluginInstall handles POST /api/plugins/install — a multipart zip upload.
func (s *Server) servePluginInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		apiErr(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if err := r.ParseMultipartForm(maxPluginUpload); err != nil {
		apiErr(w, http.StatusBadRequest, "bad upload: "+err.Error())
		return
	}
	file, _, err := r.FormFile("plugin")
	if err != nil {
		apiErr(w, http.StatusBadRequest, "missing 'plugin' file")
		return
	}
	defer file.Close()
	tmp, err := os.CreateTemp("", "cp-plugin-*.zip")
	if err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, io.LimitReader(file, maxPluginUpload)); err != nil {
		tmp.Close()
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	tmp.Close()
	man, err := s.pluginMgr.Install(tmp.Name(), plugin.InstallOptions{})
	if err != nil {
		apiErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "manifest": man})
}

// servePluginItem routes /api/plugins/<id>[/<action>].
func (s *Server) servePluginItem(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/plugins/")
	id, action, _ := strings.Cut(rest, "/")
	if !validPluginID(id) {
		apiErr(w, http.StatusBadRequest, "bad plugin id")
		return
	}
	switch {
	case action == "logs" && r.Method == http.MethodGet:
		writeJSON(w, map[string]any{"ok": true, "logs": s.pluginMgr.Logs(id)})
	case action == "enable" && r.Method == http.MethodPost:
		s.pluginErr(w, s.pluginMgr.Enable(id))
	case action == "disable" && r.Method == http.MethodPost:
		err := s.pluginMgr.Disable(id)
		if err == nil {
			// Deliberately turning a data source OFF is not signal loss: drop every
			// reading it wrote (position, wind, …) so no phantom own-ship lingers.
			// (AIS targets it fed age out via the store TTL.)
			s.vessel.ClearSource(id)
		}
		s.pluginErr(w, err)
	case action == "grants" && (r.Method == http.MethodPut || r.Method == http.MethodPost):
		var body struct {
			Grants []plugin.Capability `json:"grants"`
			Config map[string]any      `json:"config"`
		}
		if err := decodeJSON(r, &body); err != nil {
			apiErr(w, http.StatusBadRequest, err.Error())
			return
		}
		s.pluginErr(w, s.pluginMgr.SetGrants(id, body.Grants, body.Config))
	case action == "config" && (r.Method == http.MethodPut || r.Method == http.MethodPost):
		var cfg map[string]any
		if err := decodeJSON(r, &cfg); err != nil {
			apiErr(w, http.StatusBadRequest, err.Error())
			return
		}
		s.pluginErr(w, s.pluginMgr.SetConfig(id, cfg)) // config-only update keeps grants
	case action == "" && r.Method == http.MethodDelete:
		purge := r.URL.Query().Get("purgeData") != ""
		err := s.pluginMgr.Remove(id, purge)
		if err == nil {
			s.vessel.ClearSource(id) // as with disable: no phantom readings
		}
		s.pluginErr(w, err)
	default:
		apiErr(w, http.StatusNotFound, "unknown plugin endpoint")
	}
}

// servePluginsStream pushes the plugin status map whenever it changes (mirrors the
// connections-stream pattern).
func (s *Server) servePluginsStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := sseStart(w)
	if !ok {
		return
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	last := ""
	for {
		b, _ := json.Marshal(map[string]any{"plugins": s.pluginMgr.List()})
		if line := string(b); line != last {
			last = line
			fmt.Fprintf(w, "data: %s\n\n", line)
			flusher.Flush()
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

// --- per-plugin static: /plugins/<id>/{ui,serve}/* -------------------------

// servePluginStatic serves a plugin's UI bundle and published artifacts from the
// unpacked archive / its data dir, with Range + the shared asset headers (.wasm/.mjs
// mime, caching). Everything is namespaced under the plugin id.
func (s *Server) servePluginStatic(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/plugins/")
	id, tail, _ := strings.Cut(rest, "/")
	if !validPluginID(id) {
		http.NotFound(w, r)
		return
	}
	kind, rel, _ := strings.Cut(tail, "/")
	rel = path.Clean("/" + rel)[1:] // strip traversal
	if rel == "" || strings.Contains(rel, "..") {
		http.NotFound(w, r)
		return
	}
	var base string
	switch kind {
	case "ui":
		dir, ok := s.pluginMgr.VersionDir(id)
		if !ok {
			http.NotFound(w, r)
			return
		}
		base = filepath.Join(dir, "ui")
	case "serve":
		base = filepath.Join(s.pluginMgr.DataDir(id), "serve")
	default:
		http.NotFound(w, r)
		return
	}
	full := filepath.Join(base, filepath.FromSlash(rel))
	if !strings.HasPrefix(full, base+string(os.PathSeparator)) {
		http.NotFound(w, r)
		return
	}
	s.serveFile(w, r, full, rel)
}

// --- helpers ---------------------------------------------------------------

// pluginErr writes an ok/err JSON response from a Manager operation error.
func (s *Server) pluginErr(w http.ResponseWriter, err error) {
	if err != nil {
		apiErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func decodeJSON(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, maxConnBody)).Decode(v)
}

// validPluginID accepts the reverse-DNS ids the manifest allows — a safe path
// component (no slashes, dots/hyphens only).
func validPluginID(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '-') {
			return false
		}
	}
	return !strings.Contains(s, "..")
}
