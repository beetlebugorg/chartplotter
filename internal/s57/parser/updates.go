package parser

import (
	"encoding/binary"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/beetlebugorg/chartplotter/pkg/iso8211"
)

// UpdateInstruction represents the RUIN (Record Update Instruction) field values
// S-57 Part 3 §8.4.2.2 (31Main.pdf p85) and §8.4.3.2 (31Main.pdf p88)
type UpdateInstruction int

const (
	// UpdateInsert indicates a record should be inserted (RUIN = 1)
	UpdateInsert UpdateInstruction = 1

	// UpdateDelete indicates a record should be deleted (RUIN = 2)
	UpdateDelete UpdateInstruction = 2

	// UpdateModify indicates a record should be modified (RUIN = 3)
	UpdateModify UpdateInstruction = 3
)

// findUpdateFiles discovers sequential update files for a base cell
//
// Given "GB5X01SW.000", looks for "GB5X01SW.001", "GB5X01SW.002", etc.
// in the same directory. Returns paths in order.
func findUpdateFiles(fsys fs.FS, baseFilename string) ([]string, error) {
	// Get base filename without extension
	dir := filepath.Dir(baseFilename)
	base := filepath.Base(baseFilename)

	// Remove extension (.000)
	baseName := strings.TrimSuffix(base, filepath.Ext(base))

	var updates []string

	// Look for sequential updates: .001, .002, .003, etc.
	for updateNum := 1; updateNum <= 999; updateNum++ {
		updateFile := filepath.Join(dir, fmt.Sprintf("%s.%03d", baseName, updateNum))

		// Check if file exists using the provided filesystem
		if iso8211.Exists(fsys, updateFile) {
			updates = append(updates, updateFile)
		} else {
			// Stop at first missing update (updates must be sequential)
			break
		}
	}

	return updates, nil
}

// applyUpdates applies update files to parsed chart data
//
// Updates are applied at the record level before geometry construction.
// This modifies featureRecords and spatialRecords in place.
func applyUpdates(fsys fs.FS, baseChart *chartData, updateFiles []string, params datasetParams) error {
	for _, updateFile := range updateFiles {
		if err := applyUpdate(fsys, baseChart, updateFile, params); err != nil {
			return fmt.Errorf("failed to apply update %s: %w", updateFile, err)
		}
	}
	return nil
}

// featureID uniquely identifies a feature using the composite key from FOID
// Per S-57 §7.6.2 (31Main.pdf p75), the unique identifier is (AGEN, FIDN, FIDS), not just FIDN
type featureID struct {
	AGEN uint16 // Producing agency
	FIDN uint32 // Feature identification number
	FIDS uint16 // Feature identification subdivision
}

// chartData holds the intermediate chart state during update merging
type chartData struct {
	features       []*featureRecord
	spatialRecords map[spatialKey]*spatialRecord
	metadata       *datasetMetadata

	// Index for fast lookup during updates
	// CRITICAL: Must use composite key (AGEN, FIDN, FIDS) because FIDN alone is not unique
	featuresByID map[featureID]*featureRecord
}

// applyUpdate applies a single update file to the chart data
func applyUpdate(fsys fs.FS, chart *chartData, updateFile string, params datasetParams) error {
	// Open update file from filesystem using OpenFS
	parser, err := iso8211.OpenFS(fsys, updateFile)
	if err != nil {
		return fmt.Errorf("failed to open update file: %w", err)
	}
	defer parser.Close()

	isoFile, err := parser.Parse()
	if err != nil {
		return fmt.Errorf("failed to parse update file: %w", err)
	}

	// Process each record in update file
	for _, record := range isoFile.Records {
		// Feature record (FRID)
		if fridData, ok := record.Fields["FRID"]; ok && len(fridData) >= 12 {
			if err := applyFeatureUpdate(chart, record, fridData); err != nil {
				return err
			}
			continue
		}

		// Spatial record (VRID)
		if vridData, ok := record.Fields["VRID"]; ok && len(vridData) >= 8 {
			if err := applySpatialUpdate(chart, record, vridData, params); err != nil {
				return err
			}
			continue
		}
	}

	// Check if update contains new DSID metadata and merge it
	if updatedDSID := extractDSID(isoFile); updatedDSID != nil {
		// Merge updated metadata fields
		// Per S-57 spec, update files can modify UPDN (update number) and UADT (update date)
		// EDTN (edition) and DSNM (dataset name) should NOT change in updates
		if updatedDSID.updn != "" {
			chart.metadata.updn = updatedDSID.updn
		}
		if updatedDSID.uadt != "" {
			chart.metadata.uadt = updatedDSID.uadt
		}
		// Update issue date if present
		if updatedDSID.isdt != "" {
			chart.metadata.isdt = updatedDSID.isdt
		}
	}

	return nil
}

