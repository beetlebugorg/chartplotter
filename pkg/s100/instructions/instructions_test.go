package instructions

import "testing"

// Streams below are the actual output of the real Rapids rule (captured from the
// S-101 Lua engine), plus a synthetic point example exercising modifiers.

func TestRapidsCurve(t *testing.T) {
	stream := "ViewingGroup:32050;DrawingPriority:9;DisplayPlane:UnderRadar;LineStyle:_simple_,,0.96,CHGRD;LineInstruction:_simple_"
	cmds, unsup := Reduce(ParseStream(stream))
	if len(unsup) != 0 {
		t.Fatalf("unexpected unsupported: %v", unsup)
	}
	if len(cmds) != 1 {
		t.Fatalf("want 1 draw, got %d: %+v", len(cmds), cmds)
	}
	c := cmds[0]
	if c.Op != OpLine || c.Reference != "_simple_" {
		t.Fatalf("want OpLine _simple_, got %s %s", c.Op, c.Reference)
	}
	if c.ViewingGroup != 32050 || c.Priority != 9 || c.DisplayPlane != "UnderRadar" {
		t.Errorf("state not folded: vg=%d prio=%d plane=%s", c.ViewingGroup, c.Priority, c.DisplayPlane)
	}
	if c.SimpleLine == nil || c.SimpleLine.Width != 0.96 || c.SimpleLine.Color != "CHGRD" || c.SimpleLine.DashLength != 0 {
		t.Errorf("simple line not captured: %+v", c.SimpleLine)
	}
}

func TestRapidsSurface(t *testing.T) {
	stream := "ViewingGroup:32050;DrawingPriority:9;DisplayPlane:UnderRadar;ColorFill:CHGRD"
	cmds, _ := Reduce(ParseStream(stream))
	if len(cmds) != 1 || cmds[0].Op != OpColorFill || cmds[0].Reference != "CHGRD" {
		t.Fatalf("want OpColorFill CHGRD, got %+v", cmds)
	}
}

func TestRapidsPointNull(t *testing.T) {
	stream := "ViewingGroup:32050;DrawingPriority:9;DisplayPlane:UnderRadar;NullInstruction"
	cmds, _ := Reduce(ParseStream(stream))
	if len(cmds) != 1 || cmds[0].Op != OpNull {
		t.Fatalf("want one OpNull, got %+v", cmds)
	}
}

func TestPointSymbolWithModifiers(t *testing.T) {
	stream := "ViewingGroup:25010;DrawingPriority:14;LocalOffset:1.5,-2;Rotation:45;PointInstruction:BCNCAR01"
	cmds, unsup := Reduce(ParseStream(stream))
	if len(unsup) != 0 {
		t.Fatalf("unexpected unsupported: %v", unsup)
	}
	if len(cmds) != 1 {
		t.Fatalf("want 1, got %d", len(cmds))
	}
	c := cmds[0]
	if c.Op != OpPoint || c.Reference != "BCNCAR01" {
		t.Fatalf("want OpPoint BCNCAR01, got %s %s", c.Op, c.Reference)
	}
	if c.Offset != [2]float64{1.5, -2} || !c.HasRotation || c.Rotation != 45 || c.ViewingGroup != 25010 || c.Priority != 14 {
		t.Errorf("modifiers not folded: %+v", c)
	}
}

// TestRotationCRS: the real catalogue emits "Rotation:<CRS>,<angle>". The angle
// must come from arg 1 (arg 0 is the CRS), and GeographicCRS marks a true-north
// rotation. Regression: reading arg 0 as the angle made every light flare 0°.
func TestRotationCRS(t *testing.T) {
	// PortrayalCRS = screen-referenced (the 135° light flare).
	cmds, _ := Reduce(ParseStream("Rotation:PortrayalCRS,135;PointInstruction:LIGHTS11"))
	if cmds[0].Rotation != 135 || cmds[0].RotationTrueNorth {
		t.Errorf("PortrayalCRS,135 → %v (trueN=%v), want 135 screen", cmds[0].Rotation, cmds[0].RotationTrueNorth)
	}
	// GeographicCRS = true-north (a directional light's orientation).
	cmds, _ = Reduce(ParseStream("Rotation:GeographicCRS,200;PointInstruction:LIGHTS82"))
	if cmds[0].Rotation != 200 || !cmds[0].RotationTrueNorth {
		t.Errorf("GeographicCRS,200 → %v (trueN=%v), want 200 true-north", cmds[0].Rotation, cmds[0].RotationTrueNorth)
	}
	// Bare "Rotation:<angle>" (no CRS) tolerated as screen-referenced.
	cmds, _ = Reduce(ParseStream("Rotation:45;PointInstruction:BCNCAR01"))
	if cmds[0].Rotation != 45 || cmds[0].RotationTrueNorth {
		t.Errorf("bare 45 → %v (trueN=%v), want 45 screen", cmds[0].Rotation, cmds[0].RotationTrueNorth)
	}
}

