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
LDFLAGS=-ldflags="-X '${FULL_PACKAGE}/internal/buildinfo.Version=${VERSION}' \
                  -X '${FULL_PACKAGE}/internal/buildinfo.CommitHash=${COMMIT_HASH}' \
                  -X '${FULL_PACKAGE}/internal/buildinfo.BuildTime=${BUILD_TIMESTAMP}' \
                  -s -w \
                  -extldflags '-static'"

DOCKER_IMAGE ?= tidal-syncer:local

# Run compose containers as the host user so ./Music and ./data are owned by you (not 65532).
export PUID ?= $(shell id -u)
export PGID ?= $(shell id -g)

.phony: run build clean tests test-race lint docker-build docker-run up down ps logs login sync retry-failed health

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
	docker compose build --build-arg COMMIT_HASH=$(COMMIT_HASH) --build-arg BUILD_TIME=$(BUILD_TIMESTAMP)

docker-run:
	docker run --rm -it $(DOCKER_IMAGE)

up:
	docker compose up -d

down:
	docker compose down

ps:
	docker compose ps

logs:
	docker compose logs -f

login:
	docker compose run --rm tidal-syncer login

sync:
	docker compose run --rm tidal-syncer sync

retry-failed:
	docker compose run --rm tidal-syncer sync --retry-failed

health:
	docker compose run --rm tidal-syncer health
