// Package nmea ingests NMEA0183 sentences into a normalized, SignalK-shaped
// vessel-state model. It renders nothing: a connection parses sentences into
// VesselState, and downstream consumers (own-ship marker, AIS overlay,
// instrument HUD) read that model. When NMEA2000 or SignalK are added later
// they feed the same VesselState and every renderer is reused unchanged.
//
// Units are the practical marine display units, not SI, and are documented per
// field: angles in degrees, speeds in knots, depths in metres, temperature in
// °C. "Unknown" is distinct from "zero" — optional readings are pointers and
// are nil until a sentence supplies them.
//
// This file defines the model. parse0183.go maps sentences onto it; replay.go
// is the headless harness (also the basis of the future File transport).
package nmea

import (
	"sync"
	"time"
)

// Position is a WGS-84 fix in decimal degrees.
type Position struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// Navigation holds own-ship motion and orientation.
type Navigation struct {
	Position          *Position  `json:"position,omitempty"`
	COGTrue           *float64   `json:"cogTrue,omitempty"`           // degrees true
	SOG               *float64   `json:"sog,omitempty"`               // knots
	HeadingTrue       *float64   `json:"headingTrue,omitempty"`       // degrees true
	HeadingMagnetic   *float64   `json:"headingMagnetic,omitempty"`   // degrees magnetic
	MagneticVariation *float64   `json:"magneticVariation,omitempty"` // degrees, +East
	RateOfTurn        *float64   `json:"rateOfTurn,omitempty"`        // deg/min, +starboard
	SpeedThroughWater *float64   `json:"speedThroughWater,omitempty"` // knots
	Datetime          *time.Time `json:"datetime,omitempty"`          // UTC, from RMC/ZDA
}

// Depth readings, each in metres, relative to a different datum.
type Depth struct {
	BelowTransducer *float64 `json:"belowTransducer,omitempty"`
	BelowKeel       *float64 `json:"belowKeel,omitempty"`
	BelowSurface    *float64 `json:"belowSurface,omitempty"`
}

// Wind, apparent (relative to the moving vessel) and true (over ground).
type Wind struct {
	AngleApparent *float64 `json:"angleApparent,omitempty"` // degrees, 0=bow, +clockwise
	SpeedApparent *float64 `json:"speedApparent,omitempty"` // knots
	AngleTrue     *float64 `json:"angleTrue,omitempty"`     // degrees relative to bow
	SpeedTrue     *float64 `json:"speedTrue,omitempty"`     // knots
	DirectionTrue *float64 `json:"directionTrue,omitempty"` // degrees true (compass)
}

// Water holds sea-water properties.
type Water struct {
	Temperature *float64 `json:"temperature,omitempty"` // °C
}

// Environment groups sensor readings about the surroundings.
type Environment struct {
	Depth Depth `json:"depth"`
	Water Water `json:"water"`
	Wind  Wind  `json:"wind"`
}

// Route holds navigation to the active waypoint (from RMB/XTE/APB/BWC).
type Route struct {
	XTE                *float64 `json:"xte,omitempty"`                // nm cross-track; +steer right, −steer left
	BearingToWaypoint  *float64 `json:"bearingToWaypoint,omitempty"`  // degrees true
	DistanceToWaypoint *float64 `json:"distanceToWaypoint,omitempty"` // nm
	ActiveWaypoint     string   `json:"activeWaypoint,omitempty"`     // destination waypoint id
}

// VesselState is the whole normalized model. AIS targets live separately (added
// in a later step) because they are keyed per-MMSI with TTL eviction.
type VesselState struct {
	Navigation  Navigation  `json:"navigation"`
	Environment Environment `json:"environment"`
	Route       Route       `json:"route"`
	Updated     time.Time   `json:"updated"` // last time any field changed (UTC)
}

// Store is a concurrency-safe holder of the latest VesselState, shared by the
// connection manager (writer) and the stream endpoints (readers). The zero
// value is ready to use. Throttled delta emission is layered on in a later step;
// this scaffold provides the safe read/mutate primitives the parser needs.
type Store struct {
	mu    sync.RWMutex
	state VesselState
}

// Snapshot returns a copy of the current state, safe to read without the lock.
func (s *Store) Snapshot() VesselState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

// Apply parses a single sentence and merges it into the held state under the
// write lock. It reports whether the sentence was a recognized type.
func (s *Store) Apply(p *Parser, sent Sentence) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return p.Apply(sent, &s.state)
}
