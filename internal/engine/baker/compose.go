package baker

import (
	"io/fs"
	"path/filepath"
	"strings"

	tile57 "github.com/beetlebugorg/tile57/bindings/go"
)

// PrepareLive walks the ENC data under `input` and bakes each S-57 base cell (*.000) IN PARALLEL to
// the SAME relative path under `cellsDir` with a .pmtiles extension (plus a .sha content sidecar),
// creating subdirs — the mirrored tree the runtime compositor mmaps directly (there is NO district
// compose pass; tiles compose on demand). A cell whose archive is already up to date (newer than its
// .000 and its update chain) is skipped, so re-baking a provider only bakes what changed. The engine
// writes and frees each archive as it goes, so this never holds N archives in memory (peak ~
// workers). `workers` bounds concurrency (a MEMORY bound). `onProgress(done, total)` fires per baked
// cell for the import UI (may be called concurrently). Returns the number of cells baked this pass.
func PrepareLive(input, cellsDir string, workers int, onProgress func(done, total int)) (int, error) {
	return tile57.BakeTree(input, cellsDir, workers, onProgress)
}

// ListCells returns every base cell (.000) path under `root` (a single file or a directory),
// deduped by stem (a boundary cell shared by two districts appears once).
func ListCells(root string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".000") {
			return nil
		}
		stem := strings.TrimSuffix(filepath.Base(path), ".000")
		if seen[stem] {
			return nil
		}
		seen[stem] = true
		out = append(out, path)
		return nil
	})
	return out, err
}
