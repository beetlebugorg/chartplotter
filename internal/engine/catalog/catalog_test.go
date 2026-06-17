package catalog

import (
	"bytes"
	"strings"
	"testing"
)

// TestXMLToJSON mirrors the chartcat.zig "extracts fields and bbox" test.
func TestXMLToJSON(t *testing.T) {
	xml := `<EncProductCatalog><Header><date_valid>2026-06-13</date_valid></Header>
<cell><name>US5MD1MC</name><lname>Annapolis Harbor</lname><cscale>12000</cscale>
<zipfile_location>https://x/US5MD1MC.zip</zipfile_location><zipfile_size>447736</zipfile_size>
<edtn>4</edtn><updn>5</updn><isdt>2025-10-30</isdt>
<regions><region>4</region><region>6</region></regions>
<cov><panel><vertex><lat>38.925</lat><long>-76.5</long></vertex>
<vertex><lat>39</lat><long>-76.425</long></vertex></panel></cov></cell>
</EncProductCatalog>`

	var buf bytes.Buffer
	n, err := XMLToJSON(xml, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("cell count: got %d want 1", n)
	}
	out := buf.String()
	for _, want := range []string{
		`"date":"2026-06-13"`,
		`"n":"US5MD1MC"`,
		`"e":4`,
		`"u":5`,
		`"rg":[4,6]`,
		`"bb":[-76.500000,38.925000,-76.425000,39.000000]`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull: %s", want, out)
		}
	}
}
