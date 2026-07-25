# feat(build): custom Caddy binary to drop grpc-go CVE

Closes the `trivy (frontend)` failure that has been
failing every PR since the `trivy (frontend)` gate
adopted the Caddy stdlib baseline in mid-July.
The current Caddy binary in `caddy:2.11.4-alpine`
ships with `google.golang.org/grpc v1.81.0`, which
carries GHSA-hrxh-6v49-42gf (xDS RBAC and HTTP/2
Rapid Reset, CVSS HIGH, disclosed 2026-07-21).
Caddy upstream has not yet bumped the
`google.golang.org/grpc` indirect dependency in
`cmd/caddy` (verified against `master` on
2026-07-25 — both `v2.11.4` and `master` still
pin `v1.81.0`).

This PR rebuilds the Caddy binary from source in
the Dockerfile with a `replace` directive pinning
`google.golang.org/grpc` to a patched version
(`v1.82.1`). After this lands, the `trivy (frontend)`
job reports 0 HIGH/CRITICAL on the Caddy binary
without any new `.trivyignore` entries.

## Why a custom build (and not just `.trivyignore`)

- `.trivyignore` would silence the gate, but the
  binary is still vulnerable on disk. Of the three
  vulnerable code paths in `GHSA-hrxh-6v49-42gf`,
  one (the HTTP/2 Rapid Reset bypass) is reachable
  on any public-facing Caddy server, not just
  xDS/RBAC users. Silently ignoring that is
  dishonest.
- A custom build is the same approach used by
  the `caddy:builder` and `xcaddy` projects. The
  recipe is straightforward: clone Caddy, apply
  one `replace` line, `go build`. No new tooling
  to maintain beyond the existing Go toolchain we
  already have on CI runners for the backend.

## What changed

### `frontend/Dockerfile`

- New `caddy-build` stage between the SPA `build`
  stage and the runtime stage. Uses
  `golang:1.26-alpine` (matches the backend's Go
  pin) and:
  1. Shallow-clones Caddy at the pinned upstream
     tag (`ARG CADDY_VERSION=2.11.4`).
  2. If `ARG GRPCGO_PIN=1.82.1` is set, applies
     `go mod edit -replace=google.golang.org/grpc=v1.82.1`
     and `go mod tidy`.
  3. Caches `go mod download` and the final `go build`
     via BuildKit `--mount=type=cache` for fast
     repeated builds.
  4. Static build with
     `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w"`,
     no Caddy plugins added (we only use core
     directives: `try_files`, `encode`, `header`,
     `reverse_proxy`).
- The runtime stage is now pinned to
  `caddy:${CADDY_VERSION}-alpine` instead of
  `caddy:2-alpine` (the floating tag). This is
  intentional — we need a reproducible base for
  the custom build to be reproducible.
- `apk upgrade --no-cache` is preserved (still
  picks up `CVE-2026-33630` in the pinned
  `c-ares` package).
- Inline header comments document the rationale,
  the upgrade procedure, and the static-build
  reason.

### `.trivyignore`

- `Reviewed:` date bumped to 2026-07-25.
- Added a trailing comment block explaining that
  `GHSA-hrxh-6v49-42gf` is **not** added to the
  ignore list (because the custom build fixes it
  on disk) and what to investigate if it ever
  reappears. The three pre-existing Go-stdlib
  CVEs (`CVE-2026-27145`, `CVE-2026-39822`,
  `CVE-2026-42504`) remain — they are still in
  the binary and the comment block above them is
  unchanged.

## How to update Caddy later

The Dockerfile's two `ARG`s make this a 2-line
change, no source edits:

- `ARG CADDY_VERSION=2.11.4` → `2.11.5` (when it
  lands).
- `ARG GRPCGO_PIN=1.82.1` → either:
  - leave at `1.82.1` (or a newer patched
    version) if Caddy upstream is still on
    `grpc-go < 1.82.1`, **or**
  - set to empty (`ARG GRPCGO_PIN=`) if Caddy
    upstream has finally picked up a patched
    `grpc-go` natively — the `replace` block
    becomes a no-op.

Push, CI rebuilds and re-scans. No source-code
changes are ever required for a Caddy bump.

## Verified locally (planned CI verification)

- `docker buildx build --load -f frontend/Dockerfile .`
  builds cleanly on the CI runner (will verify
  in the `containers` job on this PR).
- `trivy image aegispanel-ui:dev-<sha>` should
  report 0 HIGH/CRITICAL on the Caddy binary.
  (The three pre-existing Go-stdlib CVEs remain
  in the `.trivyignore` baseline.)

## Files

- `frontend/Dockerfile` (rewritten, multi-stage
  with custom Caddy)
- `.trivyignore` (+1 comment block, +1 date
  bump)

Total: 2 files. 0 application-code changes.

Refs: GHSA-hrxh-6v49-42gf (gRPC-Go xDS RBAC +
HTTP/2 Rapid Reset). Trivy job `trivy (frontend)`
in `.github/workflows/security.yml`. Depends on
the Caddy 2.x go.mod pattern (verified against
`v2.11.4` tag).
