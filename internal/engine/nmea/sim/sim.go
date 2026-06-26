// Package sim is a NMEA0183 traffic generator for development and testing. It
// dead-reckons an own-ship and a handful of AIS targets (one optionally on a
// collision course) and emits the matching sentences — own-ship instruments by
// hand, AIS via go-ais encoding — so the rest of the stack (parser, AISStore,
// overlays, CPA) can be exercised without a real feed. Driven by `cp simulate`.
package sim

import (
	"fmt"
	"math"
	"math/rand"
	"time"

	ais "github.com/BertoldVdb/go-ais"
	"github.com/BertoldVdb/go-ais/aisnmea"
)

// Vessel is a moving point: position plus course-over-ground and speed.
type Vessel struct {
	MMSI     uint32
	Name     string
	Type     uint8 // AIS ship type
	Class    byte  // 'A' | 'B'
	Lat, Lon float64
	Course   float64      // degrees true
	Speed    float64      // knots
	Turn     float64      // deg/min, for a constant gentle course change
	wps      [][2]float64 // optional route (lat,lon waypoints) to steer through
	wpi      int          // current waypoint index
}

// Sim holds the world: own-ship + AIS targets, and the AIS encoder.
type Sim struct {
	Own     Vessel
	Targets []*Vessel
	codec   *aisnmea.NMEACodec
	depth   float64
	// Own-ship heading model. leeway is the crab angle (heading = COG − leeway);
	// fixed at 7° normally. In sailing mode own-ship weaves about baseCourse and
	// leeway swings, so the heading line and COG vector visibly diverge.
	leeway     float64
	baseCourse float64
	sailing    bool
	phase      float64 // seconds of elapsed sailing time, drives the weave
}

// Options configures a new Sim.
type Options struct {
	Lat, Lon  float64      // own-ship start
	Course    float64      // own-ship course (deg true)
	Speed     float64      // own-ship speed (kn)
	OwnRoute  [][2]float64 // optional own-ship route (lat,lon waypoints) to steer through
	Targets   int          // number of AIS targets
	Collision bool         // make one target converge on own-ship
	Sailing   bool         // own-ship tacks (COG weaves) with a varying leeway (heading ≠ COG)
	Seed      int64
	Water     *WaterMask // optional: constrain placement to navigable water from a cell
}

// New builds a Sim: own-ship at the given start, plus N AIS targets scattered
// within ~3 nm on random courses; with Collision, one target is aimed to pass
// close to own-ship.
func New(o Options) *Sim {
	if o.Speed == 0 {
		o.Speed = 6
	}
	rng := rand.New(rand.NewSource(o.Seed))
	water := o.Water
	// With a cell, keep own-ship in navigable water too.
	if water != nil && !water.IsWater(o.Lat, o.Lon) {
		if la, lo, ok := water.Sample(rng); ok {
			o.Lat, o.Lon = la, lo
		}
	}
	// place returns a navigable point: a water sample if we have a mask, else the
	// dead-reckoned point from own-ship at (brg,dist).
	place := func(brg, dist float64) (float64, float64) {
		if water != nil {
			if la, lo, ok := water.Sample(rng); ok {
				return la, lo
			}
		}
		return destination(o.Lat, o.Lon, brg, dist)
	}
	s := &Sim{
		Own:        Vessel{Name: "OWN", Lat: o.Lat, Lon: o.Lon, Course: o.Course, Speed: o.Speed},
		codec:      aisnmea.NMEACodecNew(ais.CodecNew(false, false)),
		depth:      12,
		leeway:     7, // fixed crab; sailing mode overrides this each step
		baseCourse: o.Course,
		sailing:    o.Sailing,
	}
	// Own-ship route: steer through the given waypoints (same machinery as targets),
	// starting pointed at the first one so the opening fix already heads down-route.
	if len(o.OwnRoute) > 0 {
		s.Own.wps = o.OwnRoute
		s.Own.Course = bearing(s.Own.Lat, s.Own.Lon, o.OwnRoute[0][0], o.OwnRoute[0][1])
	}
	names := []string{"SEA BREEZE", "NORDIC STAR", "BAY TRADER", "MISS MOLLY", "EL TORO", "PACIFICA", "ORION", "KESTREL", "ARGO", "TIDEWATER"}
	types := []uint8{30, 36, 37, 52, 60, 70, 80} // fishing, sailing, pleasure, tug, passenger, cargo, tanker
	for i := 0; i < o.Targets; i++ {
		brg := rng.Float64() * 360
		dist := 0.5 + rng.Float64()*2.5 // 0.5–3 nm out
		lat, lon := place(brg, dist)
		t := &Vessel{
			MMSI:   uint32(244000000 + rng.Intn(1000000)),
			Name:   names[i%len(names)],
			Type:   types[rng.Intn(len(types))],
			Class:  'A',
			Lat:    lat,
			Lon:    lon,
			Course: rng.Float64() * 360,
			Speed:  2 + rng.Float64()*12,
		}
		if rng.Intn(3) == 0 {
			t.Class = 'B'
		}
		// Realistic variation: ~⅓ run straight, ~⅓ hold a gentle turn, ~⅓ follow a
		// multi-leg route (changing course toward each waypoint).
		switch rng.Intn(3) {
		case 1:
			t.Turn = (rng.Float64()*2 - 1) * 6 // ±6 deg/min
		case 2:
			for k, n := 0, 3+rng.Intn(2); k < n; k++ {
				b, d := rng.Float64()*360, 0.5+rng.Float64()*2.5
				wla, wlo := place(b, d)
				t.wps = append(t.wps, [2]float64{wla, wlo})
			}
		}
		s.Targets = append(s.Targets, t)
	}
	if o.Collision && len(s.Targets) > 0 {
		// Place a target ~2.5 nm ahead and aim it back at own-ship for a low CPA.
		// Straight line (no route/turn) so the encounter is deterministic.
		t := s.Targets[0]
		t.Name = "CPA ALERT"
		t.wps, t.Turn = nil, 0
		t.Lat, t.Lon = place(o.Course+25, 2.5)
		t.Course = math.Mod(bearing(t.Lat, t.Lon, o.Lat, o.Lon), 360)
		t.Speed = 10
	}
	return s
}

