package baker

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	tile57 "github.com/beetlebugorg/tile57/bindings/go"
)

// ComposeENCRoot is the per-cell COMPOSITE bake: it bakes each cell under `input` (a single
// .000 or an ENC_ROOT directory) to its own native-scale PMTiles (coverage embedded in the
// metadata), then streams them through the engine's ownership partition into `outPath`. Per-cell
// archives go to a temp dir (mmap'd by the compositor, then discarded), so the whole cell set is
// never resident. This replaces the in-bake cross-cell combiner (tile57.BakeBundle) — the tiles
// half only; a host builds the style dynamically and serves global assets.
//
// onProgress(done, total) is called before each cell bake (done 0..total-1) and once more with
// done==total when the bakes are finished and the partition compose begins; nil to skip.
// onCompose (nil to skip) then reports live progress THROUGH that partition compose as it walks
// the zoom ladder (a smooth Done/Total fraction). onSkip (nil to skip) reports a cell that failed
// to bake. Returns the count of cells that contributed; an error (not 0) is returned when cells
// were present but none baked.
func ComposeENCRoot(input, outPath string, onProgress func(done, total int), onCompose func(tile57.ComposeProgress), onSkip func(cell string, err error)) (int, error) {
	cells, err := ListCells(input)
	if err != nil {
		return 0, err
	}
	if len(cells) == 0 {
		return 0, nil
	}

	cellsDir, err := os.MkdirTemp("", "tile57-cells-*")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(cellsDir)

	// 1. Bake each cell to its own PMTiles (one cell resident at a time — the bytes are freed as
	//    soon as they are written).
	perCell := make([]string, 0, len(cells))
	for i, cp := range cells {
		if onProgress != nil {
			onProgress(i, len(cells))
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
		pc := filepath.Join(cellsDir, filepath.Base(cp)+".pmtiles")
		if err := os.WriteFile(pc, b, 0o644); err != nil {
			if onSkip != nil {
				onSkip(filepath.Base(cp), err)
			}
			continue
		}
		perCell = append(perCell, pc)
	}
	if len(perCell) == 0 {
		// Cells were present but none baked → a bake ERROR, not empty coverage (so the caller
		// fails without dropping the provider/source as "no coverage").
		return 0, fmt.Errorf("bake failed for all %d cell(s)", len(cells))
	}

	// 2. Stream-compose the per-cell archives into outPath via the ownership partition.
	if onProgress != nil {
		onProgress(len(cells), len(cells))
	}
	if dir := filepath.Dir(outPath); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return 0, err
		}
	}
	return tile57.ComposeFiles(perCell, outPath, onCompose)
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
