package sim

import (
	"fmt"
	"strings"
)

// scenarios.go holds named, ready-to-run simulation presets set around Annapolis,
// MD on the Chesapeake Bay — the same waters the default cell covers. Each is a
// realistic situation for exercising own-ship UX (follow / look-ahead / heading
// line / stale-GPS) and AIS/CPA: a harbour departure, a bay crossing, a Bay
// Bridge transit, sitting at anchor, a close-quarters CPA, and a tight river run.
// `cp simulate --scenario <name>` loads one; coordinates are WGS-84 lat,lon.

// Scenario is a preset world: an own-ship start (+ optional route to steer) and a
// traffic mix. Seed and the optional water mask come from the command flags.
type Scenario struct {
	Name      string
	Desc      string
	Lat, Lon  float64      // own-ship start
	Course    float64      // initial course (deg true); ignored when Route is set
	Speed     float64      // knots
	Route     [][2]float64 // optional own-ship waypoints (lat,lon)
	Targets   int          // AIS targets
	Collision bool         // put one target on a collision course
	Sailing   bool         // own-ship tacks with varying leeway (heading ≠ COG)
}

// Options turns the scenario into sim Options, folding in the run's seed and the
// optional navigable-water mask.
func (sc Scenario) Options(seed int64, water *WaterMask) Options {
	return Options{
		Lat: sc.Lat, Lon: sc.Lon, Course: sc.Course, Speed: sc.Speed,
		OwnRoute: sc.Route, Targets: sc.Targets, Collision: sc.Collision,
		Sailing: sc.Sailing, Seed: seed, Water: water,
	}
}

// scenarios is the ordered registry (order = list/help order).
var scenarios = []Scenario{
	{
		Name: "harbor", Desc: "Departing Annapolis harbour (Severn mouth out past Tolly Point)",
		Lat: 38.9785, Lon: -76.4770, Speed: 5, Targets: 8,
		Route: [][2]float64{{38.972, -76.466}, {38.962, -76.452}, {38.958, -76.440}},
	},
	{
		Name: "bay-crossing", Desc: "Crossing the Chesapeake from Annapolis to the Eastern Shore",
		Lat: 38.966, Lon: -76.430, Speed: 7, Targets: 6,
		Route: [][2]float64{{38.964, -76.400}, {38.962, -76.370}, {38.960, -76.346}},
	},
	{
		Name: "bay-bridge", Desc: "Transiting north under the Chesapeake Bay Bridge spans",
		Lat: 38.975, Lon: -76.392, Speed: 6, Targets: 5,
		Route: [][2]float64{{38.986, -76.390}, {38.995, -76.389}, {39.006, -76.388}},
	},
	{
		Name: "anchorage", Desc: "At anchor in Whitehall Bay (idle GPS + heading-at-rest)",
		Lat: 38.992, Lon: -76.448, Course: 200, Speed: 0, Targets: 3,
	},
	{
		Name: "collision", Desc: "Close-quarters crossing in the open bay (CPA alert)",
		Lat: 38.950, Lon: -76.420, Course: 80, Speed: 7, Targets: 6, Collision: true,
	},
	{
		Name: "river", Desc: "Gunkholing up the South River (tight quarters, course-up)",
		Lat: 38.913, Lon: -76.458, Speed: 4, Targets: 4,
		Route: [][2]float64{{38.917, -76.474}, {38.920, -76.492}, {38.922, -76.508}},
	},
	{
		Name: "sailing", Desc: "Sailboat working to windward in the bay (COG tacks, heading ≠ COG)",
		Lat: 38.945, Lon: -76.415, Course: 30, Speed: 5, Targets: 4, Sailing: true,
	},
}

// ScenarioByName returns the named scenario (case-insensitive), or ok=false.
func ScenarioByName(name string) (Scenario, bool) {
	for _, sc := range scenarios {
		if strings.EqualFold(sc.Name, name) {
			return sc, true
		}
	}
	return Scenario{}, false
}

// ScenarioList is a human-readable "name — description" listing, one per line.
func ScenarioList() string {
	var b strings.Builder
	for _, sc := range scenarios {
		fmt.Fprintf(&b, "  %-13s %s\n", sc.Name, sc.Desc)
	}
	return b.String()
}