// Step advances every vessel by dt seconds (dead reckoning + gentle turns).
func (s *Sim) Step(dt float64) {
	if s.sailing {
		s.stepSail(dt)
	} else {
		advance(&s.Own, dt)
	}
	for _, t := range s.Targets {
		advance(t, dt)
	}
}

// stepSail weaves own-ship like a boat working to windward: COG tacks ±42° about
// the base course (≈110 s period) while leeway swings ±14° on a different period
// (≈55 s, so it beats against the tack) — heading = COG − leeway thus separates
// from COG and the gap keeps changing, the case the heading line exists to show.
func (s *Sim) stepSail(dt float64) {
	s.phase += dt
	s.Own.Course = math.Mod(s.baseCourse+42*math.Sin(s.phase*2*math.Pi/110)+360, 360)
	s.leeway = 14 * math.Sin(s.phase*2*math.Pi/55) // flips sign with the tack
	nm := s.Own.Speed * (dt / 3600)
	s.Own.Lat, s.Own.Lon = destination(s.Own.Lat, s.Own.Lon, s.Own.Course, nm)
}

func advance(v *Vessel, dt float64) {
	switch {
	case len(v.wps) > 0: // route-follower: steer toward the current waypoint
		tgt := v.wps[v.wpi]
		if dist(v.Lat, v.Lon, tgt[0], tgt[1]) < 0.08 { // reached → next (loops)
			v.wpi = (v.wpi + 1) % len(v.wps)
			tgt = v.wps[v.wpi]
		}
		v.Course = steer(v.Course, bearing(v.Lat, v.Lon, tgt[0], tgt[1]), 20*(dt/60)) // ≤20°/min
	case v.Turn != 0: // constant gentle turn
		v.Course = math.Mod(v.Course+v.Turn*(dt/60)+360, 360)
	}
	nm := v.Speed * (dt / 3600)
	v.Lat, v.Lon = destination(v.Lat, v.Lon, v.Course, nm)
}

// OwnSentences returns all own-ship instrument sentences for this instant
// (position/motion + environment).
func (s *Sim) OwnSentences() []string {
	return append(s.NavSentences(), s.EnvSentences()...)
}

// NavSentences returns the position/motion sentences (GGA/RMC/VTG/HDT/VHW). These
// are what stop when a GPS feed drops, so `cp simulate --drop-gps` withholds them
// while EnvSentences keep flowing — exactly what a real signal loss looks like.
func (s *Sim) NavSentences() []string {
	now := time.Now().UTC()
	hms := now.Format("150405.00")
	dmy := now.Format("020106")
	lat, ns := toNMEALat(s.Own.Lat)
	lon, ew := toNMEALon(s.Own.Lon)
	cog := s.Own.Course
	sog := s.Own.Speed
	hdg := math.Mod(cog-s.leeway+360, 360) // crab/leeway so heading ≠ COG (head-up ≠ course-up)
	return []string{
		line(fmt.Sprintf("GPGGA,%s,%s,%s,%s,%s,1,10,0.8,2,M,-33.0,M,,", hms, lat, ns, lon, ew)),
		line(fmt.Sprintf("GPRMC,%s,A,%s,%s,%s,%s,%.1f,%.1f,%s,,,A", hms, lat, ns, lon, ew, sog, cog, dmy)),
		line(fmt.Sprintf("GPVTG,%.1f,T,,M,%.1f,N,,K,A", cog, sog)),
		line(fmt.Sprintf("HEHDT,%.1f,T", hdg)),
		line(fmt.Sprintf("IIVHW,%.1f,T,,M,%.1f,N,,K", cog, sog)),
	}
}

