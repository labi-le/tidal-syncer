{ pkgs ? import <nixpkgs> { } }:

let
  init-template = pkgs.writeShellScriptBin "init-template" ''
    set -euo pipefail

    DIR_NAME=$(basename "$PWD")
    REMOTE=$(git remote get-url origin 2>/dev/null || echo "")

    DEFAULT_OWNER=""
    DEFAULT_NAME="$DIR_NAME"
    # Parse "https://github.com/owner/repo(.git)" or "git@github.com:owner/repo(.git)"
    if [[ "$REMOTE" =~ github\.com[:/]([^/]+)/([^/.]+)(\.git)?$ ]]; then
      DEFAULT_OWNER="''${BASH_REMATCH[1]}"
      DEFAULT_NAME="''${BASH_REMATCH[2]}"
    fi
    DEFAULT_GOOS="linux,darwin,windows"

    # Maintainer: git config user.name + user.email, fallback to $OWNER (set later)
    GIT_USER=$(git config user.name 2>/dev/null || echo "")
    GIT_EMAIL=$(git config user.email 2>/dev/null || echo "")
    if [[ -n "$GIT_USER" && -n "$GIT_EMAIL" ]]; then
      DEFAULT_MAINTAINER="$GIT_USER <$GIT_EMAIL>"
    elif [[ -n "$GIT_USER" ]]; then
      DEFAULT_MAINTAINER="$GIT_USER"
    else
      DEFAULT_MAINTAINER=""
    fi

    # Description: first non-empty non-heading line after H1 in README, skip placeholder
    DEFAULT_DESC=""
    if [[ -f README.md ]]; then
      DEFAULT_DESC=$(awk '
        /^# / { h1=1; next }
        h1 && NF && !/^#/ { print; exit }
      ' README.md)
      if [[ "$DEFAULT_DESC" == "project description" ]]; then
        DEFAULT_DESC=""
      fi
    fi

    # License: SPDX detection from LICENSE-like file, fallback MIT
    DEFAULT_LICENSE="MIT"
    for lf in LICENSE LICENSE.md LICENSE.txt COPYING COPYING.md; do
      [[ -f "$lf" ]] || continue
      HL=$(head -1 "$lf" | tr '[:lower:]' '[:upper:]')
      case "$HL" in
        *MIT*)                       DEFAULT_LICENSE="MIT"; break ;;
        *APACHE*)                    DEFAULT_LICENSE="Apache-2.0"; break ;;
        *"GNU GENERAL PUBLIC"*3*|*GPL*3*) DEFAULT_LICENSE="GPL-3.0"; break ;;
        *"GNU GENERAL PUBLIC"*2*|*GPL*2*) DEFAULT_LICENSE="GPL-2.0"; break ;;
        *"GNU LESSER"*3*|*LGPL*3*)   DEFAULT_LICENSE="LGPL-3.0"; break ;;
        *"GNU LESSER"*2*|*LGPL*2*)   DEFAULT_LICENSE="LGPL-2.1"; break ;;
        *BSD*3*)                     DEFAULT_LICENSE="BSD-3-Clause"; break ;;
        *BSD*2*)                     DEFAULT_LICENSE="BSD-2-Clause"; break ;;
        *MPL*2*)                     DEFAULT_LICENSE="MPL-2.0"; break ;;
        *ISC*)                       DEFAULT_LICENSE="ISC"; break ;;
        *UNLICENSE*)                 DEFAULT_LICENSE="Unlicense"; break ;;
      esac
    done

    echo "Template bootstrap. Press Enter to accept defaults."
    echo

    read -r -p "Project name [$DEFAULT_NAME]: " NAME
    NAME="''${NAME:-$DEFAULT_NAME}"

    if [[ -z "$DEFAULT_OWNER" ]]; then
      read -r -p "GitHub owner: " OWNER
    else
      read -r -p "GitHub owner [$DEFAULT_OWNER]: " OWNER
      OWNER="''${OWNER:-$DEFAULT_OWNER}"
    fi
    if [[ -z "$OWNER" ]]; then
      echo "ERROR: owner required (no git remote and no input)" >&2
      exit 1
    fi

    read -r -p "Build OSes (comma-separated) [$DEFAULT_GOOS]: " GOOS_INPUT
    GOOS_INPUT="''${GOOS_INPUT:-$DEFAULT_GOOS}"

    # Maintainer fallback to OWNER if git config empty
    [[ -z "$DEFAULT_MAINTAINER" ]] && DEFAULT_MAINTAINER="$OWNER"
    read -r -p "Maintainer [$DEFAULT_MAINTAINER]: " MAINTAINER
    MAINTAINER="''${MAINTAINER:-$DEFAULT_MAINTAINER}"

    if [[ -n "$DEFAULT_DESC" ]]; then
      read -r -p "Description [$DEFAULT_DESC]: " DESCRIPTION
      DESCRIPTION="''${DESCRIPTION:-$DEFAULT_DESC}"
    else
      read -r -p "Description: " DESCRIPTION
    fi
    [[ -z "$DESCRIPTION" ]] && DESCRIPTION="TODO: project description"

    read -r -p "License (SPDX) [$DEFAULT_LICENSE]: " LICENSE
    LICENSE="''${LICENSE:-$DEFAULT_LICENSE}"

    GOOS_JSON=$(echo "$GOOS_INPUT" \
      | tr ',' '\n' \
      | sed 's/[[:space:]]//g' \
      | grep -v '^$' \
      | jq -Rs 'split("\n") | map(select(length > 0))')

    cat <<EOF

