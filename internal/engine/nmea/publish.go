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
		set := vesselSetters[d.Path]
		if set == nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("unknown vessel path %q", d.Path)
			}
			continue
		}
		if err := set(&s.state, d.Value); err != nil {
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
func ValidVesselPath(path string) bool { _, ok := vesselSetters[path]; return ok }

// vesselSetters maps a dotted path to a function that unmarshals a value into the
// corresponding VesselState field. This IS the vessel.write schema (spec §6).
var vesselSetters = map[string]func(*VesselState, json.RawMessage) error{
	"navigation.position": func(vs *VesselState, raw json.RawMessage) error {
		var p Position
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		vs.Navigation.Position = &p
		return nil
	},
	"navigation.cogTrue":           floatSetter(func(vs *VesselState) **float64 { return &vs.Navigation.COGTrue }),
	"navigation.sog":               floatSetter(func(vs *VesselState) **float64 { return &vs.Navigation.SOG }),
	"navigation.headingTrue":       floatSetter(func(vs *VesselState) **float64 { return &vs.Navigation.HeadingTrue }),
	"navigation.headingMagnetic":   floatSetter(func(vs *VesselState) **float64 { return &vs.Navigation.HeadingMagnetic }),
	"navigation.magneticVariation": floatSetter(func(vs *VesselState) **float64 { return &vs.Navigation.MagneticVariation }),
	"navigation.rateOfTurn":        floatSetter(func(vs *VesselState) **float64 { return &vs.Navigation.RateOfTurn }),
	"navigation.speedThroughWater": floatSetter(func(vs *VesselState) **float64 { return &vs.Navigation.SpeedThroughWater }),
	"navigation.datetime": func(vs *VesselState, raw json.RawMessage) error {
		var t time.Time
		if err := json.Unmarshal(raw, &t); err != nil {
			return err
		}
		vs.Navigation.Datetime = &t
		return nil
	},
	"environment.depth.belowTransducer": floatSetter(func(vs *VesselState) **float64 { return &vs.Environment.Depth.BelowTransducer }),
	"environment.depth.belowKeel":       floatSetter(func(vs *VesselState) **float64 { return &vs.Environment.Depth.BelowKeel }),
	"environment.depth.belowSurface":    floatSetter(func(vs *VesselState) **float64 { return &vs.Environment.Depth.BelowSurface }),
	"environment.water.temperature":     floatSetter(func(vs *VesselState) **float64 { return &vs.Environment.Water.Temperature }),
	"environment.wind.angleApparent":    floatSetter(func(vs *VesselState) **float64 { return &vs.Environment.Wind.AngleApparent }),
	"environment.wind.speedApparent":    floatSetter(func(vs *VesselState) **float64 { return &vs.Environment.Wind.SpeedApparent }),
	"environment.wind.angleTrue":        floatSetter(func(vs *VesselState) **float64 { return &vs.Environment.Wind.AngleTrue }),
	"environment.wind.speedTrue":        floatSetter(func(vs *VesselState) **float64 { return &vs.Environment.Wind.SpeedTrue }),
	"environment.wind.directionTrue":    floatSetter(func(vs *VesselState) **float64 { return &vs.Environment.Wind.DirectionTrue }),
	"route.xte":                         floatSetter(func(vs *VesselState) **float64 { return &vs.Route.XTE }),
	"route.bearingToWaypoint":           floatSetter(func(vs *VesselState) **float64 { return &vs.Route.BearingToWaypoint }),
	"route.distanceToWaypoint":          floatSetter(func(vs *VesselState) **float64 { return &vs.Route.DistanceToWaypoint }),
	"route.activeWaypoint": func(vs *VesselState, raw json.RawMessage) error {
		var w string
		if err := json.Unmarshal(raw, &w); err != nil {
			return err
		}
		vs.Route.ActiveWaypoint = w
		return nil
	},
}

// floatSetter builds a setter that unmarshals a JSON number into a *float64 field
// addressed by get.
func floatSetter(get func(*VesselState) **float64) func(*VesselState, json.RawMessage) error {
	return func(vs *VesselState, raw json.RawMessage) error {
		var v float64
		if err := json.Unmarshal(raw, &v); err != nil {
			return err
		}
		*get(vs) = &v
		return nil
	}
}
