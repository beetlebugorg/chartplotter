package catalog

import (
	"fmt"
	"path/filepath"
)

// RGB is an 8-bit sRGB colour.
type RGB struct{ R, G, B uint8 }

// Palette maps a colour token (e.g. CHBLK) to its sRGB value for one viewing
// condition.
type Palette map[string]RGB

// ColorProfile is the S-101 colorProfile.xml: the same tokens resolved for each
// viewing condition. (Verified byte-identical to the S-52 DAI colours by
// cmd/s101-color-diff.)
type ColorProfile struct {
	Day, Dusk, Night Palette
}

// For returns the palette for a scheme name ("day"/"dusk"/"night"); unknown
// names fall back to Day.
func (c *ColorProfile) For(scheme string) Palette {
	switch scheme {
	case "dusk":
		return c.Dusk
	case "night":
		return c.Night
	default:
		return c.Day
	}
}

type xmlColorProfile struct {
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

// LoadColorProfile parses ColorProfiles/colorProfile.xml.
func LoadColorProfile(path string) (*ColorProfile, error) {
	var x xmlColorProfile
	if err := readXML(path, &x); err != nil {
		return nil, err
	}
	cp := &ColorProfile{Day: Palette{}, Dusk: Palette{}, Night: Palette{}}
	for _, p := range x.Palettes {
		var pal Palette
		switch p.Name {
		case "Day":
			pal = cp.Day
		case "Dusk":
			pal = cp.Dusk
		case "Night":
			pal = cp.Night
		default:
			continue
		}
		for _, it := range p.Items {
			pal[it.Token] = RGB{R: clampByte(it.SRGB.R), G: clampByte(it.SRGB.G), B: clampByte(it.SRGB.B)}
		}
	}
	return cp, nil
}

// Catalog is the loaded static drawing assets of the S-101 Portrayal Catalogue
// (symbols are rasterized separately). DrawCommand references resolve against it.
type Catalog struct {
	LineStyles map[string]*LineStyle
	AreaFills  map[string]*AreaFill
	Colors     *ColorProfile
}

// Load reads LineStyles/, AreaFills/, and ColorProfiles/colorProfile.xml from a
// PortrayalCatalog directory.
func Load(portrayalCatalogDir string) (*Catalog, error) {
	c := &Catalog{}
	var err error
	if c.LineStyles, err = LoadLineStyles(filepath.Join(portrayalCatalogDir, "LineStyles")); err != nil {
		return nil, fmt.Errorf("line styles: %w", err)
	}
	if c.AreaFills, err = LoadAreaFills(filepath.Join(portrayalCatalogDir, "AreaFills")); err != nil {
		return nil, fmt.Errorf("area fills: %w", err)
	}
	if c.Colors, err = LoadColorProfile(filepath.Join(portrayalCatalogDir, "ColorProfiles", "colorProfile.xml")); err != nil {
		return nil, fmt.Errorf("colour profile: %w", err)
	}
	return c, nil
}

func clampByte(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}
