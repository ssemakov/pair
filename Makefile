BINARY := pair
PREFIX ?= ~

.PHONY: all build install clean test fmt vet tidy

all: build

build:
	go build -o $(BINARY) .

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
