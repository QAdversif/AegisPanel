# ADR-0003: MVP v1.0 — sing-box как единственный core, Batched Apply как primary-стратегия

**Status:** Accepted (2026-07-17)
**Supersedes:** [ADR-0001](./0001-xray-as-production-core.md) (Xray as production core)
**Drives:** ARCHITECTURE.md §0, §1, §7, §7.5, §21, §25 (v9 entry)
**Authors:** solo-dev + AI architecture review
**Reviewers:** self (single-tenant, single-author)

## Context

[ADR-0001](./0001-xray-as-production-core.md) (тоже 2026-07-17, на день раньше)
зафиксировал: **Xray = production core**, sing-box = specialty для HY2/TUIC,
потому что у sing-box нет `HandlerService.AddUser/RemoveUser` через API и
каждое создание/удаление юзера = `reload` ядра = обрыв активных сессий.

Решение принималось под посыл «команда 2-3 человека, 12-18 недель до MVP»
(ARCHITECTURE.md §21). **Контекст изменился**: проект — solo-разработка, MVP
soft launch — 4-6 недель. Дополнительные вводные:

1. **Реальный код:** `internal/cores/singbox/` уже реализован (render, diff,
   validate, capabilities) и покрыт тестами. `cores/xray/` — пустая папка.
   `cores/singbox.Apply()` — stub (`ErrApplyNotImplemented`). Полная
   реализация `cores/xray/` потребует gRPC-клиент, proto-парсер, отдельный
   набор integration-тестов с Xray в docker — это **3-4 недели** самой
   разработки, **до** любых пользовательских фич.

2. **Batched Apply уже спроектирован** (ARCHITECTURE.md §7.5, код ещё не
   написан): окно 15-30 сек + один reload ядра. Для soft launch с десятками
   юзеров окно даже не достигается — `core_reload_total` будет равен
   количеству ручных правок конфига. Для 1000+ юзеров — burst на signup
   вызовет 1-2 reload в минуту, что **приемлемо** для VPN-сценария
   (переподключение клиента занимает <5 сек, REALITY/XTLS это переживают
   без потери сессии, потому что reconnect — нормальный flow).

3. **Операционная реальность:** Batched Apply добавляет **латентность
   энфорсмента лимитов 15-30 сек**. Это не tradeoff безопасности — лимиты
   всё равно энфорсятся, просто не мгновенно. Для коммерческого VPN это
   незаметно (юзер не узнает, что превышение лимита сработало в момент X,
   а не в момент X+0.1s).

4. **CoreProvider абстракция — уже работает.** Добавление Xray как second
   provider в будущем = новый пакет `internal/cores/xray/`, регистрация в
   `init()`, расширение UI-селектора ядер. **Без миграции БД, без
   переписывания фронта, без поломки существующих sing-box-инсталлов.**
   Это страховка, которая позволяет зафиксировать «sing-box only на MVP»
   без потери future-option.

5. **Solo-разработчик = серийный bottleneck.** Каждое архитектурное
   расширение умножает сложность дебага, тестирования, эксплуатации.
   Один core лучше, чем два, если один покрывает 100% сценариев MVP.

6. **HY2/TUIC через sing-box уже работает** (capability-флаг
   `CapHY2` / `CapTUIC` в `cores/singbox/singbox.go`). То, что Xray не
   умеет HY2 — для MVP **не релевантно**: HY2-юзеры будут жить на тех же
   нодах, что и VLESS-юзеры, просто через sing-box-inbound с другим
   протоколом. ADR-0001 ошибочно позиционировал HY2 как «единственное
   преимущество sing-box, нужное нишевой аудитории», но sing-box MVP
   покрывает HY2 без Xray.

## Decision

1. **MVP v1.0 ships на sing-box как единственном core.** Никакого
   `internal/cores/xray/` в v1.0. ADR-0001 отменяется.

2. **Batched Apply — primary-стратегия энфорсмента юзеров**, не
   fallback. Окно: 20 секунд (дефолт), настраивается через
   `AEGIS_BATCHED_APPLY_WINDOW`. Метрики `core_reload_total`,
   `core_reload_lost_sessions_total`, `core_user_apply_latency_seconds`
   — обязательны, идут в Prometheus с первого релиза.

3. **CoreProvider абстракция остаётся ядром дизайна.** В v1.0
   зарегистрирован только `sing-box` + `noop` (dev). `xray` —
   placeholder в коде (комментарий в `internal/cores/registry.go`),
   не импортируется в `cmd/aegis/main.go`. `GET /api/v1/cores`
   возвращает только зарегистрированные.

4. **Capability-флаги `CASCADE`, `WIREGUARD`, `ACL`** — остаются в
   enum (обратная совместимость, чтобы UI-компоненты могли их
   разрабатывать), но **ни один зарегистрированный core в v1.0
   их не поддерживает**. UI скрывает соответствующие фичи.

