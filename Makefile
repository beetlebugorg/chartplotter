VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)
BIN := bin/chartplotter

# Cross-build matrix for `make xbuild`. Override for a subset, e.g.
# `make xbuild PLATFORMS=darwin/arm64` or `PLATFORMS="darwin/arm64 linux/amd64"`.
PLATFORMS ?= linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

# serve overrides (e.g. `make serve HOST=0.0.0.0 PORT=9000 ASSETS=web`)
HOST   ?= 127.0.0.1
PORT   ?= 8080
ASSETS ?= web

# docs dev-server overrides (e.g. `make docs DOCS_HOST=0.0.0.0`)
DOCS_HOST ?= localhost
DOCS_PORT ?= 3000

# Provisioning cache dir (region zips + baked .pmtiles + charts-user + cell cache).
# Mirrors server.DefaultCacheDir(): $XDG_CACHE_HOME/chartplotter, else ~/.cache.
CACHE ?= $(if $(XDG_CACHE_HOME),$(XDG_CACHE_HOME),$(HOME)/.cache)/chartplotter

# OPTIONAL external S-101 PortrayalCatalog override (for iterating on symbology
# rules): pass --s101 <dir> --s101-fc <fc.xml> to serve/bake/emit-assets and both
# the tiles and the emitted client assets use it instead of libtile57's embedded
# catalogue. Defaults to sibling checkouts; override if they live elsewhere. Not
# needed for a normal build — the catalogue lives inside libtile57.
S101_PC    ?= $(HOME)/Projects/s101-portrayal-catalogue/PortrayalCatalog
S101_FC    ?= $(HOME)/Projects/s101-feature-catalogue/S-101FC/FeatureCatalogue.xml

.PHONY: build build-tile57 build-plugins tile57-lib vendor-style-engine xbuild xbuild-tile57 test vet fmt fmt-check tidy clean clear-cache serve docs docs-shots bake-ienc bake-noaa serve-widget demo demo-chart1 serve-demo preslib-chart1 s64-pages

# Prebaked prod test set (US Inland ENC bundle + the NOAA world archive).
# NB: keep these as bare values with NO inline `#` comments — Make folds any
# whitespace before an inline comment into the value, silently appending
# trailing spaces to the path (which then fails to stat).
IENC_SRC     ?= testdata/full/ienc
# ^ dir of per-cell IENC .zip bundles
IENC_PMTILES ?= ienc.pmtiles
# ^ baked IENC archive (project root)
IENC_MAXZOOM ?= 15
# ^ zoom cap (IENC is 1:5000 over a vast area; client overzooms)

# NOAA is baked per US Coast Guard district (https://www.charts.noaa.gov/ENCs/),
# one geographically-disjoint .pmtiles per district — the frontend's MultiArchive
# routes each tile to the one covering archive (web/pmtiles-source.mjs). Each
# district's NNCGD_ENCs.zip is downloaded once into $(NOAA_CACHE) and baked once
# into noaa-d<NN>.pmtiles at the repo root (cached; only re-baked if missing).
NOAA_URL_BASE ?= https://www.charts.noaa.gov/ENCs
# ^ NOAA ENC download host; per-district bundles are <NN>CGD_ENCs.zip
DISTRICTS     ?= 01 05 07 08 09 11 13 14 17
# ^ USCG districts to bake, zero-padded (override e.g. DISTRICTS="05 07" for a fast loop)
NOAA_CACHE    ?= $(CACHE)/noaa
# ^ download cache for the per-district zips (outside the repo; cleared by clear-cache)
NOAA_JOBS     ?= 5
# ^ districts baked CONCURRENTLY (NOAA_JOBS=9 for all at once). Each bake is itself
#   multi-threaded, so peak load ≈ NOAA_JOBS × cores and RAM scales with it too.
# Each district bakes into ONE merged archive (noaa-d<NN>.pmtiles): the
# coverage-clipped composite resolves best-available inside it, so the frontend
# loads one source per district (the per-band archives are retired).
NOAA_ARCHIVES := $(foreach d,$(DISTRICTS),noaa-d$(d).pmtiles)


