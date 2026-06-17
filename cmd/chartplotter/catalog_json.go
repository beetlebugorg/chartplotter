package main

import (
	"fmt"
	"os"

	"github.com/beetlebugorg/chartplotter/internal/engine/catalog"
)

// catalogJSONCmd distils NOAA's ENCProdCat.xml into a compact catalog.json the
// chart-manager frontend loads. Port of `chartplotter --catalog-json`.
type catalogJSONCmd struct {
	In  string `arg:"" type:"existingfile" help:"NOAA ENCProdCat.xml."`
	Out string `arg:"" type:"path" help:"Output catalog.json."`
}

func (c catalogJSONCmd) Run() error {
	xml, err := os.ReadFile(c.In)
	if err != nil {
		return err
	}
	f, err := os.Create(c.Out)
	if err != nil {
		return err
	}
	defer f.Close()
	n, err := catalog.XMLToJSON(string(xml), f)
	if err != nil {
		return err
	}
	fmt.Printf("catalog: %d cells -> %s\n", n, c.Out)
	return nil
}
