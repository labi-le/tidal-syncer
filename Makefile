PACKAGE = $(notdir $(CURDIR))

MAIN_PATH = ./cmd
BUILD_PATH = build/package/

INSTALL_PATH = /usr/bin/
CGO_ENABLED=0

FULL_PATH = $(BUILD_PATH)$(PACKAGE)

VERSION=$(shell git describe --tags --always --abbrev=0 --match='v[0-9]*.[0-9]*.[0-9]*' 2>/dev/null | sed 's/^.//')
COMMIT_HASH=$(shell git rev-parse --short HEAD)
BUILD_TIMESTAMP=$(shell date '+%Y-%m-%dT%H:%M:%S')

FULL_PACKAGE=$(shell go list -m)
LDFLAGS=-ldflags="-X '${FULL_PACKAGE}/internal.Version=${VERSION}' \
                  -X '${FULL_PACKAGE}/internal.CommitHash=${COMMIT_HASH}' \
                  -X '${FULL_PACKAGE}/internal.BuildTime=${BUILD_TIMESTAMP}' \
                  -s -w \
                  -extldflags '-static'"

DOCKER_IMAGE ?= tidal-syncer:dev

.phony: run build clean tests test-race lint docker-build docker-run compose-up logs

run:
	go run $(MAIN_PATH)

build: clean
	go build $(LDFLAGS) -v -o $(BUILD_PATH)$(PACKAGE) $(MAIN_PATH)

clean:
	rm -rf $(FULL_PATH)

tests:
	go test ./...

test-race:
	CGO_ENABLED=1 go test -race ./...

lint:
	golangci-lint run

docker-build:
	docker build -t $(DOCKER_IMAGE) .

docker-run:
	docker run --rm -it $(DOCKER_IMAGE)

compose-up:
	docker compose up -d

logs:
	docker compose logs -f
