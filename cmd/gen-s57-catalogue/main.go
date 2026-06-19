// Command gen-s57-catalogue builds web/s57-catalogue.json — the decode tables the
// ECDIS cursor-pick report needs (S-52 PresLib Part I §10.8):
//
//	rule 1  full S-57 object + attribute names
//	rule 2  enumerated value names
//	rule 4  units of measure
//	rule 5  category-"C" (administrative) attributes hidden unless requested
//
// It is a one-shot developer tool, not part of any build. It reads the IHO S-57
// Appendix A catalogue PDFs (Chapter 1 = object classes, Chapter 2 = attributes)
// via `pdftotext -layout` and the repo's s57attributes.csv, then emits the JSON
// the web app fetches once. The generated JSON is the committed source of truth;
// re-run this only when the catalogue changes.
//
// Usage:
//
//	go run ./cmd/gen-s57-catalogue \
//	  -ch1 ../chartplotter-specs/s57/specs/31ApAch1.pdf \
//	  -ch2 ../chartplotter-specs/s57/specs/31ApAch2.pdf \
//	  -attrs internal/s57/parser/s57attributes.csv \
//	  -out web/s57-catalogue.json
package main

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

// attribute carries the per-attribute decode data the pick report applies.
type attribute struct {
	Name   string            `json:"name"`             // full name, e.g. "Buoy shape"
	Type   string            `json:"type"`             // S-57 type letter: E L F I A S
	Unit   string            `json:"unit,omitempty"`   // unit of measure, e.g. "m" (rule 4)
	Admin  bool              `json:"admin,omitempty"`  // category-C administrative (rule 5)
	Values map[string]string `json:"values,omitempty"` // enumerated id -> name (rule 2)
}

type catalogue struct {
	Classes    map[string]string    `json:"classes"`    // acronym -> full name
	Attributes map[string]attribute `json:"attributes"` // acronym -> decode data
}

// units of measure for the measurement attributes (rule 4). S-57 Appendix A states
// these in prose rather than a uniform field, so they are curated here. Acronyms
// not listed carry no unit (counts, codes, names, dates).
var units = map[string]string{
	// metres
	"HEIGHT": "m", "ELEVAT": "m", "VALSOU": "m", "DRVAL1": "m", "DRVAL2": "m",
	"VALDCO": "m", "VERCLR": "m", "VERCCL": "m", "VERCOP": "m", "VERCSA": "m",
	"HORCLR": "m", "VERLEN": "m", "BURDEP": "m", "SDISMN": "m", "SDISMX": "m",
	"POSACC": "m", "SOUACC": "m", "HORACC": "m", "VERACC": "m",
	"LIFCAP": "t", // tonnes (lifting capacity)
	// degrees
	"ORIENT": "°", "SECTR1": "°", "SECTR2": "°", "VALMAG": "°", "VALACM": "°/year",
	// distances at sea / ranges
	"VALNMR": "M", // nautical miles (nominal range)
	"SDISMR": "M",
	// time / frequency
	"SIGPER": "s",   // seconds (signal period)
	"SIGFRQ": "Hz",  // hertz
	"RADWAL": "m",   // radar wave length (metres)
	"CURVEL": "kn",  // knots (current velocity)
	"ICEFAC": "/10", // ice factor (tenths)
}

func main() {
	ch1 := flag.String("ch1", "", "path to S-57 Appendix A Chapter 1 PDF (object classes)")
	ch2 := flag.String("ch2", "", "path to S-57 Appendix A Chapter 2 PDF (attributes)")
	attrsCSV := flag.String("attrs", "internal/s57/parser/s57attributes.csv", "path to s57attributes.csv")
	out := flag.String("out", "web/s57-catalogue.json", "output JSON path")
	flag.Parse()
	if *ch1 == "" || *ch2 == "" {
		fmt.Fprintln(os.Stderr, "ch1 and ch2 PDF paths are required")
		os.Exit(2)
	}

	cat := catalogue{Classes: map[string]string{}, Attributes: map[string]attribute{}}

	// Attribute names + type letters come from the repo CSV (authoritative, clean).
	attrType, err := loadAttrs(*attrsCSV, &cat)
	must(err)

	// Chapter 1: class full names + the administrative (Set Attribute_C) acronym set.
	admin, err := parseChapter1(*ch1, &cat)
	must(err)
	for acr := range admin {
		a := cat.Attributes[acr]
		a.Admin = true
		cat.Attributes[acr] = a
	}

	// Chapter 2: enumerated value names for type E / L attributes.
	must(parseChapter2(*ch2, &cat, attrType))

	// Units (rule 4).
	for acr, u := range units {
		if a, ok := cat.Attributes[acr]; ok {
			a.Unit = u
			cat.Attributes[acr] = a
		}
	}

	buf, err := json.MarshalIndent(cat, "", "  ")
	must(err)
	must(os.WriteFile(*out, append(buf, '\n'), 0o644))

	enum := 0
	for _, a := range cat.Attributes {
		if len(a.Values) > 0 {
			enum++
		}
	}
	fmt.Printf("wrote %s: %d classes, %d attributes (%d enumerated, %d admin)\n",
		*out, len(cat.Classes), len(cat.Attributes), enum, len(admin))
}

