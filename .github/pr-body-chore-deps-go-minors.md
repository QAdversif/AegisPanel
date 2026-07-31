# chore(deps): bump Go minors (prometheus, env, zerolog)

Three Go minor-version updates, all semver-compatible. No application
code changes required.

## Bumps

- `github.com/prometheus/client_golang` 1.20.5 → 1.24.1 (4 minors)
- `github.com/caarlos0/env/v11` 11.2.2 → 11.4.1 (2 minors)
- `github.com/rs/zerolog` 1.33.0 → 1.35.1 (2 minors)

## Compatible indirect upgrades pulled in

- `github.com/prometheus/client_model` 0.6.1 → 0.6.2
- `github.com/prometheus/common` 0.55.0 → 0.70.1
- `github.com/prometheus/procfs` 0.20.1 → 0.21.1
- `github.com/klauspost/compress` 1.18.5 → 1.19.1
- `github.com/mattn/go-colorable` 0.1.13 → 0.1.14
- `golang.org/x/net` 0.56.0 → 0.57.0
- `github.com/rogpeppe/go-internal` 1.15.0 (test-only, new)

## Local verification

- `go build ./...` clean
- `go test -count=1 -short ./...` — all packages PASS
- `gofmt -l backend/` — no diff
- `golangci-lint v2 run --config backend/.golangci.yml -tags=integration ./...` — 0 issues

## Risk

Low. All three are patch/minor bumps with documented semver compat.
The Prometheus ecosystem bundles tend to keep API stable across minors
even at the 0.x → 0.70.x jump on `prometheus/common`.

Part of a 4-PR dependency batch: Go minors (this), then pinia 4,
then vue-tsc 3, then vue-i18n 11. Each PR is independent and easily
revertable.
