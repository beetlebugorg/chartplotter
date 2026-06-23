package instructions

import "testing"

// Streams below are the actual output of the real Rapids rule captured by
// cmd/lua-portray-test, plus a synthetic point example exercising modifiers.

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
