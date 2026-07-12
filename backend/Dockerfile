# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Aegis panel — multi-stage Docker build.
# Produces a static binary on distroless base for a tiny attack surface.
#
# Build:   docker build -t aegispanel/aegis:dev -f backend/Dockerfile .
# Run:     docker run --rm -p 8080:8080 --env-file .env aegispanel/aegis:dev

# ---- Build stage ---------------------------------------------------------
FROM golang:1.22-alpine AS build

WORKDIR /src

# Cache dependencies separately.
COPY backend/go.mod backend/go.sum* ./
RUN go mod download

# Copy the rest of the source.
COPY backend/ ./

# Build a static binary.
ARG VERSION=dev
ARG GIT_COMMIT=unknown
ARG BUILD_TIME=unknown

RUN CGO_ENABLED=0 GOOS=linux go build \
        -trimpath \
        -ldflags "-s -w \
            -X github.com/aegispanel/aegis/internal/config.GitCommit=${GIT_COMMIT} \
            -X github.com/aegispanel/aegis/internal/config.BuildTime=${BUILD_TIME}" \
        -o /out/aegis \
        ./cmd/aegis

# ---- Runtime stage -------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app
COPY --from=build /out/aegis /app/aegis

ENV AEGIS_ENV=production \
    AEGIS_HTTP_ADDR=:8080

EXPOSE 8080

USER nonroot:nonroot

ENTRYPOINT ["/app/aegis"]
