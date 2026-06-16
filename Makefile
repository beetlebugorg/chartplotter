VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)
BIN := bin/chartplotter

.PHONY: build test vet fmt tidy clean

build: ## Build the chartplotter binary into bin/
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/chartplotter

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
