module github.com/beetlebugorg/chartplotter

go 1.26.0

toolchain go1.26.5

require (
	github.com/BertoldVdb/go-ais v0.4.0
	github.com/alecthomas/kong v1.15.0
	github.com/stretchr/testify v1.11.1
	github.com/tetratelabs/wazero v1.12.0
	golang.org/x/image v0.44.0
	modernc.org/sqlite v1.53.0
)

require golang.org/x/sync v0.21.0 // indirect

require (
	github.com/adrianmo/go-nmea v1.3.0 // indirect
	github.com/beetlebugorg/tile57/bindings/go v0.0.0
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/sys v0.45.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	modernc.org/libc v1.73.4 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)

// The engine binding lives in the ./tile57 git submodule (github.com/beetlebugorg/tile57;
// clone with --recurse-submodules, or run `git submodule update --init --recursive`).
// The CGO binding needs the engine source tree relative to itself, so this is a local
// replace, not a module-proxy dependency. To build against a DIFFERENT engine checkout,
// override with a gitignored go.work instead of editing this line — see README.md
// "Developing the engine".
replace github.com/beetlebugorg/tile57/bindings/go => ./tile57/bindings/go
