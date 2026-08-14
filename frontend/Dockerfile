# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Aegis UI production image — Vite build served by a custom Caddy binary.
# Build:   docker build -t QAdversif/AegisPanel-ui:dev -f frontend/Dockerfile .
# Run:     docker run --rm -p 8080:8080 QAdversif/AegisPanel-ui:dev
#
# # Why we rebuild Caddy here
#
# The Caddy binary shipped in `caddy:2.11.4-alpine` (and every other
# `caddy:<v>-alpine` tag at the time of writing, 2026-07-25) is compiled
# against `google.golang.org/grpc v1.81.0`. That version carries
# `GHSA-hrxh-6v49-42gf` (xDS RBAC and HTTP/2 Rapid Reset, CVSS HIGH,
# disclosed 2026-07-21). Caddy upstream has not yet bumped the
# `google.golang.org/grpc` indirect dependency in `cmd/caddy` (verified
# against `master` on 2026-07-25), so we rebuild Caddy from source with
# a `replace` directive pinning `google.golang.org/grpc` to the fixed
# version (`v1.82.1+`).
#
# # How to update Caddy later
#
#   - Bump `CADDY_VERSION` to the upstream tag (e.g. `2.11.5`).
#   - If upstream has not yet incorporated a patched grpc-go, leave
#     `GRPCGO_PIN` at a current patched version (e.g. `1.82.1`).
#   - If upstream has picked up a patched grpc-go natively, set
#     `GRPCGO_PIN=` (empty) to opt out of the `replace` directive.
#   - Push, let CI rebuild and re-scan. No source changes required.
#
# # Why a static build (`CGO_ENABLED=0`)
#
# Statically links against Go's net stack so the binary works on any
# scratch/Alpine runtime, matching what `caddy:<v>-alpine` ships. Cuts
# ~5 MiB versus the dynamic build by avoiding `libc` and `libresolv`.

# ---- Build-time args (override at build with --build-arg) ----------------
ARG CADDY_VERSION=2.11.4
ARG GRPCGO_PIN=1.82.1

# ---- Stage 1: SPA bundle -------------------------------------------------
FROM node:20-alpine AS build

WORKDIR /src

# Cache dependencies.
COPY frontend/package.json frontend/package-lock.json* ./
RUN if [ -f package-lock.json ]; then npm ci; \
    else npm install; fi

# Build the bundle.
COPY frontend/ ./
ARG VITE_API_BASE=/api
ENV VITE_API_BASE=${VITE_API_BASE}
RUN npm run build

# ---- Stage 2: custom Caddy binary ---------------------------------------
# golang:1.26-alpine is required because Caddy v2.11.4 is built with
# Go 1.25.1+ (per the upstream `go.mod`), and 1.26 is the LTS that
# also matches the project's backend toolchain pin.
FROM golang:1.26.6-alpine AS caddy-build

ARG CADDY_VERSION
ARG GRPCGO_PIN

# git for the shallow Caddy clone; ca-certificates for `go mod` fetching
# from proxy.golang.org behind TLS.
RUN apk add --no-cache git ca-certificates

WORKDIR /src

# Shallow clone of Caddy at the pinned upstream tag.
RUN git clone --depth 1 --branch v${CADDY_VERSION} \
        https://github.com/caddyserver/caddy.git .

# Apply the grpc-go `replace` directive if `GRPCGO_PIN` is set.
# `go mod edit -replace` inserts the directive; the following
# `go mod tidy` updates `go.sum` and the indirect entry to match.
# When `GRPCGO_PIN` is empty (because upstream Caddy has incorporated
# a patched grpc-go on its own), this whole block is skipped and
# we build against the upstream-pinned version verbatim.
#
# Syntax note: the replace is written as
# `old=old@v` (self-version-pin), not `old=@v` or `old=v` —
# `go mod edit` rejects the latter two with
# `unversioned new path must be local directory` because it
# reads the bare `v1.82.1` as a filesystem path. Repeating
# the module path on the right side with `@v` makes the
# version explicit.
RUN if [ -n "${GRPCGO_PIN}" ]; then \
        go mod edit -replace=google.golang.org/grpc=google.golang.org/grpc@v${GRPCGO_PIN} && \
        go mod tidy; \
    fi

# Cache go mod downloads separately so subsequent builds are fast.
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Static build. No CGO, stripped of debug info, no filesystem paths
# baked in (`-trimpath`) for reproducible builds across CI runners.
# We do NOT add non-standard plugins — the Aegis Caddyfile only uses
# core directives (try_files, encode, header, reverse_proxy), all of
# which are included in the default Caddy build.
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 \
    go build \
        -trimpath \
        -ldflags="-s -w" \
        -o /out/caddy \
        ./cmd/caddy/

# ---- Stage 3: runtime image ---------------------------------------------
FROM caddy:${CADDY_VERSION}-alpine

# Pull in the latest Alpine package updates. `caddy:<v>-alpine` tracks
# an Alpine base image and the pinned `c-ares` package picks up
# CVE-2026-33630 (use-after-free) via this upgrade. The Go stdlib CVEs
# tracked in `.trivyignore` (CVE-2026-27145 / 39822 / 42504) remain
# in the Caddy binary until Caddy upstream rebuilds with a Go
# toolchain >= 1.26.4 — see the next section in `.trivyignore` for
# the documented re-evaluation plan. The grpc-go CVE
# (GHSA-hrxh-6v49-42gf) is no longer reported after this PR because
# our custom binary uses the patched grpc-go.
RUN apk upgrade --no-cache

# Replace the upstream Caddy binary with our custom build.
COPY --from=caddy-build /out/caddy /usr/bin/caddy

# Copy the static bundle.
COPY --from=build /src/dist /srv

# Minimal Caddyfile that serves the SPA and proxies /api to the panel.
COPY <<'EOF' /etc/caddy/Caddyfile
{
    auto_https off
    admin off
}

:8080 {
    root * /srv
    encode gzip zstd
    try_files {path} /index.html
    header {
        X-Content-Type-Options "nosniff"
        X-Frame-Options "DENY"
        Referrer-Policy "strict-origin-when-cross-origin"
    }
    reverse_proxy /api/* {$BACKEND_ADDR:http://localhost:8080}
}
EOF

EXPOSE 8080
