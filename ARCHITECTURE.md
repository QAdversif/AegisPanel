# Aegis — VPN Control Panel. Архитектурный план

> **Aegis** — control panel для мульти-протокольного VPN-сервиса. Multi-core
> (sing-box, Xray, Hysteria 2), BYO Node, Cascade Topology, MCP-управление,
> full-client compatibility (Hiddify, v2rayNG/N, Streisand, Clash и др.),
> anti-censorship через Caddy + decoy-сайты + маскировку портов.
>
> **Стек:** Backend Go 1.22+, Frontend Vue 3, Caddy, fail2ban, PostgreSQL,
> ClickHouse, Redis, NATS.
>
> **Лицензия:** AGPL-3.0 (см. раздел 27).
>
> **Tenancy:** Single-tenant (одна панель = один оператор, несколько
> admin-аккаунтов внутри с разными ролями). Multi-tenant — не планируется.
>
> **Репозиторий:** Monorepo (см. раздел 28).
>
> **Документация:** VuePress, разрабатывается локально, **не публикуется** на
> текущем этапе. Будет доступна вместе с публикацией проекта на GitHub.
>
> Документ-проектирование. Цель — зафиксировать целевую архитектуру, доменные
> границы, контракты и решения «по умолчанию» до того, как появится первая
> строка продукта.

---

## 0. Термины (единый словарь)

| Термин | Определение |
| --- | --- |
| **Panel** | Центральная панель управления (control plane): UI, API, БД, оркестратор. |
| **Core** | Прокси-ядро, обрабатывающее пользовательский трафик. В MVP — sing-box, далее возможно Xray, Hysteria-2, TUIC, и т. п. |
| **Node** | Сервер (VPS / dedicated / VM), на котором развёрнут Core + Agent. |
| **Agent** | Лёгкий компонент на ноде: связь с Panel, применение конфигурации, сбор метрик, life-cycle Core. |
| **Host** | Бандл endpoint'ов = бандл пар `(Node, Inbound)` + override-слой + display name + format variables. **Endpoint** — то, что в конечном счёте попадает в подписку как одна строка (один URL). Типы: `direct` (1 endpoint) / `balancer` (N endpoint'ов + стратегия) / `chain` (Phase 2+). |
| **Cascade / Chain** | Цепочка нод, где клиент подключается к одной (Portal), а трафик идёт через другую (Bridge), возможно через Relay. Режимы: `reverse` (bridge за NAT) и `forward` (все публичные). |
| **MCP** | Model Context Protocol — стандарт для AI-ассистентов (Claude, Cursor) нативно вызывать tools панели (CRUD users/nodes, manage cascades, get stats). |
| **Inbound** | Слушатель на ноде (VLESS/REality, Shadowsocks, Hysteria, и т. п.) со своими параметрами. |
| **Subscription** | Набор Host'ов, выдаваемый пользователю в виде URL-ленты (Sing-box / Clash / V2Ray / base64). |
| **User** | Конечный пользователь VPN. |
| **Plan** | Тариф: лимит трафика, срок, набор хостов, лимит устройств. |
| **Cabinet** | Внешний личный кабинет (отдельный сервис): оплата, история, тикеты. Общается с Panel через API. |
| **Provider** | *(устаревший термин)* Раньше — абстракция API провайдеров для авто-создания нод. **Не используется**: в текущей архитектуре панель работает только с **уже существующими нодами** через SSH (BYO Node), API провайдеров намеренно не поддерживается. См. раздел 9. |

---

## 1. Видение и границы MVP

**Что входит в MVP**

- Multi-core архитектура с провайдером sing-box (с возможностью добавления других ядер без breaking changes).
- Регистрация, авто-развёртывание и мониторинг нод.
- Host manager: ручное и автоматическое формирование пулов хостов, выдача в подписки.
- Конфигурация протоколов на уровне панели (inbound-шаблоны, JSON-валидация, dry-run).
- Стабильный API для внешнего ЛК (CRUD пользователей, планы, трафик, вебхуки оплаты).
- Admin-UI на **Vue 3 + TypeScript + Vite** для базовых операций.

**Что НЕ входит в MVP (явно out of scope)**

- Платёжный шлюз — только webhook-контракт, реализация в Cabinet.
- Сложная BI-аналитика — собираем сырые события, визуализация в Grafana.
- Полноценная RBAC для админов — один уровень «super-admin» на MVP.
- Мобильное приложение — только **совместимость с популярными VPN-клиентами** (Hiddify, v2rayNG/N, Streisand, NekoBox, Shadowrocket, Clash Verge/Meta, Karing, V2Box, sing-box CLI) через стандартные URL подписки с auto-детектом формата по User-Agent (см. раздел 10.4).

---

## 2. Функциональные требования

### 2.1 Управление нодами
- Создание ноды вручную или через Provider-API.
- Авто-развёртывание: SSH/Ansible или cloud-init → установка Agent + Core.
- Управление состояниями: `provisioning → active → degraded → suspended → decommissioned`.
- Теги, регионы, группы (для blue/green и канареечных деплоев).
- Drain mode: остановить приём новых пользователей, дождаться активных сессий, вывести из ротации.

### 2.2 Конфигурация протоколов
- Шаблоны Inbound'ов с переменными (port, dest, cert, security).
- Версионирование конфигов: каждая нода хранит активную + N предыдущих ревизий, откат в один клик.
- Dry-run: рендер конфига без применения + diff с активным.
- Валидация по JSON-схеме соответствующего ядра.

