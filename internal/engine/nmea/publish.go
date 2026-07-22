package nmea

import (
	"encoding/json"
	"fmt"
	"time"
)

// publish.go is the attributed path/value write path into VesselState, used by the
// plugin broker's vessel.write capability (spec §6). Unlike Apply (which takes a raw
// NMEA Sentence), PublishDeltas takes SignalK-style dotted paths and validates them
// against the schema this package defines — the authoritative allowed set is
// vesselSetters below, keyed by the same dotted JSON paths as state.go's structs.
// Every write is attributed to a source id so conflicting providers can be arbitrated
// (per-path latest-wins with provenance; automatic quality arbitration is an open
// question, spec §13).

// Delta is one path/value update. Value is the raw JSON scalar/object for the path.
type Delta struct {
	Path  string
	Value json.RawMessage
}

// PublishDeltas applies a batch of deltas under the write lock, attributing each to
// source. It returns the number applied and the first error encountered (unknown path
// or bad value); valid deltas in a batch still apply even if a sibling is rejected.
func (s *Store) PublishDeltas(source string, deltas []Delta) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.prov == nil {
		s.prov = map[string]string{}
	}
	applied := 0
	var firstErr error
	for _, d := range deltas {
		ops, ok := vesselPaths[d.Path]
		if !ok {
			if firstErr == nil {
				firstErr = fmt.Errorf("unknown vessel path %q", d.Path)
			}
			continue
		}
		if err := ops.set(&s.state, d.Value); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", d.Path, err)
			}
			continue
		}
		s.prov[d.Path] = source
		applied++
	}
	if applied > 0 {
		s.state.Updated = time.Now().UTC()
	}
	return applied, firstErr
}

// ClearSource removes every reading last written by source — a plugin or connection
// the user deliberately disabled/removed. Signal LOSS keeps the last-known state (an
// ECDIS shows a greyed last fix); source REMOVAL must not leave a phantom vessel on
// screen. Returns how many paths were cleared; bumps Updated so streams re-emit.
func (s *Store) ClearSource(source string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	cleared := 0
	for path, src := range s.prov {
		if src != source {
			continue
		}
		if ops, ok := vesselPaths[path]; ok && ops.clear != nil {
			ops.clear(&s.state)
		}
		delete(s.prov, path)
		cleared++
	}
	if cleared > 0 {
		s.state.Updated = time.Now().UTC()
	}
	return cleared
}

// Provenance returns a copy of the path→source map (which plugin/source last wrote
// each path), for the connections/plugins UI to show where a reading came from.
func (s *Store) Provenance() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(s.prov))
	for k, v := range s.prov {
		out[k] = v
	}
	return out
}

// ValidVesselPath reports whether path is a writable vessel path.
func ValidVesselPath(path string) bool { _, ok := vesselPaths[path]; return ok }

// pathOps is one writable vessel path: set unmarshals a delta value into the state;
// clear nils the field back out (for ClearSource).
type pathOps struct {
	set   func(*VesselState, json.RawMessage) error
	clear func(*VesselState)
}

// vesselPaths maps a dotted path to its set/clear pair. This IS the vessel.write
// schema (spec §6).
var vesselPaths = map[string]pathOps{
	"navigation.position": {
		set: func(vs *VesselState, raw json.RawMessage) error {
			var p Position
			if err := json.Unmarshal(raw, &p); err != nil {
				return err
			}
			vs.Navigation.Position = &p
			return nil
		},
		clear: func(vs *VesselState) { vs.Navigation.Position = nil },
	},
	"navigation.cogTrue":           floatPath(func(vs *VesselState) **float64 { return &vs.Navigation.COGTrue }),
	"navigation.sog":               floatPath(func(vs *VesselState) **float64 { return &vs.Navigation.SOG }),
	"navigation.headingTrue":       floatPath(func(vs *VesselState) **float64 { return &vs.Navigation.HeadingTrue }),
	"navigation.headingMagnetic":   floatPath(func(vs *VesselState) **float64 { return &vs.Navigation.HeadingMagnetic }),
	"navigation.magneticVariation": floatPath(func(vs *VesselState) **float64 { return &vs.Navigation.MagneticVariation }),
	"navigation.rateOfTurn":        floatPath(func(vs *VesselState) **float64 { return &vs.Navigation.RateOfTurn }),
	"navigation.speedThroughWater": floatPath(func(vs *VesselState) **float64 { return &vs.Navigation.SpeedThroughWater }),
	"navigation.datetime": {
		set: func(vs *VesselState, raw json.RawMessage) error {
			var t time.Time
			if err := json.Unmarshal(raw, &t); err != nil {
				return err
			}
			vs.Navigation.Datetime = &t
			return nil
		},
		clear: func(vs *VesselState) { vs.Navigation.Datetime = nil },
	},
	"environment.depth.belowTransducer": floatPath(func(vs *VesselState) **float64 { return &vs.Environment.Depth.BelowTransducer }),
	"environment.depth.belowKeel":       floatPath(func(vs *VesselState) **float64 { return &vs.Environment.Depth.BelowKeel }),
	"environment.depth.belowSurface":    floatPath(func(vs *VesselState) **float64 { return &vs.Environment.Depth.BelowSurface }),
	"environment.water.temperature":     floatPath(func(vs *VesselState) **float64 { return &vs.Environment.Water.Temperature }),
	"environment.wind.angleApparent":    floatPath(func(vs *VesselState) **float64 { return &vs.Environment.Wind.AngleApparent }),
	"environment.wind.speedApparent":    floatPath(func(vs *VesselState) **float64 { return &vs.Environment.Wind.SpeedApparent }),
	"environment.wind.angleTrue":        floatPath(func(vs *VesselState) **float64 { return &vs.Environment.Wind.AngleTrue }),
	"environment.wind.speedTrue":        floatPath(func(vs *VesselState) **float64 { return &vs.Environment.Wind.SpeedTrue }),
	"environment.wind.directionTrue":    floatPath(func(vs *VesselState) **float64 { return &vs.Environment.Wind.DirectionTrue }),
	"route.xte":                         floatPath(func(vs *VesselState) **float64 { return &vs.Route.XTE }),
	"route.bearingToWaypoint":           floatPath(func(vs *VesselState) **float64 { return &vs.Route.BearingToWaypoint }),
	"route.distanceToWaypoint":          floatPath(func(vs *VesselState) **float64 { return &vs.Route.DistanceToWaypoint }),
	"route.activeWaypoint": {
		set: func(vs *VesselState, raw json.RawMessage) error {
			var w string
			if err := json.Unmarshal(raw, &w); err != nil {
				return err
			}
			vs.Route.ActiveWaypoint = w
			return nil
		},
		clear: func(vs *VesselState) { vs.Route.ActiveWaypoint = "" },
	},
}

// floatPath builds the set/clear pair for a *float64 field addressed by get.
func floatPath(get func(*VesselState) **float64) pathOps {
	return pathOps{
		set: func(vs *VesselState, raw json.RawMessage) error {
			var v float64
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			*get(vs) = &v
			return nil
		},
		clear: func(vs *VesselState) { *get(vs) = nil },
	}
}
