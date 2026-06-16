package s52

import (
	"fmt"
	"testing"
)

func TestDEPAREInstructions(t *testing.T) {
	lib := loadTestLibrary(t)

	// Check all DEPARE entries
	for _, lupt := range lib.lookupTables {
		if lupt.ObjectClass == "DEPARE" {
			fmt.Printf("\nDEPARE entry: %s\n", lupt.ID)
			fmt.Printf("  GeometryType: '%s'\n", lupt.GeometryType)
			fmt.Printf("  TableName: '%s'\n", lupt.TableName)
			fmt.Printf("  Attributes: %d\n", len(lupt.Attributes))
			fmt.Printf("  Instructions: %d\n", len(lupt.Instructions))
			if len(lupt.Instructions) > 0 {
				fmt.Printf("  First instruction: %s\n", lupt.Instructions[0].RawCommand)
			}
			fmt.Printf("  DisplayCategory: '%s'\n", lupt.DisplayCategory)
		}
	}
}
