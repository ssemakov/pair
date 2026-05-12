BINARY := pair
BINDIR := bin
BIN := $(BINDIR)/$(BINARY)
PREFIX ?= ~
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: all build install clean test fmt vet tidy

all: build

build:
	@mkdir -p $(BINDIR)
	go build -ldflags "$(LDFLAGS)" -o $(BIN) .

install: build
	install -m 0755 $(BIN) $(PREFIX)/bin/$(BINARY)

clean:
	rm -rf $(BINDIR)

test:
	go test ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

tidy:
	go mod tidy
