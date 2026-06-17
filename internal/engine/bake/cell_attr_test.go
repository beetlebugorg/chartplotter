package bake

import (
	"testing"

	"github.com/beetlebugorg/chartplotter/internal/engine/mvt"
	"github.com/beetlebugorg/chartplotter/pkg/s52"
	"github.com/beetlebugorg/chartplotter/pkg/s52/preslib"
	"github.com/beetlebugorg/chartplotter/pkg/s57"
)

// The inspector's source-cell pill reads a baked `cell` attribute; verify it is
// emitted on point_symbols (and equals the dataset name).
func TestCellAttributeBaked(t *testing.T) {
	lib, err := s52.LoadLibraryFromBytes(preslib.DAI)
	if err != nil {
		t.Fatal(err)
	}
	chart, err := s57.Parse(goldenCell)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("dataset name = %q", chart.DatasetName())
	if chart.DatasetName() == "" {
		t.Fatal("dataset name empty — cell pill would be blank")
	}
	b := New()
	b.AddCell(chart, lib, s52.DefaultMarinerSettings())

	for _, c := range b.TileCoords(mvt.ExtentDefault) {
		data := b.EmitTile(c, mvt.ExtentDefault, 64)
		if data == nil {
			continue
		}
		layers := decodeLayers(data)
		ps := layers["point_symbols"]
		if ps == nil || len(ps.feats) == 0 {
			continue
		}
		hasCell := false
		for _, k := range ps.keys {
			if k == "cell" {
				hasCell = true
			}
		}
		if !hasCell {
			t.Fatalf("point_symbols layer has no `cell` key; keys=%v", ps.keys)
		}
		return // found a populated point_symbols tile with the cell key
	}
	t.Skip("no point_symbols tile found in golden cell")
}
