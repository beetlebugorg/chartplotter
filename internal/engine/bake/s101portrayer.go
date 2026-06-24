package bake

import (
	"io/fs"

	"github.com/beetlebugorg/chartplotter/internal/engine/portrayal"
	"github.com/beetlebugorg/chartplotter/pkg/s57"
)

// Portrayer produces the portrayal passes for one S-57 feature. It is the seam
// the bake pipeline drives portrayal through: the S-101 rule engine, selected
// via SetPortrayer. A portrayer is required.
type Portrayer interface {
	Passes(f *s57.Feature) []portrayal.FeatureBuildPass
}

// BatchPortrayer is a Portrayer that precomputes a whole cell's features at
// once. AddCell calls Begin(features) before iterating and End() after. The
// S-101 engine implements this so it runs ONE portrayal pass per cell (one Lua
// chunk compile, one context) with a fresh Lua state per cell — instead of
// per-feature, which recompiled + leaked the catalogue's file-local caches.
type BatchPortrayer interface {
	Portrayer
	Begin(features []*s57.Feature)
	End()
}

// SetPortrayer installs the portrayal engine on the Baker. Set the S-101
// portrayer (NewS101Portrayer) to bake with S-101 symbology. Set before AddCell.
func (b *Baker) SetPortrayer(p Portrayer) { b.portrayer = p }

// s101Portrayer adapts portrayal.S101Builder to the Portrayer seam. S-101
// handles the boundary-style / point-style variants via context parameters
// inside the rules, so it emits a single style-independent pass (bnd/pts =
// common) rather than the S-52 plain/symbolized + simplified/paper split.
type s101Portrayer struct {
	builder *portrayal.S101Builder
	cache   map[int64]portrayal.FeatureBuild // per-cell results, keyed by feature ID
}

// NewS101Portrayer builds the S-101 portrayer from a PortrayalCatalog directory
// and a FeatureCatalogue.xml path.
func NewS101Portrayer(portrayalCatalogDir, featureCataloguePath string) (Portrayer, error) {
	bld, err := portrayal.NewS101Builder(portrayalCatalogDir, featureCataloguePath)
	if err != nil {
		return nil, err
	}
	return &s101Portrayer{builder: bld}, nil
}

// LinestyleTable builds the complex-line dash/symbol period geometry from the
// S-101 catalogue's LineStyles — what the baker's emitComplexLine tessellates.
// This is the S-101 source that replaced the S-52 PresLib linestyle table.
func (p *s101Portrayer) LinestyleTable() map[string]*lsInfo {
	return buildLinestyleTableFromCatalog(p.builder.Catalog)
}

// linestyleSource is an optional Portrayer capability: the complex-line period
// geometry keyed by line-style name (the S-101 catalogue provides it).
type linestyleSource interface {
	LinestyleTable() map[string]*lsInfo
}

// NewS101PortrayerFS builds the S-101 portrayer from an in-memory PortrayalCatalog
// FS + FeatureCatalogue.xml bytes — the build-time embedded catalogue path.
func NewS101PortrayerFS(catalogFS fs.FS, featureCatalogueXML []byte) (Portrayer, error) {
	bld, err := portrayal.NewS101BuilderFS(catalogFS, featureCatalogueXML)
	if err != nil {
		return nil, err
	}
	return &s101Portrayer{builder: bld}, nil
}

// Begin portrays the whole cell up front (one engine pass) and caches results.
func (p *s101Portrayer) Begin(features []*s57.Feature) {
	m, err := p.builder.BuildBatch(features)
	if err != nil {
		p.cache = nil // Passes falls back to per-feature builds
		return
	}
	p.cache = m
}

// End drops the per-cell cache so its memory is released before the next cell.
func (p *s101Portrayer) End() { p.cache = nil }

func (p *s101Portrayer) Passes(f *s57.Feature) []portrayal.FeatureBuildPass {
	if p.cache != nil {
		fb, ok := p.cache[f.ID()]
		if !ok {
			return nil
		}
		return []portrayal.FeatureBuildPass{{Build: fb, Bnd: portrayal.BndCommon, Pts: portrayal.PtsCommon}}
	}
	// Fallback (Begin not called): single-feature build.
	build, ok := p.builder.Build(f)
	if !ok {
		return nil
	}
	return []portrayal.FeatureBuildPass{{Build: build, Bnd: portrayal.BndCommon, Pts: portrayal.PtsCommon}}
}
