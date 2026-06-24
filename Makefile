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

# S-101 portrayal for `make serve` (transitional, until the catalogue is embedded).
# The catalogue + feature catalogue are vendored as siblings of the repo (not
# committed — IHO DRAFT licence unconfirmed); override the paths if they live
# elsewhere. Baked tiles carry their portrayal, so S-101 uses its OWN cache dir
# (a subdir of $(CACHE), still wiped by clear-cache) to avoid mixing with any
# S-52 tiles; the SOURCE ENC dir (--data) is portrayal-agnostic and stays shared.
S101_PC    ?= $(HOME)/Projects/s101-portrayal-catalogue/PortrayalCatalog
S101_FC    ?= $(HOME)/Projects/s101-feature-catalogue/S-101FC/FeatureCatalogue.xml
S101_CACHE ?= $(CACHE)/s101

.PHONY: build xbuild test vet fmt fmt-check tidy clean clear-cache serve docs docs-shots bake-ienc bake-noaa serve-prod

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
# Each district bakes into one gap-clipped archive PER navigational band
# (noaa-d<NN>-<slug>.pmtiles) so the frontend reproduces the realtime best-
# available display (coarse bands fill finer gaps, none bleed). The bake writes
# several files, so Make tracks each district by a stamp.
NOAA_BANDS  := overview general coastal approach harbor berthing
NOAA_STAMPS := $(foreach d,$(DISTRICTS),noaa-d$(d).stamp)

S101_EMBED_DIR := internal/engine/s101catalog/catalog

# Copy the external S-101 catalogue into the (gitignored) embed dir so a
# `-tags embed_s101` build bakes it into the binary. Files never enter the repo.
sync-s101: ## Sync the external S-101 PortrayalCatalog + FeatureCatalogue into the embed dir
	@rm -rf "$(S101_EMBED_DIR)"
	@mkdir -p "$(S101_EMBED_DIR)/PortrayalCatalog"
	@cp -a "$(S101_PC)/." "$(S101_EMBED_DIR)/PortrayalCatalog/"
	@cp -a "$(S101_FC)" "$(S101_EMBED_DIR)/FeatureCatalogue.xml"
	@echo "synced S-101 catalogue → $(S101_EMBED_DIR)"

# Embed the S-101 catalogue when it's available locally (the normal dev/deploy
# case); otherwise build without it (the binary then needs --s101 at runtime).
build: ## Build the self-contained shim (embeds web/ + S-101 catalogue) into bin/
	@if [ -d "$(S101_PC)" ] && [ -f "$(S101_FC)" ]; then \
	  $(MAKE) --no-print-directory sync-s101; \
	  echo "building with embedded S-101 catalogue (-tags embed_s101)…"; \
	  go build -tags embed_s101 -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/chartplotter; \
	else \
	  echo "S-101 catalogue not found at $(S101_PC); building WITHOUT it (needs --s101 at runtime)"; \
	  go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/chartplotter; \
	fi

# Quick cross-platform test builds. CGO is off, so this is pure `go build` per
# target — fast cold, near-instant on re-runs thanks to the build cache. Stamps
# the same version as `build`; strips symbols (-s -w) and paths (-trimpath) like a
# release binary. Outputs dist/chartplotter_<os>_<arch>[.exe] (cleaned by `clean`).
xbuild: ## Cross-compile per platform — both a plain binary (needs --s101) and a self-contained _s101 one (embedded catalogue), into dist/
	@mkdir -p dist
	@embed=""; \
	if [ -d "$(S101_PC)" ] && [ -f "$(S101_FC)" ]; then $(MAKE) --no-print-directory sync-s101; embed=1; \
	else echo "no S-101 catalogue ($(S101_PC)) — building only the plain (--s101 at runtime) binaries"; fi; \
	for p in $(PLATFORMS); do \
	  os=$${p%/*}; arch=$${p#*/}; ext=; [ "$$os" = windows ] && ext=.exe; \
	  echo "building $$os/$$arch (plain, needs --s101)…"; \
	  CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "-s -w $(LDFLAGS)" \
	    -o "dist/chartplotter_$${os}_$${arch}$$ext" ./cmd/chartplotter || exit 1; \
	  if [ -n "$$embed" ]; then \
	    echo "building $$os/$$arch (self-contained, embedded catalogue)…"; \
	    CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -tags embed_s101 -trimpath -ldflags "-s -w $(LDFLAGS)" \
	      -o "dist/chartplotter_$${os}_$${arch}_s101$$ext" ./cmd/chartplotter || exit 1; \
	  fi; \
	done
	@echo "→ dist/"; ls -1 dist/chartplotter_*

