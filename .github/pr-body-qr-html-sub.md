## Summary

Replaces the Phase 0 placeholder HTML sub-page with a real landing page that includes:

1. **A 256x256 QR code** encoding the base64 subscription URL. Scannable by Hiddify, Streisand, NekoBox, Karing, V2Box, and every modern client that supports camera import.
2. **Per-client URLs** (base64 / singbox / clash) in a copyable table. The user pastes the URL into their client's "import from URL" field.
3. **Vanilla-JS copy buttons** that fall back to input selection when the clipboard API is unavailable (plain HTTP, older browsers).
4. **Entitled-host count** in the "you have N hosts" line, with a friendly "no hosts" branch for users with empty subscriptions.

This is PR #46, the natural next step after the multi-port + XHTTP PR (#45).

## What's in the box

- `internal/subscription/html_qr.go` — new file. `buildQRCodePng` and `buildQRCodeDataURL`. Uses `github.com/skip2/go-qrcode` (single-dep, MIT, no transitive dependencies). Recovery level `Medium` — robust against scratched screens and reflective protectors without bloating the image.
- `internal/subscription/handler.go`:
  - `renderHTML` now builds the QR, computes the per-client URLs, and feeds everything into `buildHTMLPage`. A QR failure is logged and the page renders with an empty QR (`<img alt="...">`) — clients can still use the per-client URLs.
  - `subscriptionURLFor` is a small helper that builds the absolute URL for a given format, honouring `X-Forwarded-Proto` and `r.TLS` (so a phone camera can actually reach the URL it scans).
  - `buildHTMLPage` signature changed from `(username, url, hostCount)` to `(username, qrDataURL, base64URL, singboxURL, clashURL, hostCount)`. The page now embeds a 256x256 QR `<img>`, a per-client table with copy buttons, and a "no hosts" friendly branch.
  - `htmlClientRows` is a small seam for the table body.
- `internal/subscription/handler_test.go` — 4 new tests:
  - `TestHandler_Render_HTML_QRPresent` — locates the `data:image/png;base64,…` src, base64-decodes it, asserts the PNG magic bytes.
  - `TestHandler_Render_HTML_PerClientURLs` — asserts all three `target=` values appear in the body and there are 3 `data-copy="u…"` buttons.
  - `TestHandler_Render_HTML_HostCount` — the fixture's one host shows up as `<b>1</b> host line`.
  - `TestHandler_Render_HTML_NoHosts` — a second user with no plan renders the "no hosts" branch.
- `internal/subscription/html_qr_test.go` — new file. 4 unit tests for the QR builder: valid PNG output, `data:` URL prefix, zero/negative size accommodation.
- `backend/go.mod` / `backend/go.sum` — `github.com/skip2/go-qrcode v0.0.0-20200617195104-da1b6568686e`. Single dep, no transitive.

## Design notes

- **Data URL, not a separate endpoint.** The QR is base64-embedded as `data:image/png;base64,…`. A 256x256 medium-recovery QR is ~5 KB encoded, well under the panel's response budget. No static asset, no second request, no CORS surface.
- **QR encodes the base64 URL, not the HTML URL.** The base64 URL is the format every client can import; the singbox / clash URLs are explicit overrides on the page. Phones that auto-detect "import from QR" land on the most-compatible format by default.
- **Phone-camera-friendly scheme/host.** `subscriptionURLFor` honours `X-Forwarded-Proto` and `r.TLS` so the QR's URL is the URL the phone can actually reach. Behind a reverse proxy on HTTPS, the QR encodes `https://panel.example.com/sub/...`, not the panel's internal `http://aegis:8080/sub/...`.
- **Vanilla JS, no framework.** The page is a phone-camera target; a phone must render it without JavaScript and within the first second. The copy-button JS is the only script; on clipboard-API failure it falls back to `el.select()` so the user can hit `Ctrl+C` (or `Cmd+C` on a phone with hardware keyboard) manually.
- **QR render failure is logged, not 5xx.** If the QR builder errors (extremely rare — the inputs are user-controlled but already constrained), the page renders with an empty QR (`<img alt="Subscription QR code" src="">`). The per-client URLs still work; the user just has to type / paste manually.
- **No new migrations, no new env vars.** The change is purely runtime — the model, storage, and main are unchanged.

## Compatibility

- Boot path: no changes. `cmd/aegis/main.go` already wires the subscription service.
- Wire format: only the html format is touched. base64 / singbox / clash outputs are byte-identical to PRs #40-43.
- The Go `User.Username` is the only data shown on the page. The URL is the credential; the QR contains nothing extra.

## Follow-up

Natural next PRs in dependency order:

1. **Transport (ws / grpc / h2) for both sing-box and Clash renderers** — the sing-box renderer still emits no `transport` block; the `params.transport` field is read here for the XHTTP gate but not yet wired for the actual transport object.
2. **Sub-token rotation** + the `/s3cr3t-sub-<hex>` URL prefix.
3. **`SubscriptionPgStore`** — mirrors the inbounds / hosts / nodes pattern.
4. **Agent-side multi-port binding** — read `inbounds.ListenPorts` and bind every entry with the same protocol / params.
5. **Frontend** — Phase 0 placeholder; the real Vue 3 admin UI is the next big visible-to-user milestone.

## Checklist

- [x] `go vet -tags=integration ./...` clean
- [x] `gofmt -l` clean (LF form, which is what CI sees)
- [x] `goimports -l -local github.com/QAdversif/AegisPanel` clean
- [x] `go build -tags=integration ./...` clean
- [x] `go test ./...` passes (8 new tests; 0 broken)
- [x] `staticcheck ./...` clean
- [x] `gocritic` clean for new code (pre-existing issues untouched)
- [x] No new migrations
- [x] No new env vars
- [x] One new top-level dependency: `github.com/skip2/go-qrcode` (single dep, no transitive)
