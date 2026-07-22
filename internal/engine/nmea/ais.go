package nmea

import (
	"sort"
	"strings"
	"sync"
	"time"

	ais "github.com/BertoldVdb/go-ais"
	"github.com/BertoldVdb/go-ais/aisnmea"
)

// ais.go decodes AIS (VDM/VDO) sentences into a TTL-evicted target store. AIS is
// the scoped binary sub-piece of NMEA0183: multi-fragment, 6-bit-armored payloads,
// which we decode with go-ais (the one dependency we vendor rather than hand-roll).
// All go-ais usage is confined to this file; the connection runner just feeds raw
// VDM/VDO lines to a per-connection feeder().

// AISTarget is a decoded AIS contact (another vessel or aid). Optional readings are
// pointers so "unknown" differs from zero. Units: degrees true, knots.
type AISTarget struct {
	MMSI        uint32    `json:"mmsi"`
	Lat         float64   `json:"lat"`
	Lon         float64   `json:"lon"`
	COG         *float64  `json:"cog,omitempty"`
	SOG         *float64  `json:"sog,omitempty"`
	Heading     *float64  `json:"heading,omitempty"`
	Name        string    `json:"name,omitempty"`        // from static data (msg 5/19/24)
	CallSign    string    `json:"callSign,omitempty"`    // msg 5/24
	ShipType    int       `json:"shipType,omitempty"`    // raw AIS type code
	TypeName    string    `json:"typeName,omitempty"`    // human label for ShipType
	Destination string    `json:"destination,omitempty"` // msg 5
	Length      int       `json:"length,omitempty"`      // metres (dim A+B)
	Beam        int       `json:"beam,omitempty"`        // metres (dim C+D)
	Draught     *float64  `json:"draught,omitempty"`     // metres (msg 5)
	Status      string    `json:"status,omitempty"`      // navigational status label (msg 1/2/3)
	Class       string    `json:"class,omitempty"`       // "A" | "B"
	LastSeen    time.Time `json:"lastSeen"`
}

// AISStore holds the latest target per MMSI, evicting ones unheard-from for ttl.
type AISStore struct {
	mu      sync.Mutex
	targets map[uint32]*AISTarget
	sources map[uint32]string // per-MMSI provenance (source/plugin id); not serialized
	ttl     time.Duration
	ver     uint64
	now     func() time.Time
}

func NewAISStore(ttl time.Duration) *AISStore {
	if ttl <= 0 {
		ttl = 6 * time.Minute
	}
	return &AISStore{targets: map[uint32]*AISTarget{}, ttl: ttl, now: time.Now}
}

func (s *AISStore) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// feeder returns a per-connection sink: feed it raw VDM/VDO lines and complete
// (reassembled) messages update the store. The aisnmea codec is stateful and not
// safe for concurrent use, so each connection gets its own feeder.
func (s *AISStore) feeder() func(line string) {
	codec := aisnmea.NMEACodecNew(ais.CodecNew(false, false))
	return func(line string) {
		pkt, err := codec.ParseSentence(line)
		if err != nil || pkt == nil {
			return
		}
		s.apply(pkt)
	}
}

// Feeder exposes feeder for external decoders that own the transport — e.g. the
// reference tcp-client plugin, which parses AIS itself and mirrors this store before
// publishing targets back to the host (spec §12 milestone).
func (s *AISStore) Feeder() func(line string) { return s.feeder() }