func TestStateCarriesAcrossMultipleDraws(t *testing.T) {
	// One viewing group, two draws: a fill then a boundary line. Both should
	// inherit the viewing group; the line picks up its own _simple_ style.
	stream := "ViewingGroup:33010;AreaFillReference:DRGARE01;LineStyle:_simple_,,0.32,CHGRF;LineInstruction:_simple_"
	cmds, _ := Reduce(ParseStream(stream))
	if len(cmds) != 2 {
		t.Fatalf("want 2 draws, got %d: %+v", len(cmds), cmds)
	}
	if cmds[0].Op != OpAreaFill || cmds[0].Reference != "DRGARE01" || cmds[0].ViewingGroup != 33010 {
		t.Errorf("fill wrong: %+v", cmds[0])
	}
	if cmds[1].Op != OpLine || cmds[1].ViewingGroup != 33010 || cmds[1].SimpleLine.Color != "CHGRF" {
		t.Errorf("line wrong: %+v", cmds[1])
	}
	// The fill must NOT carry the later-defined simple line.
	if cmds[0].SimpleLine != nil {
		t.Errorf("fill leaked a simple line: %+v", cmds[0].SimpleLine)
	}
}

func TestUnsupportedSurfaced(t *testing.T) {
	_, unsup := Reduce(ParseStream("ViewingGroup:1;Foo:bar;PointInstruction:X"))
	if len(unsup) != 1 || unsup[0] != "Foo" {
		t.Fatalf("want [Foo], got %v", unsup)
	}
}

// TestSectorAugmentedGeometry: a real LightSectored stream (captured from the
// S-101 Lua engine) constructs two dashed CHBLK legs and a black-backed coloured
// arc via AugmentedRay/ArcByRadius. Each LineInstruction must stroke the current
// figure as an OpAugmentedLine carrying the ray/arc params + the simple-line
// style — never collapse to an OpLine or land in unsupported.
func TestSectorAugmentedGeometry(t *testing.T) {
	stream := "ViewingGroup:27070;DrawingPriority:24;DisplayPlane:UnderRadar;Hover:true;" +
		"AugmentedRay:GeographicCRS,83,LocalCRS,25;Dash:0,3.6;LineStyle:_simple_,5.4,0.32,CHBLK;LineInstruction:_simple_;" +
		"AugmentedRay:GeographicCRS,247,LocalCRS,25;LineInstruction:_simple_;" +
		"ArcByRadius:0,0,20,83,164;AugmentedPath:LocalCRS,GeographicCRS,LocalCRS;" +
		"LineStyle:_simple_,,1.28,CHBLK;LineInstruction:_simple_;" +
		"LineStyle:_simple_,,0.64,LITYW;LineInstruction:_simple_;ClearGeometry"
	cmds, unsup := Reduce(ParseStream(stream))
	if len(unsup) != 0 {
		t.Fatalf("unexpected unsupported: %v", unsup)
	}
	var aug []DrawCommand
	for _, c := range cmds {
		if c.Op == OpAugmentedLine {
			aug = append(aug, c)
		} else if c.Op == OpLine {
			t.Errorf("augmented stroke collapsed to OpLine: %+v", c)
		}
	}
	if len(aug) != 4 {
		t.Fatalf("want 4 augmented strokes (2 legs + 2 arc), got %d", len(aug))
	}
	// Leg 1: ray at 83°, length 25mm, dashed CHBLK.
	if aug[0].Augmented == nil || aug[0].Augmented.Kind != AugRay ||
		aug[0].Augmented.BearingDeg != 83 || aug[0].Augmented.LengthMM != 25 {
		t.Errorf("leg1 ray = %+v", aug[0].Augmented)
	}
	if aug[0].SimpleLine == nil || aug[0].SimpleLine.Color != "CHBLK" || aug[0].SimpleLine.DashLength == 0 {
		t.Errorf("leg1 style = %+v", aug[0].SimpleLine)
	}
	// Leg 2: ray at 247°, inherits the same dashed CHBLK style.
	if aug[1].Augmented == nil || aug[1].Augmented.BearingDeg != 247 {
		t.Errorf("leg2 ray = %+v", aug[1].Augmented)
	}
	// Arc backing: radius 20mm, start 83°, sweep 164°, CHBLK 1.28mm solid.
	if aug[2].Augmented == nil || aug[2].Augmented.Kind != AugArc ||
		aug[2].Augmented.RadiusMM != 20 || aug[2].Augmented.StartDeg != 83 || aug[2].Augmented.SweepDeg != 164 {
		t.Errorf("arc backing = %+v", aug[2].Augmented)
	}
	if aug[2].SimpleLine == nil || aug[2].SimpleLine.Color != "CHBLK" || aug[2].SimpleLine.Width != 1.28 {
		t.Errorf("arc backing style = %+v", aug[2].SimpleLine)
	}
	// Arc colour: white light portrayed yellow (LITYW), 0.64mm.
	if aug[3].SimpleLine == nil || aug[3].SimpleLine.Color != "LITYW" || aug[3].SimpleLine.Width != 0.64 {
		t.Errorf("arc colour style = %+v", aug[3].SimpleLine)
	}
}
