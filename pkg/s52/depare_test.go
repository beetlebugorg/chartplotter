package s52

import (
	"fmt"
	"testing"
)

func TestDEPAREParsing(t *testing.T) {
	lib := loadTestLibrary(t)

	// Count DEPARE entries
	depareCount := 0
	emptyDiscCount := 0

	for _, lupt := range lib.lookupTables {
		if lupt.ObjectClass == "DEPARE" {
			depareCount++
			if lupt.DisplayCategory == "" {
				emptyDiscCount++
				fmt.Printf("DEPARE with empty DISC: ID=%s, DisplayPriority=%d, Attrs=%d\n",
					lupt.ID, lupt.DisplayPriority, len(lupt.Attributes))
			}
		}
	}

	fmt.Printf("\nTotal DEPARE entries: %d\n", depareCount)
	fmt.Printf("DEPARE with empty DisplayCategory: %d\n", emptyDiscCount)

	// Now test GetLookupEntry
	entry := lib.GetLookupEntry("DEPARE", map[string]interface{}{})
	fmt.Printf("\nGetLookupEntry(DEPARE, {}):\n")
	if entry == nil {
		fmt.Println("  nil")
	} else {
		fmt.Printf("  DisplayCategory: '%s'\n", entry.DisplayCategory)
		fmt.Printf("  DisplayPriority: %d\n", entry.DisplayPriority)
	}
}
