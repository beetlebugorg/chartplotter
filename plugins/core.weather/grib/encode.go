package grib

import (
	"encoding/binary"
	"math"
)

// Encode writes one GRIB2 message for g using grid-point simple packing (the inverse
// of Decode). Used to build the plugin's embedded sample fixture and for round-trip
// tests; it is not a general-purpose encoder. Values are packed at 0.01 precision in
// 16 bits (ample for wind in m/s).
func Encode(g Grid) []byte {
	const decScale = 2 // 0.01 precision
	const binScale = 0
	const bits = 16

	// Simple-packing quantisation: X = round((Y·10^D − R) / 2^E), R = min·10^D.
	ds := math.Pow(10, float64(decScale))
	ref := math.Inf(1)
	for _, v := range g.Values {
		if v*ds < ref {
			ref = v * ds
		}
	}
	if math.IsInf(ref, 1) {
		ref = 0
	}
	packed := make([]uint16, len(g.Values))
	for i, v := range g.Values {
		packed[i] = uint16(math.Round((v*ds - ref)))
	}

	// Section 1 — Identification (21 octets).
	s1 := make([]byte, 21)
	putSecHeader(s1, 1)
	binary.BigEndian.PutUint16(s1[12:14], uint16(g.RefTime.Year()))
	s1[14] = byte(g.RefTime.Month())
	s1[15] = byte(g.RefTime.Day())
	s1[16] = byte(g.RefTime.Hour())
	s1[17] = byte(g.RefTime.Minute())
	s1[18] = byte(g.RefTime.Second())

	// Section 3 — Grid definition, template 3.0 (72 octets).
	s3 := make([]byte, 72)
	putSecHeader(s3, 3)
	binary.BigEndian.PutUint32(s3[6:10], uint32(g.Nx*g.Ny))
	s3[14] = 6 // shape of earth: spherical, radius 6371229 m (default)
	binary.BigEndian.PutUint32(s3[30:34], uint32(g.Nx))
	binary.BigEndian.PutUint32(s3[34:38], uint32(g.Ny))
	putMicro(s3[46:50], g.La1)
	putMicro(s3[50:54], g.Lo1)
	s3[54] = 0x30 // resolution/component flags: i,j increments given
	putMicro(s3[55:59], g.La2)
	putMicro(s3[59:63], g.Lo2)
	putMicro(s3[63:67], g.Dx)
	putMicro(s3[67:71], g.Dy)
	s3[71] = 0x00 // scan mode: +i (W→E), -j (N→S), row-major from (La1,Lo1)

	// Section 4 — Product definition, template 4.0 (34 octets).
	s4 := make([]byte, 34)
	putSecHeader(s4, 4)
	s4[9] = byte(g.Category)
	s4[10] = byte(g.Number)
	s4[17] = 1 // unit of time range: hour
	binary.BigEndian.PutUint32(s4[18:22], uint32(g.ForecastHour))
	s4[22] = 103                                      // first fixed surface: specified height above ground
	binary.BigEndian.PutUint32(s4[24:28], uint32(10)) // 10 m
	s4[28] = 255                                      // no second fixed surface

	// Section 5 — Data representation, template 5.0 (21 octets).
	s5 := make([]byte, 21)
	putSecHeader(s5, 5)
	binary.BigEndian.PutUint32(s5[5:9], uint32(len(g.Values)))
	binary.BigEndian.PutUint32(s5[11:15], math.Float32bits(float32(ref)))
	putSigned16(s5[15:17], binScale)
	putSigned16(s5[17:19], decScale)
	s5[19] = bits
	s5[20] = 0 // original values: floating point

	// Section 6 — Bitmap (none).
	s6 := make([]byte, 6)
	putSecHeader(s6, 6)
	s6[5] = 255

	// Section 7 — Data.
	dataBytes := make([]byte, len(packed)*2)
	for i, x := range packed {
		binary.BigEndian.PutUint16(dataBytes[i*2:], x)
	}
	s7 := make([]byte, 5+len(dataBytes))
	putSecHeader(s7, 7)
	copy(s7[5:], dataBytes)

	body := concat(s1, s3, s4, s5, s6, s7)
	total := 16 + len(body) + 4 // Section 0 + body + Section 8

	out := make([]byte, 0, total)
	s0 := make([]byte, 16)
	copy(s0, "GRIB")
	s0[6] = 2 // discipline: meteorological
	s0[7] = 2 // edition
	binary.BigEndian.PutUint64(s0[8:16], uint64(total))
	out = append(out, s0...)
	out = append(out, body...)
	out = append(out, "7777"...)
	return out
}

// putSecHeader writes a section's length (filled by the caller's fixed size) and
// number into the first 5 octets.
func putSecHeader(s []byte, num byte) {
	binary.BigEndian.PutUint32(s[0:4], uint32(len(s)))
	s[4] = num
}

func putMicro(b []byte, deg float64) {
	neg := deg < 0
	u := uint32(math.Round(math.Abs(deg) * 1e6))
	if neg {
		u |= 0x80000000
	}
	binary.BigEndian.PutUint32(b, u)
}

func putSigned16(b []byte, v int) {
	var u uint16
	if v < 0 {
		u = uint16(-v) | 0x8000
	} else {
		u = uint16(v)
	}
	binary.BigEndian.PutUint16(b, u)
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}
