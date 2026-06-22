// Package server is the tiny distribution shim for the 100%-wasm chartplotter:
// it serves the embedded web frontend (static files + the wasm baker, with
// HTTP Range) and one API endpoint — GET /api/cell/<NAME>?url=… — that downloads
// a raw NOAA ENC cell and caches it (the shim acting as a CORS proxy, since
// charts.noaa.gov sends no CORS headers). All parse/bake/render runs in-browser.
package server

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/beetlebugorg/chartplotter/internal/engine/tilesource"
	"github.com/beetlebugorg/chartplotter/web"
)

// Server hosts the static web assets and the /api/cell proxy. Static assets come
// from the embedded bundle (web.Assets) by default; if assetsDir is non-empty,
// on-disk files there take precedence and anything missing falls back to the
// embedded copy. Downloaded raw cells are cached under cacheDir/ENC_ROOT. The
// zero value is not usable; use New.
type Server struct {
	assetsDir   string // optional on-disk asset override (dev); "" → embedded only
	cacheDir    string // XDG cache root: REGENERABLE baked tile sets (NOAA/<d>/*.pmtiles)
	dataDir     string // XDG data root: SOURCE ENC (district zips, raw cells) — safe, not auto-deleted
	allowRemote bool
	share       shareStore    // latest "share my view" snapshot (camera + cell list)
	settings    settingsStore // persisted client display settings (<data>/client-settings.json)
	Version     string        // build version

	sets    *tileSets         // registry of ENABLED tile sets served at /tiles/{set}/…
	imports *importJobs       // background server-side bake jobs (POST /api/import)
	packsMu sync.Mutex        // guards packs
	packs   map[string]string // ALL baked packs on disk: set name → pmtiles path
	prefs   *prefs            // persisted enable/disable state (<data>/prefs.json)
	auxIdx  *auxIndex         // index of companion aux.zips for /api/aux (TXTDSC/PICREP)
}

// New returns a Server. Pass an empty assetsDir to serve the embedded asset
// bundle (the single-file default); pass a directory to override it from disk
// during development. cacheDir is the XDG cache root for REGENERABLE baked tile
// sets; dataDir is the XDG data root for the SOURCE ENC (district zips, raw cells)
// that must survive a cache wipe — pass "" to default it to cacheDir (single-dir
// mode). allowRemote is true when the bind host is not loopback (the operator
// opted into network exposure), which skips the per-request Host-header check.
func New(assetsDir, cacheDir, dataDir string, allowRemote bool) *Server {
	if dataDir == "" {
		dataDir = cacheDir
	}
	s := &Server{assetsDir: assetsDir, cacheDir: cacheDir, dataDir: dataDir, allowRemote: allowRemote, sets: newTileSets(), imports: newImportJobs(), auxIdx: newAuxIndex()}
	// Discover every baked pack on disk (provider trees + legacy tiles/), then
	// register the ENABLED ones (disabled packs stay on disk but off the map). State
	// lives in <data>/prefs.json so it survives restarts and is shared across clients.
	s.packs = scanPacks(cacheDir)
	s.prefs = loadPrefs(dataDir)
	n := 0
	for _, name := range sortedKeys(s.packs) {
		if s.prefs.isDisabled(name) {
			continue
		}
		if src, err := tilesource.Open(s.packs[name]); err == nil {
			s.sets.register(name, src)
			n++
		} else {
			log.Printf("tilesets: skip %q: %v", s.packs[name], err)
		}
	}
	if len(s.packs) > 0 {
		log.Printf("tilesets: %d pack(s) on disk, %d enabled (from %s)", len(s.packs), n, cacheDir)
	}
	return s
}

// Close releases server-held resources (open tile-set archives). Safe to call once
// at shutdown.
func (s *Server) Close() error {
	s.sets.closeAll()
	return nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	lw := &logResponseWriter{ResponseWriter: w, status: http.StatusOK}
	switch {
	case strings.HasPrefix(r.URL.Path, "/api/"):
		s.handleAPI(lw, r)
	case strings.HasPrefix(r.URL.Path, "/tiles/"):
		s.serveTileSet(lw, r)
	default:
		s.serveAsset(lw, r)
	}
	// One access-log line per request to stderr (method, status, path, range,
	// duration) — so you can watch what the browser fetches when testing.
	rng := ""
	if v := r.Header.Get("Range"); v != "" {
		rng = " " + v
	}
	log.Printf("%s %d %s%s %s", r.Method, lw.status, r.URL.RequestURI(), rng, time.Since(start).Round(time.Microsecond))
}

