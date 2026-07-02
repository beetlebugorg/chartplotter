package parser

// Chart represents a complete S-57 Electronic Navigational Chart.
// This is the top-level structure returned by the parser.
//
// Reference: S-57 Part 3 §7 (31Main.pdf p94): Structure implementation
// showing how datasets are composed of metadata and feature records.
type Chart struct {
	metadata       *datasetMetadata              // Private - use accessor methods
	params         datasetParams                 // Private - DSPM record data
	Features       []Feature                     // Public - array of extracted features
	spatialRecords map[spatialKey]*spatialRecord // Private - for update merging
}

// DatasetName returns the chart's dataset name (cell identifier).
func (c *Chart) DatasetName() string {
	if c.metadata == nil {
		return ""
	}
	return c.metadata.DatasetName()
}

// Edition returns the chart's edition number.
func (c *Chart) Edition() string {
	if c.metadata == nil {
		return ""
	}
	return c.metadata.Edition()
}

// UpdateNumber returns the chart's update number.
func (c *Chart) UpdateNumber() string {
	if c.metadata == nil {
		return ""
	}
	return c.metadata.UpdateNumber()
}

// UpdateDate returns the update application date (YYYYMMDD).
func (c *Chart) UpdateDate() string {
	if c.metadata == nil {
		return ""
	}
	return c.metadata.UpdateDate()
}

// IssueDate returns the issue date (YYYYMMDD).
func (c *Chart) IssueDate() string {
	if c.metadata == nil {
		return ""
	}
	return c.metadata.IssueDate()
}

// S57Edition returns the S-57 standard edition used (e.g., "03.1").
func (c *Chart) S57Edition() string {
	if c.metadata == nil {
		return ""
	}
	return c.metadata.S57Edition()
}

// ProducingAgency returns the producing agency code.
func (c *Chart) ProducingAgency() int {
	if c.metadata == nil {
		return 0
	}
	return c.metadata.ProducingAgency()
}

// Comment returns the metadata comment field.
func (c *Chart) Comment() string {
	if c.metadata == nil {
		return ""
	}
	return c.metadata.Comment()
}

// ExchangePurpose returns human-readable exchange purpose ("New" or "Revision").
func (c *Chart) ExchangePurpose() string {
	if c.metadata == nil {
		return "Unknown"
	}
	return c.metadata.ExchangePurpose()
}

// ProductSpecification returns human-readable product spec ("ENC" or "ODD").
func (c *Chart) ProductSpecification() string {
	if c.metadata == nil {
		return "Unknown"
	}
	return c.metadata.ProductSpecification()
}

// ApplicationProfile returns human-readable application profile.
func (c *Chart) ApplicationProfile() string {
	if c.metadata == nil {
		return "Unknown"
	}
	return c.metadata.ApplicationProfile()
}

// IntendedUsage returns the intended usage (navigational purpose) code.
//
// Values per S-57 specification:
//
//	1 = Overview, 2 = General, 3 = Coastal, 4 = Approach, 5 = Harbour, 6 = Berthing
func (c *Chart) IntendedUsage() int {
	if c.metadata == nil {
		return 0
	}
	return c.metadata.intu
}

// CoordinateUnits returns the coordinate units from the DSPM record.
// S-57 §7.3.2.1 (31Main.pdf p66) COUN field: 1=lat/lon, 2=eastings/northings.
func (c *Chart) CoordinateUnits() int {
	return c.params.COUN
}

// HorizontalDatum returns the horizontal datum code from the DSPM record.
// S-57 §7.3.2.1 (31Main.pdf p66) HDAT field: 2=WGS-84 (most common).
func (c *Chart) HorizontalDatum() int {
	return c.params.HDAT
}

// CompilationScale returns the compilation scale from the DSPM record.
// S-57 §7.3.2.1 (31Main.pdf p66) CSCL field: scale denominator (e.g., 50000 for 1:50,000).
func (c *Chart) CompilationScale() int32 {
	return c.params.CSCL
}

// DepthUnits returns the depth units from the DSPM record.
// S-57 §7.3.2.1 (31Main.pdf p66) DUNI field: 1=meters, 2=fathoms+feet, 3=feet, 4=fathoms+fractions.
func (c *Chart) DepthUnits() int {
	return c.params.DUNI
}