// loadAttrs reads acronym -> {name,type} from s57attributes.csv and returns a
// acronym->type map for chapter-2 parsing.
func loadAttrs(path string, cat *catalogue) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	types := map[string]string{}
	for i, row := range rows {
		if i == 0 || len(row) < 4 { // header / short
			continue
		}
		name, acr, typ := strings.TrimSpace(row[1]), strings.TrimSpace(row[2]), strings.TrimSpace(row[3])
		if acr == "" || acr == "Acronym" {
			continue
		}
		// ENC feature/spatial records reference attributes by code, decoded to the
		// standard 6-char UPPERCASE acronyms. The CSV also carries S-52 presentation
		// attributes ($-prefixed) and non-standard lowercase duplicates (codes 17000+)
		// that never appear in pick data — skip them so the catalogue stays lean.
		if !isStdAcronym(acr) {
			continue
		}
		cat.Attributes[acr] = attribute{Name: name, Type: typ}
		types[acr] = typ
	}
	return types, nil
}

var (
	reAcronym  = regexp.MustCompile(`^Acronym:\s+([A-Za-z0-9_]+)`)
	reSetC     = regexp.MustCompile(`^\s*Set Attribute_C:\s*(.*)$`)
	reSetCont  = regexp.MustCompile(`^\s{6,}([A-Z0-9; ]+)$`) // wrapped Set Attribute line
	reEnumRow  = regexp.MustCompile(`^\s*(\d+)\s*:\s+(\S.*?)\s*$`)
	reColSplit = regexp.MustCompile(`\s{2,}`) // drops trailing INT-1/M-4 reference columns
)

// parseChapter1 fills class full names and returns the set of administrative
// (Set Attribute_C) attribute acronyms gathered across every class.
func parseChapter1(pdf string, cat *catalogue) (map[string]bool, error) {
	lines, err := pdfLines(pdf)
	if err != nil {
		return nil, err
	}
	admin := map[string]bool{}
	for i := range lines {
		m := reAcronym.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		acr := m[1]
		// Full name is on the next non-empty line, preceding the acronym token.
		for j := i + 1; j < len(lines) && j < i+4; j++ {
			t := strings.TrimSpace(lines[j])
			if t == "" {
				continue
			}
			if idx := strings.Index(t, acr); idx > 0 {
				name := strings.TrimSpace(t[:idx])
				if name != "" {
					cat.Classes[acr] = name
				}
			}
			break
		}
		// Gather Set Attribute_C acronyms (+ wrapped continuation lines).
		for j := i + 1; j < len(lines); j++ {
			if reAcronym.MatchString(lines[j]) {
				break
			}
			cm := reSetC.FindStringSubmatch(lines[j])
			if cm == nil {
				continue
			}
			collectAcronyms(cm[1], admin)
			for k := j + 1; k < len(lines); k++ {
				cont := reSetCont.FindStringSubmatch(lines[k])
				if cont == nil {
					break
				}
				collectAcronyms(cont[1], admin)
			}
			break
		}
	}
	return admin, nil
}

// isStdAcronym reports whether acr is a standard S-57 attribute acronym
// (all-uppercase letters/digits), as opposed to an S-52 ($-prefixed) or
// non-standard lowercase catalogue entry.
func isStdAcronym(acr string) bool {
	if acr == "" {
		return false
	}
	for _, r := range acr {
		if !((r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

func collectAcronyms(s string, into map[string]bool) {
	for tok := range strings.SplitSeq(s, ";") {
		tok = strings.TrimSpace(tok)
		if tok != "" {
			into[tok] = true
		}
	}
}

// parseChapter2 fills enumerated value names for E/L attributes.
func parseChapter2(pdf string, cat *catalogue, attrType map[string]string) error {
	lines, err := pdfLines(pdf)
	if err != nil {
		return err
	}
	var cur string
	inExpected := false
	stop := func(t string) bool {
		for _, w := range []string{"Definition", "Definitions", "References", "Remarks", "Indication", "Format", "Minimum", "Maximum"} {
			if strings.HasPrefix(t, w) {
				return true
			}
		}
		return false
	}
	for _, ln := range lines {
		if m := reAcronym.FindStringSubmatch(ln); m != nil {
			cur, inExpected = m[1], false
			continue
		}
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "Expected input") {
			inExpected = true
			continue
		}
		if !inExpected || cur == "" {
			continue
		}
		if stop(t) {
			inExpected = false
			continue
		}
		em := reEnumRow.FindStringSubmatch(ln)
		if em == nil {
			continue
		}
		meaning := reColSplit.Split(strings.TrimSpace(em[2]), 2)[0]
		meaning = strings.TrimSpace(meaning)
		if meaning == "" || strings.EqualFold(meaning, "Meaning") {
			continue
		}
		a := cat.Attributes[cur]
		if a.Values == nil {
			a.Values = map[string]string{}
		}
		a.Values[em[1]] = meaning
		cat.Attributes[cur] = a
	}
	// Sanity: warn for E/L attributes that ended up without values.
	var missing []string
	for acr, typ := range attrType {
		if (typ == "E" || typ == "L") && len(cat.Attributes[acr].Values) == 0 {
			missing = append(missing, acr)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		fmt.Fprintf(os.Stderr, "warning: %d enumerated attrs without values: %s\n", len(missing), strings.Join(missing, " "))
	}
	return nil
}

// pdfLines runs `pdftotext -layout` and returns the document's lines.
func pdfLines(pdf string) ([]string, error) {
	cmd := exec.Command("pdftotext", "-layout", pdf, "-")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("pdftotext %s: %w", pdf, err)
	}
	var lines []string
	sc := bufio.NewScanner(&out)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines, sc.Err()
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
