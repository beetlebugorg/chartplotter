package s52

import (
	"testing"
)

func TestLinestyle_ParseLIND(t *testing.T) {
	tests := []struct {
		name       string
		lind       string
		wantID     string
		wantPivotX int
		wantPivotY int
		wantBBoxW  int
		wantBBoxH  int
		wantBBoxX  int
		wantBBoxY  int
		wantErr    bool
	}{
		{
			name:       "Valid LIND - ACHARE51",
			lind:       "ACHARE51001080081203030000010030600814",
			wantID:     "ACHARE51",
			wantPivotX: 108,
			wantPivotY: 812,
			wantBBoxW:  3030,
			wantBBoxH:  1,
			wantBBoxX:  306,
			wantBBoxY:  814,
			wantErr:    false,
		},
		{
			name:       "Valid LIND - ACHRES51",
			lind:       "ACHRES51001080081002729005030044600572",
			wantID:     "ACHRES51",
			wantPivotX: 108,
			wantPivotY: 810,
			wantBBoxW:  2729,
			wantBBoxH:  503,
			wantBBoxX:  446,
			wantBBoxY:  572,
			wantErr:    false,
		},
		{
			name:       "Valid LIND - CBLLNE01 from spec",
			lind:       "CBLLNE01007500075000200001000075000700",
			wantID:     "CBLLNE01",
			wantPivotX: 750,
			wantPivotY: 750,
			wantBBoxW:  200,
			wantBBoxH:  100,
			wantBBoxX:  750,
			wantBBoxY:  700,
			wantErr:    false,
		},
		{
			name:    "Too short",
			lind:    "SHORT",
			wantErr: true,
		},
		{
			name:    "Invalid pivot X",
			lind:    "TESTLS01ABCDE0075000200001000075000700",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ls := &Linestyle{}
			err := ls.ParseLIND(tt.lind)

			if (err != nil) != tt.wantErr {
				t.Errorf("ParseLIND() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				return // Don't check fields if we expected an error
			}

			if ls.ID != tt.wantID {
				t.Errorf("ID = %v, want %v", ls.ID, tt.wantID)
			}
			if ls.PivotX != tt.wantPivotX {
				t.Errorf("PivotX = %v, want %v", ls.PivotX, tt.wantPivotX)
			}
			if ls.PivotY != tt.wantPivotY {
				t.Errorf("PivotY = %v, want %v", ls.PivotY, tt.wantPivotY)
			}
			if ls.BBoxWidth != tt.wantBBoxW {
				t.Errorf("BBoxWidth = %v, want %v", ls.BBoxWidth, tt.wantBBoxW)
			}
			if ls.BBoxHeight != tt.wantBBoxH {
				t.Errorf("BBoxHeight = %v, want %v", ls.BBoxHeight, tt.wantBBoxH)
			}
			if ls.BBoxX != tt.wantBBoxX {
				t.Errorf("BBoxX = %v, want %v", ls.BBoxX, tt.wantBBoxX)
			}
			if ls.BBoxY != tt.wantBBoxY {
				t.Errorf("BBoxY = %v, want %v", ls.BBoxY, tt.wantBBoxY)
			}
		})
	}
}

func TestLinestyle_ParseLCRF(t *testing.T) {
	tests := []struct {
		name       string
		lcrf       string
		wantColors int // Number of color mappings
		wantRole   rune
		wantToken  string
	}{
		{
			name:       "Single color - CHMGD",
			lcrf:       "ACHMGD",
			wantColors: 1,
			wantRole:   'A',
			wantToken:  "CHMGD",
		},
		{
			name:       "Multiple colors",
			lcrf:       "ACHMGDBCHBLK",
			wantColors: 2,
			wantRole:   'A',
			wantToken:  "CHMGD",
		},
		{
			name:       "Empty",
			lcrf:       "",
			wantColors: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ls := &Linestyle{}
			err := ls.ParseLCRF(tt.lcrf)

			if err != nil {
				t.Errorf("ParseLCRF() error = %v", err)
				return
			}

			if ls.ColorRef != tt.lcrf {
				t.Errorf("ColorRef = %v, want %v", ls.ColorRef, tt.lcrf)
			}

			if len(ls.Colors.Roles) != tt.wantColors {
				t.Errorf("Colors count = %v, want %v", len(ls.Colors.Roles), tt.wantColors)
			}

			if tt.wantColors > 0 {
				token, ok := ls.Colors.Roles[tt.wantRole]
				if !ok {
					t.Errorf("Color role %c not found", tt.wantRole)
				}
				if token != tt.wantToken {
					t.Errorf("Color token = %v, want %v", token, tt.wantToken)
				}
			}
		})
	}
}

