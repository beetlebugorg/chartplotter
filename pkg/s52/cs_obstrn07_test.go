package s52

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// S-52 OBSTRN07 Continuation A (Figure 12) point-symbol selection.
func TestOBSTRN07_PointSymbols(t *testing.T) {
	lib := &Library{}
	sel := func(class string, attrs map[string]interface{}) (string, bool) {
		ctx := NewCSContext(attrs, "Point", nil, nil)
		ctx.ObjectClass = class
		return NewOBSTRN07(ctx, lib).selectPointSymbol()
	}

	// UWTROC, no VALSOU: always-underwater (WATLEV 3) → UWTROC03; else UWTROC04.
	s, snd := sel("UWTROC", map[string]interface{}{"WATLEV": 3})
	require.Equal(t, "UWTROC03", s)
	require.False(t, snd)
	s, _ = sel("UWTROC", map[string]interface{}{"WATLEV": 4})
	require.Equal(t, "UWTROC04", s)

	// UWTROC, VALSOU awash (WATLEV 4/5, VALSOU<=safety depth) → UWTROC04, no sounding.
	s, snd = sel("UWTROC", map[string]interface{}{"VALSOU": 0.0, "WATLEV": 5})
	require.Equal(t, "UWTROC04", s)
	require.False(t, snd)
	// UWTROC, VALSOU underwater dangerous → DANGER01 + sounding.
	s, snd = sel("UWTROC", map[string]interface{}{"VALSOU": 3.0, "WATLEV": 3})
	require.Equal(t, "DANGER01", s)
	require.True(t, snd)

	// OBSTRN, no VALSOU: WATLEV 1/2 → OBSTRN11; 4/5 → OBSTRN03; else OBSTRN01.
	s, _ = sel("OBSTRN", map[string]interface{}{"WATLEV": 1})
	require.Equal(t, "OBSTRN11", s)
	s, _ = sel("OBSTRN", map[string]interface{}{"WATLEV": 4})
	require.Equal(t, "OBSTRN03", s)
	s, _ = sel("OBSTRN", map[string]interface{}{})
	require.Equal(t, "OBSTRN01", s)

	// OBSTRN, VALSOU: deep → DANGER02; covers/uncovers (WATLEV 4/5) → DANGER03;
	// awash above water (WATLEV 1/2) → OBSTRN11; else DANGER01.
	s, snd = sel("OBSTRN", map[string]interface{}{"VALSOU": 50.0})
	require.Equal(t, "DANGER02", s)
	require.True(t, snd)
	s, _ = sel("OBSTRN", map[string]interface{}{"VALSOU": 3.0, "WATLEV": 4})
	require.Equal(t, "DANGER03", s)
	s, snd = sel("OBSTRN", map[string]interface{}{"VALSOU": 3.0, "WATLEV": 1})
	require.Equal(t, "OBSTRN11", s)
	require.False(t, snd)
	s, _ = sel("OBSTRN", map[string]interface{}{"VALSOU": 3.0})
	require.Equal(t, "DANGER01", s)
}
