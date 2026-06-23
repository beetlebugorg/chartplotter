package nmea

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCPA_HeadOn(t *testing.T) {
	// Own at origin heading north 10 kn; target 1 nm dead ahead heading south 10 kn.
	cpa, tcpa, ok := CPA(0, 0, 0, 10, 1.0/60, 0, 180, 10)
	require.True(t, ok)
	assert.InDelta(t, 0, cpa, 1e-6, "head-on → CPA ~0")
	assert.InDelta(t, 3, tcpa, 0.05, "1 nm closing at 20 kn → 3 min")
}

func TestCPA_Diverging(t *testing.T) {
	// Target 1 nm ahead but moving north (same as own) faster → opening; CPA in past.
	_, tcpa, ok := CPA(0, 0, 0, 10, 1.0/60, 0, 0, 15)
	require.True(t, ok)
	assert.Less(t, tcpa, 0.0, "diverging → TCPA negative (past)")
}

func TestCPA_Parallel(t *testing.T) {
	// Identical velocity → no relative motion → not ok, cpa = current range.
	cpa, _, ok := CPA(0, 0, 90, 8, 0, 0.5/60, 90, 8)
	assert.False(t, ok)
	assert.InDelta(t, 0.5, cpa, 1e-6)
}

func TestCPA_CrossingMiss(t *testing.T) {
	// Target 2 nm east heading north; own heading north. They never get closer
	// than ~2 nm (parallel tracks offset east).
	cpa, _, ok := CPA(0, 0, 0, 10, 0, 2.0/60, 0, 10)
	assert.False(t, ok) // same velocity → parallel
	assert.InDelta(t, 2, cpa, 1e-3)
}
