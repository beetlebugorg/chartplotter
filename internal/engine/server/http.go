// Package server hosts the chartplotter web frontend (static files with HTTP
// Range support) plus the /api onboarding surface the chart-manager UI drives:
// POST /api/provision starts a background download+bake job, GET /api/tasks
// reports its progress, DELETE /api/charts removes the provisioned archive.
// Implements the serve/handleApi path (see CHARTS-UI-SPEC §3).
package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/beetlebugorg/chartplotter/web"
)

// Server hosts the static web assets, the per-region chart archives from
// cacheDir, and the API. Static assets come from the embedded bundle (web.Assets)
// by default; if assetsDir is non-empty, on-disk files there take precedence and
// anything missing falls back to the embedded copy. All writes from user actions
// (provisioned archives, manifests, download caches) go to cacheDir — never the
// asset bundle. The zero value is not usable; use New.
type Server struct {
	assetsDir   string // optional on-disk asset override (dev); "" → embedded only
	cacheDir    string // XDG cache root; regions/<NN>.pmtiles served at /charts/<NN>.pmtiles
	allowRemote bool
	Version     string // build version, surfaced by /api/debug
	task        task

	debugMu     sync.Mutex // guards debugClient
	debugClient []byte     // last client state snapshot POSTed to /api/debug (selected items etc.)
}

// New returns a Server. Pass an empty assetsDir to serve the embedded asset
// bundle (the single-file default); pass a directory to override it from disk
// during development. cacheDir is the XDG cache root for baked archives and the
// destination for every user-initiated write. allowRemote is true when the bind
// host is not loopback (the operator opted into network exposure), which skips
// the per-request Host-header DNS-rebind check on /api.
func New(assetsDir, cacheDir string, allowRemote bool) *Server {
	return &Server{assetsDir: assetsDir, cacheDir: cacheDir, allowRemote: allowRemote}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	lw := &logResponseWriter{ResponseWriter: w, status: http.StatusOK}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		s.handleAPI(lw, r)
	} else if strings.HasPrefix(r.URL.Path, "/charts/") {
		s.serveRegion(lw, r)
	} else {
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

const jsonCT = "application/json"

// User-provisioned data filenames. These are written by a user action (a UI
// provision) and therefore live in the XDG cache dir, never in the read-only
// asset bundle.
const (
	userPMTiles  = "charts-user.pmtiles"
	userManifest = "charts-user.json"
)

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

// validCell is the ENC cell-name allowlist: ^[A-Z0-9]{5,8}$.
func validCell(c string) bool {
	if len(c) < 5 || len(c) > 8 {
		return false
	}
	for i := 0; i < len(c); i++ {
		ch := c[i]
		if !((ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9')) {
			return false
		}
	}
	return true
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
	case r.URL.Path == "/api/provision":
		s.provisionStart(w, r)
	case r.URL.Path == "/api/tasks":
		w.Header().Set("Content-Type", jsonCT)
		io.WriteString(w, s.task.json())
	case r.URL.Path == "/api/charts":
		s.handleCharts(w, r) // GET → manifest, DELETE → remove all
	case strings.HasPrefix(r.URL.Path, "/api/charts/"):
		s.deleteRegion(w, r) // DELETE /api/charts/<NN>
	case strings.HasPrefix(r.URL.Path, "/api/cell/"):
		s.serveCell(w, r) // GET raw .000 (the 100%-wasm path: NOAA proxy + cache)
	case r.URL.Path == "/api/debug":
		s.handleDebug(w, r) // GET → server+client debug snapshot, POST → store client snapshot
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
	data, _, err := loadCellCached(http.DefaultClient, s.cacheDir, name, r.URL.Query().Get("url"))
	if err != nil {
		apiErr(w, http.StatusBadGateway, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(data)
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

// serveRegion serves a baked region archive (/charts/<NN>.pmtiles) from the
// cache's regions dir, honouring HTTP Range.
func (s *Server) serveRegion(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/charts/")
	if strings.ContainsAny(name, "/\\") {
		http.NotFound(w, r)
		return
	}
	// The map-selected (cell-list) bake + its manifest live in the XDG cache,
	// served under /charts/ alongside the per-region archives.
	if name == userPMTiles || name == userManifest {
		s.serveFile(w, r, filepath.Join(s.cacheDir, name), name)
		return
	}
	num, ok := regionNumFromPMTiles(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.serveFile(w, r, regionPMTilesPath(s.cacheDir, num), name)
}

// handleCharts: GET → the installed-region manifest; DELETE → remove every
// baked region archive (clean slate).
func (s *Server) handleCharts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", jsonCT)
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(regionManifest(s.cacheDir))
	case http.MethodDelete:
		if s.task.isRunning() {
			apiErr(w, http.StatusConflict, "busy")
			return
		}
		entries, _ := os.ReadDir(regionsDir(s.cacheDir))
		for _, e := range entries {
			if n, ok := regionNumFromPMTiles(e.Name()); ok {
				_ = DeleteRegion(s.cacheDir, n)
			}
		}
		// Also drop the map-selected (cell-list) bake + its manifest — otherwise
		// "remove all" leaves it on disk and it reloads on the next apply.
		_ = os.Remove(filepath.Join(s.cacheDir, userPMTiles))
		_ = os.Remove(filepath.Join(s.cacheDir, userManifest))
		w.Header().Set("Content-Type", jsonCT)
		io.WriteString(w, `{"ok":true}`)
	default:
		apiErr(w, http.StatusMethodNotAllowed, "GET or DELETE")
	}
}

// deleteRegion removes ONE region's baked archive (DELETE /api/charts/<NN>) —
// instant, no re-bake of the others. Refused while a job is running.
func (s *Server) deleteRegion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		apiErr(w, http.StatusMethodNotAllowed, "DELETE only")
		return
	}
	if s.task.isRunning() {
		apiErr(w, http.StatusConflict, "busy")
		return
	}
	num, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/api/charts/"))
	if err != nil || !validRegions[num] {
		apiErr(w, http.StatusBadRequest, "bad region")
		return
	}
	if err := DeleteRegion(s.cacheDir, num); err != nil {
		apiErr(w, http.StatusInternalServerError, "delete failed")
		return
	}
	w.Header().Set("Content-Type", jsonCT)
	io.WriteString(w, `{"ok":true}`)
}

// handleDebug is a single-shot debug dump. POST stores the client (web-app) state
// snapshot — selection, inspected feature, view, mariner, etc. — pushed by the
// frontend; GET returns that latest client snapshot alongside live server state
// (version, cache dir, current task, installed coverage, cache listing). It's the
// one place to `curl` for "what is the app showing right now".
func (s *Server) handleDebug(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 16<<20)) // 16 MiB cap (snapshot may carry inspected geometry)
		if !json.Valid(body) {
			apiErr(w, http.StatusBadRequest, "client snapshot must be JSON")
			return
		}
		s.debugMu.Lock()
		s.debugClient = body
		s.debugMu.Unlock()
		w.Header().Set("Content-Type", jsonCT)
		io.WriteString(w, `{"ok":true}`)
		return
	}

	s.debugMu.Lock()
	client := json.RawMessage("null")
	if len(s.debugClient) > 0 {
		client = append(json.RawMessage(nil), s.debugClient...)
	}
	s.debugMu.Unlock()

	userBake := json.RawMessage("null")
	if b, err := os.ReadFile(filepath.Join(s.cacheDir, userManifest)); err == nil && json.Valid(b) {
		userBake = b
	}

	out := map[string]any{
		"version":      s.Version,
		"cache_dir":    s.cacheDir,
		"allow_remote": s.allowRemote,
		"assets":       assetsDesc(s.assetsDir),
		"task":         json.RawMessage(s.task.json()),
		"regions":      json.RawMessage(regionManifest(s.cacheDir)),
		"user_bake":    userBake,
		"cache":        s.debugCacheListing(),
	}
	w.Header().Set("Content-Type", jsonCT)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(map[string]any{"server": out, "client": client})
}

