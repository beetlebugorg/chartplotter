// Package grib is a minimal GRIB2 codec for gridded wind fields: it decodes (and, for
// building fixtures, encodes) regular lat/lon grids with grid-point simple packing —
// GRIB2 grid-definition template 3.0, product template 4.0, data-representation
// template 5.0, no bitmap. That subset is spec-compliant and covers many
// simple-packed products; GFS's complex packing + spatial differencing (template 5.3)
// is a documented follow-up. Big-endian throughout (GRIB2 is network byte order).
package grib

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"
)

// Grid is one decoded GRIB2 message: a field over a regular lat/lon grid
// (template 3.0) or a Lambert-conformal grid (template 3.30, e.g. HRRR).
type Grid struct {
	Template           int       // grid-definition template: 0 = lat/lon, 30 = Lambert
	Nx, Ny             int       // columns, rows
	La1, Lo1, La2, Lo2 float64   // grid corners, degrees (La1/Lo1 = first point)
	Dx, Dy             float64   // increments: degrees (3.0) or metres (3.30)
	RefTime            time.Time // reference (analysis) time, UTC
	Category, Number   int       // product discipline-2 category/number (2/2=UGRD, 2/3=VGRD)
	ForecastHour       int       // forecast offset, hours
	Values             []float64 // row-major from (La1,Lo1), len Nx*Ny

	// Lambert-conformal parameters (template 3.30 only).
	LoV, Latin1, Latin2 float64 // orientation longitude + standard parallels, degrees
	ScanYUp             bool    // +j scanning: rows run south→north (HRRR does)
	WindsGridRelative   bool    // u/v are along grid axes, not earth east/north
}

// Decode parses every GRIB2 message in b.
func Decode(b []byte) ([]Grid, error) {
	var out []Grid
	for len(b) >= 16 {
		if string(b[0:4]) != "GRIB" {
			// Skip to the next "GRIB" (products can be concatenated with padding).
			i := indexGRIB(b[1:])
			if i < 0 {
				break
			}
			b = b[1+i:]
			continue
		}
		if b[7] != 2 {
			return nil, fmt.Errorf("unsupported GRIB edition %d", b[7])
		}
		total := int(binary.BigEndian.Uint64(b[8:16]))
		if total <= 0 || total > len(b) {
			return nil, fmt.Errorf("bad message length %d (have %d)", total, len(b))
		}
		g, err := decodeMessage(b[:total])
		if err != nil {
			return nil, err
		}
		out = append(out, g)
		b = b[total:]
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no GRIB2 messages found")
	}
	return out, nil
}

