# feat(builder): per-user credential filter (closes v0.7.x Phase 2 multi-user TODO)

## Что внутри

Закрывает **вторую половину** v0.7.x Phase 2 multi-user TODO. Без этого фильтра Builder возвращал каждый `(user, inbound)` credential независимо от `User.HostsAllowlist` / `HostsBlocklist` — пользователь, у которого в allowlist были только host-ы на node-1, появлялся в рендере node-2.

v0.8.10+ поведение: один DB round-trip на `BuildCoreConfigForNode` (на flush, не на inbound), фильтрация per-inbound списка по `Credential.UserID ∈ allow-set`, в рендере `users: [...]` только разрешённые пользователи.

## Что в коде

| Файл | Изменение |
|---|---|
| `internal/users/service.go` | Новый метод `AllowedUsersForNode(ctx, nodeID) ([]uuid.UUID, error)` — reverse-direction read `enqueueUserDelta` (v0.8.x PR #192). `StatusActive` filter, blocklist-wins-over-allowlist, fail-closed на nil `s.hosts` + non-empty filter. Reuses `expandHostsToNodes` helper. ~80 строк + doc comment |
| `internal/cores/builder/builder.go` | Новый интерфейс `ListUsersAllowedForNode`. `BuildCoreConfigForNode` резолвит allow-set один раз, фильтрует per-inbound cred list. `NewFlushFn` принимает `usersSrc` (после `credSrc`, перед `renderer`). nil = default-allow (v0.8.0-v0.8.9 contract preserved). Empty allow-set = fail-closed (sentinel через `make(map[uuid.UUID]struct{})` чтобы отличить от nil-lookup). ~80 строк кода + 90 строк doc comments |
| `cmd/aegis/main.go` | `a.Users` передаётся в `builder.NewFlushFn` (~10 строк + doc comment) |
| `internal/cores/builder/builder_test.go` | 5 новых тестов (FullAllow, PartialAllow, EmptyAllow, NilUsers, LookupError) + 8 обновлено под новую 6-arg сигнатуру. ~240 строк |
| `internal/users/service_test.go` | 3 новых теста (allowed users for node, nil hosts fail-closed, default-allow). ~170 строк |
| `internal/cores/builder/flushfn_{smoke,integration}_test.go` | +1 nil arg в каждом NewFlushFn вызове |
| `CHANGELOG.md` [Unreleased] | Новая секция "Added (per-user credential filter in the Builder)" |
| `KNOWN_LIMITATIONS.md` | Закрыт entry "The per-credential Builder-side filter" + новый "Per-user credential filter in the Builder — closed in this PR" |
| `docs/ROADMAP.md` | v0.8.x row: per-user filter → ✅ shipped |
| `docs/operator-guide.md` | "Not designed to defend against" — per-user leak → ✅ closed in v0.8.10+ |
| `docs/README.md` | Status table row + v0.8.x-bucket remaining обновлены |
| `README.md` (root) | v0.8.x row + v1.0.0-mvp-soft-launch row обновлены (filter no longer blocks GA) |

## Семантика

- **Default-allow** (v0.8.0-v0.8.9 contract preserved):
  - `usersSrc` == nil → каждый credential passes
  - `usersSrc` returns nil/empty без error → каждый credential passes (legacy semantics)
  - `usersSrc` returns error → log warning, default-allow (fail-soft, matches credentials source и host source patterns)
- **Filter active**:
  - `usersSrc` returns non-empty list → drop credentials whose `UserID` not in set
  - `usersSrc` returns empty list (`[]` или `nil` с len==0) → drop every credential, per-tag entry omitted (fail-closed "no users on this node")
- **Blocklist wins** over allowlist (matches `enqueueUserDelta` v0.8.x)
- **StatusActive only** (`users.store.ListByStatus(ctx, StatusActive)`) — inactive/expired/disabled users исключены
- **Fail-closed на nil `s.hosts` с non-empty filter** — повторяет v0.8.x pattern для `enqueueUserDelta` (default-allow на пустых filter полях, fail-closed на непустых)

## Pre-pr.sh

**10/10 ✓**:
- backend gofmt / go build / go vet (integration tags) / go test (short) — все 26 packages зелёные
- backend golangci-lint ✓
- frontend codegen:check / type-check / lint / build ✓
- docs markdownlint ✓ (0 errors)
- agent memory 26KB (under 70KB cap)

## Diff

```
 CHANGELOG.md                                       |  31 +++
 KNOWN_LIMITATIONS.md                               |  47 ++++
 README.md                                          |   4 +-
 backend/cmd/aegis/main.go                          |  16 +-
 backend/internal/cores/builder/builder.go          | 168 +++++++++++++-
 backend/internal/cores/builder/builder_test.go     | 238 +++++++++++++++++++-
 .../cores/builder/flushfn_integration_test.go      |   2 +-
 .../internal/cores/builder/flushfn_smoke_test.go   |   4 +-
 backend/internal/users/service.go                  |  89 ++++++++
 backend/internal/users/service_test.go             | 169 +++++++++++++++
 docs/README.md                                     |   2 +-
 docs/ROADMAP.md                                    |   2 +-
 docs/operator-guide.md                             |  15 +-
 13 files changed, 754 insertions(+), 33 deletions(-)
```

## Privacy

Никаких privacy-rule items. Только код + тесты + docs.
