VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)
BIN := bin/chartplotter

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

.PHONY: build wasm test vet fmt tidy clean clear-cache serve docs dist bake-ienc serve-prod

# Prebaked prod test set (US Inland ENC bundle + the NOAA world archive).
IENC_SRC     ?= testdata/full/ienc          # dir of per-cell IENC .zip bundles
IENC_PMTILES ?= ienc.pmtiles                # baked IENC archive (project root)
IENC_MAXZOOM ?= 15                          # cap (IENC is 1:5000 over a vast area; client overzooms)
NOAA_PMTILES ?= $(firstword $(wildcard noaa-enc-*.pmtiles) noaa.pmtiles)

build: $(ASSETS)/chartplotter.wasm ## Build the self-contained shim (embeds web/ + wasm) into bin/
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/chartplotter

# go:embed needs the wasm present; build it if it's missing.
$(ASSETS)/chartplotter.wasm:
	$(MAKE) wasm

wasm: ## Build the real-time tile-baker wasm (stock go). (tinygo was dropped — it mis-parsed some foreign S-57 cells.)
	GOOS=js GOARCH=wasm go build -o $(ASSETS)/chartplotter.wasm ./cmd/chartplotter-wasm
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" $(ASSETS)/vendor/wasm_exec.js
	@echo "built $(ASSETS)/chartplotter.wasm ($$(du -h $(ASSETS)/chartplotter.wasm | cut -f1)) via go"

serve: build ## Serve the web frontend + provisioning API (HOST/PORT/ASSETS overridable)
	$(BIN) serve --host $(HOST) --port $(PORT) --assets $(ASSETS)

dist: ## Build a static prebaked "prod" site into dist/ for GitHub Pages (set PMTILES_URL or CATALOG_URL)
	scripts/build-pages.sh

bake-ienc: build $(IENC_PMTILES) ## Bake every IENC cell in $(IENC_SRC) into $(IENC_PMTILES)

# --overzoom: a standalone large-scale set with no overview cells, so it must
# overzoom down to stay visible when zoomed out (mirrors the realtime upload path).
$(IENC_PMTILES): $(BIN)
	$(BIN) bake "$(IENC_SRC)" -o "$(IENC_PMTILES)" --overzoom --max-zoom $(IENC_MAXZOOM)

# Serve the NOAA world archive + the baked IENC archive TOGETHER, prebaked, in
# production mode on 0.0.0.0:8080. Both .pmtiles live at the project root; they're
# symlinked into web/ (the served asset dir) and listed in a combined
# charts-index.json manifest the prod app loads via ?catalog=. Open the printed URL.
serve-prod: build bake-ienc ## Serve NOAA + IENC prebaked pmtiles together, prod mode, on 0.0.0.0:8080
	@test -n "$(NOAA_PMTILES)" && test -f "$(NOAA_PMTILES)" || { echo "NOAA pmtiles not found (set NOAA_PMTILES=…)"; exit 1; }
	@ln -sf "$(abspath $(NOAA_PMTILES))" web/noaa.pmtiles
	@ln -sf "$(abspath $(IENC_PMTILES))" web/ienc.pmtiles
	@printf '{\n  "districts": [\n    { "file": "noaa.pmtiles", "band": "all" },\n    { "file": "ienc.pmtiles", "band": "all" }\n  ]\n}\n' > web/charts-index.json
	@echo
	@echo "  Prebaked prod test server — open:"
	@echo "    http://localhost:8080/?prod&catalog=/charts-index.json"
	@echo "  (binds 0.0.0.0 — reachable from the LAN at http://<this-host>:8080/?prod&catalog=/charts-index.json)"
	@echo
	$(BIN) serve --host 0.0.0.0 --port 8080 --assets web

docs: ## Run the documentation site dev server (Docusaurus; DOCS_HOST/DOCS_PORT overridable)
	cd docs && { [ -d node_modules ] || npm install; } && npm start -- --host $(DOCS_HOST) --port $(DOCS_PORT)

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

tidy:
	go mod tidy

clean:
	rm -rf bin dist

clear-cache: ## Delete the provisioning cache (region zips, baked .pmtiles, charts-user, cell cache) for a clean slate
	rm -rf "$(CACHE)"
	@echo "cleared chartplotter cache: $(CACHE)"
