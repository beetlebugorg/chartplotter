package s52

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// ctxUWTROC builds an OBSTRN07 context for a UWTROC rock with the given VALSOU.
func ctxUWTROC(valsou float64) *CSContext {
	ctx := NewCSContext(map[string]interface{}{"VALSOU": valsou}, "Point", nil, nil)
	ctx.ObjectClass = "UWTROC"
	return ctx
}

func symbolIDs(instrs []Instruction) []string {
	var out []string
	for _, in := range instrs {
		if sy, ok := in.(*SYInstruction); ok {
			out = append(out, sy.SymbolID)
		}
	}
	return out
}

// S-52 OBSTRN07 Continuation A: a rock at/above sounding datum (VALSOU<=0) is
// awash -> UWTROC04; an underwater rock (VALSOU>0) -> UWTROC03. Neither uses the
// generic obstruction glyphs, and the awash rock carries no "0" depth label.
func TestOBSTRN07_UWTROC_Symbols(t *testing.T) {
	lib := &Library{}

	awash, err := NewOBSTRN07(ctxUWTROC(0), lib).Execute()
	require.NoError(t, err)
	require.Equal(t, []string{"UWTROC04"}, symbolIDs(awash), "awash rock -> UWTROC04")
	for _, in := range awash {
		_, isText := in.(*TXInstruction)
		require.False(t, isText, "awash rock (VALSOU<=0) must not draw a depth label")
	}

	underwater, err := NewOBSTRN07(ctxUWTROC(3.0), lib).Execute()
	require.NoError(t, err)
	require.Equal(t, "UWTROC03", symbolIDs(underwater)[0], "underwater rock -> UWTROC03")
}
