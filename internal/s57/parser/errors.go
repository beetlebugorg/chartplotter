package parser

import (
	"fmt"
)

// ErrInvalidCoordinate indicates coordinate out of valid bounds
type ErrInvalidCoordinate struct {
	Lat, Lon float64
}

func (e *ErrInvalidCoordinate) Error() string {
	return fmt.Sprintf("invalid coordinate: lat=%f lon=%f (lat must be ±90, lon must be ±180)",
		e.Lat, e.Lon)
}

// ErrUnknownObjectClass indicates unsupported S-57 object class
type ErrUnknownObjectClass struct {
	Code int
}

func (e *ErrUnknownObjectClass) Error() string {
	return fmt.Sprintf("unknown S-57 object class: %d", e.Code)
}

// ErrInvalidGeometry indicates geometry violates S-57 rules
type ErrInvalidGeometry struct {
	Type   GeometryType
	Reason string
}

func (e *ErrInvalidGeometry) Error() string {
	if e.Type != 0 {
		return fmt.Sprintf("invalid geometry (%v): %s", e.Type, e.Reason)
	}
	return fmt.Sprintf("invalid geometry: %s", e.Reason)
}

// ErrMissingSpatialRecord indicates FSPT pointer references non-existent spatial record
type ErrMissingSpatialRecord struct {
	FeatureID int64
	SpatialID int64
}

func (e *ErrMissingSpatialRecord) Error() string {
	return fmt.Sprintf("feature %d references missing spatial record %d",
		e.FeatureID, e.SpatialID)
}

// ErrInvalidSpatialRecord indicates spatial record is not of expected type
type ErrInvalidSpatialRecord struct {
	SpatialID int64
	Reason    string
}

func (e *ErrInvalidSpatialRecord) Error() string {
	return fmt.Sprintf("invalid spatial record %d: %s", e.SpatialID, e.Reason)
}
