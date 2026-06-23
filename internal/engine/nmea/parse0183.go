package nmea

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Sentence is a framed NMEA0183 line: the checksum has been validated (if
// present) and the address split into talker + type. Fields are the
// comma-separated values after the address token.
type Sentence struct {
	Raw    string   // the original line, sans CRLF, for the raw sniffer
	Talker string   // 2-char talker id (GP, II, AI…), or "P" for proprietary
	Type   string   // sentence formatter (RMC, GGA, DPT…)
	Fields []string // values after the address token
}

// ParseSentence frames one line: "$<addr>,f1,f2,…*hh". The leading delimiter is
// '$' (standard) or '!' (encapsulated, e.g. AIS). A checksum, when present, is
// the XOR of every byte between the delimiter and '*'; a mismatch is an error
// so the connection's status can count it. A missing checksum is tolerated.
func ParseSentence(line string) (Sentence, error) {
	line = strings.TrimSpace(strings.TrimRight(line, "\r\n"))
	if line == "" {
		return Sentence{}, errors.New("empty line")
	}
	if line[0] != '$' && line[0] != '!' {
		return Sentence{}, fmt.Errorf("no start delimiter: %.8q", line)
	}
	body := line[1:]
	if star := strings.IndexByte(body, '*'); star >= 0 {
		payload, hex := body[:star], body[star+1:]
		if len(hex) < 2 {
			return Sentence{}, errors.New("truncated checksum")
		}
		want, err := strconv.ParseUint(hex[:2], 16, 8)
		if err != nil {
			return Sentence{}, fmt.Errorf("bad checksum hex %q", hex[:2])
		}
		if byte(want) != checksum(payload) {
			return Sentence{}, fmt.Errorf("checksum mismatch: got %02X want %s", checksum(payload), hex[:2])
		}
		body = payload
	}
	parts := strings.Split(body, ",")
	addr := parts[0]
	if len(addr) < 3 {
		return Sentence{}, fmt.Errorf("short address %q", addr)
	}
	s := Sentence{Raw: line, Fields: parts[1:]}
	switch {
	case addr[0] == 'P': // proprietary, e.g. PGRMZ — variable length
		s.Talker, s.Type = "P", addr[1:]
	case len(addr) >= 5:
		s.Talker, s.Type = addr[:2], addr[2:]
	default:
		s.Type = addr
	}
	return s, nil
}

// checksum is the XOR of all bytes in payload (the chars between the start
// delimiter and '*').
func checksum(payload string) byte {
	var c byte
	for i := 0; i < len(payload); i++ {
		c ^= payload[i]
	}
	return c
}

// Parser maps framed sentences onto a VesselState. Now is injectable so tests
// are deterministic; it stamps VesselState.Updated.
type Parser struct {
	Now func() time.Time
}

func (p *Parser) now() time.Time {
	if p != nil && p.Now != nil {
		return p.Now()
	}
	return time.Now().UTC()
}

// Apply merges one sentence into vs, returning whether the type was recognized.
// Malformed individual fields are skipped (leaving prior values intact) rather
// than failing the whole sentence.
func (p *Parser) Apply(s Sentence, vs *VesselState) bool {
	f := s.Fields
	handled := true
	switch s.Type {
	case "RMC": // recommended minimum: position, SOG/COG, date/time, variation
		if pos, ok := coord(fld(f, 2), fld(f, 3), fld(f, 4), fld(f, 5)); ok {
			vs.Navigation.Position = &pos
		}
		if v, ok := num(f, 6); ok {
			vs.Navigation.SOG = &v
		}
		if v, ok := num(f, 7); ok {
			vs.Navigation.COGTrue = &v
		}
		if t, ok := rmcDatetime(fld(f, 0), fld(f, 8)); ok {
			vs.Navigation.Datetime = &t
		}
		if v, ok := num(f, 9); ok {
			if strings.EqualFold(fld(f, 10), "W") {
				v = -v
			}
			vs.Navigation.MagneticVariation = &v
		}
	case "GGA": // position with fix quality
		if fld(f, 5) != "0" {
			if pos, ok := coord(fld(f, 1), fld(f, 2), fld(f, 3), fld(f, 4)); ok {
				vs.Navigation.Position = &pos
			}
		}
	case "GLL": // position with status
		if !strings.EqualFold(fld(f, 5), "V") {
			if pos, ok := coord(fld(f, 0), fld(f, 1), fld(f, 2), fld(f, 3)); ok {
				vs.Navigation.Position = &pos
			}
		}
	case "VTG": // course & speed over ground
		if v, ok := num(f, 0); ok {
			vs.Navigation.COGTrue = &v
		}
		if v, ok := num(f, 4); ok { // SOG in knots (field 4, unit N at 5)
			vs.Navigation.SOG = &v
		}
	case "HDT": // true heading
		if v, ok := num(f, 0); ok {
			vs.Navigation.HeadingTrue = &v
		}
	case "HDM": // magnetic heading
		if v, ok := num(f, 0); ok {
			vs.Navigation.HeadingMagnetic = &v
		}
	case "HDG": // magnetic heading + deviation + variation
		if v, ok := num(f, 0); ok {
			vs.Navigation.HeadingMagnetic = &v
		}
		if v, ok := num(f, 3); ok {
			if strings.EqualFold(fld(f, 4), "W") {
				v = -v
			}
			vs.Navigation.MagneticVariation = &v
		}
	case "ROT": // rate of turn, deg/min, + to starboard
		if v, ok := num(f, 0); ok {
			vs.Navigation.RateOfTurn = &v
		}
	case "VHW": // water speed & heading; take speed through water (knots, field 4)
		if v, ok := num(f, 4); ok {
			vs.Navigation.SpeedThroughWater = &v
		}
	case "DPT": // depth below transducer + offset to surface/keel
		if d, ok := num(f, 0); ok {
			vs.Environment.Depth.BelowTransducer = &d
			if off, ok2 := num(f, 1); ok2 {
				v := d + off // +offset → surface, −offset → keel
				if off >= 0 {
					vs.Environment.Depth.BelowSurface = &v
				} else {
					vs.Environment.Depth.BelowKeel = &v
				}
			}
		}
	case "DBT": // depth below transducer (metres at field 2)
		if v, ok := num(f, 2); ok {
			vs.Environment.Depth.BelowTransducer = &v
		}
	case "DBK": // depth below keel
		if v, ok := num(f, 2); ok {
			vs.Environment.Depth.BelowKeel = &v
		}
	case "DBS": // depth below surface
		if v, ok := num(f, 2); ok {
			vs.Environment.Depth.BelowSurface = &v
		}
	case "MWV": // wind angle/speed, relative (R) or theoretical/true (T)
		ang, okA := num(f, 0)
		spd, okS := num(f, 2)
		k := toKnots(spd, fld(f, 3))
		switch strings.ToUpper(fld(f, 1)) {
		case "R":
			if okA {
				vs.Environment.Wind.AngleApparent = &ang
			}
			if okS {
				vs.Environment.Wind.SpeedApparent = &k
			}
		case "T":
			if okA {
				vs.Environment.Wind.AngleTrue = &ang
			}
			if okS {
				vs.Environment.Wind.SpeedTrue = &k
			}
		}
	case "MWD": // true wind direction & speed
		if v, ok := num(f, 0); ok {
			vs.Environment.Wind.DirectionTrue = &v
		}
		if v, ok := num(f, 4); ok { // speed in knots (field 4, unit N at 5)
			vs.Environment.Wind.SpeedTrue = &v
		}
	case "MTW": // water temperature, °C
		if v, ok := num(f, 0); ok {
			vs.Environment.Water.Temperature = &v
		}
	case "ZDA": // UTC date & time
		if t, ok := zdaDatetime(fld(f, 0), fld(f, 1), fld(f, 2), fld(f, 3)); ok {
			vs.Navigation.Datetime = &t
		}
	case "RMB": // recommended minimum navigation to active waypoint
		if v, ok := num(f, 1); ok {
			if strings.EqualFold(fld(f, 2), "L") { // direction to steer
				v = -v
			}
			vs.Route.XTE = &v
		}
		if dest := fld(f, 4); dest != "" {
			vs.Route.ActiveWaypoint = dest
		}
		if v, ok := num(f, 9); ok {
			vs.Route.DistanceToWaypoint = &v
		}
		if v, ok := num(f, 10); ok {
			vs.Route.BearingToWaypoint = &v
		}
	case "XTE": // standalone cross-track error
		if v, ok := num(f, 2); ok {
			if strings.EqualFold(fld(f, 3), "L") {
				v = -v
			}
			vs.Route.XTE = &v
		}
	default:
		handled = false
	}
	if handled {
		vs.Updated = p.now()
	}
	return handled
}

// --- field helpers ---

// fld returns field i, or "" if out of range.
func fld(fields []string, i int) string {
	if i < 0 || i >= len(fields) {
		return ""
	}
	return strings.TrimSpace(fields[i])
}

// num parses field i as a float; ok is false for empty or malformed fields.
func num(fields []string, i int) (float64, bool) {
	s := fld(fields, i)
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// coord converts an NMEA "ddmm.mmmm" (lat) or "dddmm.mmmm" (lon) value plus a
// hemisphere letter into a signed decimal-degree Position. Both lat and lon
// work because the minutes are always the two digits left of the dot.
func coord(latV, ns, lonV, ew string) (Position, bool) {
	lat, okLat := degMin(latV, ns)
	lon, okLon := degMin(lonV, ew)
	if !okLat || !okLon {
		return Position{}, false
	}
	return Position{Lat: lat, Lon: lon}, true
}

func degMin(v, hemi string) (float64, bool) {
	v = strings.TrimSpace(v)
	dot := strings.IndexByte(v, '.')
	if dot < 3 { // need at least D MM.
		return 0, false
	}
	deg, err1 := strconv.ParseFloat(v[:dot-2], 64)
	min, err2 := strconv.ParseFloat(v[dot-2:], 64)
	if err1 != nil || err2 != nil {
		return 0, false
	}
	d := deg + min/60
	switch strings.ToUpper(strings.TrimSpace(hemi)) {
	case "S", "W":
		d = -d
	}
	return d, true
}

// toKnots converts a wind/speed value to knots given its NMEA unit letter:
// N=knots, K=km/h, M=m/s. Unknown units pass through unchanged.
func toKnots(v float64, unit string) float64 {
	switch strings.ToUpper(strings.TrimSpace(unit)) {
	case "K":
		return v * 0.539957 // km/h → kn
	case "M":
		return v * 1.943844 // m/s → kn
	default: // "N" or unspecified
		return v
	}
}

// rmcDatetime combines an RMC time-of-day (hhmmss.ss) and date (ddmmyy) into UTC.
func rmcDatetime(tod, date string) (time.Time, bool) {
	hh, mm, sec, ok := timeOfDay(tod)
	if !ok || len(date) < 6 {
		return time.Time{}, false
	}
	d, e1 := strconv.Atoi(date[0:2])
	mo, e2 := strconv.Atoi(date[2:4])
	yy, e3 := strconv.Atoi(date[4:6])
	if e1 != nil || e2 != nil || e3 != nil {
		return time.Time{}, false
	}
	isec := int(sec)
	ns := int((sec - float64(isec)) * 1e9)
	// RMC carries only a two-digit year; pivot at 70 (00–69 → 20xx, 70–99 → 19xx),
	// matching the POSIX convention. ZDA, when present, supersedes this anyway.
	year := 2000 + yy
	if yy >= 70 {
		year = 1900 + yy
	}
	return time.Date(year, time.Month(mo), d, hh, mm, isec, ns, time.UTC), true
}

// zdaDatetime builds UTC from a ZDA time-of-day plus explicit day/month/4-digit year.
func zdaDatetime(tod, dd, mm, yyyy string) (time.Time, bool) {
	hh, mn, sec, ok := timeOfDay(tod)
	d, e1 := strconv.Atoi(dd)
	mo, e2 := strconv.Atoi(mm)
	y, e3 := strconv.Atoi(yyyy)
	if !ok || e1 != nil || e2 != nil || e3 != nil {
		return time.Time{}, false
	}
	isec := int(sec)
	ns := int((sec - float64(isec)) * 1e9)
	return time.Date(y, time.Month(mo), d, hh, mn, isec, ns, time.UTC), true
}

// timeOfDay parses "hhmmss" or "hhmmss.sss" into hour, minute, fractional second.
func timeOfDay(tod string) (hh, mm int, sec float64, ok bool) {
	if len(tod) < 6 {
		return 0, 0, 0, false
	}
	h, e1 := strconv.Atoi(tod[0:2])
	m, e2 := strconv.Atoi(tod[2:4])
	s, e3 := strconv.ParseFloat(tod[4:], 64)
	if e1 != nil || e2 != nil || e3 != nil {
		return 0, 0, 0, false
	}
	return h, m, s, true
}
