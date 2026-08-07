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

.phony: run build clean tests test-race lint docker-build docker-run up down ps logs login sync retry-failed health nix-build nix-test nix-vendor-hash

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

# --- Nix packaging (deploy/package.nix + the NixOS service module) -----------
# Override to pin a different nixpkgs, e.g.
#   make nix-test NIXPKGS='(builtins.getFlake "/etc/nixos").inputs.nixpkgs'
NIXPKGS ?= <nixpkgs>
NIX_EXPR_PKGS = let pkgs = import $(NIXPKGS) { }; in

nix-build:
	nix build --impure --no-link --print-out-paths \
		--expr '$(NIX_EXPR_PKGS) pkgs.callPackage ./deploy/package.nix { }'

# Package-level, pure-eval and NixOS VM tests. The VM test needs /dev/kvm.
# filterAttrs: callPackage splices `override`/`overrideDerivation` lambdas into
# any attrset it returns, and linkFarmFromDrvs chokes on them.
nix-test:
	nix build --impure --no-link \
		--expr '$(NIX_EXPR_PKGS) pkgs.linkFarmFromDrvs "tidal-syncer-nix-tests" (builtins.attrValues (pkgs.lib.filterAttrs (_: pkgs.lib.isDerivation) (pkgs.callPackage ./deploy/tests { })))'

# Any go.mod/go.sum change invalidates vendorHash, and the failure surfaces only
# when someone rebuilds the package (loudly, with the expected value). This
# prints the correct hash to paste into deploy/package.nix.
#
# The fakeHash build is EXPECTED to fail with a mismatch, so a run without a
# `got:` line means something unrelated broke (eval error, no network, $(NIXPKGS)
# unresolvable) -- surface it and fail instead of reporting success.
nix-vendor-hash:
	@out=$$(nix build --impure --no-link \
		--expr '$(NIX_EXPR_PKGS) (pkgs.callPackage ./deploy/package.nix { }).overrideAttrs (_: { vendorHash = pkgs.lib.fakeHash; })' 2>&1 || true); \
	printf '%s\n' "$$out" | grep -E 'got: +sha256-' \
		|| { printf '%s\n' "$$out"; echo "nix-vendor-hash: no hash mismatch reported - the build failed for another reason"; exit 1; }