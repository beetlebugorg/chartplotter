package bake

// pointInRings reports whether (lon, lat) is inside the even-odd union of the
// polygon rings (ray-cast). Shared geometry helper used by the bake emit path;
// the S-52 per-cell spatial index that used to live here (for the now-removed
// lookup+CSP portrayal) is gone with the S-52 engine.
func pointInRings(lon, lat float64, rings [][][]float64) bool {
	inside := false
	for _, ring := range rings {
		n := len(ring)
		if n < 3 {
			continue
		}
		j := n - 1
		for i := 0; i < n; i++ {
			xi, yi := ring[i][0], ring[i][1]
			xj, yj := ring[j][0], ring[j][1]
			if (yi > lat) != (yj > lat) {
				xint := (xj-xi)*(lat-yi)/(yj-yi) + xi
				if lon < xint {
					inside = !inside
				}
			}
			j = i
		}
	}
	return inside
}