// debugCacheListing summarises the on-disk cache: baked region archives, the
// map-selected bake, and the ENC_ROOT cell cache count.
func (s *Server) debugCacheListing() map[string]any {
	out := map[string]any{}
	var regions []string
	if ents, err := os.ReadDir(regionsDir(s.cacheDir)); err == nil {
		for _, e := range ents {
			regions = append(regions, e.Name())
		}
	}
	out["region_files"] = regions
	if fi, err := os.Stat(filepath.Join(s.cacheDir, userPMTiles)); err == nil {
		out["charts_user_bytes"] = fi.Size()
	} else {
		out["charts_user_bytes"] = nil
	}
	cells := 0
	if ents, err := os.ReadDir(filepath.Join(s.cacheDir, "ENC_ROOT")); err == nil {
		for _, e := range ents {
			if e.IsDir() {
				cells++
			}
		}
	}
	out["enc_root_cells"] = cells
	return out
}

func assetsDesc(dir string) string {
	if dir == "" {
		return "embedded"
	}
	return dir
}

// validRegions is the set of NOAA ENC region numbers (the catalog `rg` values).
var validRegions = map[int]bool{
	2: true, 3: true, 4: true, 6: true, 7: true, 8: true, 10: true, 12: true,
	13: true, 14: true, 15: true, 17: true, 22: true, 24: true, 26: true,
	30: true, 32: true, 34: true, 36: true, 40: true,
}

