package fc

import (
	"os"
	"testing"
)

// fcPath resolves the vendored S-101 Feature Catalogue, or skips.
// Override with S101_FC=/path/to/FeatureCatalogue.xml.
func fcPath(t *testing.T) string {
	t.Helper()
	p := os.Getenv("S101_FC")
	if p == "" {
		p = "/home/jcollins/Projects/s101-feature-catalogue/S-101FC/FeatureCatalogue.xml"
	}
	if _, err := os.Stat(p); err != nil {
		t.Skipf("S-101 feature catalogue not present (%s); set S101_FC to run", p)
	}
	return p
}

func TestLoadAndBridgeMaps(t *testing.T) {
	c, err := Load(fcPath(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.FeatureTypes) < 100 || len(c.SimpleAttrs) < 100 {
		t.Fatalf("registry looks short: %d feature types, %d attrs", len(c.FeatureTypes), len(c.SimpleAttrs))
	}
	t.Logf("loaded %d feature types, %d simple attributes", len(c.FeatureTypes), len(c.SimpleAttrs))

	// S-57 object class → S-101 feature code (alias-derived bridge).
	if code, ok := c.FeatureCodeForS57("M_ACCY"); !ok || code != "QualityOfNonBathymetricData" {
		t.Errorf("M_ACCY → %q (ok=%v), want QualityOfNonBathymetricData", code, ok)
	}
	// S-57 attribute acronym → S-101 attribute code.
	if code, ok := c.AttrCodeForS57("BCNSHP"); !ok || code != "beaconShape" {
		t.Errorf("BCNSHP → %q (ok=%v), want beaconShape", code, ok)
	}

	// Enumerated attribute keeps S-57 integer codes.
	bs := c.SimpleAttrs["beaconShape"]
	if bs == nil || bs.ValueType != "enumeration" || len(bs.ListedValues) == 0 {
		t.Fatalf("beaconShape attr wrong: %+v", bs)
	}
	if bs.ListedValues[0].Code != 1 || bs.ListedValues[0].Label == "" {
		t.Errorf("first listed value = %+v, want code 1 with a label", bs.ListedValues[0])
	}
}

// TestCoverageVsDAIObjectClasses reports how many of our DAI S-57 object
// classes the feature catalogue maps — the S-57→S-101 bridge coverage.
func TestCoverageVsDAIObjectClasses(t *testing.T) {
	c, err := Load(fcPath(t))
	if err != nil {
		t.Fatal(err)
	}
	// A representative slice of the 175 DAI object classes (see coverage matrix).
	sample := []string{"ACHARE", "DEPARE", "LIGHTS", "WRECKS", "SOUNDG", "COALNE", "BOYLAT", "OBSTRN", "RESARE", "M_QUAL"}
	mapped := 0
	for _, o := range sample {
		if _, ok := c.FeatureCodeForS57(o); ok {
			mapped++
		} else {
			t.Logf("no S-101 feature for S-57 %s (bridge gap → placeholder)", o)
		}
	}
	t.Logf("bridge coverage on sample: %d/%d", mapped, len(sample))
	if mapped == 0 {
		t.Fatal("no object classes mapped — alias parsing broken")
	}
}
