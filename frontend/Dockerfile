# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Aegis UI production image — Vite build served by Caddy.
# Build:   docker build -t QAdversif/AegisPanel-ui:dev -f frontend/Dockerfile .
# Run:     docker run --rm -p 8080:8080 QAdversif/AegisPanel-ui:dev

# ---- Build stage ---------------------------------------------------------
FROM node:20-alpine AS build

WORKDIR /src

# Cache dependencies.
COPY frontend/package.json frontend/package-lock.json* frontend/pnpm-lock.yaml* ./
RUN if [ -f pnpm-lock.yaml ]; then \
        corepack enable && corepack prepare pnpm@latest --activate && pnpm install --frozen-lockfile; \
    elif [ -f package-lock.json ]; then npm ci; \
    else npm install; fi

# Build the bundle.
COPY frontend/ ./
ARG VITE_API_BASE=/api
ENV VITE_API_BASE=${VITE_API_BASE}
RUN npm run build

# ---- Runtime stage -------------------------------------------------------
FROM caddy:2-alpine

# Pull in the latest alpine package updates — `caddy:2-alpine` tracks
# an Alpine base image and the pinned `c-ares` package picks up
# CVE-2026-33630 (use-after-free) via this upgrade. The Go stdlib CVEs
# flagged by trivy (CVE-2026-27145 / 39822 / 42504) are baked into the
# upstream caddy binary itself and are not fixable here — see
# `.trivyignore` for the documented baseline.
RUN apk upgrade --no-cache

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
