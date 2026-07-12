# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Aegis UI production image — Vite build served by Caddy.
# Build:   docker build -t aegispanel/aegis-ui:dev -f frontend/Dockerfile .
# Run:     docker run --rm -p 8080:8080 aegispanel/aegis-ui:dev

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
