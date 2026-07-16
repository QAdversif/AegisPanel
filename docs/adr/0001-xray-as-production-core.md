# ADR-0001: Xray как production core, sing-box как specialty

**Status:** Accepted (2026-07-17)
**Supersedes:** v7 «sing-box MVP, Xray — future option» (ARCHITECTURE.md §7)
**Drives:** ARCHITECTURE.md §7, §7.5, §21 Phase 1 / 4

## Context

AegisPanel начинался с идеи core-agnostic панели через `CoreProvider`
абстракцию (PR #31). На MVP выбрали sing-box как ядро для dev/prod.
К 2026-07 стало ясно, что выбор имеет долгосрочные последствия:

1. **sing-box не имеет API динамических юзеров.** Их «v2ray API» —
   только `StatsService` (чтение трафика) и `Clash API` (мониторинг).
   `HandlerService.AddUser/RemoveUser` **отсутствует** — feature-request в
   sing-box репозитории открыт с 2023 года, не реализован.
2. **Xray имеет полный gRPC API** для динамических юзеров:
   `HandlerService.AddUser/RemoveUser/AlterInbound` + `StatsService.QueryStats`.
   Это индустриальный стандарт — Remnawave, 3X-UI, PasarGuard, Marzban,
   Hiddify, Celerity — все на Xray.
3. **Cascade Topology** (Phase 4+) требует `reverse` outbound — Xray-only
   фича. sing-box не умеет reverse-portal.
4. **HY2 / TUIC-inbound'ы** — Xray не умеет, sing-box умеет. Это единственное
   реальное преимущество sing-box на серверной стороне.

## Decision

1. **Production core: Xray.** gRPC API для dynamic users + статистика +
   cascade + balancer (`leastLoad`, `leastPing`).
2. **Specialty core: sing-box.** HY2/TUIC-inbound'ы (Xray не умеет) + dev-окружение
   + нишевые сценарии.
3. **Для sing-box — Batched Apply** (§7.5): накопление дельт за окно 15-30 сек
   + один reload ядра. Метрика `core_reload_total` для контроля стоимости.
4. **CoreProvider абстракция остаётся ядром дизайна.** Добавление ядра =
   новая реализация в `internal/cores/<name>/`, без миграции БД или переписывания
   фронта.
5. **Дорожная карта:**
   - **Phase 1** (PR-ы после #47): реализовать `internal/cores/xray/`
     параллельно с `internal/cores/singbox/`. Оба регистрируются в реестре.
     UI позволяет выбрать ядро при создании ноды (`node.core_kind`).
   - **Phase 1**: одновременно — `BatchedApplier` для sing-box в
     `internal/cores/batched.go`. Авто-включается, если
     `Capabilities().Has(DYNAMIC_USERS) == false`.
   - **Phase 4**: Cascade Topology на Xray (`reverse` outbound).
   - **Phase 5+**: Hysteria 2 standalone / TUIC standalone core providers.

## Alternatives considered

**A. sing-box only + Batched Apply для всего.** Отклонено: не позволяет
cascading, не позволяет server-side balancer, требует reload на каждое
создание/удаление юзера (даже батч). Не конкурентоспособно с Remnawave.

**B. Xray only, sing-box выкинуть.** Отклонено: теряем HY2/TUIC-inbound'ы,
которые нужны для anti-censorship сценариев (UDP-based протоколы устойчивы к
TCP-throttling). Это нишевая, но реальная аудитория.

**C. Гибрид по ноде: Xray + sing-box на одной ноде.** Рассмотрено, отложено:
удваивает RAM на ноде (дешёвые VPS не потянут), усложняет deploy-роль.
Оставляем как опцию для будущего, если HY2-юзеры станут значимым сегментом.

## Consequences

**Положительные:**

- Динамические юзеры через gRPC = zero-downtime создание/удаление. Метрика
  `user_lifecycle_latency_seconds` остаётся < 100ms.
- Cascade Topology реализуема (Phase 4).
- Server-side balancer (Xray `leastLoad`) доступен при 100+ нодах.
- Real production deployment story (не «dev-прототип с Batched Apply»).

**Отрицательные:**

- Дополнительная работа: ~3-4 PR на `internal/cores/xray/`.
- gRPC client Xray сложнее, чем REST sing-box. Документация Xray gRPC
  фрагментарна — придётся читать proto-файлы.
- Два ядра = два набора integration-тестов. CI-матрица усложняется.

**Нейтральные:**

- `Batched Apply` остаётся как fallback-стратегия для ядер без dynamic API
  (Hysteria 2 standalone, если он тоже не будет иметь API).

## Implementation

- PR после #47: **#48 SubscriptionPgStore** (как было запланировано).
- PR **#49 PanelCfgPgStore**.
- PR **#50 Xray CoreProvider** (структура + рендер конфига из CoreConfig DTO).
- PR **#51 Xray dynamic user add/remove + StatsService** (gRPC клиент).
- PR **#52 BatchedApplier для sing-box** + интеграционные тесты.
- PR **#53 Валидатор профилей нод** (`reality-direct` vs `caddy-fronted`).

## References

- ARCHITECTURE.md §7 (Core abstraction)
- ARCHITECTURE.md §7.5 (Batched Apply) — новый раздел, v8
- ARCHITECTURE.md §19.4.4 (Node Profile separation) — новый раздел, v8
- ARCHITECTURE.md §21 (Unified Roadmap) — переписан, v8
- ARCHITECTURE.md §25 (History of changes) — v8 entry
- Внешнее ревью: «AegisPanel architectural review» (2026-07-17, AI-driven)

## Open questions

- Какой минимальный набор Xray-фич нужен для production? (Предварительно:
  VLESS+REALITY, H2, fallback, dynamic user add/remove, StatsService query.)
  Финализируем в PR #50.
- Должна ли быть версия Xray-конфига пинована в панели или следовать за
  версией на ноде? (Предварительно: пану pin'ит `XRAY_VERSION` через
  Ansible, agent проверяет и rollback'ит при несовпадении.)