// logResponseWriter captures the status code for the access log while
// forwarding everything (including http.ServeContent's Range handling) through.
type logResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *logResponseWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Flush forwards to the underlying writer so streaming responses (SSE) work
// through the log wrapper.
func (w *logResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

const jsonCT = "application/json"

func apiErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", jsonCT)
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"ok":false,"error":%q}`, msg)
}

// hostIsLocal reports whether the request Host is a loopback name — the
// DNS-rebind defence for the local webapp.
func hostIsLocal(host string) bool {
	return strings.HasPrefix(host, "127.0.0.1") ||
		strings.HasPrefix(host, "localhost") ||
		strings.HasPrefix(host, "[::1]")
}

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	if !s.allowRemote && !hostIsLocal(r.Host) {
		apiErr(w, http.StatusForbidden, "non-local host")
		return
	}
	switch {
	case r.URL.Path == "/api/health":
		w.Header().Set("Content-Type", jsonCT)
		io.WriteString(w, `{"ok":true}`)
	case r.URL.Path == "/api/cells":
		s.serveCells(w, r) // GET: names of cells currently in the server's ENC_ROOT cache
	case r.URL.Path == "/api/ienc/catalog":
		s.serveIENCCatalog(w, r) // GET: USACE Inland ENC products catalogue (server-fetched JSON)
	case strings.HasPrefix(r.URL.Path, "/api/cell/"):
		if r.Method == http.MethodPut {
			s.uploadCell(w, r) // PUT raw .000 into the cache (share: hand-imported cells)
		} else {
			s.serveCell(w, r) // GET raw .000 — the 100%-wasm path: NOAA download proxy + cache
		}
	case r.URL.Path == "/api/share":
		s.serveShare(w, r) // GET/POST the latest "share my view" snapshot
	case r.URL.Path == "/api/settings":
		s.serveSettings(w, r) // GET/POST persisted client display settings (shared across screens)
	case strings.HasPrefix(r.URL.Path, "/api/tile/"):
		s.serveTile(w, r) // GET one MVT tile baked from cached cells (tile-debugger inspect)
	case r.URL.Path == "/api/aux" || strings.HasPrefix(r.URL.Path, "/api/aux/"):
		s.serveAux(w, r) // GET aux manifest, or one TXTDSC/PICREP file on demand (not the raw zip)
	case strings.HasPrefix(r.URL.Path, "/api/import"):
		s.handleImport(w, r) // POST: server-side native bake → register a tile set; status polling
	case r.URL.Path == "/api/packs":
		s.handlePacks(w, r) // GET: all baked packs + enabled state
	case r.URL.Path == "/api/set/enable" || r.URL.Path == "/api/set/disable":
		s.handleSetEnabled(w, r) // POST: show/hide a pack on the map (data kept)
	case r.URL.Path == "/api/set":
		s.handleDeleteSet(w, r) // DELETE: unregister a tile set + remove its baked files
	case r.URL.Path == "/api/proxy":
		s.serveProxy(w, r) // dumb CORS/Range passthrough for a NOAA URL (e.g. All_ENCs.zip)
	default:
		apiErr(w, http.StatusNotFound, "unknown endpoint")
	}
}

// serveCell serves a raw S-57 base cell (.000) for the in-browser wasm baker —
// the 100%-wasm path. GET /api/cell/<NAME>?url=<noaa-zip-url>: returns the cached
// cell from ENC_ROOT, or (the shim acting as a NOAA download proxy, since
// charts.noaa.gov sends no CORS headers) downloads + caches it from `url` first.
func (s *Server) serveCell(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		apiErr(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/cell/"), ".000")
	if name == "" || !isCellName(name) {
		apiErr(w, http.StatusBadRequest, "bad cell name")
		return
	}
	data, _, err := loadCellCached(http.DefaultClient, s.dataDir, name, r.URL.Query().Get("url"))
	if err != nil {
		apiErr(w, http.StatusBadGateway, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(data)
}

// serveProxy is a dumb CORS proxy that streams a NOAA URL through to the browser
// (which can't fetch charts.noaa.gov cross-origin — no CORS headers there). It
// forwards Range so the client's random-access ZIP reader can pull just the
// cells it needs out of the multi-GB All_ENCs.zip without downloading it whole.
// No parsing, no caching, no extraction — all of that is done in-browser (wasm).
// Restricted to NOAA hosts so it isn't an open relay.
func (s *Server) serveProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		apiErr(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	raw := r.URL.Query().Get("url")
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || !isChartHost(u.Hostname()) {
		apiErr(w, http.StatusBadRequest, "url must be a charts.noaa.gov or ienccloud.us URL")
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, raw, nil)
	if err != nil {
		apiErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if rng := r.Header.Get("Range"); rng != "" {
		req.Header.Set("Range", rng) // forward range for random-access reads
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		apiErr(w, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()
	for _, h := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Last-Modified", "ETag"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Expose-Headers", "content-range,accept-ranges,content-length")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// isChartHost reports whether host is an allowed ENC download host — NOAA
// (charts.noaa.gov) or USACE Inland ENC (ienccloud.us). The proxy + server-side
// fetch are restricted to these so neither is an open relay.
func isChartHost(host string) bool {
	host = strings.ToLower(host)
	switch host {
	case "charts.noaa.gov", "www.charts.noaa.gov", "ienccloud.us", "www.ienccloud.us":
		return true
	}
	return false
}

// isCellName accepts the alphanumeric NOAA cell ids (e.g. US5MD1MC) — a safe
// single path component (no separators, dots, or traversal).
func isCellName(s string) bool {
	if len(s) == 0 || len(s) > 16 {
		return false
	}
	for _, c := range s {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// serveAsset serves a static web asset: an on-disk --assets override (if set and
// present) or the embedded bundle.
func (s *Server) serveAsset(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Path
	if rel == "" || rel == "/" {
		rel = "/index.html"
	}
	rel = path.Clean(rel)
	if strings.Contains(rel, "..") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	name := strings.TrimPrefix(rel, "/")

	// A --assets directory (dev) overrides the embedded bundle when the file is
	// present on disk; otherwise fall back to the embedded copy.
	if s.assetsDir != "" {
		full := filepath.Join(s.assetsDir, filepath.FromSlash(name))
		if fi, err := os.Stat(full); err == nil && !fi.IsDir() {
			s.serveFile(w, r, full, rel)
			return
		}
	}
	s.serveEmbedded(w, r, name, rel)
}

// serveFile streams an on-disk file with HTTP Range + permissive CORS (so the
// pmtiles:// protocol can fetch byte ranges). `rel` is the request path (used
// for the MIME type + cache policy).
func (s *Server) serveFile(w http.ResponseWriter, r *http.Request, full, rel string) {
	f, err := os.Open(full)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		http.NotFound(w, r)
		return
	}
	setAssetHeaders(w, rel)
	http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
}

// serveEmbedded streams a file from the embedded asset bundle. Embedded files
// have no modification time, so revalidation is driven by Cache-Control only.
func (s *Server) serveEmbedded(w http.ResponseWriter, r *http.Request, name, rel string) {
	f, err := web.Assets.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		http.NotFound(w, r)
		return
	}
	rs, ok := f.(io.ReadSeeker)
	if !ok { // every embed.FS file is seekable; guard defensively
		http.Error(w, "asset not seekable", http.StatusInternalServerError)
		return
	}
	setAssetHeaders(w, rel)
	http.ServeContent(w, r, fi.Name(), time.Time{}, rs)
}

// setAssetHeaders writes the Range/CORS/cache headers shared by the on-disk and
// embedded asset paths. The app code + manifests must always reflect the latest
// build/bake, so HTML/JS/JSON revalidate (otherwise a cached chartplotter-app.mjs
// keeps the old region logic after an update). The wasm baker must revalidate
// too: it is loaded together with wasm_exec.js (a .js, already no-cache), and the
// two are a matched pair — a cached .wasm against a fresh wasm_exec.js fails with
// "import object field 'runtime.ticks' is not a Function" (a tinygo↔go runtime
// mismatch). Tiles/atlases are large and change only via a fresh provision
// (cache-busted by ?t=), so they may cache.
func setAssetHeaders(w http.ResponseWriter, rel string) {
	w.Header().Set("Content-Type", mimeFor(rel))
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Expose-Headers", "content-range,accept-ranges,content-length")
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".html", ".js", ".mjs", ".json", ".wasm":
		w.Header().Set("Cache-Control", "no-cache")
	}
}

// mimeFor maps a path's extension to a content type. Explicit for the types the
// browser is strict about (.mjs/.wasm) and the chart formats.
func mimeFor(p string) string {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".json":
		return "application/json"
	case ".css":
		return "text/css; charset=utf-8"
	case ".png":
		return "image/png"
	case ".wasm":
		return "application/wasm"
	case ".pmtiles":
		return "application/octet-stream"
	case ".pbf":
		return "application/x-protobuf"
	case ".svg":
		return "image/svg+xml"
	default:
		return "application/octet-stream"
	}
}
