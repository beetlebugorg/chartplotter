package s52

import (
	"testing"
)

const testDAIPath = "../../testdata/PresLib_e4.0.0.dai"

// loadTestLibrary loads the test DAI file, skipping the test if unavailable
func loadTestLibrary(t *testing.T) *Library {
	lib, err := LoadLibrary(testDAIPath)
	if err != nil {
		t.Skipf("Skipping test: DAI file not available (%v)", err)
	}
	return lib
}
