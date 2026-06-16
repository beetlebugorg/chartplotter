package s52

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTextInstruction(t *testing.T) {
	tests := []struct {
		name        string
		instruction string
		want        *TextInstruction
		wantErr     bool
	}{
		{
			name:        "literal text - quoted",
			instruction: "TX('DR',2,3,2,'15110',-1,1,CHBLK,50)",
			want: &TextInstruction{
				Text:                 "DR",
				IsAttributeReference: false,
				HJust:                2,
				VJust:                3,
				Space:                2,
				Font:                 FontSpec{Style: 1, Weight: 5, Slant: 1, BodySize: 10},
				XOffset:              -1,
				YOffset:              1,
				Color:                "CHBLK",
				Display:              50,
			},
			wantErr: false,
		},
		{
			name:        "attribute reference - unquoted",
			instruction: "TX(OBJNAM,1,2,3,'15110',0,0,CHBLK,26)",
			want: &TextInstruction{
				Text:                 "OBJNAM",
				IsAttributeReference: true,
				HJust:                1,
				VJust:                2,
				Space:                3,
				Font:                 FontSpec{Style: 1, Weight: 5, Slant: 1, BodySize: 10},
				XOffset:              0,
				YOffset:              0,
				Color:                "CHBLK",
				Display:              26,
			},
			wantErr: false,
		},
		{
			name:        "attribute reference - LITVES",
			instruction: "TX(LITVES,2,2,2,'15110',0,0,CHBLK,29)",
			want: &TextInstruction{
				Text:                 "LITVES",
				IsAttributeReference: true,
				HJust:                2,
				VJust:                2,
				Space:                2,
				Font:                 FontSpec{Style: 1, Weight: 5, Slant: 1, BodySize: 10},
				XOffset:              0,
				YOffset:              0,
				Color:                "CHBLK",
				Display:              29,
			},
			wantErr: false,
		},
		{
			name:        "literal text - bold font",
			instruction: "TX('ROCK',2,2,2,'16110',0,0,CHBLK,29)",
			want: &TextInstruction{
				Text:                 "ROCK",
				IsAttributeReference: false,
				HJust:                2,
				VJust:                2,
				Space:                2,
				Font:                 FontSpec{Style: 1, Weight: 6, Slant: 1, BodySize: 10},
				XOffset:              0,
				YOffset:              0,
				Color:                "CHBLK",
				Display:              29,
			},
			wantErr: false,
		},
		{
			name:        "literal text - italic font",
			instruction: "TX('Note',1,1,2,'15210',0,0,CHGRD,21)",
			want: &TextInstruction{
				Text:                 "Note",
				IsAttributeReference: false,
				HJust:                1,
				VJust:                1,
				Space:                2,
				Font:                 FontSpec{Style: 1, Weight: 5, Slant: 2, BodySize: 10},
				XOffset:              0,
				YOffset:              0,
				Color:                "CHGRD",
				Display:              21,
			},
			wantErr: false,
		},
		{
			name:        "literal text - large font size",
			instruction: "TX('Title',2,3,2,'15114',0,0,CHBLK,50)",
			want: &TextInstruction{
				Text:                 "Title",
				IsAttributeReference: false,
				HJust:                2,
				VJust:                3,
				Space:                2,
				Font:                 FontSpec{Style: 1, Weight: 5, Slant: 1, BodySize: 14},
				XOffset:              0,
				YOffset:              0,
				Color:                "CHBLK",
				Display:              50,
			},
			wantErr: false,
		},
		{
			name:        "invalid format - no TX prefix",
			instruction: "SY('BOYSPH01')",
			wantErr:     true,
		},
		{
			name:        "invalid format - too few arguments",
			instruction: "TX('text',1,2)",
			wantErr:     true,
		},
		{
			name:        "invalid CHARS - too short",
			instruction: "TX('text',1,2,3,'151',0,0,CHBLK,50)",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseTextInstruction(tt.instruction)
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFontSpec_Methods(t *testing.T) {
	tests := []struct {
		name       string
		font       FontSpec
		wantBold   bool
		wantItalic bool
		wantSerif  bool
		wantMM     float64
	}{
		{
			name:       "default font",
			font:       FontSpec{Style: 1, Weight: 5, Slant: 1, BodySize: 10},
			wantBold:   false,
			wantItalic: false,
			wantSerif:  true,
			wantMM:     3.51,
		},
		{
			name:       "bold font",
			font:       FontSpec{Style: 1, Weight: 6, Slant: 1, BodySize: 10},
			wantBold:   true,
			wantItalic: false,
			wantSerif:  true,
			wantMM:     3.51,
		},
		{
			name:       "italic font",
			font:       FontSpec{Style: 1, Weight: 5, Slant: 2, BodySize: 10},
			wantBold:   false,
			wantItalic: true,
			wantSerif:  true,
			wantMM:     3.51,
		},
		{
			name:       "large font",
			font:       FontSpec{Style: 1, Weight: 5, Slant: 1, BodySize: 14},
			wantBold:   false,
			wantItalic: false,
			wantSerif:  true,
			wantMM:     4.914,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantBold, tt.font.IsBold())
			assert.Equal(t, tt.wantItalic, tt.font.IsItalic())
			assert.Equal(t, tt.wantSerif, tt.font.IsSerif())
			assert.InDelta(t, tt.wantMM, tt.font.BodySizeMM(), 0.001)
		})
	}
}

func TestSplitTextArgs(t *testing.T) {
	tests := []struct {
		name string
		args string
		want []string
	}{
		{
			name: "simple arguments",
			args: "'text',1,2,3,'15110',0,0,CHBLK,50",
			want: []string{"'text'", "1", "2", "3", "'15110'", "0", "0", "CHBLK", "50"},
		},
		{
			name: "text with comma inside quotes",
			args: "'Hello, World',1,2,3,'15110',0,0,CHBLK,50",
			want: []string{"'Hello, World'", "1", "2", "3", "'15110'", "0", "0", "CHBLK", "50"},
		},
		{
			name: "negative offsets",
			args: "'DR',2,3,2,'15110',-1,1,CHBLK,50",
			want: []string{"'DR'", "2", "3", "2", "'15110'", "-1", "1", "CHBLK", "50"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitTextArgs(tt.args)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseInstructions(t *testing.T) {
	tests := []struct {
		name        string
		instruction string
		wantLen     int
		wantTypes   []InstructionType
		wantErr     bool
	}{
		{
			name:        "empty string",
			instruction: "",
			wantLen:     0,
			wantTypes:   []InstructionType{},
			wantErr:     false,
		},
		{
			name:        "single SY instruction",
			instruction: "SY(ACHARE51)",
			wantLen:     1,
			wantTypes:   []InstructionType{InstructionSY},
			wantErr:     false,
		},
		{
			name:        "single LC instruction",
			instruction: "LC(NAVARE51)",
			wantLen:     1,
			wantTypes:   []InstructionType{InstructionLC},
			wantErr:     false,
		},
		{
			name:        "single LS instruction",
			instruction: "LS(SOLD,1,CHBLK)",
			wantLen:     1,
			wantTypes:   []InstructionType{InstructionLS},
			wantErr:     false,
		},
		{
			name:        "single AC instruction",
			instruction: "AC(DEPMD)",
			wantLen:     1,
			wantTypes:   []InstructionType{InstructionAC},
			wantErr:     false,
		},
		{
			name:        "AC with transparency",
			instruction: "AC(DEPMD,2)",
			wantLen:     1,
			wantTypes:   []InstructionType{InstructionAC},
			wantErr:     false,
		},
		{
			name:        "single AP instruction",
			instruction: "AP(DIAMOND1)",
			wantLen:     1,
			wantTypes:   []InstructionType{InstructionAP},
			wantErr:     false,
		},
		{
			name:        "single TX instruction",
			instruction: "TX('text',1,2,3,'15110',0,0,CHBLK,26)",
			wantLen:     1,
			wantTypes:   []InstructionType{InstructionTX},
			wantErr:     false,
		},
		{
			name:        "single CS instruction",
			instruction: "CS(DEPARE03)",
			wantLen:     1,
			wantTypes:   []InstructionType{InstructionCS},
			wantErr:     false,
		},
		{
			name:        "compound: SY + AC",
			instruction: "SY(ACHARE51);AC(CHBRN)",
			wantLen:     2,
			wantTypes:   []InstructionType{InstructionSY, InstructionAC},
			wantErr:     false,
		},
		{
			name:        "compound: AC + LS",
			instruction: "AC(LANDA);LS(SOLD,1,CHBLK)",
			wantLen:     2,
			wantTypes:   []InstructionType{InstructionAC, InstructionLS},
			wantErr:     false,
		},
		{
			name:        "compound: three instructions",
			instruction: "SY(BOYSPH01);LC(NAVARE51);AC(DEPMD)",
			wantLen:     3,
			wantTypes:   []InstructionType{InstructionSY, InstructionLC, InstructionAC},
			wantErr:     false,
		},
		{
			name:        "compound with TX",
			instruction: "SY(ACHARE51);TX('OBJNAM',1,2,3,'15110',0,0,CHBLK,26)",
			wantLen:     2,
			wantTypes:   []InstructionType{InstructionSY, InstructionTX},
			wantErr:     false,
		},
		{
			name:        "invalid command type",
			instruction: "INVALID(foo)",
			wantErr:     true,
		},
		{
			name:        "invalid format - no parens",
			instruction: "SY_ACHARE51",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseInstructions(tt.instruction)
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Len(t, got, tt.wantLen)

			for i, wantType := range tt.wantTypes {
				assert.Equal(t, wantType, got[i].Type())
			}
		})
	}
}

func TestParseSY(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    *SYInstruction
		wantErr bool
	}{
		{
			name:  "valid symbol ID",
			input: "SY(ACHARE51)",
			want:  &SYInstruction{SymbolID: "ACHARE51"},
		},
		{
			name:  "another valid symbol",
			input: "SY(BOYSPH01)",
			want:  &SYInstruction{SymbolID: "BOYSPH01"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instrs, err := ParseInstructions(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Len(t, instrs, 1)

			sy, ok := instrs[0].(*SYInstruction)
			require.True(t, ok, "expected *SYInstruction")
			assert.Equal(t, tt.want.SymbolID, sy.SymbolID)
		})
	}
}

func TestParseLC(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    *LCInstruction
		wantErr bool
	}{
		{
			name:  "valid linestyle ID",
			input: "LC(NAVARE51)",
			want:  &LCInstruction{LineStyleID: "NAVARE51"},
		},
		{
			name:  "another valid linestyle",
			input: "LC(ACHARE51)",
			want:  &LCInstruction{LineStyleID: "ACHARE51"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instrs, err := ParseInstructions(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Len(t, instrs, 1)

			lc, ok := instrs[0].(*LCInstruction)
			require.True(t, ok, "expected *LCInstruction")
			assert.Equal(t, tt.want.LineStyleID, lc.LineStyleID)
		})
	}
}

func TestParseLS(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    *LSInstruction
		wantErr bool
	}{
		{
			name:  "solid line",
			input: "LS(SOLD,1,CHBLK)",
			want:  &LSInstruction{Style: "SOLD", Width: 1, Color: "CHBLK"},
		},
		{
			name:  "dashed line",
			input: "LS(DASH,2,CHRED)",
			want:  &LSInstruction{Style: "DASH", Width: 2, Color: "CHRED"},
		},
		{
			name:  "dotted line",
			input: "LS(DOTT,1,CHGRN)",
			want:  &LSInstruction{Style: "DOTT", Width: 1, Color: "CHGRN"},
		},
		{
			name:  "wider line from real DAI file",
			input: "LS(SOLD,8,CHBLK)",
			want:  &LSInstruction{Style: "SOLD", Width: 8, Color: "CHBLK"},
		},
		{
			name:    "invalid - too few args",
			input:   "LS(SOLD,1)",
			wantErr: true,
		},
		{
			name:    "invalid - bad width",
			input:   "LS(SOLD,abc,CHBLK)",
			wantErr: true,
		},
		{
			name:    "invalid - zero width",
			input:   "LS(SOLD,0,CHBLK)",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instrs, err := ParseInstructions(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Len(t, instrs, 1)

			ls, ok := instrs[0].(*LSInstruction)
			require.True(t, ok, "expected *LSInstruction")
			assert.Equal(t, tt.want.Style, ls.Style)
			assert.Equal(t, tt.want.Width, ls.Width)
			assert.Equal(t, tt.want.Color, ls.Color)
		})
	}
}

func TestParseAC(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    *ACInstruction
		wantErr bool
	}{
		{
			name:  "color only",
			input: "AC(DEPMD)",
			want:  &ACInstruction{Color: "DEPMD", Transp: 0},
		},
		{
			name:  "color with transparency",
			input: "AC(DEPMD,2)",
			want:  &ACInstruction{Color: "DEPMD", Transp: 2},
		},
		{
			name:  "another color",
			input: "AC(CHBRN)",
			want:  &ACInstruction{Color: "CHBRN", Transp: 0},
		},
		{
			name:    "invalid - bad transparency",
			input:   "AC(DEPMD,abc)",
			wantErr: true,
		},
		{
			name:    "invalid - transparency out of range",
			input:   "AC(DEPMD,4)",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instrs, err := ParseInstructions(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Len(t, instrs, 1)

			ac, ok := instrs[0].(*ACInstruction)
			require.True(t, ok, "expected *ACInstruction")
			assert.Equal(t, tt.want.Color, ac.Color)
			assert.Equal(t, tt.want.Transp, ac.Transp)
		})
	}
}

func TestParseAP(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    *APInstruction
		wantErr bool
	}{
		{
			name:  "valid pattern ID",
			input: "AP(DIAMOND1)",
			want:  &APInstruction{PatternID: "DIAMOND1"},
		},
		{
			name:  "another valid pattern",
			input: "AP(CROSS1)",
			want:  &APInstruction{PatternID: "CROSS1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instrs, err := ParseInstructions(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Len(t, instrs, 1)

			ap, ok := instrs[0].(*APInstruction)
			require.True(t, ok, "expected *APInstruction")
			assert.Equal(t, tt.want.PatternID, ap.PatternID)
		})
	}
}

func TestParseCS(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    *CSInstruction
		wantErr bool
	}{
		{
			name:  "valid procedure name",
			input: "CS(DEPARE03)",
			want:  &CSInstruction{ProcedureName: "DEPARE03"},
		},
		{
			name:  "another valid procedure",
			input: "CS(RESARE02)",
			want:  &CSInstruction{ProcedureName: "RESARE02"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instrs, err := ParseInstructions(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Len(t, instrs, 1)

			cs, ok := instrs[0].(*CSInstruction)
			require.True(t, ok, "expected *CSInstruction")
			assert.Equal(t, tt.want.ProcedureName, cs.ProcedureName)
		})
	}
}

func TestParseTX_Via_ParseInstructions(t *testing.T) {
	input := "TX('OBJNAM',1,2,3,'15110',0,0,CHBLK,26)"

	instrs, err := ParseInstructions(input)
	require.NoError(t, err)
	require.Len(t, instrs, 1)

	tx, ok := instrs[0].(*TXInstruction)
	require.True(t, ok, "expected *TXInstruction")
	assert.Equal(t, "OBJNAM", tx.Text)
	assert.Equal(t, 1, tx.HJust)
	assert.Equal(t, 2, tx.VJust)
	assert.Equal(t, "CHBLK", tx.Color)
}
