BINARY := pair
PREFIX ?= ~
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: all build install clean test fmt vet tidy

all: build

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

install: build
	install -m 0755 $(BINARY) $(PREFIX)/bin/$(BINARY)

clean:
	rm -f $(BINARY)

test:
	go test ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

tidy:
	go mod tidy