5. **Roadmap:**

   | Версия | Что в релизе | Срок (solo) | Привязка к коду |
   | --- | --- | --- | --- |
   | `v0.1.0-mvp-render` | Subscription `PgStore` + Panelcfg `PgStore` + UI страницы (Nodes/Inbounds/Hosts/Users/Subscription) + OpenAPI codegen | 1 нед | `internal/subscription/`, `internal/panelcfg/`, `frontend/src/views/` |
   | `v0.2.0-mvp-agent` | `cmd/aegis-agent` (Go, musl) + HTTP-bearer транспорт + `cores/singbox.Apply/ParseStatus/ParseStats` + Ansible `install_agent` доводится | 1.5–2 нед | `backend/cmd/aegis-agent/`, `internal/cores/singbox/`, `deploy/ansible/roles/install_agent/` |
   | `v0.3.0-mvp-byo-node` | `internal/bootstrap/` (SSH-клиент, install agent, handshake) + UI «Add node» flow | 1 нед | `internal/bootstrap/` (сейчас пустой — заполняем) |
   | `v0.4.0-mvp-batched` | `internal/cores/batched.go` (generic BatchedApplier) + Redis-очередь + метрики | 0.5–1 нед | `internal/cores/batched.go` (новый файл) |
   | **`v1.0.0-mvp-soft-launch`** | Polishing: healthchecks, JSON-logs, backup-restore smoke test, `docs/user-guide/admin/quickstart.md` | 0.5 нед | `tools/scripts/`, `docs/` |
   | `v1.1.0` | mTLS + gRPC канал Panel↔Agent (вместо HTTP bearer) | 2 нед | `internal/bootstrap/`, `cmd/aegis-agent/` |
   | `v1.2.0` | Real users CRUD + plans + traffic limits + Cabinet API | 2-3 нед | `internal/users/`, `internal/plans/`, `internal/stats/` (сейчас пустые) |
   | `v1.3.0` | Webhooks (HMAC-SHA256) для внешнего ЛК | 1-2 нед | `internal/webhooks/` (пустой) |
   | `v1.4.0` | Outgoing notifications (Telegram через n8n) | 1 нед | `internal/notifications/` (пустой) |
   | `v1.5.0` | Prometheus exporter + Grafana dashboard + базовые алерты | 1 нед | `internal/obs/` (расширение) |
   | `v1.6.0` | Multi-port + inbound profiles UI | 1 нед | `frontend/src/views/InboundsView.vue` |
   | `v1.7.0` | Decoy sites v1 (оператор настраивает Caddy руками, панель даёт референсный конфиг + smoke test) | 1 нед | `internal/decoy/` (пустой) |
   | `v1.8.0` | Per-user traffic → ClickHouse (если выбран) или остаётся в Postgres | 2 нед | `internal/stats/` (расширение) |
   | **`v2.0.0`** | **Xray CoreProvider как second provider** (если после MVP выстрелит) + выбор ядра при создании ноды (`node.core_kind`) | 3-4 нед | `internal/cores/xray/` (новый пакет) |
   | `v2.1.0+` | Cascade Topology, MCP, Subscription Profiles, SRH Inspector (бывшая Phase 4) | по запросу | `internal/cascades/`, `internal/mcp/` (пустые) |

6. **Архитектурный инвариант:** каждое расширение = новый пакет, не
   правка существующего. CoreProvider, Auth Store, Backend-per-service
   (`*_BACKEND=memory|pg`) — это уже работающие extension points. Не
   плодим if/else в существующем коде.

7. **Definition of Done для каждого релиза** (применяется ко всем
   версиям roadmap):

   - Код в `main` (или feature-бранч готов к merge)
   - Unit-тесты ≥ 70% coverage на новый код
   - Integration-тест (с реальным Postgres) — pass
   - E2E-тест (docker-compose с panel + sing-box + agent) — pass
   - `golangci-lint` / `sqlfluff` / `markdownlint` — 0 ошибок
   - `trivy` / `gitleaks` / `osv-scanner` — 0 критичных
   - Документация обновлена (ADR, README, CHANGELOG)
   - Запись в `CHANGELOG.md` в секции Unreleased
   - Миграция БД — обратно совместима (`migrate down` работает)
   - Smoke-test на чистой VM проходит (один скрипт)

## Alternatives considered

**A1. Xray-only, sing-box выкинуть.** Отклонено: теряем HY2/TUIC-
inbound'ы, нужные для anti-censorship. Нишевая, но реальная аудитория.

