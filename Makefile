BINARY := gmem-cli
PREFIX ?= $(HOME)/.local

.PHONY: build install test vet clean

build:
	go build -o $(BINARY) ./cmd/gmem-cli

install: build
	install -m 0755 $(BINARY) $(PREFIX)/bin/$(BINARY)

test:
	go test ./pkg/gmem/

vet:
	go vet ./...

clean:
	rm -f $(BINARY)
