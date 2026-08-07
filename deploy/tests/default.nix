# Every Nix-level test for the packaging and the NixOS service module, flattened
# into one attrset of derivations so a caller can build them all at once:
#
#   make nix-test
#
# or a single one by name:
#
#   nix build --impure --no-link --expr \
#     'let pkgs = import <nixpkgs> { }; in (pkgs.callPackage ./deploy/tests { }).eval'
#
# Three layers, cheapest first:
#   package-*  derivation tests: binary name, TIDAL_FFMPEG wrapper, real
#              `selfcheck` run, no libtag/libstdc++ linkage.
#   eval       pure module evaluation: rendered config.yaml, computed
#              ProtectHome, RequiresMountsFor, StateDirectory-vs-tmpfiles, the
#              dashboard fold-in and every assertion.
#   module     NixOS VM test: the units actually start, the secret is injected
#              into a RuntimeDirectory and stays out of the store, /metrics
#              answers on loopback, Prometheus scrapes it. Needs /dev/kvm.
#
# The Go test suite is not repeated here: deploy/package.nix leaves `doCheck` on,
# so all 14 packages (including the taglib/wazero WASM path) run at build time.
{
  lib,
  callPackage,
  tidal-syncer ? callPackage ../package.nix { },
}:

let
  # callPackage splices `override`/`overrideDerivation` into an attrset result,
  # which breaks anything doing attrValues over it.
  packageTests = lib.filterAttrs (_: lib.isDerivation) (
    callPackage ./package.nix { inherit tidal-syncer; }
  );
in
lib.filterAttrs (_: lib.isDerivation) (
  lib.mapAttrs' (name: lib.nameValuePair "package-${name}") packageTests
  // {
    eval = callPackage ./eval.nix { };
    module = callPackage ./module.nix { };
  }
)