# --- native libtile57 engine (the SOLE tile/portrayal/asset engine) -------------
# TILE57 points at the engine repo, by default the ./tile57 git submodule
# (github.com/beetlebugorg/tile57, whose nested submodules carry the IHO
# catalogues). A fresh clone materializes it automatically — `build` runs
# `git submodule update --init --recursive` when the source is missing (or clone
# with --recurse-submodules). go.mod's replace targets ./tile57/bindings/go to
# match. Override to build against another checkout, e.g.
# `make TILE57=../tile57-experiment build` (pair it with a gitignored go.work so the
# Go binding follows; see README.md "Developing the engine"). Its static lib is
# built on demand with Zig 0.16.
TILE57     ?= tile57
TILE57_LIB := $(TILE57)/zig-out/lib/libtile57.a

# Engine-commit stamp: the tile57 checkout's HEAD, linked into the binary beside
# main.version so every bake can record WHICH engine produced its tiles (and the
# client can flag a mixed-engine cache). Resolves for the default ./tile57 submodule
# AND a TILE57=… override; "unknown" when git can't answer (submodule not yet
# initialized, tarball checkout). The `test -e .git` guard matters: git -C into a
# missing dir would walk up and report THIS repo's HEAD instead of failing (for the
# submodule .git is a gitdir FILE, for a plain clone a directory — test -e matches
# both, so either resolves cleanly).
ENGINE_COMMIT ?= $(shell test -e "$(TILE57)/.git" && git -C "$(TILE57)" rev-parse --short=9 HEAD 2>/dev/null || echo unknown)
LDFLAGS += -X main.engineCommit=$(ENGINE_COMMIT)

# Materialize the engine source if it isn't there yet. For the default ./tile57
# submodule this fetches it (and its nested IHO catalogues) with one `git submodule
# update --init --recursive`; for a TILE57=<path> override the checkout must already
# exist (we don't guess where an external engine tree should come from).
$(TILE57)/include/tile57.h:
	@if [ "$(TILE57)" = "tile57" ] && [ -f .gitmodules ]; then \
	  echo "fetching the tile57 engine submodule (git submodule update --init --recursive)…"; \
	  git submodule update --init --recursive tile57; \
	else \
	  echo "missing $(TILE57)/include/tile57.h — TILE57=$(TILE57) is not the default submodule; point it at a github.com/beetlebugorg/tile57 checkout"; \
	  exit 1; \
	fi

# Build the static library on demand (only when absent). Needs Zig 0.16 on PATH.
$(TILE57_LIB): $(TILE57)/include/tile57.h
	@command -v zig >/dev/null 2>&1 || { echo "Zig 0.16 not on PATH and $(TILE57_LIB) missing — install Zig or prebuild the lib"; exit 1; }
	@echo "building libtile57.a (zig build in $(TILE57))…"
	cd "$(TILE57)" && zig build

tile57-lib: ## Force-rebuild $(TILE57)/zig-out/lib/libtile57.a (the native engine static lib)
	@command -v zig >/dev/null 2>&1 || { echo "Zig 0.16 not on PATH"; exit 1; }
	cd "$(TILE57)" && zig build

# Build bin/chartplotter. libtile57 is the sole engine, so this is a CGO build that
# statically links the native lib; the S-101 catalogue lives inside libtile57, so
# there is no separate sync/embed step (web/ is still embedded). Fetches the ./tile57
# submodule on demand (see the $(TILE57)/include/tile57.h rule) + needs Zig 0.16.
build: $(TILE57_LIB) ## Build bin/chartplotter (CGO + native libtile57; fetches the ./tile57 submodule on demand + needs Zig 0.16)
	@# Force the link: go's build-cache action ID does NOT hash external static-lib
	@# content, so with an existing up-to-date-looking $(BIN) `go build` silently
	@# skips the relink and a fresh libtile57.a never reaches the output.
	@rm -f $(BIN)
	CGO_ENABLED=1 go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/chartplotter
	@echo "→ $(BIN) (native libtile57 engine)"

