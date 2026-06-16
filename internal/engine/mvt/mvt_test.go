package mvt

import "testing"

func TestPolygonCommandStream(t *testing.T) {
	// A 5x5 square, clockwise in tile space.
	ring := []IPoint{{X: 0, Y: 0}, {X: 5, Y: 0}, {X: 5, Y: 5}, {X: 0, Y: 5}}
	geom := encodePolygon([][]IPoint{ring})
	if geom[0] != commandInteger(1, 1) {
		t.Errorf("geom[0] = %d, want MoveTo(1)", geom[0])
	}
	if geom[3] != commandInteger(2, 3) {
		t.Errorf("geom[3] = %d, want LineTo(3)", geom[3])
	}
	if geom[len(geom)-1] != commandInteger(7, 1) {
		t.Errorf("last = %d, want ClosePath(1)", geom[len(geom)-1])
	}
}

func TestTileRoundTrip(t *testing.T) {
	tb := NewTileBuilder(ExtentDefault)
	areas := tb.Layer("areas")
	ring := []IPoint{{X: 0, Y: 0}, {X: 100, Y: 0}, {X: 100, Y: 100}, {X: 0, Y: 100}}
	areas.AddPolygon([][]IPoint{ring}, []KeyValue{
		{"class", StringVal("DEPARE")},
		{"color_token", StringVal("DEPDW")},
		{"draw_prio", IntVal(3)},
	})
	lines := tb.Layer("lines")
	lines.AddLines([][]IPoint{{{X: 10, Y: 10}, {X: 200, Y: 50}}}, []KeyValue{
		{"class", StringVal("COALNE")},
		{"color_token", StringVal("CSTLN")},
	})

	dec := decodeTile(t, tb.Encode())
	if len(dec) != 2 {
		t.Fatalf("expected 2 layers, got %d", len(dec))
	}
	a := dec["areas"]
	if a == nil {
		t.Fatal("missing areas layer")
	}
	if a.extent != 4096 {
		t.Errorf("areas extent = %d", a.extent)
	}
	if len(a.features) != 1 {
		t.Fatalf("areas features = %d", len(a.features))
	}
	if got := a.stringAttr("color_token"); got != "DEPDW" {
		t.Errorf("areas color_token = %q, want DEPDW", got)
	}
	if got := a.stringAttr("class"); got != "DEPARE" {
		t.Errorf("areas class = %q", got)
	}
	if !a.attrIsString("color_token") {
		t.Error("color_token must be a string, never RGB int")
	}
	if got := dec["lines"].stringAttr("color_token"); got != "CSTLN" {
		t.Errorf("lines color_token = %q, want CSTLN", got)
	}
}

// -- minimal MVT decoder for the test ----------------------------------------

type decValue struct {
	isString bool
	str      string
}

type decFeature struct {
	tags []uint32
}

type decLayer struct {
	name     string
	extent   uint32
	keys     []string
	values   []decValue
	features []decFeature
}

func (l *decLayer) attrIndex(key string) (uint32, bool) {
	if len(l.features) == 0 {
		return 0, false
	}
	tags := l.features[0].tags
	for i := 0; i+1 < len(tags); i += 2 {
		if l.keys[tags[i]] == key {
			return tags[i+1], true
		}
	}
	return 0, false
}

func (l *decLayer) stringAttr(key string) string {
	vi, ok := l.attrIndex(key)
	if !ok || !l.values[vi].isString {
		return ""
	}
	return l.values[vi].str
}

func (l *decLayer) attrIsString(key string) bool {
	vi, ok := l.attrIndex(key)
	return ok && l.values[vi].isString
}

type reader struct {
	data []byte
	pos  int
}

func (r *reader) atEnd() bool { return r.pos >= len(r.data) }

func (r *reader) varint() uint64 {
	var v uint64
	var shift uint
	for r.pos < len(r.data) {
		b := r.data[r.pos]
		r.pos++
		v |= uint64(b&0x7f) << shift
		if b < 0x80 {
			break
		}
		shift += 7
	}
	return v
}

// field returns (fieldNum, wireType, bytesForLen, varintForVarint, ok).
func (r *reader) field() (uint32, wireType, []byte, uint64, bool) {
	if r.atEnd() {
		return 0, 0, nil, 0, false
	}
	tag := r.varint()
	field := uint32(tag >> 3)
	wt := wireType(tag & 0x7)
	switch wt {
	case wireVarint:
		return field, wt, nil, r.varint(), true
	case wireLen:
		n := int(r.varint())
		b := r.data[r.pos : r.pos+n]
		r.pos += n
		return field, wt, b, 0, true
	case wireFixed32:
		b := r.data[r.pos : r.pos+4]
		r.pos += 4
		return field, wt, b, 0, true
	default:
		return field, wt, nil, 0, false
	}
}

func decodeTile(t *testing.T, data []byte) map[string]*decLayer {
	t.Helper()
	out := map[string]*decLayer{}
	r := &reader{data: data}
	for {
		field, _, b, _, ok := r.field()
		if !ok {
			break
		}
		if field != 3 {
			continue
		}
		l := decodeLayer(b)
		out[l.name] = l
	}
	return out
}

func decodeLayer(data []byte) *decLayer {
	l := &decLayer{extent: 4096}
	r := &reader{data: data}
	for {
		field, _, b, v, ok := r.field()
		if !ok {
			break
		}
		switch field {
		case 1:
			l.name = string(b)
		case 2:
			l.features = append(l.features, decodeFeature(b))
		case 3:
			l.keys = append(l.keys, string(b))
		case 4:
			l.values = append(l.values, decodeValue(b))
		case 5:
			l.extent = uint32(v)
		}
	}
	return l
}

func decodeFeature(data []byte) decFeature {
	var f decFeature
	r := &reader{data: data}
	for {
		field, _, b, _, ok := r.field()
		if !ok {
			break
		}
		if field == 2 { // packed tags
			ir := &reader{data: b}
			for !ir.atEnd() {
				f.tags = append(f.tags, uint32(ir.varint()))
			}
		}
	}
	return f
}

func decodeValue(data []byte) decValue {
	r := &reader{data: data}
	for {
		field, _, b, _, ok := r.field()
		if !ok {
			break
		}
		if field == 1 {
			return decValue{isString: true, str: string(b)}
		}
	}
	return decValue{}
}
