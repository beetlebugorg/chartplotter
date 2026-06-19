package s52

// OBSTRN07 represents the Obstruction symbology procedure (S-52 PresLib 4.0,
// Figures 11-15). Symbolises underwater obstructions and rocks (UWTROC) by
// depth, water level and category.
//
// Point geometry follows Continuation A (Figure 12) faithfully. Line and area
// geometry (Continuations B/C) are not yet implemented — they fall back to the
// simplified single-symbol legacy path.
type OBSTRN07 struct {
	ctx          *CSContext
	lib          *Library
	valsou       float64 // depth over the obstruction (from VALSOU or DEPVAL02)
	valsouExists bool
	watlev       int
	catobs       int
}

// NewOBSTRN07 creates a new OBSTRN07 procedure instance by parsing the context.
func NewOBSTRN07(csctx *CSContext, lib *Library) *OBSTRN07 {
	o := &OBSTRN07{
		ctx:          csctx,
		lib:          lib,
		valsou:       csctx.GetFloat("VALSOU", -1.0),
		valsouExists: csctx.Has("VALSOU"),
		watlev:       csctx.GetInt("WATLEV", 0),
		catobs:       csctx.GetInt("CATOBS", 0),
	}
	if !o.valsouExists {
		o.fetchDepthFromUnderlying()
	}
	return o
}

// Execute runs the OBSTRN07 symbology procedure.
func (o *OBSTRN07) Execute() ([]Instruction, error) {
	if o.ctx.GeometryType == "Point" || o.ctx.GeometryType == "" {
		return o.continuationA(), nil
	}
	return o.legacyLineArea(), nil
}

// continuationA implements S-52 OBSTRN07 Continuation A (Figure 12) for point
// objects (UWTROC and OBSTRN): isolated-danger test, then a symbol + optional
// sounding + optional low-accuracy marker.
func (o *OBSTRN07) continuationA() []Instruction {
	var ins []Instruction
	lowAcc := o.lib.csQUAPNT02(o.ctx.Attributes, o.ctx.Mariner)

	// Isolated danger (UDWHAZ05): draw ISODGR01 (+ low accuracy) and stop.
	if show, _, _ := o.lib.csUDWHAZ05(o.valsou, o.ctx.Attributes, o.ctx.Spatial, o.ctx.Mariner); show {
		ins = append(ins, &SYInstruction{SymbolID: "ISODGR01"})
		if lowAcc {
			ins = append(ins, &SYInstruction{SymbolID: "LOWACC01"})
		}
		return ins
	}

	symbol, sounding := o.selectPointSymbol()
	ins = append(ins, &SYInstruction{SymbolID: symbol})
	if sounding {
		if snd, err := o.lib.csSONDFRM04(o.valsou, o.ctx.Attributes, o.ctx.Mariner); err == nil {
			ins = append(ins, snd...)
		}
	}
	if lowAcc {
		ins = append(ins, &SYInstruction{SymbolID: "LOWACC01"})
	}
	return ins
}

// selectPointSymbol returns the Continuation A symbol and whether a sounding is
// drawn, per Figure 12. UWTROC uses the rock symbols; OBSTRN the obstruction
// symbols; sounded dangers use DANGER01/02/03.
func (o *OBSTRN07) selectPointSymbol() (symbol string, sounding bool) {
	uwtroc := o.ctx.ObjectClass == "UWTROC"

	if !o.valsouExists {
		// No sounding known.
		if uwtroc {
			if o.watlev == 3 { // always under water
				return "UWTROC03", false
			}
			return "UWTROC04", false
		}
		switch {
		case o.catobs == 6: // foul area
			return "OBSTRN01", false
		case o.watlev == 1 || o.watlev == 2: // partly submerged / always dry
			return "OBSTRN11", false
		case o.watlev == 4 || o.watlev == 5: // covers-uncovers / awash
			return "OBSTRN03", false
		default:
			return "OBSTRN01", false
		}
	}

	// VALSOU known. Deeper than the safety depth → the faint deep-danger symbol.
	if o.valsou > o.ctx.Mariner.SafetyDepth {
		return "DANGER02", true
	}
	// VALSOU <= SAFETY DEPTH.
	if uwtroc {
		if o.watlev == 4 || o.watlev == 5 { // covers-uncovers / awash rock
			return "UWTROC04", false
		}
		return "DANGER01", true
	}
	switch {
	case o.catobs == 6: // foul area
		return "DANGER01", true
	case o.watlev == 1 || o.watlev == 2: // partly submerged / always dry
		return "OBSTRN11", false
	case o.watlev == 4 || o.watlev == 5: // covers-uncovers / awash
		return "DANGER03", true
	default:
		return "DANGER01", true
	}
}

// legacyLineArea is the pre-existing simplified single-symbol path for line/area
// obstructions, until Continuations B/C are implemented. A sounded obstruction
// keeps the prior behaviour — the DANGER01/02 symbol (tagged live by the bake) +
// a depth label — so area/line dangers still swap against the live safety
// contour; an unsounded one uses the plain obstruction symbol.
func (o *OBSTRN07) legacyLineArea() []Instruction {
	if o.valsouExists {
		sym := "DANGER01"
		if o.valsou > o.ctx.Mariner.SafetyDepth {
			sym = "DANGER02"
		}
		ins := []Instruction{&SYInstruction{SymbolID: sym}}
		if o.valsou > 0 {
			ins = append(ins, o.depthLabelInstruction())
		}
		return ins
	}
	return []Instruction{&SYInstruction{SymbolID: o.legacySymbol()}}
}

func (o *OBSTRN07) legacySymbol() string {
	switch o.catobs {
	case 6, 7: // foul ground / area
		return "FOULGND1"
	case 9: // boom
		return "OBSTRN08"
	default:
		if o.watlev == 4 || o.watlev == 5 {
			return "OBSTRN11"
		}
		if o.isDangerous() {
			return "OBSTRN03"
		}
		return "OBSTRN01"
	}
}

// fetchDepthFromUnderlying calls DEPVAL02 to get depth from underlying depth areas.
func (o *OBSTRN07) fetchDepthFromUnderlying() {
	leastDepth, _ := o.lib.csDEPVAL02(o.ctx.Attributes, o.ctx.Spatial, o.ctx.Mariner)
	if leastDepth >= 0 {
		o.valsou = leastDepth
		o.valsouExists = true
	}
}

// isDangerous reports VALSOU <= SAFETY DEPTH (legacy path).
func (o *OBSTRN07) isDangerous() bool {
	return o.valsouExists && o.valsou <= o.ctx.Mariner.SafetyDepth
}

// depthLabelInstruction creates a centred depth label above the symbol (legacy
// path only — Continuation A uses SNDFRM04 sounding glyphs instead).
func (o *OBSTRN07) depthLabelInstruction() *TXInstruction {
	depthStr := formatDepthValue(ConvertDepth(o.valsou, DepthUnitMeters, o.lib.depthUnit))
	return &TXInstruction{
		TextInstruction: &TextInstruction{
			Text:    depthStr,
			HJust:   1, // centre
			VJust:   1, // bottom (text sits above the anchor)
			Space:   2,
			Font:    FontSpec{Style: 1, Weight: 5, Slant: 1, BodySize: 10},
			XOffset: 0,
			YOffset: 0,
			Color:   "CHBLK",
			Display: 34,
		},
	}
}