// applyFeatureUpdate handles INSERT/DELETE/MODIFY for features
func applyFeatureUpdate(chart *chartData, record *iso8211.DataRecord, fridData []byte) error {
	ruin := UpdateInstruction(fridData[11])

	// Parse feature record
	featureRec := parseFeatureRecord(record)
	if featureRec == nil {
		return fmt.Errorf("failed to parse feature record")
	}

	// Create composite key from FOID fields
	key := featureID{
		AGEN: featureRec.AGEN,
		FIDN: featureRec.FIDN,
		FIDS: featureRec.FIDS,
	}

	switch ruin {
	case UpdateInsert:
		// Add or replace feature
		// Note: Some ENC producers use INSERT even when the record exists in the base
		// This is treated as an upsert operation
		if existing, exists := chart.featuresByID[key]; exists {
			// Replace existing feature
			*existing = *featureRec
		} else {
			// Add new feature
			chart.features = append(chart.features, featureRec)
			chart.featuresByID[key] = featureRec
		}

	case UpdateDelete:
		// Remove existing feature
		existing, exists := chart.featuresByID[key]
		if !exists {
			// Feature doesn't exist - this is a no-op
			// This can happen if the base cell doesn't have the feature being deleted
			return nil
		}

		// Remove from index
		delete(chart.featuresByID, key)

		// Remove from slice
		for i, f := range chart.features {
			if f == existing {
				chart.features = append(chart.features[:i], chart.features[i+1:]...)
				break
			}
		}

	case UpdateModify:
		// Update existing feature
		existing, exists := chart.featuresByID[key]
		if !exists {
			return fmt.Errorf("MODIFY: feature (AGEN=%d, FIDN=%d, FIDS=%d) not found",
				featureRec.AGEN, featureRec.FIDN, featureRec.FIDS)
		}

		// Merge update record into existing feature
		// Per S-57 §8.4.2.2 (31Main.pdf p85): MODIFY only updates fields present in the update record
		// We must selectively update fields rather than wholesale replacement

		// Always update these core identification fields
		existing.RecordVersion = featureRec.RecordVersion
		existing.UpdateInstr = featureRec.UpdateInstr

		// Update attributes if ATTF field present in update record
		// Note: parseFeatureRecord sets Attributes to empty map if no ATTF, so we
		// can't distinguish "no ATTF" from "empty ATTF". For now, always update.
		// TODO: Consider tracking which fields were present in the raw record
		if len(featureRec.Attributes) > 0 {
			existing.Attributes = featureRec.Attributes
		}

		// Update spatial refs ONLY if FSPT field present in update record
		// Per S-57 §8.4.2.2 (b): FSPT modification controlled by FSPC field
		// If FSPT field is present, parseFeatureRecord will set SpatialRefs (even if empty)
		// If FSPT field is absent, parseFeatureRecord leaves SpatialRefs as nil
		// Gate on the CONTROL field (FSPC), not the data field (FSPT): a DELETE
		// instruction carries FSPC with NO FSPT (nothing to insert), so gating on FSPT
		// silently dropped every delete — leaving stale edges that scrambled area
		// boundaries. FSPC = insert/delete/modify N pointers at index FSIX (§8.4.2.4).
		if fspc, ok := record.Fields["FSPC"]; ok && len(fspc) >= 5 {
			existing.SpatialRefs = applyControl(existing.SpatialRefs, featureRec.SpatialRefs, fspc)
		} else if _, hasFSPT := record.Fields["FSPT"]; hasFSPT {
			existing.SpatialRefs = featureRec.SpatialRefs // no control field ⇒ full replacement
		}
		// If neither present, preserve existing SpatialRefs

		// Keep reference in index
		chart.featuresByID[key] = existing

	default:
		return fmt.Errorf("unknown RUIN value for feature: %d", ruin)
	}

	return nil
}

