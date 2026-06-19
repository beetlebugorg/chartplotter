package s52

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// S-52 RESCSP02 (Figure 28): RESTRN selects one symbol by family — entry
// (ENTRES) / anchoring (ACHRES) / fishing (FSHRES) / other (CTYARE) / info
// (INFARE) / unknown (RSRDEF), with 51/61/71 variants. A cable area's anchoring
// restriction must NOT render as the entry symbol.
func TestRESCSP02_Families(t *testing.T) {
	lib := &Library{}
	sym := func(restrn interface{}) string {
		ins, err := lib.csRESCSP02(map[string]interface{}{"RESTRN": restrn}, nil)
		require.NoError(t, err)
		require.Len(t, ins, 1)
		return ins[0].(*SYInstruction).SymbolID
	}

	// Anchoring (1/2) — the CBLARE case that was wrongly showing ENTRES51.
	require.Equal(t, "ACHRES51", sym("1"))
	require.Equal(t, "ACHRES51", sym(2))
	// Anchoring + fishing → additional-restriction "!" variant.
	require.Equal(t, "ACHRES61", sym("1,3"))
	// Anchoring + an information-only restriction → "i" variant.
	require.Equal(t, "ACHRES71", sym("1,9"))

	// Fishing/trawling.
	require.Equal(t, "FSHRES51", sym("3"))
	require.Equal(t, "FSHRES51", sym([]int{4}))

	// Entry prohibited/restricted/area-to-be-avoided.
	require.Equal(t, "ENTRES51", sym("7"))
	require.Equal(t, "ENTRES51", sym("14"))
	require.Equal(t, "ENTRES61", sym("8,1")) // entry + anchoring → "!"

	// Other / information / unknown.
	require.Equal(t, "CTYARE51", sym("13"))
	require.Equal(t, "INFARE51", sym("9"))

	// No RESTRN → no symbol.
	ins, err := lib.csRESCSP02(map[string]interface{}{}, nil)
	require.NoError(t, err)
	require.Nil(t, ins)
}
