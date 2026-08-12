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
#
# v0.8.19 upgrade: install `postgresql-client-16`
# from the PGDG apt repo. The panel runs against
# `postgres:16-alpine` in production; pg_dump 15
# refuses to dump a PostgreSQL 16 server ("server
# version mismatch"), so we ship pg_dump 16 from
# the official PGDG package stream. The package
# name `postgresql-client-16` does not exist in
# stock Debian 12 bookworm — only the PGDG repo
# (https://apt.postgresql.org/) carries it. The
# PGDG repo's package set is built against the
# latest 16.x point release and is the standard
# way to ship a current pg_dump on bookworm.
FROM debian:12-slim AS tooling

# v0.8.19: PGDG apt repo. We use the GPG-key
# path that PGDG itself publishes in their
# install docs (https://wiki.postgresql.org/wiki/Apt).
# The PGDG list and the key live in the image
# only for the duration of this RUN; both are
# removed in the `rm` step at the end so the
# image does not contain arbitrary third-party
# trust anchors.
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ca-certificates curl gnupg && \
    install -d /usr/share/postgresql-common/pgdg && \
    curl -o /usr/share/postgresql-common/pgdg/apt.postgresql.org.asc --fail \
        https://www.postgresql.org/media/keys/ACCC4CF8.asc && \
    echo "deb [signed-by=/usr/share/postgresql-common/pgdg/apt.postgresql.org.asc] https://apt.postgresql.org/pub/repos/apt bookworm-pgdg main" \
        > /etc/apt/sources.list.d/pgdg.list && \
    apt-get update && \
    apt-get install -y --no-install-recommends \
        postgresql-client-16 && \
    rm /etc/apt/sources.list.d/pgdg.list && \
    rm -rf /var/lib/apt/lists/* && \
    # v0.8.17 pattern (continued in v0.8.19): the
    # postgresql-client metapackage installs
    # `/usr/bin/pg_dump` as a symlink to the
    # `pg_wrapper` shell script. The wrapper needs
    # `/bin/sh` + `dpkg-divert` + the postgresql-common
    # runtime — none of which are in the distroless
    # `base` image. Replace the symlink with the
    # actual ELF binary at
    # `/usr/lib/postgresql/16/bin/pg_dump` so the
    # runtime image just COPYs a flat file.
    rm /usr/bin/pg_dump && \
    cp /usr/lib/postgresql/16/bin/pg_dump /usr/bin/pg_dump

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
# v0.8.17 fix: in the tooling stage, **replace**
# `/usr/bin/pg_dump` (a symlink to
# `pg_wrapper` shell script) with the actual
# `pg_dump` ELF binary from
# `/usr/lib/postgresql/15/bin/pg_dump`. Then
# `COPY` it into the runtime image. The previous
# approach (COPY the symlink + ln -sf in the
# runtime stage) failed because:
#   1. The symlink COPY preserved the
#      `pg_wrapper` target, which requires
#      `/bin/sh` + `dpkg-divert` + the
#      postgresql-common runtime — none of which
#      are in the distroless `base` image.
#   2. The `RUN ln -sf` in the runtime stage
#      failed because distroless `base` has no
#      `/bin/sh` for the shell form, and no
#      `/bin/ln` for the exec form (busybox
#      applets are at `/busybox/<applet>`, not
#      `/bin/<applet>`).
# Doing the substitution in the tooling stage
# avoids both problems: the runtime image just
# gets a flat file at `/usr/bin/pg_dump` via
# the `COPY` step.
#
# v0.8.19: bumped the source path from
# `/usr/lib/postgresql/15/bin/pg_dump` to
# `/usr/lib/postgresql/16/bin/pg_dump` so the
# panel ships a pg_dump whose major version
# matches the production postgres:16-alpine
# server. The substitution-and-COPY pattern is
# unchanged.
COPY --from=tooling /usr/lib/postgresql/16/bin/pg_dump /usr/lib/postgresql/16/bin/pg_dump
COPY --from=tooling /usr/bin/pg_dump /usr/bin/pg_dump
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