### 2.3 Host manager
- Хост = `(Node, Inbound, public-override)` + расширенные override'ы параметров inbound'а.
- Группы хостов (Pool'ы) — основная единица выдачи в подписку.
- Стратегии выдачи: `manual`, `round-robin`, `least-loaded`, `geo-aware` (по IP пользователя).
- Анти-affinity: не выдавать пользователю хосты из одной ноды, если Pool разношёрстный.
- **Типы Host'а**: `direct` (точка подключения) / `balancer` (Xray balancer или sing-box urltest на entry-ноде) / `chain` (cascade topology, Phase 2+).
- **Format variables** в `remark` и `address`: `{USERNAME}`, `{DATA_LEFT}`, `{DAYS_LEFT}`, `{STATUS_EMOJI}` и т.п. — юзер видит персонализированное имя сервера.
- **Wildcard `*` с random salt** в `sni` / `host` / `address` — анти-детект по доменам.
- **Status-based visibility** — хост виден только юзерам с определённым статусом (`active`, `on_hold`, и т.п.).
- **Priority** — порядок хостов в подписке (lower = выше).

### 2.4 Пользователи и подписки
- Создание/редактирование/удаление пользователя.
- Привязка к Plan'у, индивидуальные overrides (лимит, expire, host-allowlist).
- **Subscription URL** (см. раздел 10.4): единая конечная точка, формат отдачи контента зависит от `?target=` / `Accept` / `User-Agent`.
- **Поддерживаемые форматы подписки (MVP):** `singbox` (Hiddify, Streisand, NekoBox, Karing, V2Box, sing-box CLI), `clash-meta` (Clash Verge, Clash Meta for Android), `base64` (v2rayNG, Shadowrocket, v2rayN — fallback). Покрывает ~95% пользователей.
- **HTTP-заголовки** `Profile-Update-Interval`, `Subscription-Userinfo`, `Profile-Title` — для отображения трафика/лимита/expire в клиенте.
- **Sub-page** (`?target=html`) — HTML-страница с QR-кодом и списком клиентов для скачивания.
- Ротация subscription-URL (invalidate + новая).
- Устройства: HWID/device-limit, авто-отзыв по запросу из ЛК.

### 2.5 Трафик и лимиты
- Сбор потреблённого трафика с нод (pull и push).
- Сверка с лимитами, отключение при превышении.
- Периодический сброс (месяц/неделя) — настраиваемо.

### 2.6 Мониторинг
- Node: CPU/RAM/Net/conntrack, uptime Core, версия Core, состояние healthcheck.
- User: онлайн-флаг, скорость, объём за период.
- Системные метрики панели.

### 2.7 API для ЛК
- Аутентификация: bearer-token, выдаётся из панели на уровне каждого тенанта/админа.
- Эндпоинты: users, plans, subscriptions, hosts, traffic, webhooks/payment, webhooks/notify.
- Идемпотентность на запись (Idempotency-Key).
- Версионирование API: `/api/v1/...`, deprecation policy.

---

## 3. Нефункциональные требования

| Категория | Целевой уровень (MVP) |
| --- | --- |
| Доступность панели | 99.5% (single-region достаточно) |
| RPO / RTO панели | RPO ≤ 1h, RTO ≤ 30m |
| Latency API панели | p95 ≤ 200 мс (без тяжёлых агрегаций) |
| Кол-во нод на 1 инстанс панели | 5 000+ (проверяем нагрузочным) |
| Кол-во пользователей | 100 000+ без шардинга (при выносе stats в отдельное хранилище) |
| Безопасность | mTLS Panel↔Agent, секреты в Vault, RBAC-ready, audit-log |
| Наблюдаемость | OpenTelemetry traces + Prometheus metrics + Loki logs |
| Локализация UI | ru / en (i18n встроена) |

---

## 4. Архитектурные принципы

1. **Core-agnostic.** Внутренние модели не знают о sing-box/Xray — общаемся через `CoreProvider` интерфейс.
2. **Control plane отдельно от data plane.** Панель не проксирует трафик и не должна быть в горячем пути.
3. **Stateless API.** Горизонтально масштабируется за балансировщиком, состояние в БД/Redis.
4. **Event-driven между панелью и нодами.** Push-обновления конфигов и pull-heartbeats, всё идемпотентно.
5. **Конфиг как код.** Любая правка inbounds — это новая ревизия, история, аудит.
6. **Fail-safe.** Падение ноды → автоматический drain и алерт. Падение панели → ноды продолжают работать с последним конфигом.
7. **Secure-by-default.** Секреты не в репо, mTLS по умолчанию, JWT с короткой жизнью.
8. **Observability-first.** Без логов и метрик фича не считается готовой.

---

## 5. Высокоуровневая архитектура

```
                        ┌──────────────────────────────────┐
                        │             Admin UI             │
                        │   (SPA: Vue 3 + TypeScript)      │
                        └──────────────┬───────────────────┘
                                       │ HTTPS
                                       ▼
   ┌─────────────────────────────────────────────────────────────┐
   │                       API Gateway                           │
   │  (auth, rate-limit, request-id, CORS, version routing)      │
   └─────────────────────────────┬───────────────────────────────┘
                                 │
   ┌─────────────────┬───────────┼────────────┬────────────────┐
   │                 │           │            │                │
   ▼                 ▼           ▼            ▼                ▼
┌────────┐   ┌────────────┐ ┌─────────┐ ┌──────────┐   ┌──────────────┐
│ Node   │   │ Subscription│ │ User /  │ │ Host /   │   │ Cabinet API  │
│ Mgmt   │   │ Service    │ │ Plan    │ │ Pool     │   │ (external)   │
└───┬────┘   └────┬───────┘ └────┬────┘ └────┬─────┘   └──────┬───────┘
    │             │              │            │                │
    └────────┬────┴──────────────┴────────────┘                │
             │                                                    │
             ▼                                                    ▼
   ┌─────────────────────┐                              ┌───────────────┐
   │  Event Bus (NATS /  │                              │  Cabinet      │
   │  Redis Streams)     │                              │  (separate)   │
   └────────┬────────────┘                              └───────────────┘
            │
            ▼
   ┌─────────────────────┐         ┌──────────────────┐
   │  PostgreSQL         │         │  ClickHouse /    │
   │  (operational data) │         │  TimescaleDB     │
   └─────────────────────┘         │  (metrics/stats) │
                                   └──────────────────┘
                                          ▲
                                          │  scrape / write
                                          │
   ┌─────────────────────┐         ┌──────┴───────┐
   │  Prometheus +       │◀────────┤  Agents      │
   │  Grafana / Loki     │         │  (on Nodes)   │
   └─────────────────────┘         └──────▲───────┘
                                          │
                                          │ mTLS / WGC
                                          │
                                   ┌──────┴───────┐
                                   │    Node      │
                                   │ Agent + Core │
                                   │ (sing-box)   │
                                   └──────────────┘
```

---

## 6. Компоненты панели (декомпозиция)

Предлагаю **модульный монолит** на старте (один репозиторий, один бинарь, внутренние модули), с готовностью к выделению сервисов по мере роста. Это даёт скорость MVP без боли девопса на 0 пользователей.

| Модуль | Ответственность |
| --- | --- |
| `auth` | Логин админа, выпуск JWT, ротация ключей, audit. |
| `users` | CRUD пользователей, overrides, device-limit. |
| `plans` | Тарифы, лимиты, сбросы, интеграция с оплатой через события. |
| `nodes` | Реестр нод, жизненный цикл, health, drain. |
| `bootstrap` | **NEW**: SSH-онбординг уже существующих нод. Probe → InstallAgent → UpgradeAgent → UninstallAgent. Ansible-роли для развёртывания. **Не создаёт VPS через API провайдеров** (см. раздел 9). |
| `inbounds` | Шаблоны протоколов, версионирование, JSON-schema. |
| `cores` | Реализация `CoreProvider` (sing-box, future) + capability-флаги. |
| `hosts` | Хосты, пулы, стратегии выдачи, format variables, wildcard, status_filter. |
| `cascades` | **NEW (Phase 4+)**: chain topology, Network Map. |
| `subscriptions` | Генерация URL-лент, форматы, кеш. |
| `stats` | Сбор/агрегация трафика, on-line статус. |
| `events` | Публикация/подписка на доменные события. |
| `cabinet` | Внешний API-фасад для ЛК (scopes-based auth). |
| `webhooks` | Входящие (оплата) и исходящие (уведомления), HMAC-SHA256. |
| `notifications` | Telegram / email / webhook. |
| `obs` | Экспорт метрик, трассировка, healthchecks, disk alerts. |
| `mcp` | **NEW (Phase 4+)**: MCP-сервер для AI-ассистентов (см. раздел 17). |

---

## 7. Абстракция ядер (Core Provider)

```go
// псевдо-контракт (Go как baseline; для Python/FastAPI будет 1-в-1)
type CoreProvider interface {
    Name() string                                    // "sing-box"
    Version() string
    Capabilities() Capabilities                      // SupportedProtocols, Features
    RenderConfig(model CoreConfig) (string, error)   // model → core-конфиг (JSON/YAML)
    ValidateConfig(raw []byte) error                 // проверка до применения
    Diff(prev, next []byte) (string, error)          // unified diff
    Apply(ctx, nodeID, cfg) error                    // отдать агенту, дождаться ack
    ParseStatus(raw []byte) (CoreStatus, error)      // живой статус ядра
    ParseStats(raw []byte) ([]UserStat, error)       // per-user трафик
}
```

- Реализации лежат в `internal/cores/<name>/` и подключаются через реестр.
- `CoreConfig` — внутренний нормализованный DTO, общий для всех ядер: inbounds, outbounds, routing, dns, experimental. Маппинг в нативный JSON — за провайдером.
- **Зачем это сразу:** переход с sing-box на Xray (или добавление Hysteria-2) — это добавление адаптера, без миграции БД и без переписывания фронта.
- Capability-флаги позволяют UI скрывать то, что ядро не умеет. **Расширенный набор**:

| Флаг | Описание |
| --- | --- |
| `VLESS` / `VMESS` / `TROJAN` / `SHADOWSOCKS` | Поддержка протоколов inbound'а |
| `VLESS_REALITY` / `VLESS_XTLS_VISION` | Reality и Vision flow |
| `HY2` / `TUIC` | Hysteria 2 и TUIC |
| `WIREGUARD` | WireGuard inbound (для Phase 4+) |
| `BALANCER` | Встроенный балансер outbound'ов (Xray) / `urltest` (sing-box) |
| `ACL` | Routing-rules с `reject` / `direct` / `geoip` / custom proxies (для ACL на ноде) |
| `CASCADE` | Поддержка `reverse` / `forward` chain между нодами |
| `DYNAMIC_USERS` | add/remove user без рестарта ядра (через gRPC API) |
| `WILDCARD_RANDOM` | Поддержка `*` в SNI/host/address с random salt на стороне панели (генерируется в подписке) |
| `MULTI_PORT` | Multi-port inbound с random selection per fetch |
| `XHTTP_DOWNLOAD` | XHTTP `download_settings` — ссылка на другой host |

Флаги публикуются в API через `GET /api/cores` для UI и клиентских интеграций.

---

## 8. Узлы и агенты

### 8.1 Модель ноды
```
Node {
  id, name, region, provider, provider_ref,
  state: provisioning|active|degraded|suspended|decommissioned,
  public_ipv4, public_ipv6,
  agent_version, core_version, core_kind,
  inbound_set_id,
  tags[], last_heartbeat_at, last_config_revision,
  drain: bool,
  health: { cpu, ram, net, conn_count, uptime_s, score }
}
```

### 8.2 Agent

- Один бинарь, минимальные зависимости (musl/static).
- Каналы связи с панелью:
  - **Control channel (mTLS поверх WSS / gRPC-Web)** — получение новых конфигов, команд (drain, restart, update), аплоад статуса.
  - **Data plane** — проксирует пользовательский трафик через Core; **панель не в горячем пути**.
- Конфиг агента — на ноде (YAML): URL панели, fingerprint TLS, allowlist команд.
- Самовосстановление: watchdog Core (systemd unit / s6), автоперезапуск, backoff.
- Локальный кеш последней успешной конфигурации — на случай потери связи.

**Agent capabilities (минимум для MVP):**

| Capability | Описание |
| --- | --- |
| `apply_config` | Применить новый конфиг к Core (с авто-откатом при failure) |
| `get_status` | Текущее состояние Core (через stats API или process check) |
| `get_metrics` | CPU/RAM/Net/conn_count с ноды (собираем на стороне агента) |
| **`dynamic_user_add`** | Добавить клиента в существующий inbound **без рестарта Core** (через gRPC API ядра: `HandlerService.AddUser`) |
| **`dynamic_user_remove`** | Удалить клиента **без рестарта** |
| **`dynamic_user_list`** | Список активных клиентов на ноде |
| `get_user_traffic` | Per-user трафик (через `StatsService.QueryStats` Xray или sing-box API) |
| `restart_core` | Перезапуск Core (graceful, не роняя активные сессии если возможно) |
| `tunnel_*` | Для cascade topology: установка/удаление reverse-tunnel (Phase 2+) |

**API контракт Agent → Core:**
- Для Xray: gRPC `HandlerService.AddUser` / `RemoveUser` / `AlterInbound` + `StatsService.QueryStats` (для трафика).
- Для sing-box: REST API + in-memory `AddUser` / `RemoveUser` (через `sing-box` API ext).
- Capability `DYNAMIC_USERS` ядра определяет, поддерживается ли это; если нет — fallback на full restart (с потерей активных сессий, помечаем в логах).

**Гарантии:**
- Добавление/удаление юзера — O(1), не требует restart при `DYNAMIC_USERS=true`.
- При сбое Core — Agent рестартует его с последним конфигом.
- При сбое Agent — systemd поднимает его автоматически.

### 8.3 Состояния и переходы
```
provisioning ── apply cfg ok ─▶ active
active       ── health fail N ─▶ degraded
degraded     ── recovered     ─▶ active
active|degraded ── admin drain ─▶ draining ── sessions 0 ─▶ suspended
suspended    ── admin re-activate ─▶ provisioning (apply latest)
*            ── admin delete ─▶ decommissioned
```

---

## 9. Авто-развертывание (BYO Node)

### 9.1 Философия: Bring Your Own Node

**Панель НЕ создаёт и НЕ удаляет VPS.** Это ответственность оператора. Панель работает только с тем, что оператор сам арендовал у любого провайдера (Hetzner, OVH, Kimsufi, AWS, dedicated, домашний сервер — без разницы) и предоставил SSH-доступ.

**Что это даёт:**
- Минимальный код, низкий риск багов (не надо поддерживать API разных провайдеров, которые регулярно меняются)
- Совместимо с **любым хостингом** — даже с dedicated, домашним сервером, или VM в частном облаке
- Панель не хранит чувствительные API-ключи провайдеров (уменьшение attack surface)
- Оператор сохраняет полный контроль над жизненным циклом ноды (создание, биллинг провайдеру, удаление, апгрейд железа)
- Соответствует реальности рынка: большинство операторов VPN **уже имеют** пул серверов и не хотят давать панели доступ к API провайдера

**Что это НЕ даёт (явные ограничения):**
- Автоматический scale-up/scale-down в ответ на нагрузку
- Автоматическое создание ноды при онбординге нового клиента
- Единая панель управления жизненным циклом сервера (для этого есть провайдер)

### 9.2 Контракт (NodeBootstrapper)

```go
// Панель работает ТОЛЬКО с уже существующими нодами через SSH.
// CloudProvider API (Hetzner/AWS/DO) НЕ поддерживается намеренно.
type NodeBootstrapper interface {
    // Проверить SSH-доступ, ОС, ресурсы, свободные порты.
    // НЕ создавать ноду — только верифицировать что к ней можно подключиться.
    Probe(ctx, ref NodeRef) (NodeInfo, error)

    // Установить Agent на ноду через SSH. bootstrap_token нужен для
    // первого mTLS-handshake агента с панелью.
    InstallAgent(ctx, ref NodeRef, bootstrap_token string) error

    // Обновить Agent до новой версии (zero-downgrade: запустить новый,
    // дождаться готовности, остановить старый).
    UpgradeAgent(ctx, ref NodeRef, version string) error

    // Удалить Agent с ноды (Core остаётся, если был).
    UninstallAgent(ctx, ref NodeRef) error

    // Smoke-тест: проверить что Core запущен, порты слушаются,
    // панель видит heartbeat.
    RunSmokeTest(ctx, ref NodeRef) (SmokeResult, error)
}
```

### 9.3 Алгоритм онбординга ноды

1. **Оператор регистрирует ноду** в панели через UI/API:
   ```yaml
   node:
     name: "ams-01"
     address: "1.2.3.4"
     ssh_port: 22
     ssh_user: "root"             # или "ubuntu" с sudo
     ssh_auth:
       type: "key"                # или "password" (не рекомендуется)
       key_id: "<ref>"            # ссылка на SSH-ключ в secrets
     region: "eu-central"
     tags: ["production", "eu"]
   ```
2. **Панель запускает `Probe()`** — проверяет SSH-доступ, ОС, ресурсы (CPU/RAM/disk), свободен ли выбранный inbound-порт, исходящий доступ к панели и репо.
3. **Если ОК** — ставит задачу в очередь `bootstrap.<region>`, статус ноды → `provisioning`.
4. **Worker подключается по SSH**, выполняет Ansible-role `bootstrap_node`:
   - обновление apt/yum
   - установка Docker (если Core в Docker) или direct binary
   - открытие файрвола для inbound-порта и agent-порта
   - создание пользователя `panel-agent` (если нужно)
5. **Worker устанавливает Agent** через роль `install_agent`:
   - скачивает бинарь агента с панели (`GET /agents/{os}/{arch}/bin`)
   - регистрирует systemd unit
   - запускает с bootstrap-токеном
6. **Agent делает первый mTLS-handshake с панелью** по bootstrap-токену:
   - обменивает bootstrap на долгосрочный client cert
   - получает начальный конфиг Core
7. **Worker запускает `RunSmokeTest()`**:
   - проверяет что systemd unit активен
   - проверяет что inbound-порт слушается
   - проверяет что Core принял конфиг (через Agent API)
   - проверяет mTLS heartbeat от ноды
8. **Если всё ОК** → `active`. Если нет → `failed` с детальным логом для оператора.

### 9.4 Ansible-роли (хранятся в `deploy/ansible/<role>/`)

| Роль | Назначение | Идемпотентность |
| --- | --- | --- |
| `bootstrap_node` | Проверка окружения, установка Docker/standalone, открытие портов | ✅ |
| `install_agent` | Скачивание бинаря агента, systemd unit, запуск | ✅ |
| `upgrade_agent` | Zero-downtime обновление (запуск нового рядом, переключение, остановка старого) | ✅ |
| `uninstall_agent` | Чистое удаление с сохранением логов в `/var/log/panel-agent/` | ✅ |
| `smoke_test` | Финальная проверка работоспособности | — (read-only) |

**Идемпотентность — обязательна.** Повторный прогон на активной ноде = no-op. Это позволяет безопасно перезапускать provisioning при сбоях.

**Версионирование** — роли в репо, привязаны к релизам панели. Можно прогнать локально для отладки:
```bash
ansible-playbook -i inventories/local/ deploy/ansible/install_agent.yml
```

### 9.5 Что НЕ входит в зону ответственности панели

- ❌ Создание/удаление VPS через API провайдеров
- ❌ Управление DNS-записями, floating IP, load balancer'ами
- ❌ Автоскейлинг в ответ на нагрузку
- ❌ Оплата провайдеру
- ❌ Мониторинг billing'а провайдера (трафик сверх лимита, и т.п.)

Всё это — **на стороне оператора**. Панель отвечает только за управление тем, что уже есть.

### 9.6 Требования к ноде со стороны панели

**Минимум для онбординга:**
- **OS:** Ubuntu 22.04 / 24.04 / Debian 12 (amd64 или arm64)
- **SSH-доступ:** root или sudo-user (рекомендуется SSH-ключ, пароль допустим но не рекомендуется)
- **Исходящий доступ:** к панели (mTLS), к GitHub/релизам Core (apt/repo), к NTP-серверам, к Let's Encrypt (ACME)
- **Свободные inbound-порты** для маскировки: `443` (стандарт) + хотя бы один из `2053`/`2083`/`2087`/`2095`/`2096`/`8443` (Cloudflare-стиль)
- **Установленные/доступные пакеты:** Docker (для Core в контейнере) или standalone binary, **Caddy** (reverse proxy), **fail2ban** (защита SSH от брутфорса)
- **Минимум 1 vCPU / 1 GB RAM / 10 GB диска** для самой ноды (без учёта трафика юзеров)

**Рекомендуемый минимум для production:**
- 2 vCPU / 2 GB RAM / 40 GB SSD
- Аптайм SLA от провайдера ≥ 99.5%
- Anti-DDoS (если публичный IP не за Cloudflare/аналогом)
- **Caddy настроен на listen на 2-3 портах** для маскировки (см. раздел 19.4)
- **fail2ban активен** с jail на SSH (5 попыток / 10 мин → бан 1 час)

### 9.7 Потенциальные улучшения (Phase 2+, opt-in, не блокирует MVP)

- **Ansible-pull режим:** нода сама периодически тянет конфиг с панели через HTTPS. Полезно для нод за NAT/фаирволом, к которым панель не может достучаться по SSH напрямую. Требует reverse-инициации от ноды.
- **Bastion / jump host:** если нода за NAT/фаирволом, панель коннектится через jump-host. Настраивается per-node в SSH-config.
- **Health probe через SSH (fallback):** если mTLS heartbeat от Agent пропадает, панель шлёт простые команды через SSH (`systemctl status panel-agent`, `docker ps`, и т.п.) для диагностики.
- **Pre-flight checks library:** коллекция скриптов для проверки готовности ноды (открытые порты, MTU, часовой пояс, и т.п.) до начала provisioning.

Всё это — **отдельные модули**, не блокируют MVP. Базовый BYO-флоу полностью покрывает 95% кейсов.

---

## 10. Host manager

### 10.0 Сущности (введено в v3)

Три уровня абстракции — каждый со своей ответственностью:

| Сущность | Что это | Сколько |
|---|---|---|
| **Node** | Железка в ДЦ: SSH-доступ, health (CPU/RAM/net), drain, IP, регион. Узел может иметь несколько `Inbound` (Hysteria 2 + VLESS параллельно на одной машине — обычное дело). | 1+ |
| **Inbound** | Шаблон протокола на конкретной ноде (VLESS-Reality, Hysteria 2, Shadowsocks, …) + глобальные настройки (default port, default SNI). Хранится на ноде, apply-ится через CoreProvider. | 0+ на Node |
| **Endpoint** | Пара `(Node, Inbound)` + **override-слой** (SNI, port, transport, format variables). То, что в конечном счёте попадает в подписку — один URL. | 1+ на Host |
| **Host** | **Бандл endpoint'ов** с типом (`direct` / `balancer` / `chain`). То, что показывается в админ-UI и через что продукт продаётся пользователю. | 1+ |

**Ключевое отличие от v2:** в v2 Host был `(Node, Inbound)`-парой. Не получалось выразить кейс "3 ноды × 2 протокола = 6 протоколов в одной подписке" без ручного склеивания. В v3 `Host` это **всегда** бандл `endpoints[]`; `type=direct` означает `endpoints.length == 1`, `type=balancer` — `endpoints.length > 1` + стратегия. Никакой специальной ветки для balancer в data model.

**Пример — продукт "Латвия" с HY2 + VLESS на одной железке:**

```yaml
# В админке: один Host "Латвия", внутри 2 endpoint'а.
Host:
  id: uuid-lv
  remark: "Латвия"            # общий display name (можно с format variables)
  type: direct               # но endpoints.length = 2 — клиент увидит 2 строки
  enabled: true
  endpoints:
    - { node_id: lv-01, inbound_id: hysteria2, sni: ["cdn.example"], port: 443 }
    - { node_id: lv-01, inbound_id: vless,     sni: ["cdn.example"], port: 443 }
```

В подписке пользователь видит **2 строки** ("Латвия — Hysteria 2", "Латвия — VLESS"), что согласуется с UX реальных клиентов (Nekobox, Hiddify, V2Box): они показывают каждый endpoint как отдельный entry, и если на одной локации несколько протоколов — будет несколько строк с одним именем и разным протоколом.

**Пример — продукт "Premium" с fail-over на 3 нодах × 2 протокола:**

```yaml
Host:
  id: uuid-prem
  remark: "Premium EU"
  type: balancer
  strategy: leastLoad
  endpoints:
    - { node_id: nl-01, inbound_id: hysteria2, weight: 3 }
    - { node_id: nl-01, inbound_id: vless,     weight: 3 }
    - { node_id: de-01, inbound_id: hysteria2, weight: 2 }
    - { node_id: de-01, inbound_id: vless,     weight: 2 }
    - { node_id: fr-01, inbound_id: hysteria2, weight: 1 }
    - { node_id: fr-01, inbound_id: vless,     weight: 1 }
```

В подписке — 6 entries; клиент сам выбирает (или пробует все). Drain одной ноды или падение одного протокола = остальные 5 endpoints остаются рабочими.

### 10.1 Host — расширенная модель

Host — это **бандл endpoint'ов** + набор override'ов над параметрами inbound'а каждого endpoint'а. Публичный адрес endpoint'а не обязан совпадать с адресом ноды (типичный кейс — за Cloudflare).

**Маскировка под популярные веб-порты:** `port` хоста должен быть из списка `443`, `2053`, `2083`, `2087`, `2095`, `2096`, `8443` — это **реальный веб-трафик**, который DPI не может просто заблокировать. Caddy на ноде настраивается на listen на нескольких портах одновременно (см. раздел 19.4), что позволяет одному хосту отдаваться на разных портах. См. подробности в разделе 19.4.2.

**Полная модель Host:**

```yaml
Host:
  id: uuid
  remark: string                       # display name; supports format variables
  type: direct | balancer | chain      # NEW в v2
  enabled: bool
  priority: int                        # NEW: порядок в подписке (lower = выше)
  status_filter: [UserStatus]          # NEW: active | expired | limited | on_hold | disabled

  # === Direct & Balancer (общая база) ===
  # Каждый Host = бандл endpoint'ов. Endpoint = (Node, Inbound) + override-слой.
  # v3: было (node_id, inbound_id) — пара, стало endpoints[] — массив.
  endpoints:                            # 1+ endpoint'ов в подписке
    - endpoint_id: uuid                  # server-side generated
      node_id: uuid                      # железка
      inbound_id: uuid                   # шаблон протокола на этой железке
      address: [string, ...]             # override: set с random per request
      port: int | string                 # override: int или "8080,8443,9090"
      sni: [string, ...]                 # override: set с wildcard `*` (см. 10.1.3)
      host: [string, ...]                # override: set для HTTP/WS Host header
      path: string                       # override: path для WS / gRPC / XHTTP
      weight: int                        # NEW: per-endpoint weight (default 1)

  # === Security overrides (NEW) — default для всех endpoint'ов ===
  # Endpoint может override'нуть конкретное поле, иначе берётся это значение.
  security: inbound_default | none | tls | reality
  alpn: [h3, h2, http/1.1]             # auto-sorted по приоритету
  fingerprint: chrome | firefox | safari | edge | ios | android | none
  allow_insecure: bool
  ech_config_list: string

  # === Transport overrides (NEW, per-protocol) — default для всех endpoint'ов ===
  transport_settings:
    websocket: { heartbeat_period, ... }
    grpc: { multi_mode, idle_timeout, health_check_timeout, ... }
    kcp: { header, mtu, tti, uplink_capacity, ... }
    tcp: { header, request, response }
    xhttp: { mode, no_grpc_header, x_padding_bytes, ...,
             download_settings: <host_id> }    # NEW: ссылка на другой host
    mux:
      xray: { enabled, concurrency, xudp_concurrency, ... }
      sing_box: { enable, protocol: smux|yamux|h2mux, brutal: { up_mbps, down_mbps } }
    fragment:
      xray: { packets, length, interval }
      sing_box: { fragment, fragment_fallback_delay }
    noise:
      xray: [{ type, packet, delay, apply_to }]

  # === Advanced (NEW) — default для всех endpoint'ов ===
  use_sni_as_host: bool
  random_user_agent: bool
  http_headers: { Header-Name: value, ... }

  # === Display / meta ===
  display_name: string                 # алиас для remark (если не использовать format variables)
  country: string                      # ISO код, для UI и сортировки
  city: string
  latency_hint_ms: int                 # для geo-aware выдачи
  tags: [string]

  # === Balancer (см. 10.2) ===
  # v3: target_host_ids убран — балансер выбирает из endpoints[].
  # type=balancer с endpoints.length=1 трактуется как direct.
  balancer:
    entry_node_id: uuid                # на какой edge-ноде живёт балансер (Phase 2)
    strategy: leastLoad | roundRobin | random | leastPing | urltest
    healthcheck: { url, interval, tolerance_ms }
    failover_endpoint_ids: [uuid, ...]  # NEW: запасные endpoint'ы из этого же host

  # === Chain type (см. 10.3) ===
  chain:
    role: portal | relay | bridge
    mode: reverse | forward
    upstream_endpoint_id: uuid         # куда проксировать (Phase 2+)
    tunnel_port: int
    tunnel_reality: { dest, server_names, private_key, public_key, short_ids }
    transport: tcp | xhttp | grpc
```

**Приоритет значений (override chain):** `Endpoint value → Host value → Inbound value → System default`. Например, если у inbound'а `sni=["example.com"]`, у Host'а (по умолчанию для всех endpoint'ов) `sni=["cdn1.com", "cdn2.com"]`, а у конкретного endpoint'а `sni=["user-cdn.com"]` — в подписке именно этот endpoint получит `user-cdn.com`, остальные — `cdn1.com`/`cdn2.com`.

### 10.1.1 Format Variables (NEW)

Поля `remark` и `address` шаблонизируются через переменные, подставляемые на лету при fetch подписки. **Делает UX подписки персонализированным без доп. запросов в БД.**

| Variable | Описание | Пример |
| --- | --- | --- |
| `{SERVER_IP}` | Публичный IPv4 ноды | `1.2.3.4` |
| `{SERVER_IPV6}` | Публичный IPv6 | `2001:db8::1` |
| `{USERNAME}` | Имя юзера | `john_doe` |
| `{PROTOCOL}` | Протокол inbound'а | `vless` |
| `{TRANSPORT}` | Транспорт | `ws` |
| `{DATA_USAGE}` | Использовано трафика | `1.5 GB` |
| `{DATA_LIMIT}` | Лимит | `100 GB` или `∞` |
| `{DATA_LEFT}` | Остаток | `98.5 GB` или `∞` |
| `{DAYS_LEFT}` | Дней до конца | `30` или `∞` |
| `{EXPIRE_DATE}` | Дата (Gregorian) | `2026-08-15` |
| `{STATUS_EMOJI}` | Эмодзи статуса | `✅`, `⌛️`, `🪫`, `❌`, `🔌` |
| `{USAGE_PERCENTAGE}` | Процент использования | `15.5` |
| `{ADMIN_USERNAME}` | Создатель юзера | `admin` |

**Fallback при отсутствии значения:** даты/лимиты → `∞`, остальное → `<missing>`.

**Пример:**
```yaml
remark: "🇳🇱 {SERVER_IP} — {USERNAME} — {DATA_LEFT} — {STATUS_EMOJI}"
# Для john_doe с 87 GB остатком: "🇳🇱 ams-01.example.com — john_doe — 87 GB left — ✅"
```

**Реализация:** sandbox-шаблонизатор (Go `text/template` или Python Jinja2 в sandbox-режиме), без `eval`. Кеш по `(host_id, user_id, fetch_time_minute)` с TTL 60 сек, инвалидация при изменении host/user.

### 10.1.2 Wildcard `*` с random salt (NEW)

В полях `sni`, `host`, `address` можно указать паттерн с `*`. На каждый fetch подписки `*` заменяется на 8-char hex salt:

```yaml
sni: ["*.example.com"]
# Каждый fetch: "a1b2c3d4.example.com", "9f8e7d6c.example.com", "1f2e3d4c.example.com", ...
```

**Анти-детект:** DPI не может натренировать эвристику на конкретный домен. Особенно полезно с Reality — каждый коннект выглядит как новое legit-соединение.

**Реализация:** `salt = hex(8)`, `value = value.replace("*", salt)`, кеш результата на 60 сек.

### 10.1.3 Status-based visibility (NEW)

```yaml
status_filter: [active]              # только активные
status_filter: [active, on_hold]     # активные + на паузе
status_filter: []                    # все (default)
```

Дополняет существующий group-based filter (через inbound_tags → squads/pools). **Берём в MVP** — дешёво реализуется, полезно для tier-разделения хостов (например, «premium-хосты только для active»).

### 10.1.4 Multi-port inbound + random selection

Если у inbound'а несколько портов (`"8080,8443,9090"`) и у Host'а `port: null` — на каждый fetch подписки выбирается случайный. Полезно для port hopping и anti-DPI.

### 10.1.5 XHTTP `download_settings` (NEW)

```yaml
transport_settings:
  xhttp:
    download_settings: <host_id>  # ID другого host'а для download-операций
```

**Валидация:** referenced host не может иметь свой download host (no nesting). UI должен это явно показывать (host X → ссылается на host Y → Y нельзя использовать как download).

### 10.2 Pool (стратегии выдачи хостов)

- Набор Host'ов, выдаваемых в подписку.
- Стратегии (per-pool, runtime-переключаемые):
  - `all` — все включённые.
  - `round_robin` — сдвиг по пользователю (sticky на основе `user_id % N`).
  - `least_loaded` — выбор N наименее загруженных на момент запроса подписки (по `online/maxOnlineUsers`).
  - `geo_aware` — выбор ближайших по IP пользователя (ip2country + дешёвая distance-таблица).
  - `weighted` — пропорционально `host.weight`.
- **Antiaffinity:** если в Pool несколько хостов на одной ноде — выдавать только один (если включено в конфиге пула).
- **Tier-фильтрация:** Pool может быть привязан к Plan'у или индивидуально к User'у через `hosts_allowlist` / `hosts_blocklist`.

### 10.3 Cascade Topology (NEW, Phase 2+)

**Что это:** цепочки нод, где клиент подключается к одной (Portal), а трафик идёт через другую (Bridge), возможно через Relay. **Это НЕ балансировка нагрузки — это обход инфраструктурных ограничений.**

**Use-cases:**
- Portal за NAT / firewall (Reverse mode)
- Выход через IP «доверенного» хостера (operator-сети фильтруют по IP)
- Скрытие exit-IP от endpoint-сервисов
- Multi-hop для усложнения трассировки

#### 10.3.1 Режимы

**Reverse Chain** (Portal за NAT, Bridge за рубежом):

```
CLIENTS ──▶ Portal (entry, публичный или NAT)
              ▲
              │ (bridge сам инициирует туннель)
              │
           Bridge (exit, за NAT/abroad)
              │
           Internet
```

Bridge сам открывает persistent-соединение к Portal через `reverse` outbound. Portal проксирует трафик клиентов через этот туннель. **Portal может быть за NAT.**

**Forward Chain** (все ноды публичные):

```
CLIENTS ──▶ Portal ──▶ Relay (опц.) ──▶ Bridge ──▶ Internet
              (chained outbounds, все публичные)
```

Portal сам устанавливает соединения через цепочку outbound'ов.

#### 10.3.2 Xray-механизмы

| Механизм | Назначение |
| --- | --- |
| `reverse` outbound | Bridge инициирует туннель к Portal |
| Outbound chaining | Последовательное проксирование через `proxySettings.tag` |
| `transportLayer: true` | Корректная работа REALITY в hop-chains |
| REALITY между нодами | Шифрование межсерверного трафика (отдельные x25519 + shortIds) |

**Конфиг Portal (reverse mode) — упрощённо:**
```json
{
  "inbounds": [
    { "tag": "vless-in", "port": 443, "protocol": "vless", "settings": { "clients": [...] } }
  ],
  "outbounds": [
    {
      "tag": "tunnel",
      "protocol": "reverse",
      "settings": { "address": "127.0.0.1", "port": 4443, "flow": "xtls-rprx-vision" }
    }
  ],
  "routing": { "rules": [{ "inboundTag": ["vless-in"], "outboundTag": "tunnel" }] }
}
```

**Конфиг Bridge (reverse mode) — упрощённо:**
```json
{
  "inbounds": [
    {
      "tag": "tunnel-in",
      "port": 4443,
      "protocol": "vless",
      "settings": { "clients": [], "decryption": "none" },
      "streamSettings": {
        "network": "tcp",
        "security": "reality",
        "realitySettings": {
          "dest": "www.google.com:443",
          "serverNames": ["www.google.com"],
          "privateKey": "<bridge_x25519>",
          "shortIds": ["<auto>"]
        }
      }
    }
  ],
  "outbounds": [{ "tag": "direct", "protocol": "freedom" }]
}
```

#### 10.3.3 Ограничения (валидация в панели)

- **REALITY + WebSocket не работают** — WebSocket не поддерживает uTLS, REALITY требует uTLS fingerprint
- **Forward Chain требует public IP** на каждом хопе
- **Mixed modes в одной цепочке нельзя** — нельзя смешивать reverse и forward
- **На relay нельзя один порт для двух хопов** — relay, одновременно bridge и portal, требует разные порты
- **XHTTP в cascade** — в beta у Xray, поддержка по версии ядра

Эти ограничения проверяются в `CoreProvider.Capabilities()` и на уровне panel-валидатора при создании chain host.

#### 10.3.4 Алгоритм создания chain host

1. Панель генерирует x25519 keypair + 3-5 shortIds для туннеля.
2. Панель генерирует конфиг для upstream-ноды (Portal) с `reverse` outbound, указывающим на bridge.
3. Панель генерирует конфиг для bridge-ноды с `vless` inbound + REALITY на `tunnel_port`.
4. Валидация ограничений: если `mode: forward` + `public_ip: false` → reject.
5. Apply конфигов на обе ноды через Agent.
6. В подписке юзера chain host выглядит как обычный host с `address=portal_address, inbound=tunnel-in`. Клиент коннектится к Portal, всё остальное — дело Portal.

#### 10.3.5 Network Map UI

UI показывает ноды как nodes, связи между ними — как edges. Drag-and-drop для построения цепочек. Цвет ноды = роль (portal/relay/bridge), цвет edge = режим (reverse/forward). Вдохновлено Network Map у Celerity.

#### 10.3.6 MCP-операции (Phase 3+)

```
manage_cascade(action: create|update|delete|deploy|undeploy|reconnect, ...)
get_topology() → { nodes, edges }
```

AI-ассистент может через MCP построить cascade одной командой.

#### 10.3.7 Roadmap

- **MVP**: chain type **не включается**. Усложняет модель данных и UX.
- **Phase 2**: `chain` type с `reverse` mode, Network Map UI, базовые MCP-операции.
- **Phase 3**: `forward` mode, `relay` role, мульти-хоп, MCP-управление.
- **Phase 4**: ACL-фильтрация на bridge-ноде, политики ротации x25519.

### 10.4 Подписка — полная совместимость с клиентами

**Ключевое требование:** формат подписки должен поддерживаться **всеми популярными VPN-клиентами** — Hiddify, v2rayNG, v2rayN, Streisand, NekoBox, Nekoray, Shadowrocket, Clash Verge, Clash Meta, Karing, V2Box, sing-box CLI, и т.д. Юзер не должен быть привязан к конкретному клиенту.

#### 10.4.1 Endpoint и базовое поведение

- **Endpoint:** `GET /sub/{token}?target={format}`
- **Альтернативы для выбора формата:** query-параметр `?target=` ИЛИ HTTP-заголовок `Accept` ИЛИ автодетект по `User-Agent` (приоритет: query → Accept → User-Agent → default `base64`).
- **Контент кешируется в Redis** (TTL 60s), инвалидация при изменении Pool/Host/Node/User.
- **Можно положить за Cloudflare** для edge-кеширования (gzip-включён) с инвалидацией по webhook при изменении юзера/хоста.
- **Wildcard substitution** и **format variables** рендерятся на стороне панели в момент fetch, **не** сохраняются в кеше.

#### 10.4.2 Поддерживаемые форматы

| `target` | Формат | MIME | Кто использует |
| --- | --- | --- | --- |
| `singbox` / `sing-box` / `sb` | sing-box JSON | `application/json` | Hiddify Next, Streisand, NekoBox, Nekoray, Karing, V2Box, V2rayN, sing-box CLI |
| `clash` | Clash YAML (v1) | `text/yaml` | Clash for Windows (старый) |
| `clash-meta` / `meta` | Clash Meta YAML (расширенный) | `text/yaml` | Clash Verge, Clash Meta for Android, Clash for Windows (новый) |
| `base64` | base64 URI list | `text/plain` | v2rayNG, v2rayN, Shadowrocket, **fallback по умолчанию** |
| `v2ray` / `v2ray-json` | V2Ray JSON config | `application/json` | v2rayNG, v2rayN (legacy), V2Ray CLI |
| `html` | HTML sub-page (с QR-кодом и ссылками) | `text/html` | Браузер, ручной импорт |
| `auto` | автодетект по User-Agent (default) | по факту | если `?target` не указан |

#### 10.4.3 Маппинг User-Agent → формат (auto-детект)

Панель при запросе анализирует `User-Agent` и выбирает формат:

| User-Agent (substring) | Формат | Пример клиента |
| --- | --- | --- |
| `Hiddify*`, `HiddifyApp*`, `HiddifyNext*` | `singbox` | Hiddify Next (iOS/Android/Win/macOS/Linux) |
| `sing-box*` | `singbox` | sing-box CLI |
| `NekoBox*`, `Nekoray*`, `nekobox*` | `singbox` | NekoBox / Nekoray |
| `Karing*` | `singbox` | Karing |
| `Streisand*` | `singbox` | Streisand (iOS) |
| `V2Box*` | `singbox` | V2Box (iOS/Android) |
| `V2rayN*`, `v2rayN*` | `base64` | v2rayN (Windows) |
| `v2rayNG*`, `V2rayNG*` | `base64` | v2rayNG (Android) |
| `Shadowrocket*`, `shadowrocket*` | `base64` | Shadowrocket (iOS, платный) |
| `Clash*`, `mihomo*`, `ClashMeta*`, `ClashVerge*` | `clash-meta` | Clash Verge, Clash Meta, Clash for Windows |
| `Quantumult*`, `Loon*`, `Surfboard*` | `base64` (best effort) | проприетарные клиенты |
| `curl*`, `wget*`, `httpie*` | `base64` | CLI-инструменты, скрипты |
| `Mozilla*`, `Chrome*`, `Safari*`, `Firefox*` | `html` | браузер → sub-page |
| Unknown / empty | `base64` | safe default |

**Override через `?target=` всегда побеждает** User-Agent sniffing.

#### 10.4.4 HTTP-заголовки ответа

Панель выставляет заголовки, которые **большинство современных клиентов читают**:

```
HTTP/1.1 200 OK
Content-Type: application/json          # или text/yaml, text/plain, text/html
Content-Disposition: attachment; filename="subscription.json"
Profile-Update-Interval: 60             # секунд — как часто клиенту обновлять подписку
Profile-Title: Kawaii Smasher VPN       # заголовок подписки в клиенте
Subscription-Userinfo: upload=0; download=1234567890; total=10000000000; expire=1704067200
                             ↑ в байтах          ↑ в байтах                ↑ Unix timestamp
Profile-Web-Page-Url: https://panel.example.com     # опционально: ссылка на ЛК
Support-Url: https://t.me/support_chat              # опционально: ссылка на поддержку
Cache-Control: public, max-age=60
```

**`Subscription-Userinfo`** — формат как в shadowsocks/sing-box subscriptions: `upload; download; total; expire`. Клиент (Hiddify, Clash Meta, v2rayN) показывает юзеру остаток трафика и дату истечения прямо в подписке.

**`Profile-Update-Interval`** — клиент не должен долбить панель чаще этого. По умолчанию 60 (1 час), но в реальности — 6-12 часов (зависит от нагрузки).

#### 10.4.5 Sub-page (HTML)

Для браузера и ручного импорта — `?target=html` (или auto-detect по `Mozilla*`):

- Логотип панели/бренда
- Заголовок «Kawaii Smasher VPN»
- QR-код subscription URL (для скана в мобильном клиенте)
- Список клиентов со ссылками на скачивание (Hiddify, v2rayNG, Streisand, и т.д. — список настраивается per-panel)
- Информация о юзере: username, expire, traffic used/limit, status
- Ссылка на ЛК / Telegram support

**Open-source sub-page проекты** (можно форкнуть):
- [remnawave/subscription-page](https://github.com/remnawave/subscription-page) — современный
- [Marzban sub-page templates](https://github.com/Gozargah/Marzban) — есть встроенные

В MVP — **простая статическая HTML-страница** + один из open-source шаблонов как референс (Vue 3 SPA-бандл с минимальным shell'ом). В Phase 2 — кастомизируемый sub-page с темами и per-user branding.

#### 10.4.6 Особенности форматов (для разработчиков)

**Sing-box JSON:**
- Структура: `{"outbounds": [...], "route": {...}, "dns": {...}}`
- `selector` и `urltest` для auto-выбора ноды в клиенте
- Outbound'ы с тегами `proxy-1`, `proxy-2`, ... (или кастомные)
- Поддержка `mux`, `fragment` через `transport_settings`
- `default` outbound — `direct` (если ничего не выбрано)

**Clash Meta YAML:**
- Структура: `proxies: [...], proxy-groups: [...], rules: [...]`
- Proxy groups с `type: select` (ручной выбор) или `type: url-test` (auto по latency)
- `rule-providers` для продвинутых routing
- Поддержка `smux`/`h2` mux, `fake-packet`/`fake-packet-str` fragment
- `proxy-groups[].proxies` может содержать имена proxies или вложенные группы

**base64 URI list:**
- Каждая строка — base64-кодированный URI формата `protocol://user:pass@host:port?...#name`
- Поддерживаемые схемы: `vless://`, `vmess://`, `trojan://`, `ss://`, `hysteria2://`, `tuic://`
- Каждый URI = один хост в клиенте
- `Content-Disposition: attachment; filename="list.txt"` для скачивания

**V2Ray JSON:**
- Полный v2ray-конфиг: `{"inbounds": [...], "outbounds": [...], "routing": {...}}`
- Inbound = обычно `socks` или `http` на 127.0.0.1 (transparent proxy на стороне клиента)
- Outbound'ы по одному на хост
- Более старый формат, не рекомендуется для новых клиентов

#### 10.4.7 Минимальное покрытие (MVP)

В MVP поддерживаем **3 формата, покрывающих ~95% пользователей:**

1. ✅ `singbox` — Hiddify, Streisand, NekoBox, Karing, V2Box, sing-box CLI (современный стандарт)
2. ✅ `clash-meta` — Clash Verge, Clash Meta for Android (массовый на десктопе)
3. ✅ `base64` — v2rayNG, Shadowrocket, v2rayN, fallback (legacy, но популярен на мобильных)

**Остальные форматы** (clash v1, v2ray JSON, html sub-page) — Phase 2+, не блокируют MVP. Sub-page можно сделать сразу как статическую HTML-страницу, это не код-работа, а вёрстка.

#### 10.4.8 Тестовая матрица (для CI / ручной проверки)

При разработке и релизе проверяем, что подписка работает в:

| Клиент | Платформа | Формат | Проверяем |
| --- | --- | --- | --- |
| Hiddify Next | iOS / Android | singbox | Импорт, коннект, переключение хостов, urltest |
| Hiddify Next | Windows / macOS / Linux | singbox | То же + sys tray |
| Streisand | iOS | singbox | Импорт, коннект |
| v2rayNG | Android | base64 | Импорт, коннект, переключение |
| v2rayN | Windows | base64 / v2ray | Импорт, коннект |
| Shadowrocket | iOS | base64 | Импорт, коннект |
| Clash Verge | Windows / macOS / Linux | clash-meta | Импорт, proxy-groups, rules, urltest |
| Clash Meta for Android | Android | clash-meta | Импорт, коннект |
| Nekoray | Windows / Linux | singbox | Импорт, коннект |
| Karing | iOS / Android / Win | singbox | Импорт, коннект |
| sing-box CLI | Linux | singbox | `sing-box check`, `sing-box run` без ошибок |

**Автотесты** (в CI):
- Snapshot-тесты для каждого формата (golden file)
- JSON-schema валидация sing-box и v2ray JSON
- YAML schema валидация Clash
- URI parser для base64 (round-trip test)

### 10.5 Bulk Group Operations (NEW)

```
POST /api/groups/bulk/add
{
  "group_ids": [1, 2],
  "users": [10, 11, 12],       # конкретные юзеры
  "admins": [5, 6],            # все юзеры этих админов
  "has_group_ids": [3]         # только те, у кого уже есть группа 3
}
```

Если ни `users`, ни `admins` не указаны — действует на всех юзеров. Существующие ассоциации игнорятся (no duplicates). Аналогично `bulk/remove`.

---

## 11. Конфигурация протоколов

### 11.1 Шаблоны
- Хранятся в БД, JSON, проходят валидацию по JSON-схеме соответствующего ядра.
- Переменные шаблона: `{{ port }}`, `{{ dest }}`, `{{ cert }}`, `{{ users_via_path }}` и т. п. — Jinja-style (быстро и понятно).
- Sandbox-рендер (без `os.system` / `eval`) — `text/template` или `pongo2`.
- Dry-run API: `POST /inbounds/{id}/render` → возвращает финальный JSON и diff с активным.

**Decoy-fallback (NEW, раздел 26):** в шаблон inbound'а можно включить `fallback` — если handshake не прошёл (невалидный UUID, плохой Reality fingerprint), sing-box/Xray отправляет запрос на fallback-сервер (обычно Caddy с decoy-сайтом). Это **двойной уровень маскировки**: Reality маскирует TLS, fallback маскирует невалидные подключения.

```json
// Sing-box: fallback в inbound
{
  "inbounds": [{
    "type": "vless",
    "listen": "127.0.0.1",
    "listen_port": 10000,
    "fallback": {
      "server": "127.0.0.1",
      "server_port": 8080   // Caddy с decoy
    }
  }]
}

// Xray: fallbacks в streamSettings
{
  "streamSettings": {
    "realitySettings": {
      "serverNames": ["www.google.com"],
      "dest": "google.com:443",
      "fallbacks": [
        { "path": "/_/proxy-a1b2c3", "xver": 2, "dest": 10000 }
      ]
    }
  }
}
```

### 11.2 Per-Host Transport Overrides (NEW)

Host может **override'ить transport-специфичные параметры** поверх inbound'а. Полный набор — из раздела 10.1, ключевые блоки:

- **WebSocket:** `heartbeat_period`
- **gRPC:** `multi_mode`, `idle_timeout`, `health_check_timeout`, `permit_without_stream`, `initial_windows_size`
- **KCP:** `header`, `mtu`, `tti`, `uplink_capacity`, `downlink_capacity`, `congestion`
- **TCP:** `header: "http" | "none"`, `request`, `response`
- **XHTTP / SplitHTTP:** `mode`, `no_grpc_header`, `x_padding_bytes`, `xmux`, **`download_settings` (ref на другой host)**
- **Mux:** `xray: { enabled, concurrency, xudp_concurrency, xudpProxyUDP443 }`, `sing_box: { protocol, max_connections, max_streams, brutal: { up_mbps, down_mbps } }`
- **Fragment:** `xray: { packets, length, interval }`, `sing_box: { fragment, fragment_fallback_delay }`
- **Noise:** `xray: [{ type: rand|str|base64|hex, packet, delay, apply_to: ip|ipv4|ipv6 }]`

Каждый блок проходит JSON-schema валидацию соответствующего ядра. `CoreProvider.ValidateConfig()` гарантирует корректность нативного конфига.

### 11.3 Версионирование
- Каждое применение = новая ревизия: `revision_id`, `applied_at`, `applied_by`, `result`, `rollback_of`.
- На ноде хранятся последние N ревизий, откат одной командой.
- Аудит: кто, что, когда, с какой целью (поле `comment`).

### 11.4 Безопасность конфигов
- Секреты (private key, cert) **никогда** не пишутся в БД в открытом виде — генерируются/расшифровываются на лету через Vault/SOPS.
- При передаче агенту — mTLS + AEAD-обёртка.
- x25519 ключи для chain-tunnel'ов **генерируются панелью** и хранятся в encrypted-форме. Public key отдаётся в подписке, private — только агенту bridge-ноды через mTLS.

---

## 12. Пользователи, планы, трафик

### 12.1 Модель
```
User {
  id, external_id (из ЛК), status, plan_id, expire_at,
  traffic_limit_bytes, traffic_used_bytes, last_reset_at,
  device_limit, hosts_allowlist, hosts_blocklist,
  sub_token (rotated), telegram_id, email
}
Plan {
  id, name, traffic_limit_bytes, duration, host_pool_ids[],
  device_limit, price (опц.), reset_period
}
```

### 12.2 Подсчёт трафика
- Агент шлёт инкременты: `(user_id, bytes_up, bytes_down, ts)`.
- Панель агрегирует в ClickHouse / Timescale (быстрые аналитические запросы), в PostgreSQL — только текущий остаток.
- Сверка раз в N минут; при превышении — событие `user.exceeded` → Cabinet/notification.
- Сброс по cron'у согласно `reset_period`.

### 12.3 Статусы
`active → grace → disabled → expired → deleted`
- `grace` — короткий период (например, 72ч) после окончания оплаты, отключение при превышении лимита.
- `disabled` — пользователь существует, не пускаем; ЛК может включить.
- `expired` — финал, автоудаление через retention.

---

## 13. API для ЛК (Cabinet)

### 13.1 Контракт
- Базовый URL: `https://panel.example/api/v1/cabinet/...`
- Аутентификация: `Authorization: Bearer <cabinet_token>`, выдаётся из панели. Опционально — mTLS.
- Идемпотентность: заголовок `Idempotency-Key` для POST/PUT/PATCH/DELETE.
- Rate-limit: 100 rps на токен, 429 при превышении с `Retry-After`.
- Версионирование: мажор в URL, минор — заголовок `X-Api-Minor-Version`.
- Формат: JSON, snake_case, ISO-8601 для времени.

### 13.2 Эндпоинты (набор MVP)
| Метод | URL | Описание |
| --- | --- | --- |
| `POST` | `/users` | Создать пользователя (после оплаты) |
| `GET` | `/users/{id}` | Карточка пользователя |
| `PATCH` | `/users/{id}` | Продлить, изменить лимит, заблокировать |
| `DELETE` | `/users/{id}` | Удалить |
| `GET` | `/users/{id}/subscription` | Вернуть sub-token + URL |
| `POST` | `/users/{id}/subscription/rotate` | Ротация ссылки |
| `GET` | `/users/{id}/traffic?from&to` | Сырой трафик |
| `GET` | `/hosts` | Список пулов и хостов для витрины ЛК |
| `GET` | `/plans` | Список тарифов |
| `POST` | `/webhooks/payment` | Подтверждение оплаты (идемпотентно) |
| `POST` | `/webhooks/test` | Smoke-проверка интеграции |
| `GET` | `/health` | Проверка доступности панели для ЛК |

### 13.3 События наружу (webhooks от панели в ЛК)
- `user.expired`, `user.exceeded`, `user.disabled`, `subscription.rotated`, `node.down`, `node.drained`.
- **Полный набор событий:**

| Event | Триггер |
| --- | --- |
| `user.created` | Юзер создан |
| `user.updated` | Юзер обновлён (лимит, expire, status) |
| `user.deleted` | Юзер удалён |
| `user.enabled` / `user.disabled` | Включение/отключение |
| `user.traffic_exceeded` | Достигнут лимит трафика |
| `user.expired` | Истёк срок подписки |
| `node.online` / `node.offline` / `node.error` | Состояние ноды |
| `node.drained` | Нода выведена из ротации (drain mode) |
| `host.disk_low` / `host.disk_critical` / `host.disk_recovered` | **NEW**: дисковые алерты с hysteresis (см. ниже) |
| `subscription.rotated` | Ротация sub-token |
| `cascade.deployed` / `cascade.failed` | **NEW (Phase 2+)** |
| `sync.completed` | Цикл синхронизации юзеров на ноды завершён |

### 13.4 Webhook — подпись HMAC-SHA256 (NEW)

Каждый webhook подписывается:

```
POST /webhook HTTP/1.1
Content-Type: application/json
X-Webhook-Event: user.created
X-Webhook-Timestamp: 1700000000
X-Webhook-Signature: sha256=<hmac>
User-Agent: Panel-Webhook/1.0

{ "event": "user.created", "timestamp": "...", "data": {...} }
```

**Подпись:**
```
signature = "sha256=" + HMAC_SHA256(secret, "${timestamp}.${rawBody}")
```

**Anti-replay:** reject если `abs(now - timestamp) > 5min`.

**Проверка на стороне получателя:**
```python
expected = "sha256=" + hmac.new(
    secret.encode(),
    f"{timestamp}.{raw_body}".encode(),
    hashlib.sha256
).hexdigest()
assert hmac.compare_digest(expected, signature)
```

**Secret per endpoint:** хранится в `WebhookEndpoint.secret` (см. раздел 16), выдаётся при создании endpoint'а, **показывается один раз** как при создании API-ключа.

### 13.5 Disk alerts с hysteresis (NEW)

Паттерн Celerity — избегаем спама алертов.

- `host.disk_low` — `free_space < warning_percent` (default 20%) — **один раз** при пересечении threshold
- `host.disk_critical` — `free_space < critical_gb` (default 5 GB) — **один раз**
- `host.disk_recovered` — `free_space > warning_percent` (восстановление) — **только если до этого был `low`**

**Реализация в event bus:** храним `last_disk_state: ok | low | critical` per node. На новом sample: если пересекли threshold и `state` изменился — emit event.

Thresholds настраиваются per-panel (Settings → Security → Webhooks).

### 13.6 API Scopes (NEW)

Заимствуем naming у Celerity, применяется к API-ключам Cabinet API и будущим MCP-токенам:

| Scope | Описание |
| --- | --- |
| `users:read` / `users:write` | CRUD юзеров |
| `nodes:read` / `nodes:write` / `nodes:control` | CRUD нод + restart core, drain |
| `hosts:read` / `hosts:write` | CRUD хостов и пулов |
| `cascades:read` / `cascades:write` | Phase 2+: cascade topology |
| `stats:read` | Чтение статистики |
| `sync:write` | Trigger sync на ноды |
| `system:read` | Healthchecks, audit log |
| `mcp:invoke` | Phase 2+: для MCP-токенов |

Per-key rate-limit (default 60 req/min), IP allowlist, передача через `X-API-Key` или `Authorization: Bearer`.

### 13.7 Соглашение о совместимости
- Любое breaking change = новая версия в URL.
- Deprecation notice за 2 релиза вперёд + feature-флаг на стороне панели.

---

## 14. Мониторинг и наблюдаемость

### 14.1 Метрики (Prometheus)
- `node_*` (CPU/RAM/Net/conn/score), `node_health_state`.
- `core_*` (active_connections, bytes_in/out, restart_count, version).
- `user_online` (gauge), `user_traffic_bytes_total` (counter per user).
- `panel_http_request_duration_seconds`, `panel_queue_depth`.
- `agent_apply_config_total{result="ok|fail"}`, `agent_last_seen_seconds`.

### 14.2 Heartbeat
- Агент → панель каждые 10s (настраиваемо). Если `last_seen > 30s` → `degraded`, `>120s` → `down`, автоdrain.

### 14.3 Healthcheck
- Liveness: `/healthz` (процесс жив).
- Readiness: `/readyz` (БД, Redis, NATS доступны).
- Глубокий: `/readyz/deep` (включает проверку связи с нодами — off по умолчанию).

### 14.4 Логи
- Структурные JSON, `slog` / `zap` / аналог.
- Корреляция через `request_id` и `trace_id`.
- Централизация: Loki (или OpenSearch, если уже есть).

### 14.5 Трейсинг
- OpenTelemetry SDK, экспорт OTLP.
- Основные спаны: HTTP-обработчики, `RenderConfig`, `Apply` (panel → agent), DB-вызовы.

---

## 15. Безопасность

| Слой | Решение |
| --- | --- |
| **Edge proxy (panel)** | **Caddy** (auto HTTPS через Let's Encrypt / ZeroSSL, ACME встроенный, HTTP/3 из коробки). Security headers, rate-limit, on-demand TLS. |
| **Edge proxy (nodes)** | **Caddy** на каждой ноде как reverse proxy к sing-box/Xray на `127.0.0.1:internal_port`. Терминация TLS на Caddy, маскировка под обычный HTTPS-сервер. |
| **Brute-force protection** | **fail2ban** на SSH (5 попыток / 10 мин → бан 1 час) + кастомный jail на Panel login (401/403 responses) + опционально на agent mTLS (не критично — нет пароля). |
| **Маскировка портов** | Используем популярные веб-порты, которые **не блокируются DPI**: `443` (стандарт), `2053`/`2083`/`2087`/`2095`/`2096` (Cloudflare), `8443` (alt HTTPS). Caddy настраивается на listen на нескольких портах одновременно. |
| **Decoy-сайты (NEW)** | На панели и нодах — HTML-заглушка на «обычных» путях. Случайный проверяющий видит личный блог / IT-компанию / SaaS-лендинг, а не VPN. Реальный proxy/adminkа доступны только по секретному пути. См. раздел 26. |
| Panel↔Agent | mTLS (самоподписанный CA панели), опционально WireGuard-туннель как overlay |
| Panel↔Browser | HTTPS через Caddy, HSTS, secure cookies, CSP, X-Frame-Options DENY |
| Секреты | HashiCorp Vault / SOPS-encrypted files. Никаких приватных ключей в БД в открытом виде |
| Auth админа | Argon2id пароли, JWT (короткий TTL) + refresh-rotation, MFA-ready |
| Audit log | Все мутации (config, user, node) с `actor`, `before/after`, `ip` |
| Rate limiting | Token-bucket per IP/user/token; 429 + Retry-After (Caddy + приложение двухуровнево) |
| Input validation | JSON-schema + whitelists, никаких `eval`/динамических шаблонов в shell |
| Зависимости | SCA (Trivy/Grype), pin версий, renovate-bot |
| Backups | Ежедневный snapshot БД + конфиги нод (в виде артефактов) → S3-compatible |
| DDoS | На уровне upstream (Cloudflare / провайдер) — панель за Cloudflare, ноды по возможности тоже |
| Разделение ролей | RBAC: `super-admin`, `operator`, `viewer` (на MVP — минимум, но архитектурно готово) |

**Зачем именно Caddy:**
- **Auto HTTPS из коробки** — не нужен certbot, ACME-challenge, ручной renewal. Сертификаты обновляются сами за 30 дней до истечения.
- **HTTP/3 (QUIC)** — поддержка из коробки, без отдельной настройки.
- **Caddyfile** — простой и читаемый конфиг, не DSL nginx.
- **On-demand TLS** — может получить сертификат для нового домена "на лету" при первом запросе.
- **Cloudflare DNS-01 challenge** встроенный (для wildcard-сертификатов).
- **API для hot-reload** — можно менять конфиг без рестарта (полезно для auto-deploy).

**Зачем именно fail2ban:**
- Защита SSH от брутфорса — стандартная практика.
- Защита Panel login — критично, потому что панель публично доступна (хотя и за Cloudflare).
- Защита Agent mTLS — обычно не нужна (mTLS не подбирается), но если Agent слушает на публичном IP (а не только на localhost), можно добавить.

**Зачем маскировка под популярные порты:**
- DPI провайдера может блокировать нестандартные порты (например, если он видит TLS на порту 12345, может сбросить).
- Порты `443`, `80`, `2053`, `2083`, `2087`, `8443` — это **реальный веб-трафик**, который нельзя просто заблокировать (сломается половина интернета).
- Caddy настраивается на listen на нескольких портах — клиент выбирает любой.

---

## 16. Модель данных (укрупнённо)

**PostgreSQL (operational)**
- `admins`, `roles`, `permissions`, `audit_log`
- `users`, `plans`, `plan_pool`
- `nodes`, `node_tags`, `node_health_history`
- `inbound_sets`, `inbound_templates`, `inbound_revisions`
- `hosts`, `host_pools`, `host_pool_members` (NEW: `hosts` расширена — см. раздел 10.1)
- `subscriptions`, `subscription_rotations`
- `agents`, `agent_tokens` (одноразовые для bootstrap)
- `cabinet_tokens`, `api_keys`, `api_key_scopes` (NEW: scopes-based авторизация)
- `webhook_endpoints` (NEW: c HMAC secret, см. 13.4)
- `events_outbox` (transactional outbox для надёжной доставки)
- `disk_alert_state` (NEW: per-node last disk state для hysteresis)
- **`cascades`, `cascade_hops`** (NEW: Phase 2+)
- **`mcp_config`, `mcp_tokens`** (NEW: Phase 2+, см. раздел 22)
- **`decoy_sites`, `decoy_audits`, `panel_path_config`** (NEW: decoy-сайты и секретные пути, см. раздел 26)

**ClickHouse / Timescale (metrics)**
- `user_traffic_events` (ts, user_id, host_id, bytes_up, bytes_down)
- `node_health_samples` (ts, node_id, cpu, ram, net, conn, score)
- `core_stats_samples` (ts, node_id, conn, bytes_up, bytes_down)
- `disk_samples` (NEW: ts, node_id, free_bytes, total_bytes — для hysteresis)

**Redis**
- кеш подписок (TTL 60s), сессии админов, rate-limit counters, ephemeral locks, dedup-ключи для идемпотентности.

**Событийный слой (NATS / Redis Streams)**
- Топики: `node.config.applied`, `node.config.failed`, `user.exceeded`, `user.expired`, `node.drain.requested`, **`cascade.deploy.requested`, `cascade.reconnect.requested`** (Phase 2+).

**Детали новых сущностей:**

```yaml
WebhookEndpoint:
  id: uuid
  url: string
  secret: string                     # для HMAC, показывается один раз при создании
  events: [string, ...]              # фильтр событий
  enabled: bool
  created_at: timestamp
  last_delivery_at, last_status_code

Cascade:
  id: uuid
  name: string
  mode: reverse | forward
  enabled: bool
  created_at, updated_at

CascadeHop:
  id: uuid
  cascade_id: uuid
  position: int                      # 0 = entry, N = exit
  node_id: uuid
  role: portal | relay | bridge
  tunnel_port: int
  transport: tcp | xhttp | grpc
  reality:
    enabled: bool
    dest: string
    server_names: [string, ...]
    private_key: <encrypted>         # НЕ открытым текстом
    public_key: string
    short_ids: [string, ...]

McpConfig:
  enabled: bool
  bind: string                       # default "127.0.0.1:8081"
  auth_type: oauth2 | api_key
  rate_limit_rpm: int                # default 100
  allowed_tools: [string, ...]       # whitelist tools

McpToken:
  id: uuid
  name: string                       # "Claude Assistant"
  token_hash: string                 # НЕ сам token
  scopes: [string, ...]              # как в API-ключах
  created_at, last_used_at, expires_at

DiskAlertThresholds:                 # singleton в panel settings
  warning_percent: int               # default 20
  critical_gb: int                   # default 5
  hysteresis_enabled: bool           # default true
```

---

## 17. MCP-интеграция (NEW, Phase 2+)

**Model Context Protocol (MCP)** — стандарт для AI-ассистентов (Claude Desktop, Cursor, Continue) для прямого вызова инструментов панели. **В индустрии панелей это уникальная фича — на момент анализа только Celerity её реализовал.**

### 17.1 Архитектура

- MCP-сервер встроен в бинарь панели (опционально) или отдельный sidecar.
- **Только localhost** (Unix socket или `127.0.0.1:8081`) — **никогда не светить наружу**.
- Авторизация — API-ключ панели с минимальными scopes.
- Все вызовы логируются в `audit_log`.

### 17.2 Tool set (Phase 2)

```
# Users
list_users, get_user, create_user, update_user, delete_user, enable_user, disable_user

# Nodes
list_nodes, get_node, get_node_status, get_node_metrics, restart_core, drain_node

# Hosts
list_hosts, get_host, create_host, update_host, enable_host, disable_host

# Cascades (Phase 3)
list_cascades, get_cascade, manage_cascade (create|update|delete|deploy|undeploy|reconnect)
get_topology                                          # → { nodes, edges }

# Stats
get_stats, get_user_traffic, get_node_metrics

# System
get_health, get_audit_log
```

### 17.3 Сценарий

Оператор подключает Claude Desktop к панели через MCP, говорит:

> «У меня 5 новых нод в EU (список IP и SSH-ключей), зарегистрируй их в панели, установи агентов, настрой cascade с portal в Frankfurt и bridge в Амстердаме, проверь что всё работает, пришли отчёт»

AI вызывает tools в нужной последовательности: `create_node(ssh_creds)` × 5 → `install_agent(node_id)` × 5 → `manage_cascade(create)` → `manage_cascade(deploy)` → `get_topology` → `get_health` → текстовый отчёт оператору.

**Важно:** панель не создаёт VPS — это уже сделал оператор. AI только оркестрирует то, что оператор прислал.

### 17.4 Безопасность

- MCP-server **только на localhost** (Unix socket или 127.0.0.1)
- API key с минимальными scopes (принцип наименьших привилегий)
- Все вызовы → `audit_log` (actor, action, args, result, ts)
- Rate-limit per token (default 100 req/min)
- **Dry-run mode** для деструктивных операций (create/delete/deploy/destroy)
- Whitelist tools через `McpConfig.allowed_tools`

---

## 18. Технологический стек (рекомендация)

> Решения **по умолчанию**; обоснование и альтернативы ниже.

| Слой | Выбор | Почему |
| --- | --- | --- |
| Backend | **Go 1.22+** | Бинарь в одном файле, отличная concurrency, экосистема под VPN, опыт Remnawave/Marzneshin/3x-ui. Альтернатива: Python+FastAPI (быстрее MVP, слабее runtime). |
| Web-framework | `chi` или `gin` | Лёгкие, проверенные. |
| DB | **PostgreSQL 16** | Надёжность, расширения, JSONB для шаблонов. |
| Метрики БД | **ClickHouse** | Дешёвые агрегации по user-traffic. Альтернатива: TimescaleDB (проще, но дороже на объёмах). |
| Кеш/очереди | **Redis 7** + **NATS JetStream** | Redis — кеш, NATS — durable очереди, retry, DLQ. Альтернатива: Redis Streams для всего. |
| UI (admin) | **Vue 3 + TypeScript + Vite + Tailwind** | SPA, единый стек, простой деплой как статический бандл. |
| Observability | **Prometheus + Grafana + Loki + Tempo** | Стандарт, всё self-host. |
| **Edge proxy (panel + nodes)** | **Caddy** | Auto HTTPS из коробки (Let's Encrypt / ZeroSSL, ACME встроенный), HTTP/3, читаемый Caddyfile, hot-reload API, Cloudflare DNS-01 для wildcard. **Заменяет nginx + certbot.** |
| **Brute-force protection** | **fail2ban** | SSH + Panel login (кастомный jail). |
| Secrets | **SOPS + age** (простой путь) или **Vault** (если уже есть команда) | SOPS проще в MVP, Vault — если планируется рост. |
| Деплой | **Docker Compose** на старте, **Helm + k8s** по мере роста | Не over-инжинирим до момента X. |
| CI/CD | **GitHub Actions** | Бюджетно, привычно. |
| IaC для нод | **Ansible** | Достаточно для нод, Terraform не нужен. |

---

## 19. Развёртывание

### 19.1 Dev / staging
- `docker compose up` — Panel + Caddy + Postgres + Redis + NATS + MinIO (для артефактов).
- Caddy в dev-режиме использует самоподписанные сертификаты или HTTP-only (для localhost).
- Локальные ноды — через `vagrant` или LXC, не обязательно в CI.

### 19.2 Prod (MVP)
- Один VPS под Panel (4 vCPU / 8 GB / 80 GB NVMe) — на старте хватит.
- БД и Redis — на отдельной VM (или managed: Postgres в Selectel/Yandex, Redis Upstash или self-host).
- Ноды — **BYO Node** (раздел 9): оператор сам арендует, панель подключается по SSH.
- TLS — **Caddy** с auto HTTPS через Let's Encrypt / Cloudflare DNS-01.
- Backups: `pg_dump` ежедневно + WAL-archive, отправка в S3.

### 19.3 Prod (scale)
- Panel за k8s (2+ реплики, sticky не нужен).
- БД — managed Postgres, отдельные read-replica для отчётов.
- ClickHouse — 2 ноды (shard + replica).
- Outbox-релей → NATS → webhook-delivery-сервис.
- CDN (Cloudflare) перед панелью и нодами (для синхронизации).

**Decoy-сайты (NEW, раздел 26):** HTML-заглушка на «обычных» путях — личный блог, IT-компания, SaaS-лендинг. Реальный proxy/admin доступны по секретному пути. На панели: `panel.example.com/` отдаёт decoy, `/s3cr3t-p4n3l-xyz/` — админка. На нодах: `node01.example.com/` отдаёт decoy, `/_/proxy-a1b2c3/` — sing-box. **Секретные пути рандомизируются при install** (через `openssl rand -hex 6`).

### 19.4 Caddy — reverse proxy (NEW)

**Caddy используется на панели И на каждой ноде.** На нодах он выступает как frontend перед sing-box/Xray, терминирует TLS, маскирует под обычный HTTPS-сервер.

#### 19.4.1 Caddyfile панели

```caddyfile
# /etc/caddy/Caddyfile (panel)
{
    # Cloudflare DNS-01 для wildcard (нужен Caddy с cloudflare plugin)
    acme_dns cloudflare {env.CLOUDFLARE_API_TOKEN}
    email admin@panel.example.com
}

# API + Admin UI + Subscription endpoint
panel.example.com, *.panel.example.com {
    reverse_proxy panel_backend:8080

    encode gzip zstd

    # Security headers
    header {
        Strict-Transport-Security "max-age=31536000; includeSubDomains"
        X-Content-Type-Options "nosniff"
        X-Frame-Options "DENY"
        Referrer-Policy "strict-origin-when-cross-origin"
        # CSP — настраивается per-panel, не блокируем admin UI
        # Content-Security-Policy "default-src 'self'; ..."
    }

    # Rate limit (token-bucket per IP)
    # Caddy использует `rl` matcher через модуль github.com/mholt/caddy-ratelimit
    @api path /api/*
    rate_limit @api 100r/m {remote.ip}

    # Access log в JSON для парсинга Loki
    log {
        output file /var/log/caddy/access.log {
            roll_size 50MiB
            roll_keep 5
        }
        format json
    }
}
```

#### 19.4.2 Caddyfile ноды (маскировка)

```caddyfile
# /etc/caddy/Caddyfile (node)
{
    acme_dns cloudflare {env.CLOUDFLARE_API_TOKEN}
    email admin@panel.example.com
}

# Primary HTTPS — стандартный 443
node01.example.com:443 {
    reverse_proxy 127.0.0.1:10000  # sing-box/Xray на internal port
    encode gzip

    # TLS hardening
    tls {
        protocols tls1.2 tls1.3
        ciphers TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256 TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256 ...
    }

    # Health check endpoint для панели (отдельный path)
    handle /healthz {
        respond "ok" 200
    }

    log {
        output file /var/log/caddy/node-access.log {
            roll_size 100MiB
            roll_keep 3
        }
        format json
    }
}

# Альтернативные порты для маскировки (DPI-friendly)
# Слушаем на Cloudflare-портах: 2053, 2083, 2087, 2095, 2096
# Плюс 8443 как alt-HTTPS
node01.example.com:2053,
node01.example.com:2083,
node01.example.com:2087,
node01.example.com:2095,
node01.example.com:2096,
node01.example.com:8443 {
    reverse_proxy 127.0.0.1:10000
    tls {
        protocols tls1.2 tls1.3
    }
}
```

**Популярные порты для маскировки (почему именно они):**

| Порт | Сервис | Почему не блокируют |
| --- | --- | --- |
| `443` | HTTPS | Стандарт, нельзя блокировать (сломается весь веб) |
| `80` | HTTP | Стандарт (редирект на 443) |
| `2053` | Cloudflare-https | CDN Cloudflare, популярен для прокси |
| `2083` | cPanel SSL | Распространённый hosting-порт |
| `2087` | cPanel SSL alt | Распространённый hosting-порт |
| `2095` | cPanel webmail | Распространённый hosting-порт |
| `2096` | cPanel webmail SSL | Распространённый hosting-порт |
| `8443` | Alt HTTPS | Используется многими веб-приложениями (Spring, Tomcat, и т.д.) |

**Клиент выбирает порт** через subscription — каждый хост может иметь `port: 2053` или `port: 443` или несколько вариантов.

#### 19.4.3 Как клиент определяет, на какой порт идти

В подписке (sing-box JSON / Clash YAML) host содержит конкретный `server_port`. Если хост настроен на несколько портов — можно отдавать **массив** и клиент сам выберет (многие умеют) или панель отдаст **несколько host-записей** с разными портами.

### 19.5 fail2ban — защита от брутфорса (NEW)

#### 19.5.1 SSH jail (на нодах и панели)

```ini
# /etc/fail2ban/jail.local
[DEFAULT]
backend = systemd
bantime  = 1h
findtime = 10m
maxretry = 5

[sshd]
enabled = true
port    = ssh
filter  = sshd
logpath = %(sshd_log)s
```

**Поведение:** 5 неудачных попыток SSH за 10 минут → бан IP на 1 час через iptables/nftables.

#### 19.5.2 Panel login jail (кастомный)

```ini
# /etc/fail2ban/filter.d/panel-login.conf
[Definition]
failregex = ^<HOST> .* "POST /api/v1/auth/login HTTP.*" (401|403) .*$
ignoreregex =

# /etc/fail2ban/jail.local
[panel-login]
enabled  = true
port     = http,https
filter   = panel-login
logpath  = /var/log/caddy/panel-access.log
maxretry = 5
findtime = 10m
bantime  = 1h
```

**Поведение:** 5 неудачных логинов в Panel за 10 минут → бан IP на 1 час. Логи читаем из Caddy JSON-лога (с `format json`).

#### 19.5.3 Agent mTLS (опционально)

Agent mTLS не подбирается паролем (нужен приватный ключ), поэтому fail2ban обычно не нужен. Но если Agent слушает на публичном IP (не рекомендуется, должен быть только на localhost + reverse SSH), можно добавить jail на handshake failures.

**Рекомендация:** Agent mTLS слушает ТОЛЬКО на `127.0.0.1` или через WireGuard-туннель, fail2ban не нужен.

#### 19.5.4 Whitelist для панели

Чтобы fail2ban не забанил легитимных пользователей (например, при ошибках с токенами), добавляем whitelist в `/etc/fail2ban/jail.local`:

```ini
[DEFAULT]
ignoreip = 127.0.0.1/8 ::1 10.0.0.0/8 192.168.0.0/16
# + статические IP админов
ignoreip = 1.2.3.4 5.6.7.8
```

#### 19.5.5 Мониторинг бана

- fail2ban пишет в `/var/log/fail2ban.log` (JSON, можно собирать в Loki).
- Webhook `security.fail2ban.ban` (Phase 2+): оповещение в Telegram/email при бане подозрительного IP.
- Метрика `fail2ban_banned_total` в Prometheus (через `fail2ban_exporter`).

---

## 20. Масштабирование

| Сценарий | Решение |
| --- | --- |
| > 5k нод | Шардировать панель по региону (одна реплика отвечает за подмножество нод), маршрутизация по тегу. |
| > 100k пользователей | Вынести subscription-render в отдельный сервис с кешем в Redis, основная панель — только management. |
| Высокий трафик метрик | ClickHouse + Kafka перед ним, retention 30d online, дальше — холодный S3. |
| Гео-распределённая панель | Per-region инстансы панели, общий control-plane через CRDT или единый Postgres + read-replica. |
| Долгая apply на ноде | Staged rollout: 1% → 10% → 100%, health-gate на каждом шаге. |

---

## 21. Roadmap (предложение)

**Phase 0 — Фундамент (1–2 нед.)**
- Репо, CI, docker-compose dev-окружение, базовые миграции, каркас API, модуль `auth`, модуль `nodes` (только CRUD).
- Модель данных из раздела 16, scopes-based API-ключи (но на MVP — простой bearer).

**Phase 1 — Один core, ручные ноды (2–3 нед.)**
- `CoreProvider` интерфейс, реализация `sing-box`.
- Capability-флаги: минимум `VLESS_REALITY`, `SHADOWSOCKS`, `DYNAMIC_USERS`, `WILDCARD_RANDOM`, `MULTI_PORT`.
- Agent: рендер конфига, apply, health, metrics, **dynamic user add/remove** через gRPC API.
- Шаблоны inbounds, версионирование, dry-run, diff.
- **Host manager: расширенная модель** (раздел 10.1) с format variables, wildcard `*`, status_filter, priority, transport overrides.
- Pool + стратегии выдачи (manual/round_robin/least_loaded).
- Subscription service: endpoint + форматы (sing-box, clash, base64).

**Phase 2 — Пользователи и ЛК API (2 нед.)**
- CRUD пользователей, планы, трафик, лимиты, device-limit.
- Cabinet API: все эндпоинты, идемпотентность, scopes.
- **Webhook с HMAC-SHA256** подписью (раздел 13.4).
- **Disk alerts с hysteresis** (раздел 13.5).
- UI на Vue 3: пользователи, ноды, хосты, подписки, dashboard.

**Phase 3 — Онбординг нод через SSH (1–2 нед.)**
- **BYO Node flow** (раздел 9): регистрация существующей ноды в панели, SSH-probe, Ansible-based install.
- Ansible-роли: `bootstrap_node`, `install_agent`, `upgrade_agent`, `smoke_test`.
- Идемпотентность ролей, прогон локально для отладки.
- Без интеграции с API провайдеров (намеренно).
- Нагрузочные тесты API + chaos-тест Agent (kill Core, kill сеть).

**Phase 4 — UX и Advanced (1–2 нед.)**
- **Balancer-тип Host'а**: Xray `leastLoad` или sing-box `urltest` на edge-ноде.
- **Network Map UI** для cascade.
- **Cascade Topology (reverse mode)** — базовый `chain` тип с auto-generated x25519 + shortIds.
- **MCP-сервер** с базовым набором tools (users, nodes, hosts, get_stats).

**Phase 5 — Production hardening (1 нед.)**
- mTLS, SOPS, rate-limit, audit, observability, бэкапы.
- OpenTelemetry traces, Prometheus metrics, Grafana dashboards.

**Phase 6 — Масштабирование и расширение ядер (по запросу)**
- **Cascade forward mode** + relay role + multi-hop.
- **Второй core** (Xray / Hysteria 2) — реальная проверка абстракции.
- WireGuard inbound (PasarGuard-стиль).
- ACL на ноде (Celerity-стиль).
- Канбан-фичи: канареечные деплои, blue/green, geo-aware выдача на полную.

---

## 22. Что добавлено «сверх минимума» (обоснование)

**Из исходного плана (v1):**
- **Транзакционный outbox + NATS.** Без этого теряются вебхуки оплаты и нотификации — критично для бизнеса.
- **Версионирование конфигов + diff + rollback.** 90% операционных проблем у панель-оператора — «ой, я накатил кривой конфиг». Снимает класс инцидентов.
- **Drain mode.** Снятие ноды без обрыва активных сессий — must-have для zero-downtime.
- **Capability-флаги ядер.** UI не должен предлагать настроить TUIC, если выбранное ядро его не умеет. Дешёвая страховка от путаницы.
- **OpenTelemetry.** Включить с первого дня — потом не вытащить.
- **Subscription-cache + invalidation.** Без него подписка пользователя начнёт «тупить» на 100k+ юзеров.
- **Hardware-id/device-limit.** Стандарт для коммерческих VPN, влияет на retention.
- **Auto-rollback apply.** Если Core не поднялся за N секунд — откат на предыдущий конфиг.

**Добавлено после разбора PasarGuard / Celerity (v2):**
- **Расширенный Host как override-слой** (PasarGuard-стиль). Дешевле, чем писать кастомный шаблон для каждого нюанса, и даёт полный контроль.
- **Format Variables** в `remark` и `address` (`{USERNAME}`, `{DATA_LEFT}`, `{STATUS_EMOJI}`). **Один из самых сильных UX-аплифтов**: юзер видит персонализированный сервер. Дёшево реализуется (template engine, без доп. запросов), сильно поднимает retention.
- **Wildcard `*` с random salt** в SNI/host/address. Анти-детект per-fetch. Дешёво в реализации, сильно в эффекте.
- **Status-based host visibility.** Дополняет group-based filter. Полезно для tier-разделения хостов.
- **Per-host transport overrides** (XHTTP `download_settings`, mux, fragment, noise). Без этого пришлось бы делать новый inbound на каждый нюанс.
- **CC Agent pattern** — dynamic user management через gRPC API ядра. Без этого при создании/удалении юзера роняются активные сессии на ноде → неприемлемо для продакшна.
- **API Key scopes** (`users:read/write`, `nodes:control`, `cascades:write`, `mcp:invoke`). Без granular scopes — либо over-permissions (security hole), либо один токен на всё (нет audit-трейла по scope).
- **Webhook HMAC-SHA256** + anti-replay по timestamp. Без подписи — любой может слать фейковые события в ЛК и ронять активации.
- **Disk alerts с hysteresis.** Без hysteresis — алерты спамят каждую минуту при медленно заполняющемся диске. С hysteresis — один алерт при пересечении + один при восстановлении.
- **Cascade Topology** (Phase 4+). Killer-фича для обхода IP-фильтрации. Без неё нельзя конкурировать в сегменте, где важна устойчивость к блокировкам.
- **MCP-интеграция** (Phase 4+). Уникальная фича в индустрии, естественный UX-апгрейд для оператора.

---

## 23. Открытые вопросы (нужны от тебя)

1. **Стек бэкенда.** Принимаем Go как дефолт или предпочтёшь Python/FastAPI для скорости прототипа?
2. **Язык/фреймворк UI.** ~~HTMX на MVP, Vue 3 на росте — ок, или сразу полноценный SPA?~~ — **РЕШЕНО**: Vue 3 + TypeScript + Vite как основной стек UI (admin и cabinet). Скелет уже в репо (`frontend/`, см. CHANGELOG 0.0.1).
3. **Managed или self-host БД** на проде? (влияет на бэкап-стратегию)
4. **Целевая география нод** в первом релизе: только РФ, RU+EU+Asia, глобально?
5. ~~**Provider для авто-развёртывания в первую очередь**: Hetzner, Selectel, AWS, другое?~~ — **неактуально**, BYO Node (см. раздел 9). Оператор сам выбирает провайдера.
6. ~~**Multi-tenant в MVP** или одна панель = один проект/оператор?~~ — **РЕШЕНО**: Single-tenant. Одна панель = один оператор. Несколько admin-аккаунтов внутри (super-admin, operator, viewer). Multi-tenant (один инстанс панели обслуживает несколько операторов) — **не планируется** (см. раздел 27).
7. **Tier-1 фичи вне MVP** (что точно не делаем сейчас, чтобы не сжечь сроки).
8. **Политика retention данных**: сколько хранить сырой трафик, аудит, логи.

---

## 24. Резюме

- Архитектура **модульный монолит** с чёткими границами, готовый к выделению сервисов.
- **Core-agnostic** через `CoreProvider` + capability-флаги — sing-box сейчас, любое ядро завтра без миграций.
- **Host как богатый override-слой** с format variables, wildcard random, status_filter, per-host transport overrides. Это ядро UX подписки.
- **CC Agent = dynamic user management** через gRPC API ядра — zero-downtime при создании/удалении юзеров.
- **Webhooks с HMAC-SHA256** + anti-replay, **disk alerts с hysteresis** — без спама.
- **Scopes-based API-ключи** (`users:read/write`, `nodes:control`, `cascades:write`, `mcp:invoke`).
- **Phase 4+**: Cascade Topology (Portal → Relay → Bridge), MCP-интеграция для AI-управления.
- Нода — изолированный юнит: Agent + Core, mTLS-канал с панелью, fail-safe.
- Авто-развертывание — **BYO Node через SSH** + Ansible-роли. Панель не создаёт VPS, работает только с тем, что прислал оператор.
- Cabinet API — версионированный, идемпотентный, с вебхуками в обе стороны.
- Мониторинг, безопасность, бэкапы — встроены, а не «потом».
- Стек — Go + PostgreSQL + ClickHouse + Redis + NATS + Vue 3 + Prometheus/Grafana/Loki/Tempo.

После твоего фидбэка по открытым вопросам — двигаемся к детальному тех-дизайну модулей и контрактам (proto/JSON-схемы), без старта реализации.

---

## 25. История изменений

- **v1 (2026-07-12)** — initial draft: core-agnostic панель, MVP на sing-box, host manager + pool, provider abstraction, Cabinet API.
- **v2 (2026-07-12, после разбора PasarGuard + Celerity)** — патчи:
  - Расширенная модель Host (override-слой, format variables, wildcard `*`, status_filter, priority, per-host transport overrides, type=direct/balancer/**chain**).
  - Capability-флаги расширены: `WIREGUARD`, `HYSTERIA2`, `ACL`, `CASCADE`, `DYNAMIC_USERS`, `WILDCARD_RANDOM`, `MULTI_PORT`, `XHTTP_DOWNLOAD`.
  - CC Agent pattern (dynamic user management) выделен явно.
  - Webhooks: HMAC-SHA256 + anti-replay; полный набор событий; disk alerts с hysteresis.
  - Cabinet API: scopes-based ключи (Celerity naming).
  - Новые сущности: `WebhookEndpoint`, `Cascade`, `CascadeHop`, `McpConfig`, `McpToken`, `DiskAlertThresholds`, `ApiKey`, `ApiKeyScope`.
  - Новый раздел 10.3 — Cascade Topology (Phase 4+).
  - Новый раздел 17 — MCP-интеграция (Phase 4+).
  - Roadmap переразбит с явными фазами для cascade и MCP.
  - Раздел «Что добавлено сверх минимума» обновлён с обоснованиями.
  - Нумерация сдвинута для размещения новых разделов 17, 22.
- **v3 (2026-07-13, BYO Node)** — патчи:
  - **Раздел 9 полностью переписан**: убрана CloudProvider-абстракция с API провайдеров. Добавлен `NodeBootstrapper` интерфейс для SSH-only онбординга.
  - **Философия BYO Node**: панель не создаёт/удаляет VPS, работает только с тем, что оператор сам арендовал и прислал SSH-доступ. Совместимо с любым провайдером.
  - **Алгоритм онбординга**: Probe → InstallAgent → mTLS handshake → SmokeTest.
  - **5 Ansible-ролей**: `bootstrap_node`, `install_agent`, `upgrade_agent`, `uninstall_agent`, `smoke_test`. Идемпотентные, в репо, прогон локально для отладки.
  - **Раздел 0 (Термины)**: Provider помечен как устаревший, акцент на SSH-онбординг.
  - **Раздел 6 (Компоненты)**: `providers` модуль переименован в `bootstrap` с явным указанием что без API провайдеров.
  - **Раздел 21 (Roadmap) Phase 3** переписан: было "Provider Hetzner + Generic Ansible", стало "BYO Node flow + Ansible-роли, без API провайдеров".
  - **Раздел 17 (MCP) сценарий** обновлён: было "5 нод в EU через Hetzner", стало "5 существующих нод с SSH-ключами, зарегистрируй и настрой".
  - **Раздел 23 (Открытые вопросы)**: пункт 5 про "Provider для авто-развёртывания" помечен как неактуальный.
  - **Раздел 24 (Резюме)** обновлён.
- **v4 (2026-07-13, полная совместимость клиентов)** — патчи:
  - **Раздел 10.4 полностью переписан** под полную матрицу клиентов и форматов.
  - **Поддерживаемые форматы (MVP):** sing-box JSON, Clash Meta YAML, base64 URI list — покрывают ~95% пользователей.
  - **User-Agent auto-детект** с полным маппингом: Hiddify / sing-box / Nekoray / Karing / Streisand / V2Box → singbox, v2rayNG / v2rayN / Shadowrocket → base64, Clash Verge / mihomo / Clash Meta → clash-meta, браузер → HTML sub-page.
  - **HTTP-заголовки** `Profile-Update-Interval`, `Subscription-Userinfo` (с трафиком/лимитом/expire), `Profile-Title`, `Profile-Web-Page-Url`, `Support-Url` — для отображения в клиенте.
  - **Sub-page** (`?target=html`) — HTML с QR-кодом, списком клиентов, инфой о юзере.
  - **Тестовая матрица** для CI: Hiddify, Streisand, v2rayNG/N, Shadowrocket, Clash Verge, Clash Meta, Nekoray, Karing, sing-box CLI.
  - **Особенности форматов** зафиксированы для разработчиков (структура sing-box JSON, Clash Meta YAML, base64 URI, v2ray JSON).
  - **Раздел 1 (MVP-границы)** уточнён: совместимость с популярными клиентами через auto-детект.
  - **Раздел 2.4 (Пользователи и подписки)** расширен деталями по форматам и HTTP-заголовкам.
- **v5 (2026-07-13, инфраструктурный hardening: Caddy + fail2ban + маскировка портов)** — патчи:
  - **Раздел 15 (Безопасность)** обновлён: добавлены строки Edge proxy (Caddy), Brute-force protection (fail2ban), Маскировка портов. Объяснено почему именно Caddy и почему именно fail2ban.
  - **Раздел 18 (Тех. стек)** обновлён: добавлены строки Edge proxy (Caddy) и Brute-force protection (fail2ban). Caddy заменяет nginx + certbot.
  - **Раздел 19 (Развёртывание)** полностью переписан:
    - 19.1/19.2/19.3 — нумерация исправлена (были 18.1-18.3 от старой нумерации), обновлена строка про BYO Node.
    - **19.4 Caddy — reverse proxy (NEW)**: полный Caddyfile для панели и для ноды, объяснение популярных портов для маскировки, как клиент выбирает порт.
    - **19.5 fail2ban — защита от брутфорса (NEW)**: SSH jail, кастомный Panel login jail (читает из Caddy JSON-лога), whitelist, мониторинг бана.
  - **Раздел 9.6 (требования к ноде)** обновлён: добавлены Caddy и fail2ban в обязательные пакеты, перечислены порты для маскировки.
  - **Раздел 10.1 (Host model)** дополнен нотой про маскировку под популярные веб-порты со ссылкой на раздел 19.4.
  - **Популярные порты для маскировки:** `443` (стандарт), `2053`/`2083`/`2087`/`2095`/`2096` (Cloudflare-стиль, cPanel), `8443` (alt HTTPS). Объяснено почему именно они — DPI не может их блокировать без поломки веба.
- **v6 (2026-07-13, Decoy Sites & URL Masking)** — патчи:
  - **Новый раздел 26 (Decoy Sites & URL Masking)**: полная спецификация anti-detection через HTML-заглушки. 4 подраздела: зачем, архитектура, возможности панели и нод, встроенные пресеты (8 штук), загрузка custom через UI/API, Caddyfile с decoy (панель + нода), правила контента, безопасность, связь с другими разделами, модель данных, roadmap.
  - **Секретные пути для админки и subscription endpoint** — рандомизируются через `openssl rand -hex 6` при install. По умолчанию `/s3cr3t-p4n3l-7a8b9c/` для админки, `/s3cr3t-sub-d4e5f6/` для подписок.
  - **Секретные пути для proxy на нодах** — `/_/proxy-a1b2c3/` (рандомизируется per-node).
  - **8 встроенных пресетов decoy-сайтов**: personal-blog, it-company, saas-landing, news-portal, portfolio, wiki, 404-only, static-html (custom).
  - **Decoy + Reality** — двойная маскировка: Reality маскирует TLS-handshake, fallback в sing-box/Xray отправляет невалидные запросы на Caddy с decoy-сайтом.
  - **Раздел 15 (Безопасность)** обновлён: добавлена строка `Decoy-сайты` со ссылкой на раздел 26.
  - **Раздел 19.4 (Caddy)** обновлён: добавлена нота про decoy-сайты со ссылкой на раздел 26.
  - **Раздел 11 (Конфигурация протоколов)** обновлён: добавлен `Decoy-fallback` с примерами sing-box и Xray конфигов.
  - **Раздел 16 (Модель данных)** обновлён: добавлены сущности `decoy_sites`, `decoy_audits`, `panel_path_config`.
- **v7 (2026-07-13, фиксация решений: Aegis + AGPL-3.0 + single-tenant + monorepo + локальная дока)** — патчи:
  - **Название проекта зафиксировано: Aegis.** Обновлена шапка документа.
  - **Новый раздел 27 (Лицензия и Tenancy)**: AGPL-3.0 выбрана как защита от SaaS-пиратства с совместимостью с коммерческим использованием. Single-tenant с несколькими admin-аккаунтами (super-admin/operator/viewer) — multi-tenant явно не планируется. Обоснование выбора лицензии и tenancy.
  - **Новый раздел 28 (Структура репозитория)**: monorepo выбран для простоты соло-разработки. Полная структура каталогов: `backend/`, `frontend/`, `docs/`, `deploy/`, `tools/`. Git workflow (main/develop/feature/release/hotfix), Conventional Commits, Semantic Versioning.
  - **Документация**: VuePress в `docs/`, разрабатывается **локально, не публикуется** на текущем этапе. Будет доступна вместе с релизом проекта.
  - **Раздел 23 (Открытые вопросы)**: пункт 6 (multi-tenant) — закрыт с обоснованием.
  - **Шапка документа** обновлена: явно указаны Aegis, лицензия, tenancy, monorepo, документация.

---

## 26. Decoy Sites & URL Masking (NEW)

### 26.1 Зачем

Когда проверяющий (DPI-система провайдера, регулятор, конкурент, любопытный сканер) открывает `https://node01.example.com/` в браузере — он должен увидеть **обычный сайт**: блог, IT-компанию, SaaS-лендинг, новостной портал. **Никаких признаков VPN.**

Аналогично для панели: `https://panel.example.com/` отдаёт **decoy-сайт**, а реальная админка доступна только по **секретному пути**, который знает только оператор.

**Это второй уровень маскировки** после популярных портов (раздел 19.4.2). Вместе они дают:
- Порт = `443` (не вызывает подозрений у DPI)
- Контент = обычный сайт (не вызывает подозрений у человека)
- Реальный proxy = работает только для «своих» клиентов (по TLS fingerprint, UUID, или секретному пути)

### 26.2 Архитектура

```
Юзер с VPN-клиентом                                Случайный проверяющий
        │                                                   │
        │ TLS+Reality fingerprint, валидный UUID             │ Обычный GET /
        │                                                   │
        ▼                                                   ▼
   Caddy (443)                                         Caddy (443)
        │                                                   │
        │ handle /_/vless-in                              │ handle /
        │ reverse_proxy 127.0.0.1:10000                  │ root * /var/www/decoy/
        │                                                   │ file_server
        ▼                                                   │
   sing-box/Xray (127.0.0.1:10000)                      ▼
        │                                              HTML-страница
        │ Handshake ОК (UUID/reality)                  "Welcome to my blog"
        │ → проксирует                                    │
        ▼                                              (проверяющий уходит)
   Internet
```

**Ключевая связка:** sing-box/Xray имеет `fallback` — если handshake не прошёл (невалидный UUID, плохой reality-fingerprint), запрос отправляется на Caddy, который отдаёт decoy-HTML. С точки зрения sing-box всё «нормально» — невалидные клиенты просто прозрачно перенаправляются.

### 26.3 Возможности

#### 26.3.1 Для панели

- **Смена URL админки** через настройку `panel_path_prefix` (по умолчанию `/s3cr3t-xyz-123`, при первом запуске генерируется случайный).
- **Смена URL subscription endpoint** через `subscription_path_prefix` (отдельно от админки).
- **Decoy-сайт на основном домене** — `https://panel.example.com/` отдаёт HTML-заглушку.
- **ReDoc/OpenAPI на отдельном пути** (опционально, для разработчиков).
- **Healthcheck** на `/healthz` (для мониторинга, не маскируется).

#### 26.3.2 Для нод

- **Decoy-сайт на всех портах** — Caddy отдаёт HTML на любые пути, кроме секретного.
- **Секретный путь для proxy** — например, `/_/vless-in` (настраивается per-node, случайный при создании).
- **Загрузка custom decoy-сайта** — оператор может загрузить свой HTML (zip-архив с index.html + статикой).
- **Встроенные пресеты** — несколько готовых decoy-сайтов на выбор.

### 26.4 Встроенные пресеты decoy-сайтов

В панель встроено 6-8 готовых HTML-заглушек. Каждая — статический сайт с уникальным дизайном и контентом, без явных признаков «заглушки»:

| Пресет | Описание | Когда использовать |
| --- | --- | --- |
| `personal-blog` | Личный блог с 3-5 постами, страницей about, контактами | Дефолт для нод. Выглядит как «обычный разработчик ведёт блог». |
| `it-company` | Лендинг IT-компании: услуги, кейсы, форма «Связаться» | Если нода — корпоративный IP, который мог бы хостить сайт агентства. |
| `saas-landing` | SaaS-продукт с pricing, features, FAQ, CTA | Выглядит как стартап, выкатывающий новый сервис. |
| `news-portal` | Новостной портал с категориями и статьями | Если нода — IP провайдера, который мог бы хостить медиа. |
| `portfolio` | Портфолио дизайнера/разработчика с галереей проектов | Для домашнего IP, выглядит как личный сайт. |
| `wiki` | Wiki-проект с навигацией, статьями, поиском | Если нода — IP в дата-центре, который мог бы хостить community. |
| `404-only` | Минималистичная 404-страница «страница не найдена» | Максимальная скрытность: нода «вообще не отвечает» на HTTP-запросы. |
| `static-html` | Произвольный HTML, загруженный оператором | Если ни один пресет не подходит. |

**Все пресеты** — статический HTML+CSS+JS, без бэкенда, без трекинга, без ссылок наружу (кроме указанных в `robots.txt`/`sitemap.xml`).

### 26.5 Загрузка custom decoy-сайта

**Через UI панели:**

1. Оператор нажимает «Загрузить decoy-сайт» для конкретной ноды.
2. Загружает `.zip` архив (до 20 МБ), структура:
   ```
   decoy/
     index.html
     about.html
     assets/
       style.css
       logo.png
       ...
   ```
3. Валидация:
   - Только статика: `.html`, `.css`, `.js`, `.png`, `.jpg`, `.svg`, `.woff2`, `.json`
   - Никаких исполняемых: `.php`, `.sh`, `.exe`, `.py`, и т.д.
   - Все ссылки относительные или ведут на whitelisted-домены (CDN, иконки)
   - Размер архива ≤ 20 МБ
   - `<meta>` теги не должны содержать реальный IP ноды или внутренние пути
4. После валидации — распаковка в `/var/www/decoy/<node_id>/` на панели.
5. Через Agent — push на ноду в `/var/www/decoy/`.
6. Caddyfile на ноде автоматически обновляется (через Caddy admin API) — `root * /var/www/decoy/`.
7. **Preview в UI:** панель показывает скриншот через Playwright/headless Chrome.

**Программно через API (Phase 2+):**

```
POST /api/v1/nodes/{id}/decoy
Content-Type: multipart/form-data
Authorization: Bearer <admin_token>

[file: decoy.zip]
```

### 26.6 Caddyfile с decoy

#### 26.6.1 Панель с decoy + секретный путь к админке

```caddyfile
# /etc/caddy/Caddyfile (panel with decoy)
{
    acme_dns cloudflare {env.CLOUDFLARE_API_TOKEN}
    email admin@panel.example.com
}

panel.example.com, *.panel.example.com {
    # 1. Секретный путь к админке (рандомизируется при install)
    @admin_path path /s3cr3t-p4n3l-7a8b9c/*
    handle @admin_path {
        reverse_proxy panel_backend:8080
        encode gzip zstd
    }

    # 2. Subscription endpoint (отдельный секретный путь)
    @sub_path path /s3cr3t-sub-d4e5f6/*
    handle @sub_path {
        reverse_proxy panel_backend:8080
    }

    # 3. Healthcheck (для monitoring, не маскируется)
    handle /healthz {
        respond "ok" 200
    }

    # 4. Всё остальное — decoy-сайт
    handle /* {
        root * /var/www/decoy/panel
        file_server
        encode gzip zstd
    }

    # Security headers (применяются ко всем)
    header {
        Strict-Transport-Security "max-age=31536000; includeSubDomains"
        X-Content-Type-Options "nosniff"
        X-Frame-Options "DENY"
    }
}
```

**Генерация секретных путей** при install (в Ansible/bootstrap):
```bash
ADMIN_PATH="/s3cr3t-p4n3l-$(openssl rand -hex 6)"
SUB_PATH="/s3cr3t-sub-$(openssl rand -hex 6)"
echo "ADMIN_PATH=$ADMIN_PATH" > /etc/panel/paths.env
```

#### 26.6.2 Нода с decoy + fallback в sing-box

```caddyfile
# /etc/caddy/Caddyfile (node with decoy)
{
    acme_dns cloudflare {env.CLOUDFLARE_API_TOKEN}
    email admin@panel.example.com
}

node01.example.com:443,
node01.example.com:2053,
node01.example.com:2083,
node01.example.com:8443 {
    # Секретный путь к proxy (рандомизируется при создании ноды)
    @proxy_path path /_/proxy-a1b2c3d4
    handle @proxy_path {
        reverse_proxy 127.0.0.1:10000
    }

    # Healthcheck
    handle /healthz {
        respond "ok" 200
    }

    # Всё остальное — decoy
    handle /* {
        root * /var/www/decoy/node01
        file_server
        encode gzip zstd
    }
}
```

**Sing-box конфиг с fallback (для VLESS+Reality):**
```json
{
  "inbounds": [
    {
      "type": "vless",
      "listen": "127.0.0.1",
      "listen_port": 10000,
      "users": [{"uuid": "..."}],
      "tls": {
        "server_name": "www.google.com",
        "reality": {
          "handshake": {"server": "google.com:443"},
          "private_key": "...",
          "short_id": ["..."]
        }
      }
    }
  ],
  "outbounds": [
    {"type": "direct", "tag": "direct"}
  ]
}
```

**Логика fallback:** если TLS-handshake с Reality не прошёл (невалидный short_id или fingerprint), Caddy получает запрос, не находит `/_/proxy-a1b2c3d4`, и отдаёт decoy. Если клиент идёт по правильному пути — Caddy проксирует в sing-box.

**Важно:** для Reality лучше НЕ использовать `fallback` в sing-box, а работать через разные пути в Caddy. Reality сам по себе уже маскирует — клиент с валидным fingerprint получает прокси, невалидный получает «настоящий» TLS на google.com (который отвечает реальным google'ом). Это **двойной уровень** маскировки.

### 26.7 Decoy-контент — правила

**Что должно быть:**
- Реалистичный контент (посты, услуги, контакты)
- Валидный HTML5, без ошибок в разметке
- Реальные ссылки на whitelisted-ресурсы (CDN картинки, Google Fonts, и т.д.)
- `robots.txt` и `sitemap.xml` (чтобы выглядело как реальный сайт)
- Разные title/meta description на разных страницах

**Что НЕ должно быть:**
- Реальный IP ноды где-либо в коде (только домен из cert)
- Ссылки на `/admin`, `/api`, `/metrics` (даже зачёркнутые)
- Упоминания VPN, прокси, tunnel (даже в комментариях)
- Трекеры (Google Analytics, Yandex Metrica, Facebook Pixel) — могут засветить реальный IP
- WebSocket на внешние сервисы (DPI может это отследить)
- Формы, которые реально что-то отправляют

**Автоматическая проверка:** при загрузке custom decoy панель сканирует HTML на:
- Наличие IP-адресов (regex)
- Ссылки на `/admin`, `/api`, `/dashboard`, `/login`
- Упоминания VPN, proxy, tunnel, Xray, sing-box
- Внешние трекеры

Если найдено — отклоняет с ошибкой.

### 26.8 Безопасность decoy

- **Audit log** — все операции с decoy (upload, change, delete) логируются с `actor`, `node_id`, `file_hash`, `timestamp`.
- **File integrity** — панель хранит `sha256` каждого decoy, при изменении файлов на ноде (вне панели) — alert.
- **Sandbox-рендеринг** — UI предпросмотра через headless Chrome с `disable-web-security`, без доступа к panel API.
- **CSP для decoy** — `Content-Security-Policy: default-src 'self'; img-src 'self' cdn.example.com; ...` (чтобы XSS в decoy не скомпрометировал ноду).
- **Не путать с панелью** — decoy на ноде и decoy на панели — разные, не пересекаются. Секретные пути тоже разные.
- **Ротация** — периодическая смена секретных путей (раз в 30-90 дней, настраивается) с уведомлением админа по email/telegram.

### 26.9 Связь с другими разделами

- **Раздел 10.1 (Host model)** — у хоста может быть поле `decoy_preset: "personal-blog"` или `decoy_file_id: "<ref>"`.
- **Раздел 11.2 (Transport overrides)** — fallback в шаблоне inbound'а для sing-box/Xray.
- **Раздел 15 (Безопасность)** — decoy как anti-censorship механизм.
- **Раздел 16 (Модель данных)** — сущности `DecoySite`, `DecoyFile`, `DecoyAudit`.
- **Раздел 19.4 (Caddy)** — обновлённые Caddyfile с decoy-блоками.
- **Раздел 25 (История)** — v6 запись.

### 26.10 Модель данных (дополнение к разделу 16)

```yaml
DecoySite:
  id: uuid
  name: string                       # "personal-blog", "it-company", "custom"
  is_preset: bool                    # встроенный пресет или custom
  node_id: uuid | null               # null = decoy панели, иначе per-node
  file_path: string                  # путь в storage панели (для preset = встроенный)
  sha256: string                     # для integrity check
  size_bytes: int
  uploaded_at: timestamp
  uploaded_by: admin_id

DecoyAudit:
  id: uuid
  decoy_site_id: uuid
  action: upload | change | delete | render
  actor: admin_id
  ip: string
  user_agent: string
  timestamp: timestamp

PanelPathConfig:                    # singleton в panel settings
  admin_path: string                 # "/s3cr3t-p4n3l-7a8b9c"
  sub_path: string                   # "/s3cr3t-sub-d4e5f6"
  path_rotated_at: timestamp
```

### 26.11 Roadmap

- **MVP:** decoy-пресеты (3-4 встроенных), секретные пути через ENV-переменные, базовая загрузка custom через UI.
- **Phase 2:** автогенерация секретных путей при install, ротация путей, audit log, integrity check, Playwright preview.
- **Phase 3:** программный API загрузки decoy, marketplace пресетов (community-driven).
- **Phase 4:** динамическая смена decoy по user-agent / geo (разные сайты для разных проверяющих).

---

## 27. Лицензия и Tenancy (NEW, решено)

### 27.1 Лицензия: AGPL-3.0

**Выбор:** GNU Affero General Public License v3.0.

**Почему AGPL-3.0 (а не MIT/Apache):**

- **Защита от SaaS-пиратства.** AGPL — единственная open-source лицензия, которая требует раскрытия исходного кода даже при использовании через сеть (network use = distribution). Если кто-то форкнет Aegis и запустит его как SaaS-сервис, не публикуя изменения, — он нарушает лицензию.
- **Совместимость с коммерческим использованием.** Можно свободно использовать, модифицировать, распространять. Главное — раскрывать изменения.
- **Стандарт в индустрии VPN-панелей.** Remnawave, Marzneshin, Hiddify (частично), PasarGuard используют AGPL или подобные copyleft-лицензии.
- **Соответствует модели "open core"**: код открыт, но коммерческая поддержка / кастомные интеграции / hosted-версия могут продаваться отдельно.

**Что требует AGPL-3.0:**
- Все форки и модификации должны быть под AGPL-3.0
- Исходный код (включая изменения) должен быть доступен пользователям сети
- Лицензия и уведомление об авторских правах должны сохраняться

**Что НЕ требует:**
- Можно продавать (но с раскрытием кода)
- Можно использовать коммерчески
- Не требуется открывать complementary/separate code (например, клиентские приложения)

**Альтернативы, которые НЕ выбрали:**
- **MIT / Apache 2.0** — слишком свободно, любой может сделать SaaS и закрыть код
- **BSL / Business Source License** — некопирующая лицензия, через N лет становится open-source. Сложно для community
- **Проприетарная** — закрывает community contributions, ограничивает adoption

**Файл LICENSE** в корне репо с полным текстом AGPL-3.0.

**Уведомления в коде:**
- Каждый файл с исходным кодом имеет SPDX-комментарий: `// SPDX-License-Identifier: AGPL-3.0-or-later`
- Это требуется для совместимости с лицензией

### 27.2 Tenancy: Single-tenant с несколькими админами

**Решение:** **Single-tenant.** Одна панель = один оператор.

**Что это значит:**
- **Один оператор** владеет одной инсталляцией панели.
- **Несколько admin-аккаунтов** внутри панели с разными ролями (super-admin, operator, viewer).
- **Multi-tenant (один инстанс обслуживает несколько операторов с изоляцией) — не планируется.**
- Если два разных оператора хотят использовать Aegis — они разворачивают две независимые инсталляции (это и есть BYO).

**Почему single-tenant, а не multi-tenant:**

1. **Проще архитектура** — нет tenant_id в каждой таблице, нет row-level security, нет сложной авторизации.
2. **Проще биллинг** — нет нужды считать использование per-tenant, выставлять счета.
3. **Соответствует модели развёртывания** — панель деплоится per-оператор, это уже подразумевает изоляцию на уровне инфраструктуры.
4. **Совпадает с конкурентами** — Remnawave, Marzneshin, 3x-ui, PasarGuard все single-tenant.
5. **Каждый оператор — свой панельный инстанс** — это даёт ему полный контроль над своими данными, секретами, нодами.

**Роли admin-аккаунтов (RBAC):**

| Роль | Права |
| --- | --- |
| `super-admin` | Полный доступ ко всему, включая создание других админов, изменение настроек панели, доступ к audit log |
| `operator` | Управление нодами, хостами, юзерами, планами, webhook'ами. **Не может** менять настройки панели или создавать других админов |
| `viewer` | Только чтение — dashboard, статистика, списки. Без операций записи |

**Масштабирование на будущее:**
- Multi-tenant — **не в плане** (явное решение)
- Если кому-то нужен "shared hosting" Aegis — это отдельный сервис поверх, не сам Aegis

---

## 28. Структура репозитория (NEW, решено)

### 28.1 Monorepo

**Один репозиторий** содержит всё: backend, frontend, docs, deploy, infra.

**Почему monorepo (а не polyrepo):**

1. **Проще для соло-разработчика** — один `git clone`, один PR, одна история.
2. **Атомарные изменения** — изменение API-контракта + обновление фронта + обновление доков — в одном коммите.
3. **Единое версионирование** — один `git tag` для всего релиза, нет проблем "frontend v1.5 + backend v1.4 = несовместимо".
4. **Общий CI** — один pipeline линтит, тестит, билдит всё.
5. **Проще ревью** — все изменения в одном месте.

**Структура каталогов:**

```
aegis/                              # корень репо
├── README.md                       # главная страница
├── LICENSE                         # AGPL-3.0
├── AGPL-3.0-or-later               # SPDX-идентификатор (для reference)
├── .gitignore                      # стандартный + специфика Go/Vue/Ansible
├── .editorconfig
├── Makefile                        # top-level: make dev, make test, make docs
├── ARCHITECTURE.md                 # ← этот документ
│
├── backend/                        # Go 1.22+
│   ├── go.mod
│   ├── go.sum
│   ├── Makefile                    # go-specific: make build, make test, make lint
│   ├── Dockerfile
│   ├── cmd/
│   │   └── aegis/
│   │       └── main.go             # entrypoint
│   ├── internal/                   # приватные пакеты
│   │   ├── auth/
│   │   ├── users/
│   │   ├── plans/
│   │   ├── nodes/
│   │   ├── bootstrap/              # BYO Node + Ansible integration
│   │   ├── providers/              # legacy, deprecated
│   │   ├── inbounds/
│   │   ├── cores/                  # CoreProvider + реализации (sing-box, ...)
│   │   ├── hosts/
│   │   ├── cascades/               # Phase 4+
│   │   ├── subscriptions/
│   │   ├── stats/
│   │   ├── events/
│   │   ├── cabinet/                # внешний API
│   │   ├── webhooks/
│   │   ├── notifications/
│   │   ├── obs/                    # observability
│   │   ├── mcp/                    # Phase 4+
│   │   ├── decoy/                  # decoy-сайты
│   │   └── caddy/                  # Caddy integration
│   ├── api/                        # generated OpenAPI, REST handlers
│   ├── migrations/                 # SQL миграции (goose / golang-migrate)
│   ├── pkg/                        # публичные пакеты (могут импортиться)
│   │   ├── client/                 # Go SDK для Aegis API
│   │   └── types/                  # общие типы
│   └── test/                       # e2e тесты
│
├── frontend/                       # Vue 3 + TypeScript
│   ├── package.json
│   ├── pnpm-lock.yaml              # или package-lock.json
│   ├── tsconfig.json
│   ├── vite.config.ts
│   ├── index.html
│   ├── Makefile
│   ├── Dockerfile
│   ├── public/
│   ├── src/
│   │   ├── main.ts
│   │   ├── App.vue
│   │   ├── router/
│   │   ├── stores/                 # Pinia
│   │   ├── views/                  # pages
│   │   │   ├── DashboardView.vue
│   │   │   ├── NodesView.vue
│   │   │   ├── HostsView.vue
│   │   │   ├── UsersView.vue
│   │   │   ├── PlansView.vue
│   │   │   ├── SubscriptionsView.vue
│   │   │   ├── DecoySitesView.vue
│   │   │   ├── WebhooksView.vue
│   │   │   └── SettingsView.vue
│   │   ├── components/             # переиспользуемые компоненты
│   │   ├── composables/            # use* hooks
│   │   ├── api/                    # axios/fetch клиент
│   │   ├── i18n/                   # ru/en
│   │   ├── utils/
│   │   └── assets/
│   └── test/                       # Vitest
│
├── docs/                           # VuePress 2
│   ├── package.json
│   ├── pnpm-lock.yaml
│   ├── .vuepress/
│   │   ├── config.ts
│   │   ├── sidebar.ts              # автогенерация
│   │   └── public/
│   ├── guide/
│   │   ├── index.md                # что такое Aegis
│   │   ├── architecture.md         # ← импорт из ARCHITECTURE.md
│   │   ├── getting-started.md
│   │   └── installation.md
│   ├── api/
│   │   ├── index.md
│   │   ├── auth.md
│   │   ├── nodes.md
│   │   ├── hosts.md
│   │   └── subscriptions.md
│   ├── user-guide/
│   │   ├── admin/
│   │   └── cabinet/
│   ├── developer/
│   │   ├── contributing.md
│   │   └── modules.md
│   ├── internal/
│   │   ├── design-decisions.md
│   │   └── roadmap.md
│   └── README.md
│
├── deploy/                         # Ansible + Caddy + скрипты
│   ├── ansible/
│   │   ├── ansible.cfg
│   │   ├── inventories/
│   │   │   ├── local/
│   │   │   │   └── hosts.ini
│   │   │   └── example/
│   │   │       └── hosts.yml
│   │   ├── group_vars/
│   │   │   └── all/
│   │   │       ├── panel.yml
│   │   │       └── node.yml
│   │   ├── roles/
│   │   │   ├── bootstrap_node/      # см. раздел 9.4
│   │   │   ├── install_agent/
│   │   │   ├── upgrade_agent/
│   │   │   ├── uninstall_agent/
│   │   │   ├── smoke_test/
│   │   │   ├── install_caddy/       # NEW
│   │   │   ├── install_fail2ban/    # NEW
│   │   │   └── setup_decoy/         # NEW
│   │   └── playbooks/
│   │       ├── panel.yml
│   │       └── node.yml
│   ├── caddy/
│   │   ├── Caddyfile.panel         # см. раздел 19.4
│   │   ├── Caddyfile.node
│   │   └── snippets/                # переиспользуемые сниппеты
│   ├── fail2ban/
│   │   ├── jail.local
│   │   └── filter.d/
│   │       ├── panel-login.conf
│   │       └── sshd.conf
│   ├── docker/
│   │   ├── docker-compose.dev.yml
│   │   ├── docker-compose.prod.yml
│   │   └── .env.example
│   └── systemd/
│       ├── aegis-panel.service
│       └── aegis-agent.service
│
├── tools/                          # dev-утилиты
│   ├── scripts/
│   │   ├── gen-openapi.sh          # генерация OpenAPI из Go
│   │   ├── gen-mocks.sh
│   │   └── db-seed.sh
│   └── make/
│       └── helpers.mk
│
└── .github/                        # CI/CD (после публикации на GitHub)
    ├── workflows/
    │   ├── ci.yml                  # линт + тест + билд
    │   ├── release.yml             # релиз с tag
    │   └── docker.yml              # Docker image
    ├── ISSUE_TEMPLATE/
    └── PULL_REQUEST_TEMPLATE.md
```

### 28.2 Контроль версий

**Git** с самого начала. `.gitignore` настраивается сразу.

**Стратегия веток (после публикации на GitHub):**

- `main` — стабильный код, каждый коммит = релиз
- `develop` — активная разработка
- `feature/*` — фичи (бранчи от develop)
- `fix/*` — баг-фиксы
- `release/*` — подготовка релиза
- `hotfix/*` — срочные фиксы main

**Стратегия коммитов:** Conventional Commits.

**Теги:** Semantic Versioning (`v1.0.0`, `v1.1.0`, `v0.9.0-rc.1`).

**CHANGELOG.md** — автогенерация из conventional commits через `git-cliff` или подобное.

### 28.3 Публикация

**Планируется на GitHub** как `github.com/QAdversif/AegisPanel` (или кастомное имя, решаем).

**Документация — НЕ публикуется на текущем этапе.** Разрабатывается локально в `docs/`. Будет доступна вместе с релизом проекта, когда будет готов MVP. Это даёт время:
- Накопить качественный контент
- Не показывать сырые/неполные доки
- Контролировать что именно публикуется

**Перед публикацией нужно:**
- README с описанием, скриншотами
- LICENSE (AGPL-3.0)
- CONTRIBUTING.md
- CODE_OF_CONDUCT.md
- SECURITY.md (security policy)
- Helm chart или docker-compose для быстрого старта
- Все доки в `docs/` готовы к публикации

### 28.4 CI/CD

**На текущем этапе (локальная разработка):**
- `make lint` — линтеры Go, Vue, YAML
- `make test` — юнит-тесты
- `make build` — билд всех артефактов
- `make docs` — запуск VuePress dev-сервера
- `make docker-dev` — запуск dev-окружения через docker-compose
- `make smoke` — smoke-тесты всей системы

**После публикации на GitHub:**
- GitHub Actions для CI
- Автогенерация OpenAPI spec
- Docker images (ghcr.io)
- Release workflow с tag'ами
- Security scanning (Trivy, CodeQL)