serve: build ## Serve the web frontend + provisioning API, S-101 portrayal (HOST/PORT/ASSETS/S101_* overridable)
	$(BIN) serve --host $(HOST) --port $(PORT) --assets $(ASSETS) \
	  --s101 $(S101_PC) --s101-fc $(S101_FC) --cache $(S101_CACHE)

bake-ienc: build $(IENC_PMTILES) ## Bake every IENC cell in $(IENC_SRC) into $(IENC_PMTILES)

# --overzoom: a standalone large-scale set with no overview cells, so it must
# overzoom down to stay visible when zoomed out (mirrors the realtime upload path).
$(IENC_PMTILES): $(BIN)
	$(BIN) bake "$(IENC_SRC)" -o "$(IENC_PMTILES)" --overzoom --max-zoom $(IENC_MAXZOOM)

# Build the binary first (single-threaded), then bake the districts $(NOAA_JOBS)
# at a time via a recursive parallel sub-make (the district .pmtiles targets are
# independent, so -j fans them out; download + bake of each runs concurrently).
bake-noaa: build ## Bake each USCG district ($(DISTRICTS)) into per-band noaa-d<NN>-<slug>.pmtiles, $(NOAA_JOBS) in parallel
	$(MAKE) -j$(NOAA_JOBS) $(NOAA_STAMPS)

# Keep the downloaded district zips — without this Make treats them as
# intermediate (made by one pattern rule, consumed by another) and deletes them
# after baking, forcing a re-download on the next run.
.PRECIOUS: $(NOAA_CACHE)/%CGD_ENCs.zip

# Download a district's NOAA bundle once (cached by its file target).
$(NOAA_CACHE)/%CGD_ENCs.zip:
	@mkdir -p $(NOAA_CACHE)
	@echo "downloading $*CGD_ENCs.zip from NOAA…"
	curl -fSL --retry 3 -o "$@" "$(NOAA_URL_BASE)/$*CGD_ENCs.zip"

# Bake a district bundle into per-band gap-clipped archives (--bands writes
# noaa-d<NN>-<slug>.pmtiles for each band present). NO --overzoom: a district
# bundle carries its own overview/general cells, so the zoomed-out skeleton is
# already present. $(BIN) is an order-only prereq so rebuilding the binary doesn't
# force a (very slow) re-bake. Stamped because the bake produces several files.
noaa-d%.stamp: $(NOAA_CACHE)/%CGD_ENCs.zip | $(BIN)
	$(BIN) bake "$<" -o "noaa-d$*.pmtiles" --bands
	@touch "$@"

