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

// s101Portrayer adapts portrayal.S101Builder to the Portrayer seam. The S-101
// rules branch on the PlainBoundaries / SimplifiedSymbols context parameters, so
// to let the client toggle boundary style (areas) and point-symbol style live we
// portray the cell THREE times — default (symbolized boundaries + paper symbols),
// PlainBoundaries=true, and SimplifiedSymbols=true — and emit per-feature variant
// passes (bnd 0/1 for areas, pts 0/1 for points). Features that don't vary stay a
// single common pass.
type s101Portrayer struct {
	builder    *portrayal.S101Builder
	cache      map[int64]portrayal.FeatureBuild // default: symbolized boundaries + paper symbols
	plain      map[int64]portrayal.FeatureBuild // PlainBoundaries=true (area boundaries)
	simplified map[int64]portrayal.FeatureBuild // SimplifiedSymbols=true (point symbols)
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

// Begin portrays the whole cell up front and caches results. It runs the default
// pass plus the PlainBoundaries / SimplifiedSymbols variant passes so the client
// can toggle boundary + point-symbol style live. Variant passes are best-effort:
// if one fails, that axis degrades to the common pass.
func (p *s101Portrayer) Begin(features []*s57.Feature) {
	m, err := p.builder.BuildBatch(features)
	if err != nil {
		p.cache = nil // Passes falls back to per-feature builds
		return
	}
	p.cache = m
	if v, err := p.builder.BuildBatchOverrides(features, map[string]string{"PlainBoundaries": "true"}); err == nil {
		p.plain = v
	}
	if v, err := p.builder.BuildBatchOverrides(features, map[string]string{"SimplifiedSymbols": "true"}); err == nil {
		p.simplified = v
	}
}

// End drops the per-cell caches so their memory is released before the next cell.
func (p *s101Portrayer) End() { p.cache, p.plain, p.simplified = nil, nil, nil }

func (p *s101Portrayer) Passes(f *s57.Feature) []portrayal.FeatureBuildPass {
	if p.cache == nil {
		// Fallback (Begin not called): single-feature build, no variants.
		build, ok := p.builder.Build(f)
		if !ok {
			return nil
		}
		return []portrayal.FeatureBuildPass{{Build: build, Bnd: portrayal.BndCommon, Pts: portrayal.PtsCommon}}
	}
	def, ok := p.cache[f.ID()]
	if !ok {
		return nil
	}
	switch f.Geometry().Type {
	case s57.GeometryTypePolygon:
		// Area boundaries: symbolized (default, bnd=1) + plain (bnd=0) so the
		// client's "Area boundaries" toggle switches between them. Falls back to
		// one common pass if the plain build is unavailable.
		if pl, ok := p.plain[f.ID()]; ok {
			return []portrayal.FeatureBuildPass{
				{Build: def, Bnd: portrayal.BndSymbolized, Pts: portrayal.PtsCommon},
				{Build: pl, Bnd: portrayal.BndPlain, Pts: portrayal.PtsCommon},
			}
		}
	case s57.GeometryTypePoint:
		// Point symbols: paper-chart (default, pts=0) + simplified (pts=1). SOUNDG
		// digit glyphs don't vary, so leave them a common pass (no doubling).
		if f.ObjectClass() != "SOUNDG" {
			if sm, ok := p.simplified[f.ID()]; ok {
				return []portrayal.FeatureBuildPass{
					{Build: def, Bnd: portrayal.BndCommon, Pts: portrayal.PtsPaper},
					{Build: sm, Bnd: portrayal.BndCommon, Pts: portrayal.PtsSimplified},
				}
			}
		}
	}
	return []portrayal.FeatureBuildPass{{Build: def, Bnd: portrayal.BndCommon, Pts: portrayal.PtsCommon}}
}