func (s *AISStore) apply(p *aisnmea.VdmPacket) {
	if p == nil || p.Packet == nil || p.MessageType == "VDO" { // VDO is own-ship, not a target
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	upsert := func(mmsi uint32) *AISTarget {
		t := s.targets[mmsi]
		if t == nil {
			t = &AISTarget{MMSI: mmsi}
			s.targets[mmsi] = t
		}
		t.LastSeen = s.clock()
		return t
	}
	setPos := func(t *AISTarget, lat, lon float64) {
		if lat <= 90 && lon <= 180 { // 91/181 = not available
			t.Lat, t.Lon = lat, lon
		}
	}
	setDim := func(t *AISTarget, d ais.FieldDimension) {
		if l := int(d.A) + int(d.B); l > 0 {
			t.Length = l
		}
		if b := int(d.C) + int(d.D); b > 0 {
			t.Beam = b
		}
	}
	setName := func(t *AISTarget, n string) {
		if n = trimAIS(n); n != "" {
			t.Name = n
		}
	}
	switch m := p.Packet.(type) {
	case ais.PositionReport: // msg 1/2/3 (Class A)
		t := upsert(m.UserID)
		t.Class = "A"
		setPos(t, float64(m.Latitude), float64(m.Longitude))
		t.COG, t.SOG, t.Heading = cogOpt(float64(m.Cog)), sogOpt(float64(m.Sog)), hdgOpt(m.TrueHeading)
		t.Status = navStatusLabel(m.NavigationalStatus)
	case ais.StandardClassBPositionReport: // msg 18 (Class B)
		t := upsert(m.UserID)
		t.Class = "B"
		setPos(t, float64(m.Latitude), float64(m.Longitude))
		t.COG, t.SOG, t.Heading = cogOpt(float64(m.Cog)), sogOpt(float64(m.Sog)), hdgOpt(m.TrueHeading)
	case ais.ExtendedClassBPositionReport: // msg 19 (Class B + name/type/dims)
		t := upsert(m.UserID)
		t.Class = "B"
		setPos(t, float64(m.Latitude), float64(m.Longitude))
		t.COG, t.SOG, t.Heading = cogOpt(float64(m.Cog)), sogOpt(float64(m.Sog)), hdgOpt(m.TrueHeading)
		setName(t, m.Name)
		setShipType(t, m.Type)
		setDim(t, m.Dimension)
	case ais.ShipStaticData: // msg 5 (Class A static — voyage data)
		t := upsert(m.UserID)
		if t.Class == "" {
			t.Class = "A"
		}
		setName(t, m.Name)
		setShipType(t, m.Type)
		setDim(t, m.Dimension)
		if cs := trimAIS(m.CallSign); cs != "" {
			t.CallSign = cs
		}
		if d := trimAIS(m.Destination); d != "" {
			t.Destination = d
		}
		if dr := float64(m.MaximumStaticDraught); dr > 0 {
			t.Draught = &dr
		}
	case ais.StaticDataReport: // msg 24 (Class B static — two parts)
		t := upsert(m.UserID)
		if t.Class == "" {
			t.Class = "B"
		}
		setName(t, m.ReportA.Name)
		setShipType(t, m.ReportB.ShipType)
		setDim(t, m.ReportB.Dimension)
		if cs := trimAIS(m.ReportB.CallSign); cs != "" {
			t.CallSign = cs
		}
	default:
		return // not a type we track; don't bump the version
	}
	s.ver++
}

func setShipType(t *AISTarget, code uint8) {
	if code == 0 {
		return
	}
	t.ShipType = int(code)
	if l := shipTypeLabel(code); l != "" {
		t.TypeName = l
	}
}

// Upsert merges an externally-decoded target (e.g. from a plugin's ais.publish,
// spec §4) into the store, attributed to source, and bumps the version. Incoming
// non-zero/non-nil fields win; unset fields preserve the existing value, so a
// position-only and a static-only update for the same MMSI compose exactly as the
// built-in VDM decode path does. A zero MMSI is ignored.
func (s *AISStore) Upsert(t AISTarget, source string) {
	if t.MMSI == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sources == nil {
		s.sources = map[uint32]string{}
	}
	now := s.clock()
	if existing := s.targets[t.MMSI]; existing != nil {
		mergeTarget(existing, &t, now)
	} else {
		cp := t
		if cp.LastSeen.IsZero() {
			cp.LastSeen = now
		}
		s.targets[t.MMSI] = &cp
	}
	s.sources[t.MMSI] = source
	s.ver++
}

// EvictSource removes every target last written by source (e.g. when a plugin's
// ais.write grant is revoked) and returns how many were removed.
func (s *AISStore) EvictSource(source string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for mmsi, src := range s.sources {
		if src == source {
			delete(s.targets, mmsi)
			delete(s.sources, mmsi)
			n++
		}
	}
	if n > 0 {
		s.ver++
	}
	return n
}

// mergeTarget overlays src's set fields onto dst and refreshes LastSeen.
func mergeTarget(dst, src *AISTarget, now time.Time) {
	dst.LastSeen = now
	if src.Lat != 0 || src.Lon != 0 {
		dst.Lat, dst.Lon = src.Lat, src.Lon
	}
	if src.COG != nil {
		dst.COG = src.COG
	}
	if src.SOG != nil {
		dst.SOG = src.SOG
	}
	if src.Heading != nil {
		dst.Heading = src.Heading
	}
	if src.Name != "" {
		dst.Name = src.Name
	}
	if src.CallSign != "" {
		dst.CallSign = src.CallSign
	}
	if src.ShipType != 0 {
		dst.ShipType = src.ShipType
	}
	if src.TypeName != "" {
		dst.TypeName = src.TypeName
	}
	if src.Destination != "" {
		dst.Destination = src.Destination
	}
	if src.Length != 0 {
		dst.Length = src.Length
	}
	if src.Beam != 0 {
		dst.Beam = src.Beam
	}
	if src.Draught != nil {
		dst.Draught = src.Draught
	}
	if src.Status != "" {
		dst.Status = src.Status
	}
	if src.Class != "" {
		dst.Class = src.Class
	}
}

// Snapshot returns the live (non-stale) targets, sorted by MMSI, pruning expired
// ones as a side effect.
func (s *AISStore) Snapshot() []AISTarget {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := s.clock().Add(-s.ttl)
	out := make([]AISTarget, 0, len(s.targets))
	for mmsi, t := range s.targets {
		if t.LastSeen.Before(cutoff) {
			delete(s.targets, mmsi)
			delete(s.sources, mmsi)
			continue
		}
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MMSI < out[j].MMSI })
	return out
}

