// Package s57 reads IHO S-57 Electronic Navigational Chart cells for the
// chart library: header metadata (DSID/DSPM), coverage bounds (M_COVR), the
// exchange-set catalogue (CATALOG.031), and parsed features where a consumer
// still needs them (e.g. the NMEA simulator's water mask).
//
// All tiling, portrayal, and styling is done by the native libtile57 engine —
// this package deliberately carries no rendering support.
//
// # Metadata / coverage parse
//
//	opts := s57.DefaultParseOptions()
//	opts.ObjectClassFilter = []string{"M_COVR"} // cheap: header + coverage only
//	chart, err := s57.ParseWithOptions("US5MA22M.000", opts)
//	fmt.Printf("%s 1:%d %+v\n", chart.DatasetName(), chart.CompilationScale(), chart.Bounds())
//
// # Feature access
//
//	chart, err := s57.ParseWithOptions("US5MA22M.000", s57.DefaultParseOptions())
//	for _, f := range chart.Features() {
//	    class := f.ObjectClass()      // "DEPARE", "DRGARE", …
//	    geom := f.Geometry()          // Rings / Coordinates
//	    depth, ok := f.Attribute("DRVAL1")
//	    _ = class; _ = geom; _, _ = depth, ok
//	}
//
// Update files (.001, .002, …) beside the base cell are applied automatically
// unless ParseOptions.ApplyUpdates is false.
package s57