// EnvSentences returns the environment sentences (depth/wind/water temp), which
// keep flowing even when the GPS drops.
func (s *Sim) EnvSentences() []string {
	return []string{
		line(fmt.Sprintf("SDDPT,%.1f,0.5,", s.depth)),
		line(fmt.Sprintf("IIMWV,%.1f,R,%.1f,N,A", 45.0, 12.0)),
		line(fmt.Sprintf("IIMTW,%.1f,C", 18.0)),
	}
}

// AISPositions returns one VDM position report per target.
func (s *Sim) AISPositions() []string {
	var out []string
	for _, t := range s.Targets {
		out = append(out, s.encodeAIS(positionPacket(t))...)
	}
	return out
}

// AISStatics returns one VDM static-data report (name/type) per target.
func (s *Sim) AISStatics() []string {
	var out []string
	for _, t := range s.Targets {
		out = append(out, s.encodeAIS(staticPacket(t))...)
	}
	return out
}

func (s *Sim) encodeAIS(p ais.Packet) []string {
	return s.codec.EncodeSentence(aisnmea.VdmPacket{TalkerID: "AI", MessageType: "VDM", Channel: 1, Packet: p})
}

func positionPacket(t *Vessel) ais.Packet {
	hdr := ais.Header{MessageID: 1, UserID: t.MMSI}
	if t.Class == 'B' {
		hdr.MessageID = 18
		return ais.StandardClassBPositionReport{
			Header: hdr, Valid: true,
			Sog: ais.Field10(t.Speed), Cog: ais.Field10(t.Course),
			Latitude: ais.FieldLatLonFine(t.Lat), Longitude: ais.FieldLatLonFine(t.Lon),
			TrueHeading: uint16(t.Course),
		}
	}
	return ais.PositionReport{
		Header: hdr, Valid: true, NavigationalStatus: 0,
		Sog: ais.Field10(t.Speed), Cog: ais.Field10(t.Course),
		Latitude: ais.FieldLatLonFine(t.Lat), Longitude: ais.FieldLatLonFine(t.Lon),
		TrueHeading: uint16(t.Course),
	}
}

func staticPacket(t *Vessel) ais.Packet {
	return ais.ShipStaticData{
		Header: ais.Header{MessageID: 5, UserID: t.MMSI}, Valid: true,
		Name: t.Name, Type: t.Type, CallSign: "SIM",
		Dimension: ais.FieldDimension{A: 20, B: 10, C: 4, D: 4},
	}
}

// --- geo + formatting helpers ---

func line(body string) string {
	var c byte
	for i := 0; i < len(body); i++ {
		c ^= body[i]
	}
	return fmt.Sprintf("$%s*%02X", body, c)
}

func toNMEALat(lat float64) (string, string) {
	h := "N"
	if lat < 0 {
		h, lat = "S", -lat
	}
	d := int(lat)
	return fmt.Sprintf("%02d%07.4f", d, (lat-float64(d))*60), h
}

func toNMEALon(lon float64) (string, string) {
	h := "E"
	if lon < 0 {
		h, lon = "W", -lon
	}
	d := int(lon)
	return fmt.Sprintf("%03d%07.4f", d, (lon-float64(d))*60), h
}

const earthNm = 3440.065

// destination returns the point reached from (lat,lon) along bearingDeg for distNm.
func destination(lat, lon, bearingDeg, distNm float64) (float64, float64) {
	br := bearingDeg * math.Pi / 180
	d := distNm / earthNm
	la1, lo1 := lat*math.Pi/180, lon*math.Pi/180
	la2 := math.Asin(math.Sin(la1)*math.Cos(d) + math.Cos(la1)*math.Sin(d)*math.Cos(br))
	lo2 := lo1 + math.Atan2(math.Sin(br)*math.Sin(d)*math.Cos(la1), math.Cos(d)-math.Sin(la1)*math.Sin(la2))
	return la2 * 180 / math.Pi, lo2 * 180 / math.Pi
}

// bearing returns the initial true bearing from point 1 to point 2 in degrees.
func bearing(lat1, lon1, lat2, lon2 float64) float64 {
	la1, la2 := lat1*math.Pi/180, lat2*math.Pi/180
	dlo := (lon2 - lon1) * math.Pi / 180
	y := math.Sin(dlo) * math.Cos(la2)
	x := math.Cos(la1)*math.Sin(la2) - math.Sin(la1)*math.Cos(la2)*math.Cos(dlo)
	return math.Mod(math.Atan2(y, x)*180/math.Pi+360, 360)
}

// dist is the approximate distance between two points in nautical miles.
func dist(lat1, lon1, lat2, lon2 float64) float64 {
	dlat := (lat2 - lat1) * 60
	dlon := (lon2 - lon1) * 60 * math.Cos((lat1+lat2)/2*math.Pi/180)
	return math.Hypot(dlat, dlon)
}

// steer turns cur toward want by at most maxStep degrees, taking the short way.
func steer(cur, want, maxStep float64) float64 {
	d := math.Mod(want-cur+540, 360) - 180 // signed delta in [-180,180)
	if d > maxStep {
		d = maxStep
	} else if d < -maxStep {
		d = -maxStep
	}
	return math.Mod(cur+d+360, 360)
}
