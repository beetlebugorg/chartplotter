package baker

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	tile57 "github.com/beetlebugorg/tile57/bindings/go"
)

// PrepareLive bakes each cell under `input` (a .000 or an ENC_ROOT dir) to its own native-scale
// PMTiles in `cellsDir` (KEPT — the runtime compositor mmaps these directly), and returns the
// per-cell archive paths. There is NO district compose pass: tiles compose on demand at serve
// time (see tile57.OpenCompose). It is the input-prep half of the live compositor.
//
// Incremental: a cell whose per-cell archive already exists, is non-empty, and is at least as new
// as its source .000 is reused — so adding a district re-bakes only its new cells (not the whole
// provider). onProgress(done, total, cell) fires before each cell actually baked (nil to skip);
// onSkip reports a cell that failed (nil to skip). Returns an error (not an empty slice) only when
// cells were present but none are available.
func PrepareLive(input, cellsDir string, onProgress func(done, total int, cell string), onSkip func(cell string, err error)) ([]string, error) {
	cells, err := ListCells(input)
	if err != nil {
		return nil, err
	}
	if len(cells) == 0 {
		return nil, nil
	}
	if err := os.MkdirAll(cellsDir, 0o755); err != nil {
		return nil, err
	}
	perCell := make([]string, 0, len(cells))
	for i, cp := range cells {
		stem := strings.TrimSuffix(filepath.Base(cp), ".000")
		pc := filepath.Join(cellsDir, stem+".pmtiles")
		// Reuse an up-to-date archive: present, non-empty, and not older than its source cell (a
		// re-downloaded district rewrites the .000, so its mtime advances → we re-bake it).
		if fi, err := os.Stat(pc); err == nil && fi.Size() > 0 {
			if si, err := os.Stat(cp); err == nil && !si.ModTime().After(fi.ModTime()) {
				perCell = append(perCell, pc)
				continue
			}
		}
		if onProgress != nil {
			onProgress(i, len(cells), stem)
		}
		b, err := tile57.BakeCell(cp)
		if err != nil {
			if onSkip != nil {
				onSkip(filepath.Base(cp), err)
			}
			continue
		}
		if len(b) == 0 {
			continue
		}
		if err := os.WriteFile(pc, b, 0o644); err != nil {
			if onSkip != nil {
				onSkip(filepath.Base(cp), err)
			}
			continue
		}
		// Content sha of the archive, written beside it (<archive>.sha), for the provider's
		// content-addressed cache-bust token (a sha-of-shas). A reused cell keeps its sidecar,
		// so a re-key reads N tiny sidecars instead of re-hashing the whole archive set.
		sum := sha256.Sum256(b)
		_ = os.WriteFile(pc+".sha", []byte(hex.EncodeToString(sum[:])), 0o644)
		perCell = append(perCell, pc)
	}
	if len(perCell) == 0 {
		return nil, fmt.Errorf("bake failed for all %d cell(s)", len(cells))
	}
	if onProgress != nil {
		onProgress(len(cells), len(cells), "")
	}
	return perCell, nil
}

// ListCells returns every base cell (.000) path under `root` (a single file or a directory),
// deduped by stem (a boundary cell shared by two districts bakes once).
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
