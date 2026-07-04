package server

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/beetlebugorg/chartplotter/internal/engine/baker"
	tile57 "github.com/beetlebugorg/tile57/bindings/go"
)

// provider-enc-root storage model (specs/provider-enc-root.md): one baked archive
// per PROVIDER (noaa / ienc / user); DISTRICTS (d5, a river, an upload id) are
// download/delete subfolders under the provider's ENC_ROOT, NOT separate sets. A
// provider is baked as ONE tile57 bundle from its whole ENC_ROOT tree, so
// best-available across cells/districts is a per-feature decision inside a single
// archive (the baker's finestCsclAt) instead of a fragile cross-source composition.
//
// A client-facing pack key stays "<provider>-<district>" (e.g. "noaa-d5"); the
// server splits it into the provider (the baked SET name) + the district (the
// ENC_ROOT subfolder). A bare key with no "-" is already a provider.

// providerOf returns the provider component of a pack key ("noaa" from "noaa-d5"),
// lowercased — this is the baked SET name (one archive per provider). A key with no
// "-" is returned lowercased as-is (already a bare provider).
func providerOf(key string) string {
	if i := strings.IndexByte(key, '-'); i > 0 {
		return strings.ToLower(key[:i])
	}
	return strings.ToLower(key)
}

// districtOf returns the district component of a pack key ("d5" from "noaa-d5"), or
// "" when the key is a bare provider. The district is a management/delete label —
// the ENC_ROOT subfolder name.
func districtOf(key string) string {
	if i := strings.IndexByte(key, '-'); i > 0 && i < len(key)-1 {
		return key[i+1:]
	}
	return ""
}

// isDistrictName accepts a safe single path component for a district subfolder (same
// rules as a set name: letters, digits, '-', '_', '.', no separators or traversal).
func isDistrictName(d string) bool { return isSetName(d) }

// providerDataDir is <DATA>/<PROVIDER>/ — the provider's persistent home (holding its
// ENC_ROOT source tree). Uppercased to match the pre-existing per-provider layout.
func (s *Server) providerDataDir(provider string) string {
	return filepath.Join(s.dataDir, strings.ToUpper(provider))
}

// encRootDir is <DATA>/<PROVIDER>/ENC_ROOT/ — the bake input, walked recursively by
// tile57.BakeBundle for every *.000 across all installed district subfolders. It
// lives under the DATA dir (persistent, survives a cache wipe: it is the downloads'
// home + the bake input), so ClearCache never touches it.
func (s *Server) encRootDir(provider string) string {
	return filepath.Join(s.providerDataDir(provider), "ENC_ROOT")
}

// districtDir is <DATA>/<PROVIDER>/ENC_ROOT/<district>/ — one district's cells; the
// download/delete unit. Downloads write cells (+ updates + aux + a _catalog.json
// title sidecar) here; delete removes the whole subfolder and re-bakes the provider.
func (s *Server) districtDir(provider, district string) string {
	return filepath.Join(s.encRootDir(provider), district)
}

// providerDistricts lists the installed district subfolders under a provider's
// ENC_ROOT (sorted) — the district→cell map is the folder listing itself, so the UI
// reads "which districts are installed" straight from disk.
func (s *Server) providerDistricts(provider string) []string {
	entries, err := os.ReadDir(s.encRootDir(provider))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && isDistrictName(e.Name()) {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// installedProviders lists every provider that currently has a non-empty ENC_ROOT
// (at least one district folder on disk), sorted — the set of bakeable providers.
func (s *Server) installedProviders() []string {
	entries, err := os.ReadDir(s.dataDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		provider := strings.ToLower(e.Name())
		if !isSetName(provider) {
			continue
		}
		if len(s.providerDistricts(provider)) > 0 {
			out = append(out, provider)
		}
	}
	sort.Strings(out)
	return out
}

// districtCatFile is the per-district sidecar of parsed CATALOG.031 titles (the raw
// catalogue isn't kept at the ENC_ROOT root — that would flip BakeBundle into
// catalog-only mode — so titles are stashed here and re-gathered per provider bake).
const districtCatFile = "_catalog.json"

// cacheDistrict writes one district's downloaded exchange-set content into its
// ENC_ROOT subfolder: each cell FLAT as <STEM>.000 (+ .001… updates), aux content
// files (TXTDSC/PICREP) flat beside them, and a _catalog.json of parsed titles. The
// bake reads the whole provider ENC_ROOT from here (no temp staging); the flat layout
// is transparent to BakeBundle's recursive *.000 walk. Best-effort; errors logged.
func (s *Server) cacheDistrict(provider, district string, cells map[string]baker.CellData, aux map[string][]byte, cat []tile57.CatalogEntry) {
	dir := s.districtDir(provider, district)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	// Drop any previously-cached updates for the stems we're about to (re)write, so
	// the persisted source matches EXACTLY the edition written now (a ?updates=0
	// re-import must not leave stale .001+ on disk for the disk-read bake to apply).
	stems := make(map[string]bool, len(cells))
	for name := range cells {
		stems[strings.TrimSuffix(name, ".000")] = true
	}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if ext := encExtServer(e.Name()); ext != "" && ext != ".000" && stems[strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))] {
				_ = os.Remove(filepath.Join(dir, e.Name()))
			}
		}
	}
	for name, cd := range cells {
		stem := strings.TrimSuffix(name, ".000")
		if !isCellName(stem) {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, stem+".000"), cd.Base, 0o644); err != nil {
			continue
		}
		for un, ub := range cd.Updates {
			_ = os.WriteFile(filepath.Join(dir, filepath.Base(un)), ub, 0o644)
		}
	}
	for name, b := range aux {
		if isAuxContentServer(name) {
			_ = os.WriteFile(filepath.Join(dir, filepath.Base(name)), b, 0o644)
		}
	}
	if len(cat) > 0 {
		if b, err := json.Marshal(cat); err == nil {
			_ = os.WriteFile(filepath.Join(dir, districtCatFile), b, 0o644)
		}
	}
	if s.cellIdx != nil {
		stems := make([]string, 0, len(cells))
		for name := range cells {
			stems = append(stems, strings.TrimSuffix(name, ".000"))
		}
		s.cellIdx.forget(stems) // re-imported cells: drop stale bounds so the rebuild re-parses
		s.cellIdx.rebuild()     // re-index in the background (single-flight; a mid-scan rebuild re-runs)
	}
}

