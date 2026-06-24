package baker_test

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/beetlebugorg/chartplotter/internal/engine/baker"
	"github.com/beetlebugorg/chartplotter/internal/engine/portrayal"
	"github.com/beetlebugorg/chartplotter/internal/engine/s101"
	"github.com/beetlebugorg/chartplotter/pkg/s100/fc"
	"github.com/beetlebugorg/chartplotter/pkg/s57"
)

// TestS101Audit runs the engine over every testdata cell and reports, per class:
// total, ok, errored, unmapped, and empty (mapped+no-error but emitted nothing).
// Plus the distinct unmapped classes and distinct error messages.
func TestS101Audit(t *testing.T) {
	pc := os.Getenv("S101_CATALOG")
	if pc == "" {
		pc = "../s101catalog/catalog/PortrayalCatalog"
	}
	fcPath := os.Getenv("S101_FC")
	if fcPath == "" {
		fcPath = "../s101catalog/catalog/FeatureCatalogue.xml"
	}
	if _, err := os.Stat(filepath.Join(pc, "Rules", "main.lua")); err != nil {
		t.Skip("no catalogue")
	}
	cat, err := fc.Load(fcPath)
	if err != nil {
		t.Fatal(err)
	}

	glob := os.Getenv("S101_CELLS")
	if glob == "" {
		glob = "../../../testdata/*.000"
	}
	cells, _ := filepath.Glob(glob)
	type stat struct{ total, ok, errd, unmapped, empty int }
	classStat := map[string]*stat{}
	unmappedClasses := map[string]int{}
	errMsgs := map[string]int{}
	// CSP-relevant classes we care about validating.
	watch := map[string]bool{
		"LIGHTS": true, "WRECKS": true, "OBSTRN": true, "UWTROC": true,
		"SOUNDG": true, "DEPARE": true, "DEPCNT": true, "BOYLAT": true,
		"BCNLAT": true, "TOPMAR": true, "RESARE": true, "ACHARE": true,
		"BRIDGE": true, "SBDARE": true, "M_QUAL": true,
	}
	watchSample := map[string]string{} // class -> a sample emitted stream head
	sectorLights := 0
	sectorSamples := []string{}

	for _, cellPath := range cells {
		data, err := os.ReadFile(cellPath)
		if err != nil {
			t.Fatal(err)
		}
		chart, err := baker.ParseCellBytes(filepath.Base(cellPath), data)
		if err != nil {
			t.Logf("parse %s: %v", cellPath, err)
			continue
		}
		features := chart.Features()
		ptrs := make([]*s57.Feature, len(features))
		for i := range features {
			ptrs[i] = &features[i]
		}
		eng, err := s101.NewEngine(filepath.Join(pc, "Rules"), cat)
		if err != nil {
			t.Fatal(err)
		}
		depthIdx := portrayal.BuildDepthIndex(ptrs)
		var batch []s101.Feature
		idClass := map[string]string{}
		for i := range features {
			f := &features[i]
			g := f.Geometry()
			if len(g.Coordinates) == 0 && len(g.Rings) == 0 {
				continue
			}
			id := strconv.Itoa(i)
			idClass[id] = f.ObjectClass()
			primitive := prim(g.Type)
			var points [][3]float64
			if f.ObjectClass() == "SOUNDG" {
				primitive = "MultiPoint"
				for _, c := range g.Coordinates {
					if len(c) >= 3 {
						points = append(points, [3]float64{c[0], c[1], c[2]})
					} else if len(c) == 2 {
						points = append(points, [3]float64{c[0], c[1], 0})
					}
				}
			} else if g.Type == s57.GeometryTypePoint {
				for _, c := range g.Coordinates {
					if len(c) >= 3 {
						points = append(points, [3]float64{c[0], c[1], c[2]})
					} else if len(c) == 2 {
						points = append(points, [3]float64{c[0], c[1], 0})
					}
				}
			}
			batch = append(batch, s101.Feature{
				ID:          id,
				ObjectClass: f.ObjectClass(),
				Primitive:   primitive,
				Attributes:  strAttrs(f.Attributes()),
				Derived:     portrayal.DerivedAttrs(f, depthIdx),
				Points:      points,
			})
		}
		res, err := eng.Portray(batch)
		if err != nil {
			t.Fatal(err)
		}
		for id, stream := range res {
			cls := idClass[id]
			s := classStat[cls]
			if s == nil {
				s = &stat{}
				classStat[cls] = s
			}
			s.total++
			switch {
			case strings.HasPrefix(stream, "UNMAPPED:"):
				s.unmapped++
				unmappedClasses[cls]++
			case strings.HasPrefix(stream, "ERROR:"):
				s.errd++
				errMsgs[normErr(stream)]++
			case stream == "":
				s.empty++
			default:
				s.ok++
				// Sector-light probe: LIGHTS carrying SECTR1 should portray arcs.
				if cls == "LIGHTS" {
					if a := batchAttr(batch, id); a != nil && a["SECTR1"] != "" {
						sectorLights++
						if len(sectorSamples) < 4 {
							h := stream
							if len(h) > 400 {
								h = h[:400]
							}
							sectorSamples = append(sectorSamples, h)
						}
					}
				}
				if watch[cls] && watchSample[cls] == "" {
					h := stream
					if len(h) > 140 {
						h = h[:140]
					}
					watchSample[cls] = h
				}
			}
		}
		eng.Close()
	}

	t.Logf("=== sector lights (LIGHTS with SECTR1) = %d ===", sectorLights)
	for i, s := range sectorSamples {
		t.Logf("  sector[%d]: %s", i, s)
	}
	t.Logf("=== unmapped classes (no S-57→S-101 alias) ===")
	for _, c := range sortedKeys(unmappedClasses) {
		t.Logf("  %-8s x%d", c, unmappedClasses[c])
	}
	t.Logf("=== error messages ===")
	for m, n := range errMsgs {
		t.Logf("  [%d] %s", n, m)
	}
	t.Logf("=== watched CSP classes (total/ok/err/unmapped/empty) ===")
	for _, c := range sortedKeys2(watch) {
		s := classStat[c]
		if s == nil {
			t.Logf("  %-8s (absent)", c)
			continue
		}
		t.Logf("  %-8s %d/%d/%d/%d/%d", c, s.total, s.ok, s.errd, s.unmapped, s.empty)
	}
	t.Logf("=== sample emitted streams for watched classes ===")
	for _, c := range sortedKeys2(watch) {
		if watchSample[c] != "" {
			t.Logf("  %-8s %s", c, watchSample[c])
		}
	}
	// Empty-but-mapped is the silent-suppression gap; list those classes.
	t.Logf("=== classes with empty (mapped, no-error, emitted nothing) ===")
	for _, c := range sortedKeys3(classStat) {
		s := classStat[c]
		if s.empty > 0 {
			t.Logf("  %-8s empty=%d / total=%d", c, s.empty, s.total)
		}
	}
}

func batchAttr(batch []s101.Feature, id string) map[string]string {
	for i := range batch {
		if batch[i].ID == id {
			return batch[i].Attributes
		}
	}
	return nil
}

func sortedKeys(m map[string]int) []string {
	var k []string
	for x := range m {
		k = append(k, x)
	}
	sort.Strings(k)
	return k
}
func sortedKeys2(m map[string]bool) []string {
	var k []string
	for x := range m {
		k = append(k, x)
	}
	sort.Strings(k)
	return k
}
func sortedKeys3[V any](m map[string]V) []string {
	var k []string
	for x := range m {
		k = append(k, x)
	}
	sort.Strings(k)
	return k
}
