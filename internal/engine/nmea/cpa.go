package nmea

import "math"

// cpa.go computes the Closest Point of Approach (CPA) and Time to CPA (TCPA)
// between own-ship and an AIS target — the core of collision detection. Both
// vessels are treated as moving at constant velocity over a short horizon, in a
// local flat-earth frame (nautical miles) centred on own-ship; good to well
// within a few nm, which is the range that matters for collision avoidance.

// CPA returns the closest approach distance (nm) and time to it (minutes) given
// each vessel's position (deg), course-over-ground (deg true) and speed (kn).
// ok is false when there's no relative motion (parallel/identical velocity), in
// which case cpaNm is the current range and tcpaMin is 0. A negative tcpaMin
// means the closest approach is in the past (the vessels are diverging).
func CPA(ownLat, ownLon, ownCog, ownSog, tLat, tLon, tCog, tSog float64) (cpaNm, tcpaMin float64, ok bool) {
	const rad = math.Pi / 180
	cosLat := math.Cos(ownLat * rad)
	// Relative position of the target w.r.t. own-ship, in nm (x=east, y=north).
	rx := (tLon - ownLon) * 60 * cosLat
	ry := (tLat - ownLat) * 60
	// Velocity vectors in kn (course is from north, clockwise).
	ovx, ovy := ownSog*math.Sin(ownCog*rad), ownSog*math.Cos(ownCog*rad)
	tvx, tvy := tSog*math.Sin(tCog*rad), tSog*math.Cos(tCog*rad)
	// Relative velocity (target − own).
	vx, vy := tvx-ovx, tvy-ovy
	v2 := vx*vx + vy*vy
	if v2 < 1e-9 {
		return math.Hypot(rx, ry), 0, false
	}
	tcpaH := -(rx*vx + ry*vy) / v2 // hours
	cx, cy := rx+vx*tcpaH, ry+vy*tcpaH
	return math.Hypot(cx, cy), tcpaH * 60, true
}