**A2. Гибрид на одной ноде: sing-box + Xray.** Рассмотрено, отклонено
(ADR-0001 уже отверг): удваивает RAM на дешёвых VPS, усложняет
deploy-роль. Можно вернуть в v2.1+ если HY2-юзеры станут значимым
сегментом.

**A3. Два ядра параллельно с v1.0 (Xray + sing-box сразу).** Рассмотрено,
отклонено: +3-4 недели на Xray CoreProvider, ничего не даёт пользователю
на старте, удваивает набор integration-тестов в CI. Чистый убыток для
solo-dev темпа.

**A4. MVP на sing-box + Xray как «core of the future» в коде с самого
начала (extensibility-over-YAGNI).** Рассмотрено, отклонено: stub-пакет
`cores/xray/` без реализации — мёртвый код, вводит в заблуждение. Лучше
явно сказать «Xray = v2.0+» и вернуться к нему, когда будет demand.

**A5. Отказаться от CoreProvider абстракции, написать MVP как
sing-box-only монолит.** Рассмотрено, отклонено: абстракция уже
реализована (Phase 0), не окупится её выкидывать. Возможность добавить
второй core без миграции БД — это страховка, которая стоит ~0 LOC
в runtime.

## Consequences

**Положительные:**

- **Таймлайн сокращается с 25-35 недель до 5-7 недель до MVP-1.0**
  (расчёт в §21). Batched Apply покрывает 100% сценариев MVP.
- Один core = один набор тестов, один набор документации, один
  операционный playbook. Меньше cognitive load для solo.
- Архитектурная страховка через CoreProvider сохранена: v2.0+ добавит
  Xray без breaking changes.
- Метрики `core_reload_total` / `core_reload_lost_sessions_total`
  дают оператору visibility в стоимость Batched Apply — если на
  проде окно окажется узким, увеличить `AEGIS_BATCHED_APPLY_WINDOW`
  = одна env-переменная, без релиза.

**Отрицательные:**

- **15-30 сек латентность на создание/удаление юзера в sing-box**
  vs мгновенная в Xray. На MVP — приемлемо, на high-scale —
  придётся увеличивать окно или мигрировать на Xray.
- **Reload ядра при каждом батче = обрыв TCP-сессий.** Mitigations:
  (а) sing-box RELOAD безопасен для VLESS/REALITY (клиент переподключается
  за <1 сек), (б) HY2/QUIC сессии переживают reload через connection
  migration, (в) `core_reload_lost_sessions_total` метрика
  покажет, насколько это болезненно в реальности.
- **ADR-0001 отменён через 1 день после принятия.** Урок: не фиксировать
  дорогостоящие архитектурные решения (Xray CoreProvider = 3-4 недели
  работы) без подтверждённого бизнес-requirement. Зафиксирован в
  agent memory: «ADR под дорогую работу требует ≥ 1 недели на
  пересмотр».

**Нейтральные:**

- `internal/cores/xray/` остаётся пустой папкой / не импортируется
  в `cmd/aegis/main.go` до v2.0. Это явный backlog, не мёртвый код.
- HY2/TUIC-юзеры на v1.0 живут на тех же нодах, что VLESS-юзеры.
  Если нагрузка HY2 вырастет до % от общей — выделить отдельные
  ноды в v2.1+.

## Implementation

- **PR #48**: ADR-0003 (этот документ) — запись в `docs/adr/`.
- **PR #49**: ARCHITECTURE.md v9 entry + обновления §0, §1, §7, §7.5, §21.
- **PR #50**: ADR-0001 — пометка Superseded.
- **PR #51**: CHANGELOG.md — Unreleased секция с roadmap.
- **PR #52..#56**: релизы v0.1.0 → v1.0.0 согласно таблице roadmap.

## References

- [ADR-0001](./0001-xray-as-production-core.md) — отменённое решение.
- [ADR-0002](./0002-node-profile-separation.md) — Node Profile separation, остаётся в силе.
- ARCHITECTURE.md §0, §1, §7, §7.5, §21, §25 (v9 entry).
- Внешнее ревью: «AegisPanel architectural review» (2026-07-17).
- Remnawave/3X-UI/Marzban — production panels с похожим подходом
  (одно ядро на релиз, Batched Apply как паттерн).

## Open questions

- **Метрика `core_reload_lost_sessions_total`** — как именно считать?
  Гипотеза: дельта active connections до/после reload, сэмплинг
  каждые 100ms через sing-box API. Финализируем при реализации
  BatchedApplier в v0.4.0.
- **HY2 connection migration** при sing-box reload — насколько
  реально работает? Финализируем после первых нагрузочных тестов
  в v0.2.0.
- **Нужен ли dashboard для Batched Apply в UI?** Метрика +
  `AEGIS_BATCHED_APPLY_WINDOW` env-переменная достаточно для MVP.
  Grafana dashboard — v1.5.0.
