package s57

import (
	"io/fs"

	"github.com/beetlebugorg/chartplotter/internal/s57/parser"
	"github.com/beetlebugorg/chartplotter/pkg/iso8211"
)

// CellHeader is a cell's identity and compilation scale, read from only its
// leading DSID/DSPM records — no features, no geometry. It answers "what is this
// cell, and at what scale/band" far more cheaply than a full Parse.
//
// S-57 stores NO bounding box in the header. A cell's geographic extent comes from
// its M_COVR coverage features or the exchange-set catalogue (see Catalog); read
// one of those when bounds are needed.
type CellHeader struct {
	DatasetName      string // cell code, e.g. "US5MD1MC"
	Edition          string // edition number
	UpdateNumber     string // "0" for a base cell
	IssueDate        string // YYYYMMDD
	ProducingAgency  int    // IHO agency code (550 = NOAA)
	CompilationScale int32  // scale denominator (0 if the cell has no DSPM)
}

func convertHeader(h *parser.CellHeader) *CellHeader {
	return &CellHeader{
		DatasetName:      h.DatasetName,
		Edition:          h.Edition,
		UpdateNumber:     h.UpdateNumber,
		IssueDate:        h.IssueDate,
		ProducingAgency:  h.ProducingAgency,
		CompilationScale: h.CompilationScale,
	}
}

// ReadHeaderFS reads a cell's header (DSID/DSPM) from a custom io/fs.FS without
// parsing its features or geometry. Pair with iso8211.MemFS to read from raw
// in-memory bytes:
//
//	h, err := s57.ReadHeaderFS(iso8211.MemFS{"/c.000": data}, "/c.000")
func ReadHeaderFS(fsys fs.FS, filename string) (*CellHeader, error) {
	h, err := parser.ReadHeaderFS(fsys, filename)
	if err != nil {
		return nil, err
	}
	return convertHeader(h), nil
}

// ReadHeader reads a cell's header from the OS filesystem — the convenience
// equivalent of ReadHeaderFS over the local files.
func ReadHeader(filename string) (*CellHeader, error) {
	return ReadHeaderFS(iso8211.OSFS(), filename)
}
