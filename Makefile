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

.PHONY: build wasm wasm-go test vet fmt tidy clean clear-cache serve docs dist

build: $(ASSETS)/chartplotter.wasm ## Build the self-contained shim (embeds web/ + wasm) into bin/
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/chartplotter

# go:embed needs the wasm present; build it (via tinygo) if it's missing.
$(ASSETS)/chartplotter.wasm:
	$(MAKE) wasm

wasm: ## [experiment] Build the real-time tile-baker wasm via tinygo (needs tinygo + go 1.25, see mise.toml)
	tinygo build -o $(ASSETS)/chartplotter.wasm -target=wasm -no-debug ./cmd/chartplotter-wasm
	cp "$$(tinygo env TINYGOROOT)/targets/wasm_exec.js" $(ASSETS)/vendor/wasm_exec.js
	@echo "built $(ASSETS)/chartplotter.wasm ($$(du -h $(ASSETS)/chartplotter.wasm | cut -f1)) via tinygo"

wasm-go: ## [experiment] Build the tile-baker wasm via stock go (larger; fallback if tinygo unavailable)
	GOOS=js GOARCH=wasm go build -o $(ASSETS)/chartplotter.wasm ./cmd/chartplotter-wasm
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" $(ASSETS)/vendor/wasm_exec.js
	@echo "built $(ASSETS)/chartplotter.wasm ($$(du -h $(ASSETS)/chartplotter.wasm | cut -f1)) via go"

serve: build ## Serve the web frontend + provisioning API (HOST/PORT/ASSETS overridable)
	$(BIN) serve --host $(HOST) --port $(PORT) --assets $(ASSETS)

dist: ## Build a static prebaked "prod" site into dist/ for GitHub Pages (set PMTILES_URL or CATALOG_URL)
	scripts/build-pages.sh

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
