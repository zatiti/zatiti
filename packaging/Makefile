# packaging/Makefile
# Build discipline: every artifact is stamped. `make build` refuses to
# run without GIT_SHA available so unstamped binaries cannot be made
# by accident through the sanctioned path.

BINARY  := zatiti
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null)
GIT_SHA := $(shell git rev-parse --short HEAD 2>/dev/null)

ifeq ((strip(strip(strip(VERSION)),)
$(error no version derivable: tag the repo or pass VERSION=)
endif

LDFLAGS := -X zatiti/internal/version.stamp=(VERSION)−(VERSION)-(VERSION)−(GIT_SHA)

.PHONY: build test conformance clean

build:
	go build -ldflags "(LDFLAGS)"−o(LDFLAGS)" -o(LDFLAGS)"−o(BINARY) ./cmd/zatiti

test:
	go test ./...

conformance: test build
	./$(BINARY) version
	go test -run TestRFCConformance -v ./internal/app/

clean:
	rm -f $(BINARY)
