package server

import (
	"testing"

	"github.com/beetlebugorg/chartplotter/internal/engine/baker"
	tile57 "github.com/beetlebugorg/tile57/bindings/go"
)

func TestCommonPrefixIdentity(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"US5MD1MC"}, "us5md1mc"},                      // single cell → full stem
		{[]string{"US5MD1MC", "US5MD2NW", "US5MD3SE"}, "us5md"}, // shared prefix
		{[]string{"US5MD1MC", "GB5X01SW"}, ""},                  // no usable prefix
		{nil, ""},
	}
	for _, c := range cases {
		if got := commonPrefixIdentity(c.in); got != c.want {
			t.Errorf("commonPrefixIdentity(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBuildSetMeta_CatalogOverlay(t *testing.T) {
	cellMeta := map[string]baker.CellMeta{
		"US5MD1MC": {Name: "US5MD1MC", Title: "US5MD1MC", Scale: 12000, Agency: 550, IssueDate: "20251030", BBox: [4]float64{-76.5, 38.9, -76.4, 39.0}, HasBBox: true},
		"US5MD2NW": {Name: "US5MD2NW", Title: "US5MD2NW", Scale: 20000, Agency: 550},
	}
	cat := []tile57.CatalogEntry{
		{File: "US5MD1MC\\US5MD1MC.000", Impl: "BIN", LongName: "Annapolis Harbor", HasBBox: true, BBox: [4]float64{-76.5, 38.9, -76.4, 39.0}},
		{File: "US5MD2NW\\US5MD2NW.000", Impl: "BIN", LongName: "Chesapeake Bay", HasBBox: true, BBox: [4]float64{-76.6, 39.0, -76.4, 39.2}},
	}

	m := buildSetMeta("user-us5md", cellMeta, cat)

	if m.CellCount != 2 {
		t.Errorf("CellCount = %d, want 2", m.CellCount)
	}
	if m.ScaleMin != 12000 || m.ScaleMax != 20000 {
		t.Errorf("scale range = [%d,%d], want [12000,20000]", m.ScaleMin, m.ScaleMax)
	}
	if m.Agency != "NOAA (US)" {
		t.Errorf("Agency = %q, want NOAA (US)", m.Agency)
	}
	// Union bbox: catalogue supplied US5MD2NW's box (header had none).
	if len(m.BBox) != 4 || m.BBox[1] != 38.9 || m.BBox[3] != 39.2 {
		t.Errorf("BBox = %v, want union [-76.6,38.9,-76.4,39.2]", m.BBox)
	}
	// Catalogue long names overlay the dataset-name fallback.
	titles := map[string]string{}
	for _, c := range m.Cells {
		titles[c.Name] = c.Title
	}
	if titles["US5MD1MC"] != "Annapolis Harbor" || titles["US5MD2NW"] != "Chesapeake Bay" {
		t.Errorf("titles not overlaid from catalogue: %v", titles)
	}
}

func TestBuildSetMeta_SingleCellTitle(t *testing.T) {
	cellMeta := map[string]baker.CellMeta{
		"US5MD1MC": {Name: "US5MD1MC", Title: "Annapolis Harbor", Scale: 12000},
	}
	m := buildSetMeta("user-us5md1mc", cellMeta, nil)
	if m.Title != "Annapolis Harbor" {
		t.Errorf("Title = %q, want Annapolis Harbor", m.Title)
	}
}