// Version increments on every applied update — cheap change detection for SSE.
func (s *AISStore) Version() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ver
}

// cogOpt/sogOpt/hdgOpt map AIS sentinel values to nil ("not available").
func cogOpt(v float64) *float64 {
	if v >= 360 {
		return nil
	}
	return &v
}

func sogOpt(v float64) *float64 {
	if v >= 102.3 {
		return nil
	}
	return &v
}

func hdgOpt(th uint16) *float64 {
	if th >= 511 {
		return nil
	}
	v := float64(th)
	return &v
}

// trimAIS strips AIS string padding (@/space) and surrounding whitespace.
func trimAIS(s string) string {
	return strings.TrimSpace(strings.TrimRight(s, " @"))
}

// navStatusLabel maps an AIS navigational-status code to a label ("" if unknown).
func navStatusLabel(code uint8) string {
	switch code {
	case 0:
		return "Under way (engine)"
	case 1:
		return "At anchor"
	case 2:
		return "Not under command"
	case 3:
		return "Restricted manoeuvrability"
	case 4:
		return "Constrained by draught"
	case 5:
		return "Moored"
	case 6:
		return "Aground"
	case 7:
		return "Fishing"
	case 8:
		return "Under way (sailing)"
	case 11:
		return "Towing astern"
	case 12:
		return "Pushing ahead"
	default: // 9,10,13,14 reserved; 15 = not defined
		return ""
	}
}

// shipTypeLabel maps an AIS ship-type code to a coarse label per the ITU-R M.1371
// ranges ("" if unknown). The tens digit carries the category for 20–99.
func shipTypeLabel(code uint8) string {
	switch {
	case code == 30:
		return "Fishing"
	case code == 31 || code == 32 || code == 52:
		return "Tug/Towing"
	case code == 35:
		return "Military"
	case code == 36:
		return "Sailing"
	case code == 37:
		return "Pleasure craft"
	case code == 50:
		return "Pilot vessel"
	case code == 51:
		return "Search & rescue"
	case code == 53:
		return "Port tender"
	case code == 55:
		return "Law enforcement"
	case code >= 20 && code <= 29:
		return "Wing-in-ground"
	case code >= 40 && code <= 49:
		return "High-speed craft"
	case code >= 60 && code <= 69:
		return "Passenger"
	case code >= 70 && code <= 79:
		return "Cargo"
	case code >= 80 && code <= 89:
		return "Tanker"
	case code >= 90 && code <= 99:
		return "Other"
	default:
		return ""
	}
}
