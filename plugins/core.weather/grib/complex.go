package grib

import (
	"encoding/binary"
	"fmt"
	"math"
)

// unpackComplex decodes GRIB2 complex packing (data-representation template 5.2) and
// complex packing with spatial differencing (5.3) — the packing GFS uses. It follows
// the NCEP g2clib comunpack algorithm: read the spatial-difference extras, then the
// per-group reference values / bit-widths / lengths, then the packed group data, then
// undo the differencing and apply the reference/scale.
func unpackComplex(s5, s7 []byte, numPoints int) ([]float64, error) {
	if len(s5) < 47 {
		return nil, fmt.Errorf("complex: section 5 too short (%d)", len(s5))
	}
	tmpl := binary.BigEndian.Uint16(s5[9:11])
	ref := float64(math.Float32frombits(binary.BigEndian.Uint32(s5[11:15])))
	binScale := signed16(s5[15:17])
	decScale := signed16(s5[17:19])
	refBits := int(s5[19])
	ng := int(binary.BigEndian.Uint32(s5[31:35]))
	groupWidthRef := int(s5[35])
	groupWidthBits := int(s5[36])
	groupLenRef := int(binary.BigEndian.Uint32(s5[37:41]))
	groupLenInc := int(s5[41])
	lastGroupLen := int(binary.BigEndian.Uint32(s5[42:46]))
	groupLenBits := int(s5[46])

	spatialOrder, extraOctets := 0, 0
	if tmpl == 3 {
		if len(s5) < 49 {
			return nil, fmt.Errorf("complex: 5.3 section 5 too short (%d)", len(s5))
		}
		spatialOrder = int(s5[47])
		extraOctets = int(s5[48])
	}
	if ng <= 0 || ng > numPoints+1 {
		return nil, fmt.Errorf("complex: implausible group count %d", ng)
	}

	pos := 0
	// The NCEP encoder byte-aligns each subsection of the data section; without this
	// the group data desyncs and the second-order integration runs away to garbage.
	align := func() {
		if r := pos % 8; r != 0 {
			pos += 8 - r
		}
	}

	// 1. Spatial-difference extras: the first value(s) (unsigned) and the overall
	//    minimum of the differences (sign-magnitude), each `extraOctets` octets.
	var ival1, ival2, minsd int64
	if spatialOrder > 0 {
		nb := extraOctets * 8
		ival1 = int64(readBits(s7, pos, nb))
		pos += nb
		if spatialOrder == 2 {
			ival2 = int64(readBits(s7, pos, nb))
			pos += nb
		}
		minsd = readSignedBits(s7, pos, nb)
		pos += nb
	}
	align()

	// 2. Group reference values.
	refs := make([]int64, ng)
	for i := 0; i < ng; i++ {
		refs[i] = int64(readBits(s7, pos, refBits))
		pos += refBits
	}
	align()
	// 3. Group widths.
	widths := make([]int, ng)
	for i := 0; i < ng; i++ {
		widths[i] = groupWidthRef + int(readBits(s7, pos, groupWidthBits))
		pos += groupWidthBits
	}
	align()
	// 4. Group lengths (the last group's length is given explicitly).
	lengths := make([]int, ng)
	for i := 0; i < ng; i++ {
		lengths[i] = groupLenRef + groupLenInc*int(readBits(s7, pos, groupLenBits))
		pos += groupLenBits
	}
	lengths[ng-1] = lastGroupLen
	align()

	// 5. Packed group data → integer field.
	ifld := make([]int64, numPoints)
	n := 0
	for gi := 0; gi < ng; gi++ {
		w, l := widths[gi], lengths[gi]
		if n+l > numPoints {
			l = numPoints - n
		}
		if w == 0 {
			for j := 0; j < l; j++ {
				ifld[n] = refs[gi]
				n++
			}
		} else {
			for j := 0; j < l; j++ {
				ifld[n] = refs[gi] + int64(readBits(s7, pos, w))
				pos += w
				n++
			}
		}
	}
	if n != numPoints {
		return nil, fmt.Errorf("complex: unpacked %d of %d points", n, numPoints)
	}

	// 6. Undo spatial differencing.
	switch spatialOrder {
	case 1:
		ifld[0] = ival1
		for i := 1; i < numPoints; i++ {
			ifld[i] += minsd
			ifld[i] += ifld[i-1]
		}
	case 2:
		if numPoints > 0 {
			ifld[0] = ival1
		}
		if numPoints > 1 {
			ifld[1] = ival2
		}
		for i := 2; i < numPoints; i++ {
			ifld[i] += minsd
			ifld[i] += 2*ifld[i-1] - ifld[i-2]
		}
	}

	// 7. Apply reference + scale: Y = (R + X·2^E) / 10^D.
	bs := math.Pow(2, float64(binScale))
	ds := math.Pow(10, float64(decScale))
	out := make([]float64, numPoints)
	for i := 0; i < numPoints; i++ {
		out[i] = (ref + float64(ifld[i])*bs) / ds
	}
	return out, nil
}

// readSignedBits reads a GRIB2 sign-magnitude integer of nb bits (the high bit is the
// sign) starting at bit offset pos.
func readSignedBits(data []byte, pos, nb int) int64 {
	if nb == 0 {
		return 0
	}
	raw := readBits(data, pos, nb)
	sign := raw >> uint(nb-1)
	mag := int64(raw & ((1 << uint(nb-1)) - 1))
	if sign != 0 {
		return -mag
	}
	return mag
}
