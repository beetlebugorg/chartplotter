package portrayal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/beetlebugorg/chartplotter/pkg/geo"
	"github.com/beetlebugorg/chartplotter/pkg/s57"
)

func s101Builder(t *testing.T) *S101Builder {
	t.Helper()
	pc := os.Getenv("S101_CATALOG")
	if pc == "" {
		pc = "/home/jcollins/Projects/s101-portrayal-catalogue/PortrayalCatalog"
	}
	fcPath := os.Getenv("S101_FC")
	if fcPath == "" {
		fcPath = "/home/jcollins/Projects/s101-feature-catalogue/S-101FC/FeatureCatalogue.xml"
	}
	if _, err := os.Stat(filepath.Join(pc, "Rules", "main.lua")); err != nil {
		t.Skipf("S-101 catalogue not present; set S101_CATALOG/S101_FC")
	}
	if _, err := os.Stat(fcPath); err != nil {
		t.Skipf("S-101 feature catalogue not present")
	}
	b, err := NewS101Builder(pc, fcPath)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// s101BuilderEmbedded builds from the in-repo embedded catalogue (the one the
// baker ships), so the test runs without an external catalogue checkout.
func s101BuilderEmbedded(t *testing.T) *S101Builder {
	t.Helper()
	pc := "../s101catalog/catalog/PortrayalCatalog"
	fcPath := "../s101catalog/catalog/FeatureCatalogue.xml"
	if _, err := os.Stat(filepath.Join(pc, "Rules", "main.lua")); err != nil {
		t.Skip("no embedded catalogue")
	}
	b, err := NewS101Builder(pc, fcPath)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestS101BuildDateDependent: a seasonal buoy (S-57 PERSTA/PEREND) is portrayed
// date-dependent — buildFeature surfaces the periodic range on the FeatureBuild
// and the CHDATD01 date-dependent marker symbol is emitted (the S-101
// ProcessFixedAndPeriodicDates path wired into the engine).
func TestS101BuildDateDependent(t *testing.T) {
	b := s101BuilderEmbedded(t)
	buoy := s57.NewFeature(1, "BOYLAT",
		s57.Geometry{Type: s57.GeometryTypePoint, Coordinates: [][]float64{{-76.3, 38.9}}},
		map[string]interface{}{"CATLAM": 2, "BOYSHP": 2, "COLOUR": "3", "PERSTA": "--0301", "PEREND": "--1201"},
	)
	build, ok := b.Build(&buoy)
	if !ok {
		t.Fatal("build failed")
	}
	if build.DateStart != "--0301" || build.DateEnd != "--1201" {
		t.Errorf("date range = %q..%q, want --0301..--1201", build.DateStart, build.DateEnd)
	}
	if build.TimeValid != "closedInterval" {
		t.Errorf("TimeValid = %q, want closedInterval", build.TimeValid)
	}
	hasMarker := false
	for _, p := range build.Primitives {
		if sc, ok := p.(SymbolCall); ok && sc.SymbolName == "CHDATD01" {
			hasMarker = true
		}
	}
	if !hasMarker {
		t.Errorf("no CHDATD01 date-dependent marker emitted; got %#v", build.Primitives)
	}
}

// TestS101BuildPointSymbol drives a real S-57 feature through the full build
// seam: S-57 acronyms → S-101 rule → instructions → geometry-placed Primitive.
func TestS101BuildPointSymbol(t *testing.T) {
	b := s101Builder(t)

	pt := s57.NewFeature(1, "SILTNK",
		s57.Geometry{Type: s57.GeometryTypePoint, Coordinates: [][]float64{{12.5, 55.7}}},
		map[string]interface{}{"CATSIL": 3, "CONVIS": 1},
	)
	build, ok := b.Build(&pt)
	if !ok {
		t.Fatal("build failed")
	}
	var sym *SymbolCall
	for i := range build.Primitives {
		if sc, ok := build.Primitives[i].(SymbolCall); ok {
			sym = &sc
			break
		}
	}
	if sym == nil {
		t.Fatalf("no SymbolCall emitted; got %#v", build.Primitives)
	}
	if sym.SymbolName != "TOWERS03" {
		t.Errorf("symbol = %q, want TOWERS03", sym.SymbolName)
	}
	if sym.Anchor.Lat != 55.7 || sym.Anchor.Lon != 12.5 {
		t.Errorf("anchor = %+v, want {55.7,12.5}", sym.Anchor)
	}
}

// TestS101BuildAreaFillAndLine drives a polygon feature; the SiloTank surface
// branch emits ColorFill:CHBRN + a boundary line, emitted onto the rings.
func TestS101BuildAreaFillAndLine(t *testing.T) {
	b := s101Builder(t)
	ring := [][]float64{{0, 0}, {0, 1}, {1, 1}, {1, 0}, {0, 0}}
	poly := s57.NewFeature(2, "SILTNK",
		s57.Geometry{Type: s57.GeometryTypePolygon, Coordinates: ring},
		map[string]interface{}{"CATSIL": 1},
	)
	build, ok := b.Build(&poly)
	if !ok {
		t.Fatal("build failed")
	}
	var fill *FillPolygon
	for i := range build.Primitives {
		if fp, ok := build.Primitives[i].(FillPolygon); ok {
			fill = &fp
			break
		}
	}
	if fill == nil || fill.ColorToken != "CHBRN" {
		t.Fatalf("want FillPolygon CHBRN, got %#v", build.Primitives)
	}
	if len(fill.Rings) == 0 || len(fill.Rings[0]) == 0 {
		t.Errorf("fill not emitted onto geometry: %+v", fill.Rings)
	}
}

// TestS101BuildUnknownClassPlaceholder: an object class with no S-101 alias
// renders the QUESMRK1 placeholder rather than vanishing.
func TestS101BuildUnknownClassPlaceholder(t *testing.T) {
	b := s101Builder(t)
	f := s57.NewFeature(3, "ZZZZZZ",
		s57.Geometry{Type: s57.GeometryTypePoint, Coordinates: [][]float64{{1, 2}}}, nil)
	build, ok := b.Build(&f)
	if !ok {
		t.Fatal("build should succeed with placeholder")
	}
	if len(build.Primitives) != 1 {
		t.Fatalf("want 1 placeholder primitive, got %d", len(build.Primitives))
	}
	if sc, ok := build.Primitives[0].(SymbolCall); !ok || sc.SymbolName != "QUESMRK1" {
		t.Errorf("want QUESMRK1 placeholder, got %#v", build.Primitives[0])
	}
}

// TestDisplayCategoryForViewingGroup checks the viewing-group→display-category
// band mapping (the fix for "everything baked as cat=Other"). 1xxxx=Base,
// 2xxxx=Standard, 3xxxx/9xxxx=Other, text-selectors (5xxxx/<10000)=unset.
func TestDisplayCategoryForViewingGroup(t *testing.T) {
	cases := []struct {
		vg   int
		want int
	}{
		{11050, displayBase},     // no-data / chart furniture
		{12010, displayBase},     // land area
		{13030, displayBase},     // depth area
		{14010, displayBase},     // isolated underwater danger
		{21010, displayStandard}, // unknown object
		{27070, displayStandard}, // lights
		{32050, displayOther},    // other display element
		{90010, displayOther},    // data-quality overlay
		{11, 0},                  // text-group selector (independent)
		{50010, 0},               // 5xxxx text band
		{0, 0},                   // unset
	}
	for _, c := range cases {
		if got := displayCategoryForViewingGroup(c.vg); got != c.want {
			t.Errorf("displayCategoryForViewingGroup(%d) = %d, want %d", c.vg, got, c.want)
		}
	}
}

// TestS101BuildDisplayCategory: a real LANDARE bakes as Display Base (12010),
// not the old hardcoded Standard — proving the category is read from the rule's
// emitted viewing group.
func TestS101BuildDisplayCategory(t *testing.T) {
	b := s101Builder(t)
	ring := [][]float64{{0, 0}, {0, 1}, {1, 1}, {1, 0}, {0, 0}}
	land := s57.NewFeature(7, "LNDARE",
		s57.Geometry{Type: s57.GeometryTypePolygon, Coordinates: ring}, nil)
	build, ok := b.Build(&land)
	if !ok {
		t.Fatal("build failed")
	}
	if build.DisplayCategory != displayBase {
		t.Errorf("LNDARE display category = %d, want displayBase(%d)", build.DisplayCategory, displayBase)
	}
}

// TestS101LightFlareRotation: a LIGHTS feature's flare symbol is rotated (the
// catalogue default 135°, screen-referenced). Regression: the CRS-qualified
// "Rotation:PortrayalCRS,135" parsed to 0°, so flares never rotated.
func TestS101LightFlareRotation(t *testing.T) {
	b := s101Builder(t)
	f := s57.NewFeature(8, "LIGHTS",
		s57.Geometry{Type: s57.GeometryTypePoint, Coordinates: [][]float64{{-76.4, 38.6}}},
		map[string]interface{}{"COLOUR": 3, "LITCHR": 2, "SIGGRP": "(1)", "SIGPER": 4},
	)
	build, ok := b.Build(&f)
	if !ok {
		t.Fatal("build failed")
	}
	var rotated bool
	for _, p := range build.Primitives {
		if sc, ok := p.(SymbolCall); ok && sc.RotationDeg != 0 {
			rotated = true
			if sc.RotationTrueNorth {
				t.Errorf("flare should be screen-referenced, got true-north (rot=%v)", sc.RotationDeg)
			}
		}
	}
	if !rotated {
		t.Errorf("no rotated light symbol emitted; prims=%#v", build.Primitives)
	}
}

// TestS101Soundings: a SOUNDG multipoint portrays one (or more) sounding glyph
// per point, each placed at its own location. Regression: the bridge sent
// "Point", so the Sounding rule errored ("Invalid primitive type") and no
// soundings drew.
func TestS101Soundings(t *testing.T) {
	b := s101Builder(t)
	f := s57.NewFeature(11, "SOUNDG",
		s57.Geometry{Type: s57.GeometryTypePoint, Coordinates: [][]float64{
			{-76.40, 38.60, 5.0},
			{-76.41, 38.61, 12.0},
		}}, nil)
	build, ok := b.Build(&f)
	if !ok {
		t.Fatal("build failed")
	}
	var anchors []geo.LatLon
	for _, p := range build.Primitives {
		if sc, ok := p.(SymbolCall); ok && (strings.HasPrefix(sc.SymbolName, "SOUNDG") || strings.HasPrefix(sc.SymbolName, "SOUNDS")) {
			anchors = append(anchors, sc.Anchor)
		}
	}
	if len(anchors) == 0 {
		t.Fatalf("no sounding glyphs emitted; prims=%#v", build.Primitives)
	}
	// Each sounding must sit at its own point, not the (zero) feature anchor.
	var sawP1, sawP2 bool
	for _, a := range anchors {
		if approxLL(a, -76.40, 38.60) {
			sawP1 = true
		}
		if approxLL(a, -76.41, 38.61) {
			sawP2 = true
		}
	}
	if !sawP1 || !sawP2 {
		t.Errorf("soundings not placed at their points (p1=%v p2=%v); anchors=%v", sawP1, sawP2, anchors)
	}
}

func approxLL(a geo.LatLon, lon, lat float64) bool {
	return a.Lon > lon-1e-6 && a.Lon < lon+1e-6 && a.Lat > lat-1e-6 && a.Lat < lat+1e-6
}

// TestS101DangerDefaultDepth: an OBSTRN with no VALSOU inside a DEPARE portrays
// (no error) because it inherits defaultClearanceDepth from the depth area's
// DRVAL1. Regression: OBSTRN07 errored ("Neither valueOfSounding or
// defaultClearanceDepth have a value") for all depth-less dangers.
func TestS101DangerDefaultDepth(t *testing.T) {
	b := s101Builder(t)
	// A 0..1° square depth area, DRVAL1 = 8 m.
	depare := s57.NewFeature(30, "DEPARE",
		s57.Geometry{Type: s57.GeometryTypePolygon, Coordinates: [][]float64{
			{0, 0}, {0, 1}, {1, 1}, {1, 0}, {0, 0}}},
		map[string]interface{}{"DRVAL1": 8.0, "DRVAL2": 12.0})
	// An obstruction inside it, NO VALSOU.
	obstrn := s57.NewFeature(31, "OBSTRN",
		s57.Geometry{Type: s57.GeometryTypePoint, Coordinates: [][]float64{{0.5, 0.5}}},
		map[string]interface{}{"CATOBS": 6})
	m, err := b.BuildBatch([]*s57.Feature{&depare, &obstrn})
	if err != nil {
		t.Fatal(err)
	}
	if got := m[31]; len(got.Primitives) == 0 {
		t.Fatalf("OBSTRN inside a DEPARE produced no primitives (rule errored on missing depth)")
	}
	// An obstruction OUTSIDE any depth area still has no depth → stays suppressed
	// (genuinely unknown), which is acceptable.
	obstrnOut := s57.NewFeature(32, "OBSTRN",
		s57.Geometry{Type: s57.GeometryTypePoint, Coordinates: [][]float64{{5, 5}}},
		map[string]interface{}{"CATOBS": 6})
	m2, _ := b.BuildBatch([]*s57.Feature{&depare, &obstrnOut})
	_ = m2 // no assertion: just must not panic
}

// TestS101DeepDangerNoOverflow: an obstruction DEEPER than the safety contour
// (VALSOU > 30) takes the "deep sounding" path, which reads feature.Point.
// Regression: the spatial glue returned nil for a point's spatial, so the
// framework's GetSpatial infinitely recursed → stack overflow (101 OBSTRN/WRECKS
// suppressed). The host now resolves a real Point spatial.
func TestS101DeepDangerNoOverflow(t *testing.T) {
	b := s101Builder(t)
	for _, depth := range []float64{40, 100, 12.3} {
		f := s57.NewFeature(40, "OBSTRN",
			s57.Geometry{Type: s57.GeometryTypePoint, Coordinates: [][]float64{{-76.4, 38.6}}},
			map[string]interface{}{"VALSOU": depth, "WATLEV": 3})
		build, ok := b.Build(&f)
		if !ok || len(build.Primitives) == 0 {
			t.Fatalf("deep obstruction VALSOU=%v produced no primitives (stack overflow?); ok=%v", depth, ok)
		}
	}
}

// TestS101OpeningBridge: an opening bridge (BRIDGE CATBRG=2) portrays via the
// SpanOpening rule. Regression: the rule's unguarded
// verticalClearanceClosed.verticalClearanceValue deref crashed because the host
// never synthesised the clearance complex attribute → all 51 bridges errored.
func TestS101OpeningBridge(t *testing.T) {
	b := s101Builder(t)
	line := [][]float64{{-76.40, 38.60}, {-76.39, 38.61}}
	// With VERCCL present (closed clearance 5.0 m).
	br := s57.NewFeature(20, "BRIDGE",
		s57.Geometry{Type: s57.GeometryTypeLineString, Coordinates: line},
		map[string]interface{}{"CATBRG": 2, "VERCCL": 5.0})
	build, ok := b.Build(&br)
	if !ok {
		t.Fatal("build failed")
	}
	if len(build.Primitives) == 0 {
		t.Fatalf("opening bridge produced no primitives (rule errored?)")
	}
	// And without VERCCL (the crash case): must still portray, not error out.
	br2 := s57.NewFeature(21, "BRIDGE",
		s57.Geometry{Type: s57.GeometryTypeLineString, Coordinates: line},
		map[string]interface{}{"CATBRG": 2})
	build2, ok := b.Build(&br2)
	if !ok || len(build2.Primitives) == 0 {
		t.Fatalf("opening bridge without VERCCL produced no primitives (rule crashed); ok=%v prims=%d", ok, len(build2.Primitives))
	}
}

// TestS101NameLabel: a feature with OBJNAM produces a DrawText name label via
// the PortrayFeatureName wrapper + featureName complex-attr data.
func TestS101NameLabel(t *testing.T) {
	b := s101Builder(t)
	f := s57.NewFeature(99, "BOYLAT",
		s57.Geometry{Type: s57.GeometryTypePoint, Coordinates: [][]float64{{-76.4, 38.6}}},
		map[string]interface{}{"OBJNAM": "G C 5", "CATLAM": 1},
	)
	build, ok := b.Build(&f)
	if !ok {
		t.Fatal("build failed")
	}
	var label string
	for _, p := range build.Primitives {
		if dt, ok := p.(DrawText); ok {
			label = dt.Text
		}
	}
	// The LateralBuoy rule formats the name as "by %s" (catalogue's format), so
	// the label contains the OBJNAM — the point is that the name text renders.
	if !strings.Contains(label, "G C 5") {
		t.Errorf("name label = %q, want it to contain \"G C 5\"; prims=%d", label, len(build.Primitives))
	}
}

// TestS101BuildAllAroundLightCharacteristic: an all-around light's description
// (LITDSN02) must carry its character + period, not collapse to just the colour.
// LITDSN02 reads these from the rhythmOfLight complex attribute, which the bridge
// synthesizes from S-57 LITCHR/SIGGRP/SIGPER — without it the text was e.g. "G".
func TestS101BuildAllAroundLightCharacteristic(t *testing.T) {
	b := s101BuilderEmbedded(t)
	lt := s57.NewFeature(1, "LIGHTS",
		s57.Geometry{Type: s57.GeometryTypePoint, Coordinates: [][]float64{{12.5, 55.7}}},
		map[string]interface{}{"LITCHR": 4, "COLOUR": "4", "SIGPER": "1"}, // Quick, green, 1s
	)
	build, ok := b.Build(&lt)
	if !ok {
		t.Fatal("build failed")
	}
	var text string
	for _, p := range build.Primitives {
		if dt, ok := p.(DrawText); ok && strings.ContainsAny(dt.Text, "QGFlsm") {
			text = dt.Text
			break
		}
	}
	if !strings.Contains(text, "Q") {
		t.Errorf("light text = %q, want the Quick character 'Q' (rhythmOfLight not synthesized?)", text)
	}
	if !strings.Contains(text, "G") {
		t.Errorf("light text = %q, want the green colour 'G'", text)
	}
	if !strings.Contains(text, "1s") {
		t.Errorf("light text = %q, want the 1s period", text)
	}
}

// TestS101BuildSectorLight drives an S-57 sectored light through the full build
// seam and asserts the rule's constructed AugmentedFigure elements come through:
// the dashed legs (rays, tagged with the nominal range for the full-light-lines
// toggle) and the coloured arc — driven by the catalogue, not a Go re-derivation.
func TestS101BuildSectorLight(t *testing.T) {
	b := s101BuilderEmbedded(t)

	lt := s57.NewFeature(1, "LIGHTS",
		s57.Geometry{Type: s57.GeometryTypePoint, Coordinates: [][]float64{{12.5, 55.7}}},
		map[string]interface{}{
			"SECTR1": "045", "SECTR2": "090",
			"COLOUR": "3", "VALNMR": "9", "LITCHR": 2,
		},
	)
	build, ok := b.Build(&lt)
	if !ok {
		t.Fatal("build failed")
	}
	var legs, arcs int
	var arcColor string
	for _, p := range build.Primitives {
		fig, ok := p.(AugmentedFigure)
		if !ok {
			continue
		}
		if fig.Ray {
			legs++
			if fig.FullLengthNM != 9 {
				t.Errorf("leg nominal range = %v, want 9 (from VALNMR)", fig.FullLengthNM)
			}
		} else {
			arcs++
			if fig.ColorToken == "LITRD" {
				arcColor = fig.ColorToken
			}
		}
	}
	if legs < 2 {
		t.Errorf("legs = %d, want >=2 (two sector limits)", legs)
	}
	if arcs < 1 {
		t.Errorf("arcs = %d, want >=1 (the sector arc)", arcs)
	}
	if arcColor != "LITRD" {
		t.Errorf("no LITRD (red) arc found; COLOUR=3 should portray red")
	}
}

// TestS101BuildTopmark proves a co-located S-57 TOPMAR is folded into its parent
// buoy as the S-101 topmark complex attribute, so the buoy's TOPMAR02 CSP emits
// a topmark symbol (TMARDEF2 here: floating, no specific shape match yet).
func TestS101BuildTopmark(t *testing.T) {
	b := s101Builder(t)

	at := s57.Geometry{Type: s57.GeometryTypePoint, Coordinates: [][]float64{{12.5, 55.7}}}
	buoy := s57.NewFeature(1, "BOYLAT", at, map[string]interface{}{"BOYSHP": 2, "COLOUR": "3"})
	top := s57.NewFeature(2, "TOPMAR", at, map[string]interface{}{"TOPSHP": 1, "COLOUR": "3"})

	m, err := b.BuildBatch([]*s57.Feature{&buoy, &top})
	if err != nil {
		t.Fatal(err)
	}
	// The standalone TOPMAR is suppressed (folded into the parent).
	if len(m[2].Primitives) != 0 {
		t.Errorf("standalone TOPMAR should be suppressed, got %#v", m[2].Primitives)
	}
	// The buoy now carries a topmark symbol (TOPMAR02 → a TOPMARxx/TMARDEFx call).
	var topSym bool
	for _, p := range m[1].Primitives {
		if sc, ok := p.(SymbolCall); ok && (strings.HasPrefix(sc.SymbolName, "TOPMAR") || strings.HasPrefix(sc.SymbolName, "TMARDEF")) {
			topSym = true
		}
	}
	if !topSym {
		t.Errorf("buoy should emit a topmark symbol; primitives=%#v", m[1].Primitives)
	}
}

// TestS101BuildMorfac proves the S-57 MORFAC → S-101 class decomposition by
// CATMOR: each category routes to its dedicated rule and portrays a symbol
// (point pilings/bollards/buoys are the bulk of harbour MORFAC features).
func TestS101BuildMorfac(t *testing.T) {
	b := s101Builder(t)
	at := s57.Geometry{Type: s57.GeometryTypePoint, Coordinates: [][]float64{{12.5, 55.7}}}
	cases := []struct{ catmor, wantSym string }{
		{"5", ""}, // pile
		{"3", ""}, // bollard
		{"1", ""}, // dolphin
		{"7", ""}, // mooring buoy
	}
	for _, c := range cases {
		f := s57.NewFeature(1, "MORFAC", at, map[string]interface{}{"CATMOR": c.catmor})
		build, ok := b.Build(&f)
		if !ok {
			t.Fatalf("CATMOR=%s: build failed", c.catmor)
		}
		var sym bool
		for _, p := range build.Primitives {
			if _, ok := p.(SymbolCall); ok {
				sym = true
			}
		}
		// Magenta "unknown object" is the failure we're fixing — assert it's gone.
		if isUnknownBuild(build) {
			t.Errorf("CATMOR=%s: still portrays as unknown object", c.catmor)
		}
		if !sym {
			t.Errorf("CATMOR=%s: want a symbol, got %#v", c.catmor, build.Primitives)
		}
	}
}

// isUnknownBuild reports whether a build is the magenta unknown-object mark.
func isUnknownBuild(b FeatureBuild) bool {
	for _, p := range b.Primitives {
		if sc, ok := p.(SymbolCall); ok && (sc.SymbolName == "QUESMRK1" || sc.SymbolName == "ISODGR51") {
			return true
		}
	}
	return false
}