// applySpatialUpdate handles INSERT/DELETE/MODIFY for spatial records
func applySpatialUpdate(chart *chartData, record *iso8211.DataRecord, vridData []byte, params datasetParams) error {
	ruin := UpdateInstruction(vridData[7])

	// Parse spatial record
	spatialRec := parseSpatialRecordWithParams(record, params)
	if spatialRec == nil {
		return fmt.Errorf("failed to parse spatial record")
	}

	// Build key from record type and ID
	key := spatialKey{
		RCNM: int(spatialRec.RecordType),
		RCID: spatialRec.ID,
	}

	switch ruin {
	case UpdateInsert:
		// Add or replace spatial record
		// Note: Some ENC producers use INSERT even when the record exists in the base
		// This is treated as an upsert operation
		chart.spatialRecords[key] = spatialRec

	case UpdateDelete:
		// Remove existing spatial record
		if _, exists := chart.spatialRecords[key]; !exists {
			// Record doesn't exist - this is a no-op
			return nil
		}
		delete(chart.spatialRecords, key)

	case UpdateModify:
		// Update existing spatial record
		existing, exists := chart.spatialRecords[key]
		if !exists {
			return fmt.Errorf("MODIFY: spatial record %v not found", key)
		}

		// Per S-57 §8.4.3.2 (31Main.pdf p88): MODIFY only updates fields present in the update record
		// We must selectively merge fields rather than wholesale replacement
		// This is critical - update records may omit fields that should be preserved!

		// Always update core identification fields
		existing.RecordVersion = spatialRec.RecordVersion
		existing.UpdateInstr = spatialRec.UpdateInstr

		// Update coordinates ONLY if SG2D or SG3D field present in update record.
		// S-57 §8.4.3.2: a coordinate update is NOT a wholesale replacement — the SGCC
		// (Coordinate Control) field says insert/delete/modify CCNC coordinates at
		// index CCIX, with SG2D/SG3D supplying the new ones. Replacing the whole list
		// (the old behavior) collapsed e.g. a 123-point M_COVR coverage ring to the 3
		// points in the update, which then covered nothing — breaking everything that
		// keys off cell coverage (best-available band suppression, no-data, …).
		// Gate on the CONTROL field (SGCC), not the data field (SG2D/SG3D): a coordinate
		// DELETE carries SGCC with NO SG2D, so gating on SG2D dropped deletes. SGCC =
		// insert/delete/modify N coords at index CCIX (§8.4.3.2/3.3). With no SGCC, a
		// SG2D/SG3D update is a full replacement (and for a straight-line edge whose
		// existing coords are empty, replacement == the §8.4.3.3 "append" rule).
		if sgcc, ok := record.Fields["SGCC"]; ok && len(sgcc) >= 5 {
			existing.Coordinates = applyControl(existing.Coordinates, spatialRec.Coordinates, sgcc)
		} else if _, hasSG2D := record.Fields["SG2D"]; hasSG2D {
			existing.Coordinates = spatialRec.Coordinates
		} else if _, hasSG3D := record.Fields["SG3D"]; hasSG3D {
			existing.Coordinates = spatialRec.Coordinates
		}
		// If neither SG2D nor SG3D nor SGCC present, preserve existing coordinates

		// Update VRPT (the edge's begin/end node pointers) via the VRPC control
		// field — S-57 §8.4.3.2: a VRPT edit is an indexed insert/delete/modify
		// (VRPC = VPUI instr + VPIX index + NVPT count), NOT a wholesale replace.
		// Gate on the CONTROL field (VRPC), not the data field (VRPT): e.g. an edge
		// whose end-node pointer is MODIFIED ships VRPC{modify,idx=2,count=1} + one
		// new VRPT — replacing the whole list dropped the begin-node pointer, so the
		// edge lost an endpoint (endNode=0), truncating it and tearing a sliver out
		// of the area boundary. Same class as the SGCC/FSPC fix below. With no VRPC
		// present, a bare VRPT is a full replacement.
		if vrpc, ok := record.Fields["VRPC"]; ok && len(vrpc) >= 5 {
			existing.VectorPointers = applyControl(existing.VectorPointers, spatialRec.VectorPointers, vrpc)
		} else if _, hasVRPT := record.Fields["VRPT"]; hasVRPT {
			existing.VectorPointers = spatialRec.VectorPointers
		}
		// If neither VRPC nor VRPT present, preserve existing vector pointers

		// Keep existing record in map (already there, but make it explicit)
		chart.spatialRecords[key] = existing

	default:
		return fmt.Errorf("unknown RUIN value for spatial: %d", ruin)
	}

	return nil
}

// applyControl applies an S-57 update-control field (SGCC for coordinates §8.4.3.2,
// FSPC for feature-spatial pointers §8.4.2.2 — identical structure) to an existing
// list. The control is a repeating group of 5-byte entries: UI (b11: 1=insert,
// 2=delete, 3=modify), IX (b12, 1-based index), NC (b12, count). Insert/modify
// consume items from `upd` (the update's new SG2D/SG3D or FSPT, in order); delete
// consumes none. Instructions are applied in sequence to the evolving list — so a
// small edit no longer wipes out the whole base list (the bug that collapsed a
// 123-point coverage ring to 3).
func applyControl[T any](existing, upd []T, ctrl []byte) []T {
	out := append([]T(nil), existing...) // don't mutate the base record's slice
	ui := 0                              // cursor into the update's new items
	take := func(n int) []T {
		end := ui + n
		if end > len(upd) {
			end = len(upd)
		}
		o := upd[ui:end]
		ui = end
		return o
	}
	for off := 0; off+5 <= len(ctrl); off += 5 {
		instr := ctrl[off]
		idx := int(binary.LittleEndian.Uint16(ctrl[off+1:off+3])) - 1 // 1-based → 0-based
		nc := int(binary.LittleEndian.Uint16(ctrl[off+3 : off+5]))
		if idx < 0 {
			idx = 0
		}
		switch instr {
		case 1: // insert nc items before idx
			ins := take(nc)
			if idx > len(out) {
				idx = len(out)
			}
			merged := make([]T, 0, len(out)+len(ins))
			merged = append(merged, out[:idx]...)
			merged = append(merged, ins...)
			merged = append(merged, out[idx:]...)
			out = merged
		case 2: // delete nc items starting at idx
			end := idx + nc
			if end > len(out) {
				end = len(out)
			}
			if idx < len(out) && idx < end {
				out = append(out[:idx], out[end:]...)
			}
		case 3: // modify nc items starting at idx (replace with new ones)
			repl := take(nc)
			for k := 0; k < len(repl) && idx+k < len(out); k++ {
				out[idx+k] = repl[k]
			}
		}
	}
	return out
}