func TestLinestyle_ParseLVCT(t *testing.T) {
	tests := []struct {
		name        string
		lvcts       []string
		wantMinCmds int // Minimum number of commands expected (parser creates multiple commands per LVCT)
	}{
		{
			name: "Single LVCT",
			lvcts: []string{
				"SPA;SW1;PU306,812;PD906,812;",
			},
			wantMinCmds: 1, // Should parse into multiple commands (SPA, SW1, PD)
		},
		{
			name: "Multiple LVCT",
			lvcts: []string{
				"SPA;SW1;PU446,810;PD747,810;",
				"PU595,810;SCEMACHRE2,2;",
				"PU1208,810;SCEMACHRE1,2;",
			},
			wantMinCmds: 3, // Should have at least 3 commands across all LVCTs
		},
		{
			name: "Empty LVCT",
			lvcts: []string{
				"",
			},
			wantMinCmds: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ls := &Linestyle{}

			for _, lvct := range tt.lvcts {
				err := ls.ParseLVCT(lvct)
				if err != nil {
					t.Errorf("ParseLVCT() error = %v", err)
					return
				}
			}

			if len(ls.VectorCommands) < tt.wantMinCmds {
				t.Errorf("VectorCommands count = %v, want at least %v", len(ls.VectorCommands), tt.wantMinCmds)
			}
		})
	}
}

func TestLinestyle_HasSymbolCalls(t *testing.T) {
	tests := []struct {
		name string
		lvct string
		want bool
	}{
		{
			name: "Has SC command",
			lvct: "PU595,810;SCEMACHRE2,2;",
			want: true,
		},
		{
			name: "No SC command",
			lvct: "SPA;SW1;PU446,810;PD747,810;",
			want: false,
		},
		{
			name: "Empty",
			lvct: "",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ls := &Linestyle{}
			if tt.lvct != "" {
				ls.ParseLVCT(tt.lvct)
			}

			if got := ls.HasSymbolCalls(); got != tt.want {
				t.Errorf("HasSymbolCalls() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLinestyle_Validate(t *testing.T) {
	tests := []struct {
		name         string
		linestyle    *Linestyle
		wantWarnings int
	}{
		{
			name: "Valid linestyle",
			linestyle: &Linestyle{
				ID:             "ACHARE51",
				BBoxWidth:      3030,
				BBoxHeight:     1,
				VectorCommands: []VectorCommand{{RawCommand: "test"}},
			},
			wantWarnings: 0,
		},
		{
			name: "Missing ID",
			linestyle: &Linestyle{
				BBoxWidth:      100,
				BBoxHeight:     100,
				VectorCommands: []VectorCommand{{RawCommand: "test"}},
			},
			wantWarnings: 1,
		},
		{
			name: "Zero bbox dimensions",
			linestyle: &Linestyle{
				ID:             "TEST",
				BBoxWidth:      0,
				BBoxHeight:     0,
				VectorCommands: []VectorCommand{{RawCommand: "test"}},
			},
			wantWarnings: 1,
		},
		{
			name: "No vector commands",
			linestyle: &Linestyle{
				ID:         "TEST",
				BBoxWidth:  100,
				BBoxHeight: 100,
			},
			wantWarnings: 1,
		},
		{
			name: "Multiple issues",
			linestyle: &Linestyle{
				ID:         "",
				BBoxWidth:  0,
				BBoxHeight: 0,
			},
			wantWarnings: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnings := tt.linestyle.Validate()

			if len(warnings) != tt.wantWarnings {
				t.Errorf("Validate() warnings = %d, want %d. Warnings: %v",
					len(warnings), tt.wantWarnings, warnings)
			}
		})
	}
}