func decodeMessage(b []byte) (Grid, error) {
	var g Grid
	var s5, s7 []byte // data-representation + data sections, decoded after the walk
	numPoints := 0
	p := 16 // past Section 0

	for p+5 <= len(b) {
		if string(b[p:p+4]) == "7777" { // Section 8: end
			break
		}
		secLen := int(binary.BigEndian.Uint32(b[p : p+4]))
		secNum := b[p+4]
		if secLen < 5 || p+secLen > len(b) {
			return g, fmt.Errorf("bad section %d length %d", secNum, secLen)
		}
		s := b[p : p+secLen]
		switch secNum {
		case 1: // Identification: reference time
			year := int(binary.BigEndian.Uint16(s[12:14]))
			g.RefTime = time.Date(year, time.Month(s[14]), int(s[15]), int(s[16]), int(s[17]), int(s[18]), 0, time.UTC)
		case 3: // Grid definition (template 3.0 regular lat/lon, or 3.30 Lambert)
			tmpl := binary.BigEndian.Uint16(s[12:14])
			g.Template = int(tmpl)
			switch tmpl {
			case 0:
				g.Nx = int(binary.BigEndian.Uint32(s[30:34]))
				g.Ny = int(binary.BigEndian.Uint32(s[34:38]))
				g.La1 = micro(s[46:50])
				g.Lo1 = micro(s[50:54])
				g.La2 = micro(s[55:59])
				g.Lo2 = micro(s[59:63])
				g.Dx = micro(s[63:67])
				g.Dy = micro(s[67:71])
			case 30: // Lambert conformal (HRRR/NAM); Dx/Dy are metres
				g.Nx = int(binary.BigEndian.Uint32(s[30:34]))
				g.Ny = int(binary.BigEndian.Uint32(s[34:38]))
				g.La1 = micro(s[38:42])
				g.Lo1 = micro(s[42:46])
				g.WindsGridRelative = s[46]&0x08 != 0 // resolution/component flag bit 5
				g.LoV = micro(s[51:55])
				g.Dx = float64(binary.BigEndian.Uint32(s[55:59])) / 1e3
				g.Dy = float64(binary.BigEndian.Uint32(s[59:63])) / 1e3
				g.ScanYUp = s[64]&0x40 != 0
				g.Latin1 = micro(s[65:69])
				g.Latin2 = micro(s[69:73])
			default:
				return g, fmt.Errorf("unsupported grid template %d (want 3.0/3.30)", tmpl)
			}
		case 4: // Product definition (template 4.0)
			tmpl := binary.BigEndian.Uint16(s[7:9])
			if tmpl != 0 {
				return g, fmt.Errorf("unsupported product template %d (want 4.0)", tmpl)
			}
			g.Category = int(s[9])
			g.Number = int(s[10])
			g.ForecastHour = int(binary.BigEndian.Uint32(s[18:22]))
		case 5:
			numPoints = int(binary.BigEndian.Uint32(s[5:9]))
			s5 = s
		case 7:
			s7 = s[5:]
		}
		p += secLen
	}
	if s5 == nil || s7 == nil {
		return g, fmt.Errorf("message missing data-representation or data section")
	}

	tmpl := binary.BigEndian.Uint16(s5[9:11])
	switch tmpl {
	case 0: // simple packing
		refValue := math.Float32frombits(binary.BigEndian.Uint32(s5[11:15]))
		g.Values = unpackSimple(s7, numPoints, float64(refValue), signed16(s5[15:17]), signed16(s5[17:19]), int(s5[19]))
	case 2, 3: // complex packing (2), with spatial differencing (3)
		vals, err := unpackComplex(s5, s7, numPoints)
		if err != nil {
			return g, err
		}
		g.Values = vals
	default:
		return g, fmt.Errorf("unsupported data-rep template %d (want 5.0/5.2/5.3)", tmpl)
	}
	if g.Values == nil {
		return g, fmt.Errorf("message has no data section")
	}
	return g, nil
}

// unpackSimple applies the simple-packing formula:
//
//	Y = (R + X·2^E) / 10^D
//
// where X is the bitsPerValue-bit unsigned integer read big-endian from the stream.
func unpackSimple(data []byte, n int, ref float64, binScale, decScale, bits int) []float64 {
	out := make([]float64, n)
	bs := math.Pow(2, float64(binScale))
	ds := math.Pow(10, float64(decScale))
	if bits == 0 { // constant field: every value == ref/10^D
		for i := range out {
			out[i] = ref / ds
		}
		return out
	}
	var bitPos int
	for i := 0; i < n; i++ {
		x := readBits(data, bitPos, bits)
		out[i] = (ref + float64(x)*bs) / ds
		bitPos += bits
	}
	return out
}

// readBits reads `bits` bits starting at bit offset `pos` (MSB-first).
func readBits(data []byte, pos, bits int) uint64 {
	var v uint64
	for i := 0; i < bits; i++ {
		bit := pos + i
		byteIdx := bit >> 3
		if byteIdx >= len(data) {
			break
		}
		b := (data[byteIdx] >> (7 - uint(bit&7))) & 1
		v = (v << 1) | uint64(b)
	}
	return v
}

// micro reads a GRIB2 lat/lon/increment (unsigned 1e-6 degrees, high bit = sign for
// lat/lon but increments are unsigned; we treat as signed magnitude for corners).
func micro(b []byte) float64 {
	u := binary.BigEndian.Uint32(b)
	if u&0x80000000 != 0 { // sign bit (GRIB2 uses sign-magnitude for lat/lon)
		return -float64(u&0x7fffffff) / 1e6
	}
	return float64(u) / 1e6
}

func signed16(b []byte) int {
	u := binary.BigEndian.Uint16(b)
	if u&0x8000 != 0 {
		return -int(u & 0x7fff)
	}
	return int(u)
}

func indexGRIB(b []byte) int {
	for i := 0; i+4 <= len(b); i++ {
		if string(b[i:i+4]) == "GRIB" {
			return i
		}
	}
	return -1
}
