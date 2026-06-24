package s101

import (
	"strconv"
	"strings"
)

// This file builds the synthesized attribute tree a feature's rule reads. S-57
// stores everything as flat simple attributes on the feature; several S-101
// rules instead read *complex* (nested) attributes — featureName, the bridge
// clearances, orientation, and the light sectorCharacteristics structure. The
// bridge synthesizes those from the S-57 source so the real catalogue rules run
// unchanged. The host (host.go) serves the tree through the framework's
// attributePath protocol (HostFeatureGetComplexAttributeCount / -SimpleAttribute).

// cnode is one node of the tree: leaf simple values keyed by S-101 sub-attribute
// code, and nested complex children keyed by code (each code → its ordered
// instances). The feature root reuses this shape: its simple map holds the
// feature's own simple attributes (translated to S-101 codes, list values split)
// and its children hold the synthesized top-level complex attributes.
type cnode struct {
	simple   map[string][]string
	children map[string][]*cnode
}

func newCNode() *cnode {
	return &cnode{simple: map[string][]string{}, children: map[string][]*cnode{}}
}

// addSimple appends non-empty values under code (empties are dropped so the
// framework reads "attribute absent" rather than an empty string).
func (n *cnode) addSimple(code string, vals ...string) {
	for _, v := range vals {
		if v != "" {
			n.simple[code] = append(n.simple[code], v)
		}
	}
}

func (n *cnode) addChild(code string, child *cnode) {
	n.children[code] = append(n.children[code], child)
}

// resolve walks the framework attributePath ("code:idx;code:idx;…") from this
// node to the container the framework is querying. An empty path is the node
// itself. Returns nil if any segment is missing — the framework then reads count
// 0 / no value, i.e. "attribute absent".
func (n *cnode) resolve(path string) *cnode {
	cur := n
	if path == "" {
		return cur
	}
	for _, seg := range strings.Split(path, ";") {
		code, idxStr, ok := strings.Cut(seg, ":")
		if !ok {
			return nil
		}
		idx, err := strconv.Atoi(idxStr)
		if err != nil || idx < 1 {
			return nil
		}
		list := cur.children[code]
		if idx > len(list) {
			return nil
		}
		cur = list[idx-1]
	}
	return cur
}

// buildRoot assembles the synthesized tree for one feature: every S-57 simple
// attribute (translated to its S-101 code, list values split into array
// entries), the derived attributes, plus the complex attributes the rules read
// but S-57 stores flat.
func (e *Engine) buildRoot(objClass string, s57, derived map[string]string, name string, topmark map[string]string) *cnode {
	root := newCNode()
	for acr, val := range s57 {
		if code, ok := e.cat.AttrCodeForS57(acr); ok {
			root.simple[code] = e.splitValue(code, val)
		}
	}
	// Derived attributes are already in S-101 code form (no S-57 alias).
	for code, val := range derived {
		root.addSimple(code, val)
	}

	// featureName (S-57 OBJNAM/NOBJNM → the S-101 featureName complex attribute).
	if name != "" {
		fn := newCNode()
		fn.addSimple("name", name)
		fn.addSimple("language", "eng")
		fn.addSimple("nameUsage", "1") // default → selected even if language differs
		root.addChild("featureName", fn)
	}

	// Clearances (bridges): S-57 simple attribute → S-101 complex attribute
	// wrapping a *Value sub-attribute.
	for code, c := range clearances {
		if v := s57[c.s57]; v != "" {
			cl := newCNode()
			cl.addSimple(c.value, v)
			root.addChild(code, cl)
		} else if code == "verticalClearanceClosed" && objClass == "BRIDGE" {
			// An opening bridge's SpanOpening rule dereferences
			// verticalClearanceClosed.verticalClearanceValue unguarded (a DRAFT
			// catalogue bug); present-but-empty reads a nil value (→ hazard alert)
			// instead of crashing on a missing complex attribute.
			root.addChild(code, newCNode())
		}
	}

	// Orientation (S-57 ORIENT → the S-101 orientation complex attribute). Read by
	// NavigationLine (leading/clearing lines), RecommendedTrack, directional
	// lights, etc. — same flat-simple→complex shape as the clearances above.
	if v := s57["ORIENT"]; v != "" {
		o := newCNode()
		o.addSimple("orientationValue", v)
		root.addChild("orientation", o)
	}

	// Topmark (buoys/beacons): a co-located S-57 TOPMAR feature folded in by the
	// baker → the S-101 topmark complex attribute the TOPMAR02 CSP reads.
	if len(topmark) > 0 {
		tm := newCNode()
		tm.addSimple("topmarkDaymarkShape", topmark["shape"])
		if c := topmark["colour"]; c != "" {
			tm.simple["colour"] = e.splitValue("colour", c)
		}
		if len(tm.simple) > 0 {
			root.addChild("topmark", tm)
		}
	}

	// Light sectors / directional character (LIGHTS routed to LightSectored).
	e.buildLightSectors(root, objClass, s57)

	return root
}

