// Command s101-color-diff is Phase 2 (Workstream A) of the S-101 backport
// (specs/s101-portrayal-backport.md). It parses the S-101 colorProfile.xml into
// token -> {day,dusk,night} sRGB and diffs it against the colours our embedded
// S-52 DAI produces today. A clean (or near-clean) diff proves the colour seam:
// the S-101 colour profile can drive colortables.json unchanged.
//
// Usage:
//
//	go run ./cmd/s101-color-diff --profile /path/to/PortrayalCatalog/ColorProfiles/colorProfile.xml
package main

import (
	"encoding/xml"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/beetlebugorg/chartplotter/pkg/s52"
	"github.com/beetlebugorg/chartplotter/pkg/s52/preslib"
)

type colorProfile struct {
	Palettes []struct {
		Name  string `xml:"name,attr"`
		Items []struct {
			Token string `xml:"token,attr"`
			SRGB  struct {
				R int `xml:"red"`
				G int `xml:"green"`
				B int `xml:"blue"`
			} `xml:"srgb"`
		} `xml:"item"`
	} `xml:"palette"`
}

var schemes = []struct {
	dai     s52.ColorScheme
	profile string // palette name in colorProfile.xml
}{
	{s52.ColorSchemeDay, "Day"},
	{s52.ColorSchemeDusk, "Dusk"},
	{s52.ColorSchemeNight, "Night"},
}

func main() {
	profile := flag.String("profile", "/home/jcollins/Projects/s101-portrayal-catalogue/PortrayalCatalog/ColorProfiles/colorProfile.xml", "path to S-101 colorProfile.xml")
	tol := flag.Int("tol", 0, "per-channel tolerance (0-255) treated as a match")
	flag.Parse()

	data, err := os.ReadFile(*profile)
	if err != nil {
		fatal("read %s: %v", *profile, err)
	}
	var cp colorProfile
	if err := xml.Unmarshal(data, &cp); err != nil {
		fatal("parse %s: %v", *profile, err)
	}
	// palette name -> token -> hex
	s101 := map[string]map[string]string{}
	for _, p := range cp.Palettes {
		m := map[string]string{}
		for _, it := range p.Items {
			m[it.Token] = fmt.Sprintf("#%02x%02x%02x", clamp(it.SRGB.R), clamp(it.SRGB.G), clamp(it.SRGB.B))
		}
		s101[p.Name] = m
	}

	lib, err := s52.LoadLibraryFromBytes(preslib.DAI)
	if err != nil {
		fatal("load DAI: %v", err)
	}

	type mismatch struct{ scheme, token, dai, s101 string }
	var mismatches []mismatch
	var missingInS101, missingInDAI []string
	total := 0

	for _, s := range schemes {
		cols, err := lib.GetColorsByScheme(s.dai)
		if err != nil {
			fatal("colors %s: %v", s.dai, err)
		}
		theirs := s101[s.profile]
		seen := map[string]bool{}
		for tok, c := range cols {
			total++
			seen[tok] = true
			dh := strings.ToLower(c.ConvertToHex())
			sh, ok := theirs[tok]
			if !ok {
				missingInS101 = append(missingInS101, s.profile+"/"+tok)
				continue
			}
			if !within(dh, sh, *tol) {
				mismatches = append(mismatches, mismatch{s.profile, tok, dh, sh})
			}
		}
		for tok := range theirs {
			if !seen[tok] {
				missingInDAI = append(missingInDAI, s.profile+"/"+tok)
			}
		}
	}

	sort.Slice(mismatches, func(i, j int) bool {
		if mismatches[i].scheme != mismatches[j].scheme {
			return mismatches[i].scheme < mismatches[j].scheme
		}
		return mismatches[i].token < mismatches[j].token
	})
	sort.Strings(missingInS101)
	sort.Strings(missingInDAI)

	fmt.Printf("Compared %d (token×scheme) colour cells across Day/Dusk/Night (tolerance ±%d/channel).\n", total, *tol)
	fmt.Printf("  exact/within-tol matches: %d\n", total-len(mismatches)-len(missingInS101))
	fmt.Printf("  mismatched RGB:           %d\n", len(mismatches))
	fmt.Printf("  in DAI, absent in S-101:  %d\n", len(missingInS101))
	fmt.Printf("  in S-101, absent in DAI:  %d\n\n", len(missingInDAI))

	if len(mismatches) > 0 {
		fmt.Println("RGB mismatches (scheme token DAI→S101):")
		for _, m := range mismatches {
			fmt.Printf("  %-6s %-7s %s -> %s\n", m.scheme, m.token, m.dai, m.s101)
		}
		fmt.Println()
	}
	if len(missingInS101) > 0 {
		fmt.Printf("Tokens in DAI missing from S-101 profile: %s\n\n", strings.Join(missingInS101, " "))
	}
	if len(missingInDAI) > 0 {
		fmt.Printf("Tokens in S-101 profile absent from DAI: %s\n\n", strings.Join(missingInDAI, " "))
	}

	if len(mismatches) == 0 && len(missingInS101) == 0 {
		fmt.Println("RESULT: PASS — S-101 colour profile reproduces every DAI colour. Colour seam proven.")
	} else {
		fmt.Println("RESULT: differences above are the colour gap list (expected to be small).")
	}
}

func clamp(v int) int {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}

// within reports whether two #rrggbb strings are equal channel-wise within tol.
func within(a, b string, tol int) bool {
	if tol <= 0 {
		return a == b
	}
	if len(a) != 7 || len(b) != 7 {
		return a == b
	}
	for i := 1; i < 7; i += 2 {
		x := hexByte(a[i], a[i+1])
		y := hexByte(b[i], b[i+1])
		d := x - y
		if d < 0 {
			d = -d
		}
		if d > tol {
			return false
		}
	}
	return true
}

func hexByte(hi, lo byte) int { return nib(hi)*16 + nib(lo) }
func nib(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return 0
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "s101-color-diff: "+format+"\n", args...)
	os.Exit(1)
}