// providerCellData reads every base cell (+ its .001… updates) under a provider's
// ENC_ROOT, DE-DUPLICATED by stem (a boundary cell shared by two districts is read
// once), for the bake's metadata sidecar + cell manifest. The bake itself reads the
// tree directly (BakeBundle); this is only the in-memory view the register tail needs.
func (s *Server) providerCellData(provider string) map[string]baker.CellData {
	root := s.encRootDir(provider)
	cells := map[string]baker.CellData{}      // stem+".000" → base
	updates := map[string]map[string][]byte{} // stem → update-name → bytes
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		ext := encExtServer(d.Name())
		if ext == "" {
			return nil
		}
		stem := strings.TrimSuffix(d.Name(), filepath.Ext(d.Name()))
		if !isCellName(stem) {
			return nil
		}
		if ext == ".000" {
			if _, seen := cells[stem+".000"]; seen {
				return nil // dedup: first district's copy wins
			}
			if b, e := os.ReadFile(path); e == nil {
				cells[stem+".000"] = baker.CellData{Base: b}
			}
			return nil
		}
		if updates[stem] == nil {
			updates[stem] = map[string][]byte{}
		}
		if _, seen := updates[stem][d.Name()]; !seen {
			if b, e := os.ReadFile(path); e == nil {
				updates[stem][d.Name()] = b
			}
		}
		return nil
	})
	for name, cd := range cells {
		if u := updates[strings.TrimSuffix(name, ".000")]; len(u) > 0 {
			cd.Updates = u
			cells[name] = cd
		}
	}
	return cells
}

// providerAux gathers the aux content files (TXTDSC/PICREP text + pictures) across a
// provider's ENC_ROOT, de-duplicated by upper-cased basename, for the provider's one
// companion aux.zip.
func (s *Server) providerAux(provider string) map[string][]byte {
	root := s.encRootDir(provider)
	aux := map[string][]byte{}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if encExtServer(d.Name()) != "" || isCatalogFile(d.Name()) || !isAuxContentServer(d.Name()) {
			return nil
		}
		k := strings.ToUpper(d.Name())
		if _, ok := aux[k]; !ok {
			if b, e := os.ReadFile(path); e == nil {
				aux[k] = b
			}
		}
		return nil
	})
	return aux
}

// providerCatalog merges the parsed CATALOG.031 title entries stashed per district
// (_catalog.json) across a provider's ENC_ROOT, for the provider's metadata sidecar
// (per-cell titles). Missing/broken sidecars are skipped.
func (s *Server) providerCatalog(provider string) []tile57.CatalogEntry {
	root := s.encRootDir(provider)
	var cat []tile57.CatalogEntry
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != districtCatFile {
			return nil
		}
		if b, e := os.ReadFile(path); e == nil {
			var entries []tile57.CatalogEntry
			if json.Unmarshal(b, &entries) == nil {
				for i := range entries {
					// HasBBox is derived (json:"-"), so reconstitute it from a non-zero bbox.
					entries[i].HasBBox = entries[i].BBox != [4]float64{}
				}
				cat = append(cat, entries...)
			}
		}
		return nil
	})
	return cat
}
