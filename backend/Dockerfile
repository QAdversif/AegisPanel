# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Aegis panel — multi-stage Docker build.
#
# v0.8.15: switched from distroless/static to
# distroless/base + a tooling stage so the panel
# can exec `pg_dump` and `aegis-agent` on the
# local filesystem at runtime. The distroless
# `static` variant has no dynamic linker, so
# forking those binaries fails with ENOENT for
# the loader (`/lib64/ld-linux-x86-64.so.2`).
#
# Build:   docker build -t QAdversif/AegisPanel:dev -f backend/Dockerfile .
# Run:     docker run --rm -p 8080:8080 --env-file .env QAdversif/AegisPanel:dev

# ---- Tooling stage ------------------------------------------------------
# Builds a `pg_dump` binary + the shared objects
# it links against, in a regular Debian image
# (the `static` distroless variant does not
# contain any pre-built clients or libraries).
# We copy the artifacts into the runtime image
# so the panel can exec `pg_dump` from inside
# the distroless container.
#
# v0.8.16 fix: install `postgresql-client-15`
# (the versioned metapackage that matches
# Debian 12 "bookworm" — `postgresql-client-16`
# does NOT exist on bookworm; that package name
# only exists on Debian 13 "trixie" or on the
# PGDG apt repo). The bare `postgresql-client`
# metapackage installs `postgresql-common`
# which symlinks `/usr/bin/pg_dump` to the
# `pg_wrapper` shell script — and `pg_wrapper`
# requires a shell. The distroless `base` image
# ships `busybox` (so shell scripts run in
# principle), but in practice the path through
# `postgresql-common`'s wrapper resolves to
# `dpkg-divert`-protected symlinks that the
# distroless image does not have. The versioned
# `postgresql-client-15` ships the actual
# `pg_dump` ELF binary at
# `/usr/lib/postgresql/15/bin/pg_dump` — copy
# that path directly.
FROM debian:12-slim AS tooling

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        postgresql-client-15 \
        ca-certificates && \
    rm -rf /var/lib/apt/lists/*

# ---- Build stage --------------------------------------------------------
FROM golang:1.26-alpine AS build

WORKDIR /src

# Cache dependencies separately.
COPY backend/go.mod backend/go.sum* ./
RUN go mod download

# Copy the rest of the source.
COPY backend/ ./

# Build a static binary for the panel itself.
# The panel binary stays static (CGO_ENABLED=0)
# because it has no exec() needs of its own.
ARG VERSION=dev
ARG GIT_COMMIT=unknown
ARG BUILD_TIME=unknown

RUN CGO_ENABLED=0 GOOS=linux go build \
        -trimpath \
        -ldflags "-s -w \
            -X github.com/QAdversif/AegisPanel/internal/config.GitCommit=${GIT_COMMIT} \
            -X github.com/QAdversif/AegisPanel/internal/config.BuildTime=${BUILD_TIME}" \
        -o /out/aegis \
        ./cmd/aegis

# v0.8.15: also build the per-node agent binary
# in the same stage. The bootstrap installer
# (`internal/bootstrap/installer.go`) reads
# `${AEGIS_AGENT_BINARY}` (default `./bin/aegis-agent`,
# resolved against WORKDIR=/app) and SFTPs it
# to the node. If the binary is missing, the
# install path fails at `os.Stat(in.AgentSource)`
# with "no such file or directory" — surfaced
# as a 502 Bad Gateway on POST /api/v1/nodes/{id}/provision.
#
# The agent is built with CGO_ENABLED=0 + the
# `-extldflags=-static` flag from the agent
# Dockerfile, so the distroless node runtime
# can load it without a dynamic linker.
RUN CGO_ENABLED=0 go build \
        -trimpath \
        -ldflags "-s -w -extldflags=-static -X main.version=${VERSION}" \
        -o /out/aegis-agent \
        ./cmd/aegis-agent

# ---- Runtime stage -------------------------------------------------------
# v0.8.15: switched from `distroless/static` to
# `distroless/base`. The base variant ships
# glibc + the dynamic linker + busybox shell,
# which lets the panel fork `pg_dump` and the
# agent binary. The CVE surface is still
# minimal (no apt, no package manager, no
# shell on $PATH for the nonroot user), and
# the savings vs. debian:12-slim are real.
FROM gcr.io/distroless/base-debian12:nonroot

WORKDIR /app

# Panel binary.
COPY --from=build /out/aegis /app/aegis

# v0.8.15: agent binary. The installer reads
# it from ${AEGIS_AGENT_BINARY} which defaults
# to `./bin/aegis-agent` (resolved against
# /app in the runtime image).
COPY --from=build /out/aegis-agent /app/bin/aegis-agent

# v0.8.15: pg_dump + its dynamic dependencies,
# copied from the `debian:12-slim` tooling
# stage. We copy the entire /usr/lib/x86_64-linux-gnu
# directory rather than enumerating each .so —
# the size cost is ~50 MB and the correctness
# benefit is that any future postgresql-client
# upgrade that adds a new .so dependency Just
# Works without touching this Dockerfile.
#
# v0.8.16 fix: copy the actual `pg_dump` binary
# from `/usr/lib/postgresql/15/bin/pg_dump`
# (the versioned path, installed by
# `postgresql-client-15` on Debian 12 "bookworm").
# The `/usr/bin/pg_dump` symlink to `pg_wrapper` is
# not used because the wrapper requires a shell
# + dpkg-divert that's not in the distroless
# runtime. **Replace** `/usr/bin/pg_dump` with a
# symlink to the real binary, so
# `exec.CommandContext("pg_dump", ...)` from the
# backup service (which uses the bare path
# `/usr/bin/pg_dump`) still finds the expected
# path. The symlink step is a separate `RUN` that
# runs AFTER the `COPY` of the real binary, so
# the symlink can point to a target inside the
# image.
RUN ln -sf /usr/lib/postgresql/15/bin/pg_dump /usr/bin/pg_dump
COPY --from=tooling /usr/lib/postgresql/15/bin/pg_dump /usr/lib/postgresql/15/bin/pg_dump
COPY --from=tooling /usr/lib/x86_64-linux-gnu /usr/lib/x86_64-linux-gnu

# CA bundle for the `ca-certificates` shipped
# in the tooling image (libpq + the agent both
# rely on it for TLS to the panel's Postgres
# + the agent's outbound HTTPS for the
# subscription-update path).
COPY --from=tooling /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

ENV AEGIS_ENV=production \
    AEGIS_HTTP_ADDR=:8080 \
    AEGIS_AGENT_BINARY=/app/bin/aegis-agent

EXPOSE 8080

USER nonroot:nonroot

ENTRYPOINT ["/app/aegis"]