# Serve the per-district NOAA archives + the baked IENC archive TOGETHER,
# prebaked, in production mode on 0.0.0.0:8080. Every .pmtiles lives at the project
# root; they're symlinked into web/ (the served asset dir) and listed in a combined
# charts-index.json manifest the prod app loads via ?catalog=. Open the printed URL.
serve-prod: build bake-noaa ## Serve per-district per-band NOAA + IENC prebaked pmtiles together, prod mode, on 0.0.0.0:8080
	@ln -sf "$(abspath $(IENC_PMTILES))" web/ienc.pmtiles
	@for d in $(DISTRICTS); do for s in $(NOAA_BANDS); do \
	  f="noaa-d$$d-$$s.pmtiles"; [ -f "$$f" ] && ln -sf "$(abspath .)/$$f" "web/$$f" || true; \
	done; done
	@{ \
	  printf '{\n  "districts": [\n'; \
	  for d in $(DISTRICTS); do for s in $(NOAA_BANDS); do \
	    f="noaa-d$$d-$$s.pmtiles"; [ -f "$$f" ] && printf '    { "file": "%s", "band": "%s" },\n' "$$f" "$$s"; \
	  done; done; \
	  printf '    { "file": "ienc.pmtiles", "band": "all" }\n  ]\n}\n'; \
	} > web/charts-index.json
	@echo
	@echo "  Prebaked prod test server — open:"
	@echo "    http://localhost:8080/?prod&catalog=/charts-index.json"
	@echo "  (binds 0.0.0.0 — reachable from the LAN at http://<this-host>:8080/?prod&catalog=/charts-index.json)"
	@echo
	$(BIN) serve --host 0.0.0.0 --port 8080 --assets web

docs: ## Run the documentation site dev server (Docusaurus; DOCS_HOST/DOCS_PORT overridable)
	cd docs && { [ -d node_modules ] || npm install; } && npm start -- --host $(DOCS_HOST) --port $(DOCS_PORT)

# Regenerate the documentation UI screenshots (docs/static/img/ui/*.png) from the
# live app, so they stay in sync when the UI changes. Needs baked charts in the
# S-101 cache (e.g. after `make serve` has imported a region); Chromium +
# playwright-core must be available. Starts a throwaway server, captures, stops it.
DOCS_SHOTS_PORT ?= 8199
docs-shots: build ## Regenerate docs UI screenshots from the live app into docs/static/img/ui/
	@set -e; \
	$(BIN) serve --host 127.0.0.1 --port $(DOCS_SHOTS_PORT) --assets web \
	  --s101 $(S101_PC) --s101-fc $(S101_FC) --cache $(S101_CACHE) & \
	srv=$$!; trap "kill $$srv 2>/dev/null || true" EXIT; \
	for i in $$(seq 1 50); do \
	  curl -fsS "http://127.0.0.1:$(DOCS_SHOTS_PORT)/api/health" >/dev/null 2>&1 && break; \
	  sleep 0.2; \
	done; \
	node scripts/docs-shots.mjs "http://127.0.0.1:$(DOCS_SHOTS_PORT)"; \
	if command -v magick >/dev/null 2>&1; then \
	  magick docs/static/img/ui/annapolis.png -resize 50% docs/static/img/ui/annapolis.png; \
	  echo "downscaled annapolis.png for GitHub (→ 800x600)"; \
	fi

test:
	go test ./...

vet:
	go vet ./...

# Format with the gofmt of the toolchain go.mod pins (Go 1.26), NOT whatever
# gofmt happens to be on PATH — gofmt's rules change between Go minor releases,
# so a stray 1.25 gofmt reintroduces drift that the 1.26 CI check rejects. Invoke
# gofmt over `.` (not `go fmt ./...`, which skips files behind build tags like
# embed_s101) so the file set matches the CI `gofmt -l .` gate exactly.
fmt:
	@"$$(go env GOROOT)/bin/gofmt" -w .

# Mirror the CI gofmt gate exactly, using the same toolchain gofmt as `fmt`.
fmt-check:
	@GOFMT="$$(go env GOROOT)/bin/gofmt"; \
	  out="$$($$GOFMT -l .)"; \
	  test -z "$$out" || { echo "needs gofmt:"; echo "$$out"; exit 1; }

tidy:
	go mod tidy

clean:
	rm -rf bin dist

clear-cache: ## Delete the provisioning cache (region zips, baked .pmtiles, charts-user, cell cache) for a clean slate
	rm -rf "$(CACHE)"
	@echo "cleared chartplotter cache: $(CACHE)"
