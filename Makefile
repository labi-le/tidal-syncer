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

.phony: run build clean tests test-race lint docker-build docker-run up down ps logs login sync retry-failed health nix-build nix-test nix-vendor-hash nix-vendor-hash-print nix-vendor-hash-check

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
# A Nixpkgs "specifier" evaluated as a VALUE, not a path, because NIX_EXPR_PKGS
# splices it into an expression. A channel or directory (<nixpkgs>, /etc/nixos)
# imports directly; a flake ref must first be reduced to a package set -- its
# `.outPath` -- because `import` of a flake would evaluate its flake.nix. Set
# NIXPKGS_URL for a flake ref and NIXPKGS for a channel/path.
#
#   make nix-build NIXPKGS_URL=https://github.com/NixOS/nixpkgs/archive/<rev>.tar.gz
#   make nix-test NIXPKGS='(builtins.getFlake "/etc/nixos").inputs.nixpkgs'
#
# The default is the rev of nixpkgs the consuming NixOS host (labi-le/nixos)
# actually builds this package with. The go-modules FOD hash is a function of
# nixpkgs' `go mod download` implementation and the Go toolchain it selects, so
# pinning it keeps a hash valid in CI identical to one valid on the host: an
# unpinned <nixpkgs> makes the check drift as the channel moves, which turns it
# into a source of false failures rather than false passes. Bump NIXPKGS_REV
# deliberately, in its own commit, after re-running `make nix-vendor-hash`.
NIXPKGS_REV ?= 643809054d65fdd466a63e3155b8c498cb483c04
NIXPKGS_URL ?= github:NixOS/nixpkgs/$(NIXPKGS_REV)

# A flake ref reduced to its nixpkgs package set. `getFlake` resolves and locks
# `NIXPKGS_URL`, and its `.outPath` is the source tree -- which for any
# nixpkgs revision is directly importable as the package set. Going through
# `.inputs.nixpkgs` would be wrong: nixpkgs' own flake.nix declares `nixpkgs =
# self` as an OUTPUT rather than receiving itself as an input, so that attribute
# does not exist.
NIXPKGS_PINNED = (builtins.getFlake "$(NIXPKGS_URL)").outPath
NIXPKGS ?= <nixpkgs>
NIX_EXPR_PKGS = let pkgs = import $(NIXPKGS) { }; in

# The pinned nixpkgs, for anything that must agree with the consuming host: the
# vendorHash gate in .github/workflows/nix.yml and a deliberate local re-check.
NIX_EXPR_PINNED = let pkgs = import $(NIXPKGS_PINNED) { }; in

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

# The value alone on stdout, for scripted consumers: the `heal` job in
# .github/workflows/nix.yml reads it to patch deploy/package.nix. Same pinned
# nixpkgs and same failure discipline as the other two targets.
nix-vendor-hash-print:
	@out=$$(nix build --impure --no-link \
		--expr '$(NIX_EXPR_PINNED) (pkgs.callPackage ./deploy/package.nix { }).overrideAttrs (_: { vendorHash = pkgs.lib.fakeHash; })' 2>&1 || true); \
	hash=$$(printf '%s\n' "$$out" | grep -oE 'got: +sha256-[A-Za-z0-9+/]+=*' | head -1 | grep -oE 'sha256-[A-Za-z0-9+/]+=*'); \
	if [ -z "$$hash" ]; then \
		printf '%s\n' "$$out"; echo "nix-vendor-hash-print: no hash mismatch reported - the build failed for another reason" >&2; exit 1; \
	fi; \
	printf '%s\n' "$$hash"

# The CI gate (see .github/workflows/nix.yml). Exits 0 when the vendorHash in
# deploy/package.nix matches what this go.mod/go.sum actually resolves to, and
# non-zero when it does not, so a stale hash can never reach main.
#
# It builds the go-modules FOD only: the build product of the outer derivation is
# irrelevant to the hash and taking the whole build would cost the Go compile
# plus the test suite. The FOD is not an attribute of the derivation --
# buildGoModule wires it as a plain let binding -- so it can only be exercised
# by building, hence the stub buildPhase. Expect well under a minute.
#
# Pinned nixpkgs rather than $(NIXPKGS): the hash is a function of nixpkgs'
# `go mod download` and the Go toolchain it picks, so the check must run against
# the same revision the consuming NixOS host uses or it drifts into false alarms.
nix-vendor-hash-check:
	@out=$$(nix build --impure --no-link --print-out-paths \
		--expr '$(NIX_EXPR_PINNED) (pkgs.callPackage ./deploy/package.nix { }).overrideAttrs (old: { doCheck = false; buildPhase = "true"; postInstall = ""; dontFixup = true; })' 2>&1) || true; \
	if printf '%s\n' "$$out" | grep -qE 'got: +sha256-'; then \
		echo "vendorHash in deploy/package.nix is stale for these go.mod/go.sum:" >&2; \
		printf '%s\n' "$$out" | grep -E 'specified:|got: +sha256-' | sed 's/^ */  /' >&2; \
		echo "paste the 'got:' value into deploy/package.nix (or run 'make nix-vendor-hash')" >&2; \
		exit 1; \
	fi; \
	if printf '%s\n' "$$out" | grep -qE '^/nix/store/'; then \
		exit 0; \
	fi; \
	printf '%s\n' "$$out" >&2; \
	echo "nix-vendor-hash-check: no verdict - the build failed for another reason (see above)" >&2; exit 1