// provisionStart claims the single job slot and spawns the background bake.
// Body is either {regions:[…]} (preferred — download NOAA's per-region bundle
// zips, the authoritative complete list) or {cells:[…]} (explicit cell list).
// Returns immediately with the task id (+ busy:true if a job is already running).
func (s *Server) provisionStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		apiErr(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Regions []int    `json:"regions"`
		Cells   []string `json:"cells"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&body); err != nil {
		apiErr(w, http.StatusBadRequest, "bad json")
		return
	}

	if len(body.Regions) > 0 {
		if len(body.Regions) > 20 {
			apiErr(w, http.StatusBadRequest, "too many regions")
			return
		}
		for _, n := range body.Regions {
			if !validRegions[n] {
				apiErr(w, http.StatusBadRequest, "bad region")
				return
			}
		}
		id, ok := s.task.tryBegin(len(body.Regions))
		if !ok {
			w.Header().Set("Content-Type", jsonCT)
			fmt.Fprintf(w, `{"ok":true,"task":%d,"busy":true}`, s.task.currentID())
			return
		}
		regions := append([]int(nil), body.Regions...)
		go s.runRegionJob(regions)
		w.Header().Set("Content-Type", jsonCT)
		fmt.Fprintf(w, `{"ok":true,"task":%d}`, id)
		return
	}

	// Cap high enough for a multi-region (even whole-folio) cell list.
	if len(body.Cells) == 0 || len(body.Cells) > 10000 {
		apiErr(w, http.StatusBadRequest, "bad cell count")
		return
	}
	for _, c := range body.Cells {
		if !validCell(c) {
			apiErr(w, http.StatusBadRequest, "bad cell name")
			return
		}
	}

	id, ok := s.task.tryBegin(len(body.Cells))
	if !ok {
		w.Header().Set("Content-Type", jsonCT)
		fmt.Fprintf(w, `{"ok":true,"task":%d,"busy":true}`, s.task.currentID())
		return
	}
	names := append([]string(nil), body.Cells...)
	go s.runProvisionJob(names)

	w.Header().Set("Content-Type", jsonCT)
	fmt.Fprintf(w, `{"ok":true,"task":%d}`, id)
}

func (s *Server) sink() *ProgressSink {
	return &ProgressSink{
		download: func(done, total int, cell string) { s.task.setDownload(done, total, cell) },
		imp:      func(done, total int) { s.task.setImport(done, total) },
	}
}

// runProvisionJob runs ProvisionCore (explicit cell list). The bake is written
// to the XDG cache dir — a user action never writes into the asset bundle.
func (s *Server) runProvisionJob(names []string) {
	if _, err := ProvisionCore(s.cacheDir, names, s.sink()); err != nil {
		s.task.finishErr(sanitizeErr(err))
		return
	}
	s.task.finishOk()
}

// runRegionJob bakes each requested region into its OWN archive in the cache
// (regions/<NN>.pmtiles), skipping any already baked. One pmtiles per region, so
// this only ever bakes the NEW region(s) — never the union.
func (s *Server) runRegionJob(regions []int) {
	sink := s.sink()
	for i, num := range regions {
		s.task.setDownload(i, len(regions), fmt.Sprintf("region %d", num))
		if err := ProvisionRegionToCache(s.cacheDir, num, sink); err != nil {
			s.task.finishErr(sanitizeErr(err))
			return
		}
	}
	s.task.finishOk()
}

// sanitizeErr reduces an error to a short JSON-safe identifier-ish token.
func sanitizeErr(err error) string {
	msg := err.Error()
	if len(msg) > 96 {
		msg = msg[:96]
	}
	return strings.Map(func(r rune) rune {
		if r == '"' || r == '\\' || r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		return r
	}, msg)
}

// serveAsset serves a static frontend file, honouring HTTP Range (via
// http.ServeContent) and adding permissive CORS so the pmtiles:// protocol can
// fetch byte ranges. User-provisioned data is served from the XDG cache dir; the
// rest comes from an on-disk --assets override (if set and present) or the
// embedded bundle.
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

	// A user-provisioned archive/manifest lives in the XDG cache, not the bundle.
	if name == userPMTiles || name == userManifest {
		s.serveFile(w, r, filepath.Join(s.cacheDir, name), rel)
		return
	}

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
// keeps the old region logic after an update). Tiles/atlases are large and change
// only via a fresh provision (cache-busted by ?t=), so they may cache.
func setAssetHeaders(w http.ResponseWriter, rel string) {
	w.Header().Set("Content-Type", mimeFor(rel))
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Expose-Headers", "content-range,accept-ranges,content-length")
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".html", ".js", ".mjs", ".json":
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