# Back-compat alias — libtile57 is now the default engine, so this is just `build`.
build-tile57: build ## Alias for `build` (libtile57 is the sole engine now)

# In-tree reference plugins compiled to Tier-A WASM (wasip1). Pure Go, CGO off, no
# tile57 — builds standalone. Output stays beside each plugin's manifest so the
# directory is directly runnable with `chartplotter plugin dev`.
CORE_PLUGINS := core.nmea0183 core.weather
build-plugins: ## Build the in-tree reference plugins to plugin.wasm (wasip1)
	@for p in $(CORE_PLUGINS); do \
		echo "→ plugins/$$p/plugin.wasm (wasip1)"; \
		GOOS=wasip1 GOARCH=wasm CGO_ENABLED=0 go build -o plugins/$$p/plugin.wasm ./plugins/$$p || exit 1; \
	done

# Quick cross-platform test builds. CGO is off, so this is pure `go build` per
# target — fast cold, near-instant on re-runs thanks to the build cache. Stamps
# the same version as `build`; strips symbols (-s -w) and paths (-trimpath) like a
# release binary. Outputs dist/chartplotter_<os>_<arch>[.exe] (cleaned by `clean`).
# Cross-compile the CGO+libtile57 binary via the Zig C toolchain (`zig cc`) — how
# the tile57-only build keeps single-command cross-compilation despite needing
# CGO. Covers linux + windows (amd64/arm64), all
# proven to cross-link from any host with Zig alone. darwin is built NATIVELY on a
# macOS CI runner: with GOOS=darwin, Go's crypto/x509 links Apple frameworks
# (Security/CoreFoundation) that Zig doesn't bundle. The S-101 catalogue lives in
# libtile57, so there's no embed step. Fetches the ./tile57 submodule on demand + Zig 0.16.
# Outputs dist/chartplotter_<os>_<arch>[.exe].
xbuild xbuild-tile57: $(TILE57)/include/tile57.h ## Cross-compile CGO+libtile57 binaries with zig cc (linux+windows; darwin builds on a Mac runner)
	VERSION="$(VERSION)" TILE57="$(TILE57)" ENGINE_COMMIT="$(ENGINE_COMMIT)" scripts/xbuild-tile57.sh

serve: build ## Serve the web frontend + provisioning API on :8080 (HOST/PORT/ASSETS overridable)
	$(BIN) serve --host $(HOST) --port $(PORT) --assets $(ASSETS)

bake-ienc: build $(IENC_PMTILES) ## Bake every IENC cell in $(IENC_SRC) into $(IENC_PMTILES)

# A standalone large-scale set with no overview cells floats down to the world
# view automatically (the coarsest populated band extends to minzoom 0).
$(IENC_PMTILES): $(BIN)
	$(BIN) bake "$(IENC_SRC)" -o "$(IENC_PMTILES)" --max-zoom $(IENC_MAXZOOM)

# Build the binary first (single-threaded), then bake the districts $(NOAA_JOBS)
# at a time via a recursive parallel sub-make (the district .pmtiles targets are
# independent, so -j fans them out; download + bake of each runs concurrently).
bake-noaa: build ## Bake each USCG district ($(DISTRICTS)) into per-band noaa-d<NN>-<slug>.pmtiles, $(NOAA_JOBS) in parallel
	$(MAKE) -j$(NOAA_JOBS) $(NOAA_ARCHIVES)

# Keep the downloaded district zips — without this Make treats them as
# intermediate (made by one pattern rule, consumed by another) and deletes them
# after baking, forcing a re-download on the next run.
.PRECIOUS: $(NOAA_CACHE)/%CGD_ENCs.zip

