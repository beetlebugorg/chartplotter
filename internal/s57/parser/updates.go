package parser

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/beetlebugorg/chartplotter-go/pkg/iso8211"
	"github.com/spf13/afero"
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
func findUpdateFiles(fs afero.Fs, baseFilename string) ([]string, error) {
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
		exists, err := afero.Exists(fs, updateFile)
		if err != nil {
			return nil, fmt.Errorf("error checking for update file %s: %w", updateFile, err)
		}
		if exists {
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
func applyUpdates(fs afero.Fs, baseChart *chartData, updateFiles []string, params datasetParams) error {
	for _, updateFile := range updateFiles {
		if err := applyUpdate(fs, baseChart, updateFile, params); err != nil {
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
func applyUpdate(fs afero.Fs, chart *chartData, updateFile string, params datasetParams) error {
	// Open update file from filesystem using OpenFS
	parser, err := iso8211.OpenFS(fs, updateFile)
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
		_, hasFSPT := record.Fields["FSPT"]
		if hasFSPT {
			existing.SpatialRefs = featureRec.SpatialRefs
		}
		// If FSPT not present in update, preserve existing SpatialRefs

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

		// Update coordinates ONLY if SG2D or SG3D field present in update record
		_, hasSG2D := record.Fields["SG2D"]
		_, hasSG3D := record.Fields["SG3D"]
		if hasSG2D || hasSG3D {
			// Update present - replace coordinates
			existing.Coordinates = spatialRec.Coordinates
		}
		// If neither SG2D nor SG3D present, preserve existing coordinates

		// Update VRPT ONLY if VRPT field present in update record
		_, hasVRPT := record.Fields["VRPT"]
		if hasVRPT {
			// VRPT update present - replace vector pointers
			existing.VectorPointers = spatialRec.VectorPointers
		}
		// If VRPT not present, preserve existing vector pointers

		// Keep existing record in map (already there, but make it explicit)
		chart.spatialRecords[key] = existing

	default:
		return fmt.Errorf("unknown RUIN value for spatial: %d", ruin)
	}

	return nil
}
