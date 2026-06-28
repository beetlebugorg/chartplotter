package parser

import (
	"fmt"
	"io"
	"io/fs"

	"github.com/beetlebugorg/chartplotter/pkg/iso8211"
)

// CellHeader is the lightweight identity + compilation scale of an S-57 cell,
// read from only its leading DSID/DSPM records — no feature or spatial records,
// no geometry. It is the cheap answer when a caller needs to know WHAT a cell is
// (and at what scale/band) without portraying it.
//
// Note: S-57 stores NO bounding box in the header. A cell's geographic extent
// comes from its M_COVR coverage features (or the exchange-set catalogue's CATD
// bbox), neither of which is read here. Use the catalogue or an M_COVR-only parse
// for bounds.
type CellHeader struct {
	DatasetName      string // DSID DSNM — cell code, e.g. "US5MD1MC"
	Edition          string // DSID EDTN
	UpdateNumber     string // DSID UPDN ("0" for a base cell)
	IssueDate        string // DSID ISDT (YYYYMMDD)
	ProducingAgency  int    // DSID AGEN — IHO agency code (550 = NOAA)
	CompilationScale int32  // DSPM CSCL — scale denominator (0 if no DSPM)
}

// ReadHeaderFS reads only a cell's leading dataset-metadata records (DSID + DSPM)
// from fsys, stopping as soon as both are seen — or when the metadata block ends
// (the first feature/spatial record) — without ever reading the feature or
// spatial records. This is dramatically cheaper than Parse when only identity and
// scale are needed (e.g. bucketing cells by band, or filling in metadata whose
// bounds come from elsewhere). Updates are NOT applied: the result reflects the
// base cell as given.
func ReadHeaderFS(fsys fs.FS, filename string) (*CellHeader, error) {
	p, err := iso8211.OpenFS(fsys, filename)
	if err != nil {
		return nil, err
	}
	defer p.Close()
	return readHeader(p)
}

func readHeader(p *iso8211.Parser) (*CellHeader, error) {
	h := &CellHeader{}
	var gotDSID, gotDSPM bool
	// DSID and DSPM live in the dataset general-information / geographic-reference
	// records at the very front of the file, before any feature (FRID) or spatial
	// (VRID) record. Read records one at a time until both are in hand, or until the
	// metadata block is over — so a cell that omits DSPM doesn't drag us through the
	// whole file.
	for !(gotDSID && gotDSPM) {
		rec, err := p.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if d, ok := rec.Fields["DSID"]; ok && !gotDSID {
			m := parseDSID(d)
			h.DatasetName = m.dsnm
			h.Edition = m.edtn
			h.UpdateNumber = m.updn
			h.IssueDate = m.isdt
			h.ProducingAgency = m.agen
			gotDSID = true
		}
		if d, ok := rec.Fields["DSPM"]; ok && !gotDSPM {
			h.CompilationScale = parseDSPM(d).CSCL
			gotDSPM = true
		}
		// First feature/spatial record ⇒ the metadata block has ended; nothing more
		// to find. Stop rather than scan the rest of the cell.
		if _, ok := rec.Fields["FRID"]; ok {
			break
		}
		if _, ok := rec.Fields["VRID"]; ok {
			break
		}
	}
	if !gotDSID {
		return nil, fmt.Errorf("no DSID record in cell header")
	}
	return h, nil
}
