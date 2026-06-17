// Package catalog distils NOAA's ENCProdCat.xml product catalog (~10 MB, ~7k
// cells) into the compact catalog.json the chart-manager frontend loads.
// A small in-place tag scanner (the catalog is flat and regular, so a full
// XML parser would be overkill).
package catalog

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// tag returns the text between <name>…</name> within s (first occurrence),
// trimmed, or "" with ok=false.
func tag(s, name string) (string, bool) {
	open := "<" + name + ">"
	close := "</" + name + ">"
	i := strings.Index(s, open)
	if i < 0 {
		return "", false
	}
	after := s[i+len(open):]
	j := strings.Index(after, close)
	if j < 0 {
		return "", false
	}
	return strings.Trim(after[0:j], " \t\r\n"), true
}

// num returns a tag's value when it is a non-empty run of digits, else "0".
// The raw text is written straight into JSON as a number.
func num(s, name string) string {
	v, ok := tag(s, name)
	if !ok || v == "" {
		return "0"
	}
	for i := 0; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return "0"
		}
	}
	return v
}

func writeJSONStr(w *bytes.Buffer, s string) {
	w.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '"':
			w.WriteString("\\\"")
		case '\\':
			w.WriteString("\\\\")
		case '\n', '\r', '\t':
			w.WriteByte(' ')
		default:
			w.WriteByte(c)
		}
	}
	w.WriteByte('"')
}

// XMLToJSON parses the product-catalog XML and streams a compact JSON document
// to w: {"date":"…","cells":[{n,l,s,e,u,d,z,zs,cg,rg,bb:[W,S,E,N]}…]}. Returns
// the cell count.
func XMLToJSON(xml string, out io.Writer) (int, error) {
	var w bytes.Buffer
	w.WriteString("{\"date\":")
	date, _ := tag(xml, "date_valid")
	writeJSONStr(&w, date)
	w.WriteString(",\"cells\":[")

	rest := xml
	count := 0
	for {
		i := strings.Index(rest, "<cell>")
		if i < 0 {
			break
		}
		after := rest[i+len("<cell>"):]
		end := strings.Index(after, "</cell>")
		if end < 0 {
			break
		}
		block := after[0:end]
		rest = after[end+len("</cell>"):]

		name, ok := tag(block, "name")
		if !ok {
			continue
		}

		// Coverage bbox: min/max over every <vertex>'s lat/long across panels.
		west, east, south, north := 1e9, -1e9, 1e9, -1e9
		b := block
		for {
			vi := strings.Index(b, "<vertex>")
			if vi < 0 {
				break
			}
			b = b[vi+len("<vertex>"):]
			latS, ok1 := tag(b, "lat")
			lonS, ok2 := tag(b, "long")
			if !ok1 || !ok2 {
				continue
			}
			lat, err1 := strconv.ParseFloat(latS, 64)
			lon, err2 := strconv.ParseFloat(lonS, 64)
			if err1 != nil || err2 != nil {
				continue
			}
			west = min(west, lon)
			east = max(east, lon)
			south = min(south, lat)
			north = max(north, lat)
		}

		if count > 0 {
			w.WriteByte(',')
		}
		count++
		w.WriteString("{\"n\":")
		writeJSONStr(&w, name)
		w.WriteString(",\"l\":")
		lname, _ := tag(block, "lname")
		writeJSONStr(&w, lname)
		fmt.Fprintf(&w, ",\"s\":%s,\"e\":%s,\"u\":%s,\"d\":", num(block, "cscale"), num(block, "edtn"), num(block, "updn"))
		isdt, _ := tag(block, "isdt")
		writeJSONStr(&w, isdt)
		w.WriteString(",\"z\":")
		zloc, _ := tag(block, "zipfile_location")
		writeJSONStr(&w, zloc)
		fmt.Fprintf(&w, ",\"zs\":%s", num(block, "zipfile_size"))
		// cg = Coast Guard district (first listed). 0 when absent.
		fmt.Fprintf(&w, ",\"cg\":%s", num(block, "coast_guard_district"))
		// rg = NOAA ENC region numbers — a cell can list several.
		w.WriteString(",\"rg\":[")
		if regions, ok := tag(block, "regions"); ok {
			rb := regions
			rfirst := true
			for {
				ri := strings.Index(rb, "<region>")
				if ri < 0 {
					break
				}
				rb = rb[ri+len("<region>"):]
				re := strings.Index(rb, "</region>")
				if re < 0 {
					break
				}
				val := strings.Trim(rb[0:re], " \t\r\n")
				rb = rb[re+len("</region>"):]
				if val == "" {
					continue
				}
				digits := true
				for k := 0; k < len(val); k++ {
					if val[k] < '0' || val[k] > '9' {
						digits = false
					}
				}
				if !digits {
					continue
				}
				if !rfirst {
					w.WriteByte(',')
				}
				rfirst = false
				w.WriteString(val)
			}
		}
		w.WriteByte(']')
		if west <= east {
			// %.6f uses IEEE round-half-to-even; a round-half-away formatter
			// would differ by 1 in the 6th bbox decimal (~0.1 m) on ~45/7136
			// cells with exact-half inputs like "-123.5022195". Functionally
			// identical.
			fmt.Fprintf(&w, ",\"bb\":[%.6f,%.6f,%.6f,%.6f]", west, south, east, north)
		} else {
			w.WriteString(",\"bb\":null")
		}
		w.WriteByte('}')
	}
	w.WriteString("]}")

	if _, err := out.Write(w.Bytes()); err != nil {
		return 0, err
	}
	return count, nil
}