# Download a district's NOAA bundle once (cached by its file target).
$(NOAA_CACHE)/%CGD_ENCs.zip:
	@mkdir -p $(NOAA_CACHE)
	@echo "downloading $*CGD_ENCs.zip from NOAA…"
	curl -fSL --retry 3 -o "$@" "$(NOAA_URL_BASE)/$*CGD_ENCs.zip"

# Bake a district bundle into ONE merged archive (the coverage-clipped composite
# resolves best-available inside it; each band also bakes FILLUP_DZ zooms past
# its window). $(BIN) is an order-only prereq so rebuilding the binary doesn't
# force a (very slow) re-bake.
noaa-d%.pmtiles: $(NOAA_CACHE)/%CGD_ENCs.zip | $(BIN)
	$(BIN) bake "$<" -o "$@"

# Serve the per-district NOAA archives + the baked IENC archive TOGETHER,
# prebaked, in read-only widget mode on 0.0.0.0:8080. Every .pmtiles lives at the
# project root; they're symlinked into web/ (the served asset dir) and listed in a
# combined charts-index.json manifest the widget app loads via ?catalog=. Open the
# printed URL.
serve-widget: build bake-noaa ## Serve per-district NOAA + IENC prebaked pmtiles together, read-only widget mode, on 0.0.0.0:8080
	@ln -sf "$(abspath $(IENC_PMTILES))" web/ienc.pmtiles
	@for d in $(DISTRICTS); do \
	  f="noaa-d$$d.pmtiles"; [ -f "$$f" ] && ln -sf "$(abspath .)/$$f" "web/$$f" || true; \
	done
	@{ \
	  printf '{\n  "districts": [\n'; \
	  for d in $(DISTRICTS); do \
	    f="noaa-d$$d.pmtiles"; [ -f "$$f" ] && printf '    { "file": "%s" },\n' "$$f"; \
	  done; \
	  printf '    { "file": "ienc.pmtiles" }\n  ]\n}\n'; \
	} > web/charts-index.json
	@echo
	@echo "  Prebaked widget test server — open:"
	@echo "    http://localhost:8080/?widget&catalog=/charts-index.json"
	@echo "  (binds 0.0.0.0 — reachable from the LAN at http://<this-host>:8080/?widget&catalog=/charts-index.json)"
	@echo
	$(BIN) serve --host 0.0.0.0 --port 8080 --assets web

# ---- read-only demo bundle (the `widget` mode, packaged for static hosting) ----
# A self-contained, no-backend chart viewer over ONE location (Annapolis) with all
# the bands NOAA publishes there, so a visitor can zoom from the whole bay down to
# the docks on a few MB of tiles. `make demo` assembles dist/demo/ (override with
# DEMO_OUT, e.g. DEMO_OUT=docs/static/demo in CI): the per-band .pmtiles + manifest,
# the generated S-101 client assets, and the committed static frontend (demo.html
# as index.html). Serve it from ANY static host / CDN — no server logic required.
DEMO_CELLS   ?= US2EC03M US3EC08M US4MD1DC US4MD1EC US5MD1MC US5MD1MD US5MD1ME US5MD1LB US5MD1LC US5MD1NB US5MD1NC
DEMO_CACHE   ?= $(CACHE)/demo
DEMO_OUT     ?= dist/demo
DEMO_MAXZOOM ?= 16

# The client-side WASM style engine (web/vendor/tile57-style-engine). The server-less
# widget/demo runs the SAME tile57 chartstyle engine CLIENT-side (no /api/style.json),
# so this .wasm must track the engine — a stale one silently drops the SCAMIN scale
# gate and every feature shows at every zoom. Rebuilt from $(TILE57) and re-vendored so
# a committed .wasm can never drift; `demo` depends on it so the shipped bundle always
# carries the current engine's style.
vendor-style-engine: ## Rebuild + re-vendor the WASM style engine (web/vendor/tile57-style-engine) from $(TILE57)
	@echo "building the WASM style engine (zig build wasm in $(TILE57))…"
	cd "$(TILE57)" && zig build wasm
	@cp "$(TILE57)/zig-out/bin/style-engine.wasm" web/vendor/tile57-style-engine/style-engine.wasm
	@cp "$(TILE57)/bindings/js/index.js" "$(TILE57)/bindings/js/index.d.ts" web/vendor/tile57-style-engine/
	@echo "  vendored web/vendor/tile57-style-engine/style-engine.wasm ($$(wc -c < web/vendor/tile57-style-engine/style-engine.wasm) bytes)"

