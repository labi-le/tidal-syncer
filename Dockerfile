# syntax=docker/dockerfile:1
#
# tidal-syncer — multi-stage, distroless (static), nonroot, bundled static ffmpeg.
# amd64-only by design for the MVP (plan §H); arm64 is intentionally absent.

# ─────────────────────────────────────────────────────────────────────────────
# Stage 1 — builder: compile a fully static (CGO_ENABLED=0) tidal-syncer binary.
# The module is pure Go (modernc.org/sqlite, taglib-via-wazero), so no cgo / libc
# is required — which is what lets the final image be distroless/static.
# ─────────────────────────────────────────────────────────────────────────────
FROM golang:1.26 AS builder

WORKDIR /src

# Resolve modules first so this layer stays cached until go.mod/go.sum change.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build metadata injected into github.com/labi-le/tidal-syncer/internal.* —
# mirrors the Makefile ldflags contract. Pass real values at build time with:
#   docker build \
#     --build-arg VERSION="$(git describe --tags --always)" \
#     --build-arg COMMIT_HASH="$(git rev-parse --short HEAD)" \
#     --build-arg BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)" .
ARG VERSION=dev
ARG COMMIT_HASH=unknown
ARG BUILD_TIME=unknown

# CGO_ENABLED=0 + -extldflags '-static' → a self-contained, libc-free binary.
ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64
RUN go build \
        -trimpath \
        -ldflags="-s -w -extldflags '-static' \
            -X 'github.com/labi-le/tidal-syncer/internal.Version=${VERSION}' \
            -X 'github.com/labi-le/tidal-syncer/internal.CommitHash=${COMMIT_HASH}' \
            -X 'github.com/labi-le/tidal-syncer/internal.BuildTime=${BUILD_TIME}'" \
        -o /out/tidal-syncer \
        ./cmd

# Pre-create the runtime volume mountpoints so anonymous/named volumes inherit
# nonroot (65532) ownership; the distroless final stage has no shell to mkdir.
RUN mkdir -p /app/Music /app/data

# ─────────────────────────────────────────────────────────────────────────────
# Stage 2 — ffmpeg: fetch a PINNED, checksum-verified static ffmpeg (amd64).
# Source: johnvansickle.com static builds, licensed GPL-3.0 — GPLv3.txt is copied
# into the final image alongside the binary. The build FAILS on checksum
# mismatch. The DASH downloader execs this binary; its path is injected via the
# TIDAL_FFMPEG env (set in the final stage). amd64-only (MVP, plan §H).
# Verified static: `ldd ffmpeg` → "not a dynamic executable" (mandatory for the
# glibc-free distroless/static base).
# ─────────────────────────────────────────────────────────────────────────────
FROM debian:bookworm-slim AS ffmpeg

ARG FFMPEG_VERSION=6.0.1
ARG FFMPEG_SHA256=28268bf402f1083833ea269331587f60a242848880073be8016501d864bd07a5
ARG FFMPEG_URL=https://johnvansickle.com/ffmpeg/old-releases/ffmpeg-${FFMPEG_VERSION}-amd64-static.tar.xz

RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl xz-utils \
 && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL -o /tmp/ffmpeg.tar.xz "${FFMPEG_URL}" \
 && printf '%s  /tmp/ffmpeg.tar.xz\n' "${FFMPEG_SHA256}" | sha256sum -c - \
 && mkdir -p /opt/ffmpeg \
 && tar -xJf /tmp/ffmpeg.tar.xz -C /opt/ffmpeg --strip-components=1 \
 && /opt/ffmpeg/ffmpeg -version \
 && rm -f /tmp/ffmpeg.tar.xz

# ─────────────────────────────────────────────────────────────────────────────
# Stage 3 — final: distroless static, nonroot (UID 65532), no shell / no pkg mgr.
# ─────────────────────────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot

LABEL org.opencontainers.image.title="tidal-syncer" \
      org.opencontainers.image.description="Sync your TIDAL library to local FLAC with full metadata." \
      org.opencontainers.image.source="https://github.com/labi-le/tidal-syncer" \
      org.opencontainers.image.licenses="MIT"

# Application binary + bundled static ffmpeg, both at stable absolute paths.
COPY --from=builder /out/tidal-syncer    /usr/local/bin/tidal-syncer
COPY --from=ffmpeg  /opt/ffmpeg/ffmpeg    /usr/local/bin/ffmpeg
COPY --from=ffmpeg  /opt/ffmpeg/GPLv3.txt /usr/local/share/ffmpeg/GPLv3.txt

# Runtime volume mountpoints, owned by the nonroot UID.
COPY --from=builder --chown=65532:65532 /app /app

# The DASH downloader resolves ffmpeg from this env (os/exec); keep it in sync
# with the COPY path above.
ENV TIDAL_FFMPEG=/usr/local/bin/ffmpeg

# Default runtime paths (override via --config and bind mounts):
#   /app/config.yaml — config FILE, bind-mount read-only. NOT declared a VOLUME:
#                      it is a file, and a VOLUME would shadow it with an empty dir.
#   /app/Music       — output FLAC library.
#   /app/data        — SQLite cache + cross-process lock.
# Host bind dirs must be writable by UID 65532.
VOLUME ["/app/Music", "/app/data"]

# Already nonroot in the base; restated numerically for an unambiguous UID:GID.
USER 65532:65532

ENTRYPOINT ["/usr/local/bin/tidal-syncer"]
HEALTHCHECK --interval=5m --timeout=10s --start-period=20s \
    CMD ["/usr/local/bin/tidal-syncer", "health"]
