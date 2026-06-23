// Command lua-smoke is the Phase 6 de-risk for the S-101 backport
// (specs/s101-portrayal-backport.md): it confirms that gopher-lua (pure-Go,
// Lua 5.1) can actually parse/compile every rule in the S-101 Portrayal
// Catalogue. It only *compiles* each file (no execution — that needs the host
// API), which is exactly the syntax/version check we need: any use of Lua 5.2+
// constructs gopher-lua can't parse shows up here as a compile failure.
//
// Usage:
//
//	go run ./cmd/lua-smoke --rules /path/to/PortrayalCatalog/Rules
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	lua "github.com/yuin/gopher-lua"
	"github.com/yuin/gopher-lua/parse"
)

func main() {
	rules := flag.String("rules", "/home/jcollins/Projects/s101-portrayal-catalogue/PortrayalCatalog/Rules", "path to S-101 Rules directory")
	flag.Parse()

	entries, err := os.ReadDir(*rules)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lua-smoke: read %s: %v\n", *rules, err)
		os.Exit(1)
	}

	type failure struct{ file, err string }
	var files []string
	for _, e := range entries {
		if strings.EqualFold(filepath.Ext(e.Name()), ".lua") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	var failures []failure
	for _, name := range files {
		path := filepath.Join(*rules, name)
		if err := compile(path); err != nil {
			failures = append(failures, failure{name, err.Error()})
		}
	}

	fmt.Printf("Compiled %d Lua rule files with gopher-lua %s.\n", len(files), luaVersion())
	if len(failures) == 0 {
		fmt.Println("RESULT: PASS — every rule parses as Lua 5.1. gopher-lua is viable for Workstream D.")
		return
	}
	fmt.Printf("RESULT: %d FAILURES — gopher-lua could not parse these:\n\n", len(failures))
	for _, f := range failures {
		fmt.Printf("  %-28s %s\n", f.file, firstLine(f.err))
	}
	os.Exit(1)
}

// compile parses + compiles the file to a function proto without running it.
func compile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	chunk, err := parse.Parse(f, path)
	if err != nil {
		return err
	}
	_, err = lua.Compile(chunk, path)
	return err
}

func luaVersion() string { return lua.LuaVersion }

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