demo: build vendor-style-engine ## Assemble the read-only Annapolis widget demo bundle into $(DEMO_OUT)
	DEMO_CACHE="$(DEMO_CACHE)" DEMO_CELLS="$(DEMO_CELLS)" NOAA_URL_BASE="$(NOAA_URL_BASE)" scripts/fetch-demo-cells.sh
	@mkdir -p "$(DEMO_OUT)"
	$(BIN) bake "$(DEMO_CACHE)" -o "$(DEMO_OUT)/demo.pmtiles" --max-zoom $(DEMO_MAXZOOM) --manifest "$(DEMO_OUT)/charts-index.json"
	$(BIN) emit-assets "$(DEMO_OUT)" $(if $(wildcard $(S101_PC)),--s101 "$(S101_PC)")
	@echo "assembling static frontend → $(DEMO_OUT)"
	@cp web/demo.html "$(DEMO_OUT)/index.html"
	@cp web/manifest.webmanifest web/catalog.json web/icon-192.png web/icon-512.png web/apple-touch-icon.png "$(DEMO_OUT)/"
	@cp -R web/src web/vendor web/glyphs web/basemap "$(DEMO_OUT)/"
	@echo "  demo bundle ready: $(DEMO_OUT)/ — host it on any static server / CDN"

# ---- live "ECDIS Chart 1" tiles for the docs symbol-compliance page ----
# The S-52 PresLib "ECDIS Chart 1" reference sheet, baked to tiles so the docs
# Chart-1 page embeds it LIVE: one <chart-plotter> widget that reuses the demo
# bundle's frontend assets ($(DEMO_OUT)) and points its tile manifest here via
# catalog="…". So this target emits ONLY the tiles + manifest (~1 MB) — no second
# frontend copy. The whole sheet is one contiguous synthetic ENC, so a click in the
# page's test list just setView()s the widget to that panel at its compilation scale.
# Source cells come from the IHO PresLib draft (fetched + cached; see the script).
PRESLIB_CACHE    ?= $(CACHE)/preslib
DEMO_CHART1_OUT  ?= dist/chart1
CHART1_MAXZOOM   ?= 16

demo-chart1: build ## Bake the S-52 ECDIS Chart 1 sheet to tiles for the docs (into $(DEMO_CHART1_OUT))
	PRESLIB_CACHE="$(PRESLIB_CACHE)" scripts/fetch-preslib-cells.sh
	@mkdir -p "$(DEMO_CHART1_OUT)"
	$(BIN) bake "$(PRESLIB_CACHE)/cells" -o "$(DEMO_CHART1_OUT)/chart1.pmtiles" --max-zoom $(CHART1_MAXZOOM) --manifest "$(DEMO_CHART1_OUT)/charts-index.json"
	@echo "  chart1 tiles ready: $(DEMO_CHART1_OUT)/ — served beside the demo bundle as /chart1/"

# LOCAL PREVIEW ONLY. The bundle is pure static files — deploy it to ANY
# range-capable static host (GitHub Pages, S3/CloudFront, nginx, `npx serve`); it
# needs no backend. PMTiles are read with HTTP Range, which python's http.server
# does NOT support, so we preview with the chartplotter binary acting purely as a
# range-capable static file server (the widget page makes no /api calls).
serve-demo: demo ## Preview the static demo bundle locally (range-capable static serve; HOST/PORT overridable)
	@echo "  Read-only widget demo — open: http://$(HOST):$(PORT)/"
	$(BIN) serve --host $(HOST) --port $(PORT) --assets "$(DEMO_OUT)"