// buildLightSectors synthesizes the nested sectorCharacteristics → lightSector →
// sectorLimit / directionalCharacter structure LightSectored reads, from the S-57
// LIGHTS simple attributes. One S-57 LIGHTS feature carries exactly one sector
// (one SECTR1/SECTR2/COLOUR/VALNMR set); multiple sectors at a position are
// separate co-located features, so each becomes a single-sector LightSectored.
func (e *Engine) buildLightSectors(root *cnode, objClass string, s57 map[string]string) {
	if objClass != "LIGHTS" {
		return
	}
	sectored := s57["SECTR1"] != "" && s57["SECTR2"] != ""
	directional := hasListVal(s57["CATLIT"], 1) && s57["ORIENT"] != ""
	if !sectored && !directional {
		return
	}

	sc := newCNode()
	sc.addSimple("lightCharacteristic", s57["LITCHR"])
	sc.addSimple("signalGroup", s57["SIGGRP"])
	sc.addSimple("signalPeriod", s57["SIGPER"])

	ls := newCNode()
	ls.simple["colour"] = e.splitValue("colour", s57["COLOUR"])
	ls.addSimple("valueOfNominalRange", s57["VALNMR"])
	if v := s57["LITVIS"]; v != "" {
		ls.simple["lightVisibility"] = e.splitValue("lightVisibility", v)
	}

	if sectored {
		sl := newCNode()
		one := newCNode()
		one.addSimple("sectorBearing", s57["SECTR1"])
		two := newCNode()
		two.addSimple("sectorBearing", s57["SECTR2"])
		sl.addChild("sectorLimitOne", one)
		sl.addChild("sectorLimitTwo", two)
		ls.addChild("sectorLimit", sl)
	} else { // directional
		dc := newCNode()
		o := newCNode()
		o.addSimple("orientationValue", s57["ORIENT"])
		dc.addChild("orientation", o)
		ls.addChild("directionalCharacter", dc)
	}

	sc.addChild("lightSector", ls)
	root.addChild("sectorCharacteristics", sc)
}

// splitValue splits an S-57 attribute value into the array entries the framework
// expects: enumeration/integer attributes are S-57 lists (comma-separated, e.g.
// COLOUR "1,3"); text/real/date values are single-valued and returned verbatim.
func (e *Engine) splitValue(code, val string) []string {
	if sa := e.cat.SimpleAttrs[code]; sa != nil && (sa.ValueType == "enumeration" || sa.ValueType == "integer") {
		parts := strings.Split(val, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	return []string{val}
}

// hasListVal reports whether the S-57 comma-separated list value contains want.
func hasListVal(csv string, want int) bool {
	for _, p := range strings.Split(csv, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(p)); err == nil && n == want {
			return true
		}
	}
	return false
}

// resolveCode maps an S-57 object class to its S-101 feature code, disambiguating
// the classes whose S-57→S-101 mapping is one-to-many (a conversion that depends
// on the feature's attributes, not a fixed alias). Falls back to the catalogue
// alias for every other class.
func (e *Engine) resolveCode(objClass string, attrs map[string]string) (string, bool) {
	switch objClass {
	case "LIGHTS":
		// S-57 LIGHTS aliases to LightAllAround/LightSectored/LightAirObstruction/
		// LightFogDetector; the target depends on the light's attributes.
		return e.resolveLightClass(attrs), true
	case "ADMARE":
		// Aliases to AdministrationArea and VesselTrafficServiceArea; the plain
		// administration area is the correct default (VTS is a distinct S-57 class).
		if _, ok := e.cat.FeatureTypes["AdministrationArea"]; ok {
			return "AdministrationArea", true
		}
	}
	return e.cat.FeatureCodeForS57(objClass)
}

// resolveLightClass picks the S-101 light class for an S-57 LIGHTS feature. A
// light with two sector limits, or a directional light, is portrayed by
// LightSectored (which draws sector legs/arcs and directional characters); air
// obstruction and fog detector lights have dedicated rules; everything else is
// an all-around light.
func (e *Engine) resolveLightClass(attrs map[string]string) string {
	if attrs["SECTR1"] != "" && attrs["SECTR2"] != "" {
		return "LightSectored"
	}
	catlit := attrs["CATLIT"]
	switch {
	case hasListVal(catlit, 1): // directional function
		return "LightSectored"
	case hasListVal(catlit, 6): // air obstruction
		return "LightAirObstruction"
	case hasListVal(catlit, 7): // fog detector
		return "LightFogDetector"
	}
	return "LightAllAround"
}
