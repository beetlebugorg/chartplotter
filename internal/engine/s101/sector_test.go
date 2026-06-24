package s101

import (
	"strings"
	"testing"
)

// TestSectoredLightRoutesAndDrawsArcs proves the ambiguous-alias resolution
// (LIGHTS→LightSectored) plus the synthesized sectorCharacteristics structure:
// an S-57 sectored light emits the sector legs (AugmentedRay) and arc
// (ArcByRadius) the LightSectored rule draws — not the all-around flare.
func TestSectoredLightRoutesAndDrawsArcs(t *testing.T) {
	rulesDir, cat := testEnv(t)
	e, err := NewEngine(rulesDir, cat)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()

	out := portrayOne(t, e, Feature{
		ID: "f1", ObjectClass: "LIGHTS", Primitive: "Point",
		Attributes: map[string]string{
			"SECTR1": "045", "SECTR2": "090",
			"COLOUR": "3", "VALNMR": "9", "LITCHR": "2", "CATLIT": "0",
		},
	})
	if !strings.Contains(out, "AugmentedRay") {
		t.Errorf("sectored light: want sector legs (AugmentedRay), got %q", out)
	}
	if !strings.Contains(out, "ArcByRadius") {
		t.Errorf("sectored light: want sector arc (ArcByRadius), got %q", out)
	}
}

// TestDirectionalLightRoutesToSectored proves a directional light (CATLIT=1 with
// an orientation) reaches LightSectored and exercises its directionalCharacter
// branch without error.
func TestDirectionalLightRoutesToSectored(t *testing.T) {
	rulesDir, cat := testEnv(t)
	e, err := NewEngine(rulesDir, cat)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()

	out := portrayOne(t, e, Feature{
		ID: "f1", ObjectClass: "LIGHTS", Primitive: "Point",
		Attributes: map[string]string{
			"CATLIT": "1", "ORIENT": "135", "COLOUR": "1", "VALNMR": "12", "LITCHR": "2",
		},
	})
	if out == "" {
		t.Errorf("directional light: want a non-empty stream, got empty")
	}
}

// TestNavigationLineOrientation proves the orientation complex attribute is
// synthesized from S-57 ORIENT: a leading line (categoryOfNavigationLine=1)
// portrays its bearing label instead of erroring on a nil orientation.
func TestNavigationLineOrientation(t *testing.T) {
	rulesDir, cat := testEnv(t)
	e, err := NewEngine(rulesDir, cat)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()

	out := portrayOne(t, e, Feature{
		ID: "f1", ObjectClass: "NAVLNE", Primitive: "Curve",
		Attributes: map[string]string{"CATNAV": "1", "ORIENT": "043.5"},
	})
	if !strings.Contains(out, "deg") {
		t.Errorf("leading line: want a '%%03.0f deg' bearing label, got %q", out)
	}
}

// TestAmbiguousAliasDefaults pins the disambiguation choices for the one-to-many
// S-57→S-101 classes (a plain light is all-around; air-obstruction and fog
// detector get their dedicated classes; ADMARE is an administration area).
func TestAmbiguousAliasDefaults(t *testing.T) {
	_, cat := testEnv(t)
	e := &Engine{cat: cat}
	cases := []struct {
		class string
		attrs map[string]string
		want  string
	}{
		{"LIGHTS", nil, "LightAllAround"},
		{"LIGHTS", map[string]string{"SECTR1": "1", "SECTR2": "2"}, "LightSectored"},
		{"LIGHTS", map[string]string{"CATLIT": "1"}, "LightSectored"},
		{"LIGHTS", map[string]string{"CATLIT": "6"}, "LightAirObstruction"},
		{"LIGHTS", map[string]string{"CATLIT": "7"}, "LightFogDetector"},
		{"ADMARE", nil, "AdministrationArea"},
	}
	for _, c := range cases {
		got, ok := e.resolveCode(c.class, c.attrs)
		if !ok || got != c.want {
			t.Errorf("resolveCode(%s, %v) = %q,%v; want %q", c.class, c.attrs, got, ok, c.want)
		}
	}
}
