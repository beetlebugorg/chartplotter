package s57

import (
	"bytes"
	"io/fs"
	"path"
	"strconv"
	"strings"

	"github.com/beetlebugorg/chartplotter/pkg/iso8211"
)

// Catalog is the parsed contents of an S-57 exchange-set catalogue file
// (CATALOG.031): the directory of every file in the set, with each cell's
// long name and geographic coverage. It is the cheap way to learn a set's
// inventory and per-cell bounding boxes WITHOUT parsing every .000 cell.
type Catalog struct {
	Entries []CatalogEntry
}

// CatalogEntry is one CATD (catalogue directory) record. S-57 Appendix B.1: the
// CATD field lists RCNM, RCID, FILE, LFIL, VOLM, IMPL, SLAT, WLON, NLAT, ELON,
// CRCS, COMT. The catalogue is always ASCII (it must be readable without the
// DDR), so subfields are unit-terminator (0x1f) delimited, except the fixed-width
// RCNM(2)+RCID and IMPL(3) prefixes which run straight into the following subfield.
type CatalogEntry struct {
	File     string  // path as recorded (e.g. "US5MD1MC\\US5MD1MC.000")
	LongName string  // LFIL — the human chart title (e.g. "Annapolis Harbor")
	Impl     string  // "BIN" (a cell), "ASC", or "TXT" (auxiliary text)
	CRC      string  // CRCS — the file's CRC (hex), if present
	Comment  string  // COMT
	HasBBox  bool    // true when SLAT/WLON/NLAT/ELON were all present
	West     float64 // WLON
	South    float64 // SLAT
	East     float64 // ELON
	North    float64 // NLAT
}

// Base returns the file's basename with the path separators normalised
// (NOAA records "US5MD1MC\\US5MD1MC.000" with a backslash).
func (e CatalogEntry) Base() string {
	f := strings.ReplaceAll(e.File, "\\", "/")
	return path.Base(f)
}

// IsCell reports whether this entry is a base ENC cell (a BIN .000 file) — the
// rows worth baking, as opposed to updates, text descriptions, or the catalogue
// itself.
func (e CatalogEntry) IsCell() bool {
	return e.Impl == "BIN" && strings.HasSuffix(strings.ToUpper(e.Base()), ".000")
}

// CellStem returns the cell name without extension (e.g. "US5MD1MC") for a cell
// entry, or "" if this entry is not a .000 cell.
func (e CatalogEntry) CellStem() string {
	if !e.IsCell() {
		return ""
	}
	b := e.Base()
	return b[:len(b)-len(path.Ext(b))]
}

// Cells returns just the base-cell entries (BIN .000), the inventory to bake.
func (c *Catalog) Cells() []CatalogEntry {
	out := make([]CatalogEntry, 0, len(c.Entries))
	for _, e := range c.Entries {
		if e.IsCell() {
			out = append(out, e)
		}
	}
	return out
}

// Bounds returns the union bounding box [west, south, east, north] of every
// cell entry that carries coverage, and false if none did.
func (c *Catalog) Bounds() (bb [4]float64, ok bool) {
	first := true
	for _, e := range c.Entries {
		if !e.HasBBox {
			continue
		}
		if first {
			bb = [4]float64{e.West, e.South, e.East, e.North}
			first = false
			ok = true
			continue
		}
		bb[0] = min(bb[0], e.West)
		bb[1] = min(bb[1], e.South)
		bb[2] = max(bb[2], e.East)
		bb[3] = max(bb[3], e.North)
	}
	return bb, ok
}

// ParseCatalog parses a CATALOG.031 exchange-set catalogue from raw bytes.
func ParseCatalog(data []byte) (*Catalog, error) {
	return parseCatalogISO(iso8211.MemFS{"/CATALOG.031": data}, "/CATALOG.031")
}

// ParseCatalogFS parses a CATALOG.031 from a filesystem (e.g. an unzipped
// ENC_ROOT or os.DirFS), matching ParseFS for cells.
func ParseCatalogFS(fsys fs.FS, filename string) (*Catalog, error) {
	return parseCatalogISO(fsys, filename)
}

func parseCatalogISO(fsys fs.FS, filename string) (*Catalog, error) {
	p, err := iso8211.OpenFS(fsys, filename)
	if err != nil {
		return nil, err
	}
	defer p.Close()
	f, err := p.Parse()
	if err != nil {
		return nil, err
	}
	cat := &Catalog{}
	for _, rec := range f.Records {
		raw, ok := rec.Fields["CATD"]
		if !ok {
			continue
		}
		if e, ok := decodeCATD(raw); ok {
			cat.Entries = append(cat.Entries, e)
		}
	}
	return cat, nil
}

// decodeCATD splits one CATD field into a CatalogEntry. Layout (NOAA/IHO ASCII
// catalogue, verified against a real NOAA CATALOG.031):
//
//	[0] RCNM(2) + RCID(digits) + FILE   [1] LFIL   [2] VOLM
//	[3] IMPL(3) + SLAT   [4] WLON   [5] NLAT   [6] ELON   [7] CRCS   [8] COMT
//
// The bbox subfields are blank for non-cell files (TXT/ASC), so HasBBox gates
// on all four parsing.
func decodeCATD(raw []byte) (CatalogEntry, bool) {
	// Trim the trailing field terminator (0x1e) the record carries.
	raw = bytes.TrimRight(raw, "\x1e")
	parts := strings.Split(string(raw), "\x1f")
	if len(parts) < 4 {
		return CatalogEntry{}, false
	}
	var e CatalogEntry

	// [0]: RCNM (2 chars) + RCID (run of digits) + FILE (remainder).
	head := parts[0]
	if len(head) < 2 {
		return CatalogEntry{}, false
	}
	rest := head[2:] // drop RCNM ("CD")
	i := 0
	for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
		i++ // consume RCID digits
	}
	e.File = rest[i:]
	if e.File == "" {
		return CatalogEntry{}, false
	}

	e.LongName = parts[1]
	// parts[2] is VOLM — not retained.

	// [3]: IMPL (3 chars: BIN/ASC/TXT) + SLAT (remainder, may be empty).
	if len(parts) > 3 {
		impl := parts[3]
		if len(impl) >= 3 {
			e.Impl = impl[:3]
			slat := impl[3:]
			wlon := field(parts, 4)
			nlat := field(parts, 5)
			elon := field(parts, 6)
			if s, okS := parseFloat(slat); okS {
				if w, okW := parseFloat(wlon); okW {
					if n, okN := parseFloat(nlat); okN {
						if ea, okE := parseFloat(elon); okE {
							e.South, e.West, e.North, e.East = s, w, n, ea
							e.HasBBox = true
						}
					}
				}
			}
		} else {
			e.Impl = impl
		}
	}
	e.CRC = field(parts, 7)
	e.Comment = field(parts, 8)
	return e, true
}

func field(parts []string, i int) string {
	if i < len(parts) {
		return parts[i]
	}
	return ""
}

func parseFloat(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
