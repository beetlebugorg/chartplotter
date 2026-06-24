// Minimal protobuf wire-format writer for the MVT encoder: varints,
// length-delimited fields, packed uint32, and fixed32.
package mvt

// wireType is the protobuf wire type (low 3 bits of a tag).
type wireType uint32

const (
	wireVarint  wireType = 0
	wireFixed64 wireType = 1
	wireLen     wireType = 2
	wireFixed32 wireType = 5
)

// zigzag32 maps a signed int32 to the unsigned zig-zag encoding MVT uses for
// geometry deltas.
func zigzag32(n int32) uint32 {
	return uint32((n << 1) ^ (n >> 31))
}

func varintLen(v uint64) int {
	n := 1
	for v >= 0x80 {
		n++
		v >>= 7
	}
	return n
}

func tagLen(field uint32, w wireType) int {
	return varintLen(uint64(field<<3) | uint64(w))
}

// writer is an append-only protobuf byte buffer.
type writer struct {
	buf []byte
}

func (w *writer) bytes() []byte { return w.buf }

func (w *writer) writeVarint(v uint64) {
	for v >= 0x80 {
		w.buf = append(w.buf, byte(v)|0x80)
		v >>= 7
	}
	w.buf = append(w.buf, byte(v))
}

func (w *writer) writeTag(field uint32, wt wireType) {
	w.writeVarint(uint64(field<<3) | uint64(wt))
}

func (w *writer) writeVarintField(field uint32, v uint64) {
	w.writeTag(field, wireVarint)
	w.writeVarint(v)
}

func (w *writer) writeStringField(field uint32, s string) {
	w.writeTag(field, wireLen)
	w.writeVarint(uint64(len(s)))
	w.buf = append(w.buf, s...)
}

func (w *writer) writeFixed32Field(field uint32, v uint32) {
	w.writeTag(field, wireFixed32)
	w.buf = append(w.buf, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

// writePackedU32 writes a packed repeated uint32 (varint-encoded) field.
func (w *writer) writePackedU32(field uint32, vals []uint32) {
	var payload int
	for _, v := range vals {
		payload += varintLen(uint64(v))
	}
	w.writeTag(field, wireLen)
	w.writeVarint(uint64(payload))
	for _, v := range vals {
		w.writeVarint(uint64(v))
	}
}

// -- length helpers (must match the write* methods above) --------------------

func varintFieldLen(field uint32, v uint64) int {
	return tagLen(field, wireVarint) + varintLen(v)
}

func bytesFieldLen(field uint32, n int) int {
	return tagLen(field, wireLen) + varintLen(uint64(n)) + n
}

func fixed32FieldLen(field uint32) int {
	return tagLen(field, wireFixed32) + 4
}

func packedU32FieldLen(field uint32, vals []uint32) int {
	var payload int
	for _, v := range vals {
		payload += varintLen(uint64(v))
	}
	return tagLen(field, wireLen) + varintLen(uint64(payload)) + payload
}

func msgFieldLen(field uint32, bodyLen int) int {
	return tagLen(field, wireLen) + varintLen(uint64(bodyLen)) + bodyLen
}