Applying:
  Project name : $NAME
  Owner        : $OWNER
  Homepage     : https://github.com/$OWNER/$NAME
  Builds       : $GOOS_INPUT
  Maintainer   : $MAINTAINER
  Description  : $DESCRIPTION
  License      : $LICENSE

EOF

    # JSON-escape free-form strings (handles quotes/specials safely)
    MAINTAINER_JSON=$(printf '%s' "$MAINTAINER" | jq -Rs .)
    DESC_JSON=$(printf '%s' "$DESCRIPTION" | jq -Rs .)
    LICENSE_JSON=$(printf '%s' "$LICENSE" | jq -Rs .)

    yq -i ".nfpms[0].homepage = \"https://github.com/$OWNER/$NAME\"" .goreleaser.yaml
    yq -i ".nfpms[0].maintainer = $MAINTAINER_JSON" .goreleaser.yaml
    yq -i ".nfpms[0].description = $DESC_JSON" .goreleaser.yaml
    yq -i ".nfpms[0].license = $LICENSE_JSON" .goreleaser.yaml
    yq -i ".builds[0].goos = $GOOS_JSON" .goreleaser.yaml

    if [[ "$NAME" != "$DIR_NAME" ]]; then
      yq -i ".project_name = \"$NAME\"" .goreleaser.yaml
      echo "Note: project name ($NAME) != directory name ($DIR_NAME)."
      echo "      Pinned project_name in .goreleaser.yaml."
      echo "      Makefile PACKAGE still derives from \$(notdir \$(CURDIR)) — adjust if needed."
    fi

    # Drop GIT_REPOSITORY* env pass-through (no longer templated after hardcode)
    if [[ -f .github/workflows/releaser.yaml ]]; then
      yq -i '
        (.jobs.goreleaser.steps[]
          | select(.uses // "" | test("goreleaser-action"))
          | .env) |=
            with_entries(select(.key | test("^GIT_REPOSITORY") | not))
      ' .github/workflows/releaser.yaml
    fi

    if [[ -f README.md ]]; then
      sed -i "s/project-name/$NAME/g" README.md
    fi

    echo
    echo "Done. Review with: git diff"
  '';
in
pkgs.mkShell {
  packages = with pkgs; [
    go_1_26
    goreleaser
    golangci-lint
    ffmpeg
    yq-go
    jq
    sqlite-interactive
    git
    init-template
  ];

  shellHook = ''
    echo "Dev shell ready."
    echo "Run 'init-template' to personalize this template for your repo."
  '';
}
