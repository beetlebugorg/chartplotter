//go:build tile57

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tile57 "github.com/beetlebugorg/chartplotter-native/bindings/go"
	"github.com/beetlebugorg/chartplotter/internal/engine/bake"
	"github.com/beetlebugorg/chartplotter/internal/engine/baker"
)

// bakeTile57Bundle bakes an on-disk ENC input (a .000 cell or a directory of cells)
// into a self-contained chart bundle under outDir via the native libtile57 engine.
// maxZoom caps the highest baked zoom (0 = no cap). progress nil uses the lib's
// built-in console progress. Returns the cell count + bbox (west,south,east,north).
func bakeTile57Bundle(input, outDir string, maxZoom int, progress func(tile57.BakeProgress)) (int, [4]float64, error) {
	mz := uint8(24) // ABI: 0/24 means "no clamp"; only narrow when the user caps it
	if maxZoom > 0 && maxZoom < 24 {
		mz = uint8(maxZoom)
	}
	return tile57.BakeBundle(input, outDir, "", "", "", 0, mz, tile57.PickInclude, progress)
}

// runTile57Bands bakes the ENC inputs into one gap-clipped PMTiles archive PER
// navigational band (<out-stem>-<slug>.pmtiles) with the native libtile57 engine,
// mirroring the Go baker's --bands output so the frontend loads each into its
// chart-<slug> source. Cells are grouped into bands by compilation scale (the same
// BandForScale mapping the Go baker uses); each band's cell subset is baked on its
// own via tile57.BakeCells, so the archive is naturally clipped to that band's
// coverage. Cross-band best-available (coarse fills finer gaps, none bleed) is then
// composed CLIENT-side across the per-band sources, exactly as for the Go baker's
// per-band archives. Honors --max-zoom and --overzoom; writes --manifest + aux.zip.
func (c bakeCmd) runTile57Bands() error {
	cells, aux, err := collectCells(c.In)
	if err != nil {
		return err
	}
	if len(cells) == 0 {
		return fmt.Errorf("no .000 base cells found in: %s", strings.Join(c.In, ", "))
	}

	// Per-cell compilation scale (cheap coverage-only parse) → band + coverage bbox.
	metas := baker.ExtractCellMeta(cells, func(name string, err error) {
		fmt.Fprintf(os.Stderr, "  skip %s: %v\n", name, err)
	})

	type bandAcc struct {
		cells []tile57.CellInput
		bbox  bbox4
	}
	byBand := map[bake.Band]*bandAcc{}
	for name, cd := range cells { // name is "<stem>.000"
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		scale := 0
		if m, ok := metas[stem]; ok {
			scale = m.Scale
		}
		band := bake.BandForScale(uint32(scale))
		acc := byBand[band]
		if acc == nil {
			acc = &bandAcc{bbox: emptyBBox()}
			byBand[band] = acc
		}
		acc.cells = append(acc.cells, tile57.CellInput{
			Base:    cd.Base,
			Updates: orderedUpdates(cd.Updates),
			Name:    stem,
		})
		if m, ok := metas[stem]; ok && m.HasBBox {
			acc.bbox = unionBBox(acc.bbox, m.BBox)
		}
	}

	ext := filepath.Ext(c.Out)
	stem := strings.TrimSuffix(c.Out, ext)
	var entries []map[string]any
	overall := emptyBBox()

	// Bake each band coarse→fine (BakeBands order matches the Band enum), writing
	// <stem>-<slug><ext>. minZ/maxZ clamp the archive to the band's native zoom span
	// (--overzoom floats every band down to the world view; --max-zoom caps the top).
	for i, bb := range bake.BakeBands() {
		acc := byBand[bake.Band(i)]
		if acc == nil || len(acc.cells) == 0 {
			continue
		}
		minZ := uint8(bb.Min)
		if c.Overzoom {
			minZ = 0
		}
		maxZ := uint8(bb.Max)
		if c.MaxZoom > 0 && uint32(c.MaxZoom) < bb.Max {
			maxZ = uint8(c.MaxZoom)
		}
		data, err := tile57.BakeCells(acc.cells, "", minZ, maxZ, tile57.PickInclude, nil)
		if err != nil {
			// A band whose cells cover nothing at its zooms produces no tiles; skip it
			// (the same as the Go baker emitting no archive for an empty band).
			fmt.Fprintf(os.Stderr, "  %-9s — no tiles (%v)\n", bb.Slug, err)
			continue
		}
		out := stem + "-" + bb.Slug + ext
		if err := os.WriteFile(out, data, 0o644); err != nil {
			return err
		}
		st, _ := os.Stat(out)
		fmt.Printf("  %-9s → %s (%d cell(s), %.1f MB)\n", bb.Slug, out, len(acc.cells), float64(st.Size())/(1<<20))
		entries = append(entries, map[string]any{
			"file":   filepath.Base(out),
			"band":   bb.Slug,
			"bounds": acc.bbox.slice(),
		})
		overall = unionBBox(overall, acc.bbox.slice())
	}
	if len(entries) == 0 {
		return fmt.Errorf("no cells produced tiles in any band")
	}
	fmt.Printf("baked %d cell(s) → %d band archive(s) via libtile57\n", len(cells), len(entries))

	auxFile, err := writeAuxZip(stem, aux)
	if err != nil {
		return err
	}

	if c.Manifest != "" {
		man := map[string]any{"districts": entries}
		if auxFile != "" {
			man["aux"] = auxFile
		}
		if err := writeManifestJSON(c.Manifest, man); err != nil {
			return err
		}
		fmt.Printf("wrote manifest %s\n", c.Manifest)
	}
	return nil
}

// bbox4 is a running [west,south,east,north] union.
type bbox4 struct {
	minLon, minLat, maxLon, maxLat float64
	set                            bool
}

func emptyBBox() bbox4 { return bbox4{} }

func unionBBox(b bbox4, o [4]float64) bbox4 {
	if o[2] <= o[0] || o[3] <= o[1] { // degenerate
		return b
	}
	if !b.set {
		return bbox4{o[0], o[1], o[2], o[3], true}
	}
	if o[0] < b.minLon {
		b.minLon = o[0]
	}
	if o[1] < b.minLat {
		b.minLat = o[1]
	}
	if o[2] > b.maxLon {
		b.maxLon = o[2]
	}
	if o[3] > b.maxLat {
		b.maxLat = o[3]
	}
	return b
}

func (b bbox4) slice() [4]float64 {
	return [4]float64{b.minLon, b.minLat, b.maxLon, b.maxLat}
}
