## Summary

Adds two Phase 1 subscription features:

1. **Multi-port inbound** — `inbounds.ListenPorts []int`. The subscription renderer picks a random port from `ListenPort ∪ ListenPorts` on every fetch, defeating per-port DPI correlation (3X-UI / Marzban convention). The agent will bind every port in a future PR; this PR wires the model, storage, and renderers.
2. **XHTTP `download_settings`** — `host_endpoints.download_host_id UUID NULL FK` and the sing-box renderer's `download_settings: { address, port }` block. The endpoint references a separate "download farm" host (operator-controlled CDN) that is NOT in the user's pool. The Service looks the host up by id directly.

This is PR #45, the natural next step after the format-vars PR (#44).

## What's in the box

### Multi-port

- `internal/inbounds/inbound.go` — new `ListenPorts []int` field, `omitempty` JSON tag. The DB enforces `UNIQUE (node_id, listen_port)` on the primary port; collisions with another inbound's primary are caught at insert time.
- `internal/inbounds/validate.go` — `normaliseListenPorts`: caps entries at 8, dedupes, validates each port against `[1, 65535]`. The cap keeps the per-fetch random pick cheap and matches real-world CDN-fronted deployments.
- `internal/inbounds/pg_store.go` — Create / Update / scan plumbed through the new `listen_ports INTEGER[]` column (pgx binds `[]int` directly; nil becomes the column's DEFAULT `'{}'`).
- `internal/inbounds/store.go` — `MemoryStore.cloneInbound` deep-copies `ListenPorts`.
- `internal/inbounds/service.go` — `CreateInput.ListenPorts`, `UpdateInput.ListenPorts`, validation in Create and Update.
- `internal/inbounds/handler.go` — `createRequest.ListenPorts` / `updateRequest.ListenPorts`.
- Migration `0008_inbounds_listen_ports.sql` — `ALTER TABLE inbounds ADD COLUMN listen_ports INTEGER[] NOT NULL DEFAULT '{}'`.

### XHTTP download_settings

- `internal/hosts/host.go` — new `Endpoint.DownloadHostID *uuid.UUID`. The reference is per-endpoint because the same inbound can be reused across hosts and the download farm is per-deployment.
- `internal/hosts/validate.go` — `validateDownloadHostID`: rejects `uuid.Nil`. The cross-entity check (does the host exist?) is deferred to the subscription Service at resolve time, so a host can be created with a forward-reference.
- `internal/hosts/store.go` — `MemoryStore.cloneEndpoint` deep-copies the `*uuid.UUID`.
- `internal/hosts/pg_store.go` — Create / scan plumbed through the new `download_host_id UUID NULL` column. `nullableUUID` helper for pgx.
- `internal/hosts/service.go` — `normaliseEndpoints` calls `validateDownloadHostID` for each endpoint.
- Migration `0009_endpoint_download_host.sql` — `ALTER TABLE host_endpoints ADD COLUMN download_host_id UUID NULL REFERENCES hosts(id) ON DELETE SET NULL`. `ON DELETE SET NULL` so deleting a download host degrades gracefully.

### Renderers

- `internal/subscription/port.go` — `pickPort(in)` returns a random entry from `ListenPort ∪ ListenPorts` via a package-level picker (`rand.IntN` from `math/rand/v2`). The picker is swappable via `setRandPicker` so tests can pin a deterministic sequence.
- `internal/subscription/render.go` — `effectiveAddress` calls `pickPort(ep.Inbound)`. The endpoint-level `ep.Endpoint.Port` override still wins (single-port override > random multi-port pick).
- `internal/subscription/service.go` — `ResolveEndpointsForUser` populates `ResolvedEndpoint.Download` when `ep.DownloadHostID` is set. The download host is fetched directly by id (NOT through the user's pool — operator-controlled). The picker picks a random endpoint of the download host.
- `internal/subscription/render_singbox.go` — VLESS outbound emits `download_settings: { address, port }` when `ep.Download != nil` AND `params.transport == "xhttp"`. The block is gated on the transport; sing-box would reject a download_settings block on a non-XHTTP outbound.
- The base64 and Clash renderers ignore the download reference (their wire formats have no equivalent). The XHTTP flag is a sing-box-only concept.

### Tests

- `internal/subscription/port_xhttp_test.go` — 12 tests:
  - `pickPort` unit tests: empty / nil `ListenPorts` falls back to `ListenPort`; picker honours its index.
  - `RenderBase64` / `RenderSingbox` / `RenderClash` multi-port round-trip: pin the picker, assert the picked port shows up in the wire format.
  - `RenderSingbox` XHTTP `download_settings` block: the resolver populates `Download` and the renderer emits the block.
  - `RenderSingbox` no-block-without-xhttp: an inbound with `transport: "ws"` does NOT emit the block even when `DownloadHostID` is set.
  - `RenderBase64` / `RenderClash` XHTTP download ignored: the wire format does not include the CDN address.
  - `resolveDownload` missing host: a `DownloadHostID` that points to a non-existent host is silently skipped, `Download` is nil.

## Design notes

- **Multi-port semantics**: `ListenPort` is the primary port (DB-constrained unique). `ListenPorts` is the additional-port list. The renderer picks from the union. The agent will bind every entry in a future PR; for now the panel just stores and serves them.
- **XHTTP download host vs user's pool**: the download host is operator-controlled (CDN). It is NOT in the user's pool. The Service looks it up by id directly via `s.hosts.Get(ctx, hostID)`. A user on the same panel cannot see the CDN host in their subscription.
- **Resolver populates `Download` eagerly, renderer gates the block on transport**: the Service does not know the transport semantics. It resolves the download host regardless of transport so the renderer can decide. An inbound with `transport: "ws"` and a `DownloadHostID` populates `Download` but the sing-box renderer omits the block.
- **Per-fetch random selection**: same pattern as the existing `*` salt. The picker is the same package-level `randPicker` used by `pickPort` and the download-host endpoint selection. Tests pin it via `setRandPicker` with a deterministic function.
- **Fail-soft everywhere**: a missing download host, an empty CDN host, an endpoint whose download lookup fails — all silently skipped. The subscription must still serve for the rest of the entitled endpoints.
- **No transport-block work**: the sing-box renderer still emits no `transport` block for ws/grpc/h2. The `download_settings` field is at the outbound top level (sing-box 1.8+ spec), NOT inside a transport block, so this PR is self-contained. The full transport-block work lands in a future PR.

## Compatibility

- Boot path: no changes. `cmd/aegis/main.go` already wires the inbounds and hosts services. The subscription Service's new `resolveDownload` is internal.
- Wire format: the base64 and Clash outputs are byte-identical for endpoints without `ListenPorts` (the historical single-port case) and without `DownloadHostID` (the historical case). The new fields are optional and the storage layer defaults them to `nil` / `[]`.
- The sing-box output gains a `download_settings` block only when both the endpoint has `DownloadHostID` and the inbound declares `transport: "xhttp"`. Existing clients (no xhttp, no download ref) see no change.
- The Go model's `omitempty` ensures a single-port inbound serialises to JSON without a `listen_ports` field, and an endpoint without a download host serialises without `download_host_id`.

## Follow-up

Natural next PRs in dependency order:

1. **Transport (ws / grpc / h2) for both sing-box and Clash renderers** — the sing-box renderer still emits no `transport` block; the `params.transport` field is read here for the XHTTP gate but not yet wired for the actual transport object.
2. **QR code in HTML sub-page** — `go-qrcode` or pure-Go.
3. **Sub-token rotation** + the `/s3cr3t-sub-<hex>` URL prefix.
4. **`SubscriptionPgStore`** — mirrors the inbounds / hosts / nodes pattern.
5. **Agent-side multi-port binding** — read `inbounds.ListenPorts` and bind every entry with the same protocol / params.
6. **Frontend** — Phase 0 placeholder; the real Vue 3 admin UI is the next big visible-to-user milestone.

## Checklist

- [x] `go vet -tags=integration ./...` clean
- [x] `gofmt -l` clean for new files
- [x] `goimports -l -local github.com/QAdversif/AegisPanel` clean for new files
- [x] `go build -tags=integration ./...` clean
- [x] `go test ./...` passes (12 new tests; 0 broken)
- [x] `staticcheck ./...` clean
- [x] `gocritic` clean for new code (pre-existing issues untouched)
- [x] No new migrations beyond 0008 and 0009
- [x] No new env vars
- [x] No new top-level dependencies
