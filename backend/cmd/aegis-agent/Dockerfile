# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Aegis Agent — per-node binary. The image is
# designed for the bootstrap install path: the
# panel's `internal/bootstrap` package uploads the
# resulting static binary to the node and installs
# it as a systemd unit.
#
# Why distroless (not Alpine, not debian-slim):
# - The agent reads the bearer secret from
#   /etc/aegis/agent.env and serves four HTTP
#   endpoints. It has no shell, no package
#   manager, no apt cache. The smaller the
#   surface, the smaller the CVE blast radius.
# - The static-binary build (`-extldflags=-static`)
#   means the agent has zero dynamic linker
#   dependencies on the host — runs on every
#   Linux distro without glibc-vs-musl worries.
#
# Why two stages:
# - The build stage needs Go 1.26+; the runtime
#   stage is just the distroless static base.
#   The build stage is throw-away; the published
#   image is < 20 MB.

# ---- Build stage ---------------------------------------------------------

FROM golang:1.26-alpine AS build

WORKDIR /src

# Cache the module downloads. go.mod / go.sum
# rarely change between rebuilds; the source
# changes often. Copying them first lets the
# Docker layer cache skip `go mod download`
# on most rebuilds.
COPY backend/go.mod backend/go.sum* ./
RUN go mod download

# Copy the rest of the source.
COPY backend/ ./

# Build a fully static binary. The `-extldflags=-static`
# forces the C toolchain to do a static link
# (otherwise `cgo` would default to dynamic glibc
# on the Alpine builder, which is incompatible
# with the distroless runtime).
ARG VERSION=dev
ENV CGO_ENABLED=0
RUN go build \
        -trimpath \
        -ldflags "-s -w -X main.version=${VERSION}" \
        -o /out/aegis-agent \
        ./cmd/aegis-agent

# ---- Runtime stage -------------------------------------------------------

# distroless static-debian12:nonroot has no shell,
# no apt, no busybox. The agent's PID 1 is the Go
# binary itself — the HTTP server takes over
# signal handling.
FROM gcr.io/distroless/static-debian12:nonroot

# The agent reads AEGIS_AGENT_LISTEN_ADDR from
# the environment; the default `:8080` lives in
# the binary. The bootstrap install also writes
# /etc/aegis/agent.env at runtime; we don't mount
# it here (the install path does).
ENV AEGIS_AGENT_LISTEN_ADDR=:8080

COPY --from=build /out/aegis-agent /usr/local/bin/aegis-agent

EXPOSE 8080

USER nonroot:nonroot

ENTRYPOINT ["/usr/local/bin/aegis-agent"]