docs: ## Run the documentation site dev server (Docusaurus; DOCS_HOST/DOCS_PORT overridable)
	cd docs && { [ -d node_modules ] || npm install; } && npm start -- --host $(DOCS_HOST) --port $(DOCS_PORT)

# Render the S-52 PresLib "ECDIS Chart 1" panels (one PNG per reference-plot page)
# with our implementation, for visual diffing against the spec's reference plots
# (PresLib e4.0.0 Part I §16). Self-contained: extracts the cells, bakes+serves via
# the import path, screenshots each panel, tears down. Needs the PresLib zip in
# testdata/ + a headless Chromium. Output → testdata/preslib-chart1-out/ (gitignored).
preslib-chart1: ## Render PresLib "ECDIS Chart 1" panels for spec comparison (one PNG per reference page)
	scripts/preslib-chart1.sh

# Render the IHO S-64 ENC test dataset's rendering pages (one PNG per test section)
# for diffing against the S-64 reference plots. Same self-contained flow as
# preslib-chart1, but the S-64 tests vary the mariner settings per page (§3.1 renders
# Base/Standard/Other). Needs the S-64 zip in testdata/ + a headless Chromium.
# Output → testdata/s64-pages-out/ (gitignored).
s64-pages: ## Render S-64 ENC test pages for spec comparison (one PNG per test section)
	scripts/s64-pages.sh

# Regenerate the documentation UI screenshots (docs/static/img/ui/*.png) from the
# live app, so they stay in sync when the UI changes. Needs baked charts in the
# S-101 cache (e.g. after `make serve` has imported a region); Chromium +
# playwright-core must be available. Starts a throwaway server, captures, stops it.
DOCS_SHOTS_PORT ?= 8199
docs-shots: build ## Regenerate docs UI screenshots from the live app into docs/static/img/ui/
	@set -e; \
	$(BIN) serve --host 127.0.0.1 --port $(DOCS_SHOTS_PORT) --assets web & \
	srv=$$!; trap "kill $$srv 2>/dev/null || true" EXIT; \
	for i in $$(seq 1 50); do \
	  curl -fsS "http://127.0.0.1:$(DOCS_SHOTS_PORT)/api/health" >/dev/null 2>&1 && break; \
	  sleep 0.2; \
	done; \
	node scripts/docs-shots.mjs "http://127.0.0.1:$(DOCS_SHOTS_PORT)"; \
	if command -v magick >/dev/null 2>&1; then \
	  for s in annapolis cape-lookout; do \
	    [ -f docs/static/img/ui/$$s.png ] && magick docs/static/img/ui/$$s.png -resize 50% docs/static/img/ui/$$s.png && echo "downscaled $$s.png for GitHub (→ 800x600)"; \
	  done; \
	fi

test:
	go test ./...

vet:
	go vet ./...

# Format with the gofmt of the toolchain go.mod pins (Go 1.26), NOT whatever
# gofmt happens to be on PATH — gofmt's rules change between Go minor releases,
# so a stray 1.25 gofmt reintroduces drift that the 1.26 CI check rejects. Scope
# to THIS repo's tracked *.go via `git ls-files` (not `.`, which walks into the
# ./tile57 engine submodule — chartplotter-go doesn't own its formatting).
fmt:
	@"$$(go env GOROOT)/bin/gofmt" -w $$(git ls-files '*.go')

# Mirror the CI gofmt gate exactly, using the same toolchain gofmt as `fmt`.
fmt-check:
	@GOFMT="$$(go env GOROOT)/bin/gofmt"; \
	  out="$$($$GOFMT -l $$(git ls-files '*.go'))"; \
	  test -z "$$out" || { echo "needs gofmt:"; echo "$$out"; exit 1; }

tidy:
	go mod tidy

clean:
	rm -rf bin dist

clear-cache: ## Delete the provisioning cache (region zips, baked .pmtiles, charts-user, cell cache) for a clean slate
	rm -rf "$(CACHE)"
	@echo "cleared chartplotter cache: $(CACHE)"
