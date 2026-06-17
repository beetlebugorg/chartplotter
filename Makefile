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

.PHONY: build test vet fmt tidy clean clear-cache serve docs

build: ## Build the chartplotter binary into bin/
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/chartplotter

serve: build ## Serve the web frontend + provisioning API (HOST/PORT/ASSETS overridable)
	$(BIN) serve --host $(HOST) --port $(PORT) --assets $(ASSETS)

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
