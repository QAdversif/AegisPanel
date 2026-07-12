# ARCHITECTURE — Addendum #1: PasarGuard & Celerity analysis

> Дополнение к основному `ARCHITECTURE.md`. Здесь — разбор того, что почерпнул
> из PasarGuard V5 и Celerity (ClickDevTech), и конкретные патчи к разделам
> основного плана. Не дублирует основной документ, а расширяет.

---

## 0. Почему именно эти две

- **PasarGuard V5** — зрелая Go-панель, продакшн-ready, активно поддерживается. Их host-configuration документ — один из лучших в индустрии, очень чёткая модель override'ов.
- **Celerity** — молодой Node.js проект, но с **уникальной killer-фичей** (Cascade Topology) и **нативной MCP-интеграцией** для AI-управления. Подтверждает несколько рискованных идей нашего плана (cascade, MCP).

Remnawave разбирали раньше — его сильные стороны (Config Profile, injectHosts-директивы) уже учтены в основном плане.

---

## 1. Что PasarGuard делает лучше нас

### 1.1 Host как богатый override-слой

У нас в основном плане Host = `(Node, Inbound, public_override)` — довольно бедно. У PasarGuard override'ы покрывают почти все параметры inbound'а. Расширяем модель:

**Новая модель Host (патч к разделу 10 основного плана):**

```yaml
Host:
  id: uuid
  remark: string               # display name, поддерживает format variables
  type: direct | balancer | chain     # NEW: добавляем chain (cascade)
  enabled: bool
  priority: int                # NEW: порядок в подписке (lower = выше)
  status_filter: [UserStatus]  # NEW: active, expired, limited, on_hold

  # === Direct & Balancer (существующее) ===
  node_id: uuid
  inbound_id: uuid
  address: [string, ...]       # NEW: set с random selection per request
  port: int | string           # NEW: int или "8080,8443,9090"
  sni: [string, ...]           # NEW: set
  host: [string, ...]          # NEW: set для HTTP/WS Host header
  path: string                 # NEW

  # === Security overrides (NEW) ===
  security: inbound_default | none | tls | reality
  alpn: [string, ...]          # auto-sorted h3 → h2 → http/1.1
  fingerprint: string          # chrome, firefox, safari, edge, ios, android, none
  allow_insecure: bool
  ech_config_list: string

  # === Transport overrides (NEW, per-protocol) ===
  transport_settings:
    websocket: { heartbeat_period, ... }
    grpc: { multi_mode, idle_timeout, health_check_timeout, ... }
    kcp: { header, mtu, tti, uplink_capacity, ... }
    tcp: { header, request, response }
    xhttp: { mode, no_grpc_header, x_padding_bytes, ...,
             download_settings: <host_id> }   # NEW: ссылка на другой host
    mux:
      xray: { enabled, concurrency, xudp_concurrency, ... }
      sing_box: { enable, protocol: smux|yamux|h2mux, brutal: {...} }
    fragment:
      xray: { packets, length, interval }
      sing_box: { fragment, fragment_fallback_delay }
    noise:
      xray: [{ type, packet, delay, apply_to }]

  # === Advanced (NEW) ===
  use_sni_as_host: bool
  random_user_agent: bool
  http_headers: { Header-Name: value, ... }

  # === Balancer (из прошлого дополнения) ===
  balancer:
    entry_node_id: uuid
    target_host_ids: [uuid, ...]
    strategy: leastLoad | roundRobin | random | leastPing
    healthcheck: { url, interval, tolerance_ms }
    failover_host_ids: [uuid, ...]

  # === Chain (NEW, см. секцию 2) ===
  chain:
    role: portal | relay | bridge
    mode: reverse | forward
    upstream_node_id: uuid      # куда форвардить (для portal/relay)
    tunnel_port: int
    tunnel_reality:
      dest: string
      server_names: [string, ...]
      private_key: string       # auto-generated
      short_ids: [string, ...]  # auto-generated
    transport: tcp | xhttp | grpc
    transport_settings: { ... }
```

### 1.2 Format Variables в Host

**Что это:** шаблонизация полей `remark` и `address` через переменные, которые подставляются при генерации подписки.

**Переменные (наш набор):**

| Variable | Описание | Пример |
| --- | --- | --- |
| `{SERVER_IP}` | Публичный IPv4 ноды | `1.2.3.4` |
| `{SERVER_IPV6}` | Публичный IPv6 | `2001:db8::1` |
| `{USERNAME}` | Имя юзера | `john_doe` |
| `{PROTOCOL}` | Протокол инбаунда | `vless` |
| `{TRANSPORT}` | Транспорт | `ws` |
| `{DATA_USAGE}` | Использовано трафика | `1.5 GB` |
| `{DATA_LIMIT}` | Лимит | `100 GB` или `∞` |
| `{DATA_LEFT}` | Остаток | `98.5 GB` или `∞` |
| `{DAYS_LEFT}` | Дней до конца | `30` или `∞` |
| `{EXPIRE_DATE}` | Дата (Gregorian) | `2026-08-15` |
| `{STATUS_EMOJI}` | Эмодзи статуса | `✅`, `⌛️`, `🪫`, `❌`, `🔌` |
| `{USAGE_PERCENTAGE}` | Процент использования | `15.5` |
| `{ADMIN_USERNAME}` | Создатель | `admin` |

**Fallback при отсутствии значения:**
- Для дат/лимитов — `∞`
- Для неопределённых — `<missing>`

**Пример:**
```yaml
remark: "🇳🇱 {SERVER_IP} — {USERNAME} — {DATA_LEFT} — {STATUS_EMOJI}"
# Result: "🇳🇱 1.2.3.4 — john_doe — 87 GB left — ✅"
```

**Плюсы:** мгновенный uplift retention (юзер видит «свой» сервер), zero-cost для панели (template engine, без доп. запросов в БД).

**Реализация:** sandbox-шаблонизатор (text/template в Go или Jinja2 в Python), никаких `eval`. Кеш по `(host_id, user_id, fetch_time)` с инвалидацией при изменении host/user.

### 1.3 Wildcard `*` с random salt

**Что:** в `sni`, `host`, `address` можно указать паттерн с `*`, на каждый fetch подписки `*` заменяется на случайную соль.

```yaml
sni: ["*.example.com"]
# Каждый fetch: "a1b2c3d4.example.com", "9f8e7d6c.example.com", ...
```

**Анти-детект:** DPI не может натренировать эвристику на конкретный домен.

**Реализация:** генерим 8-char hex salt, делаем `replace("*", salt)`, кешируем на 60 секунд.

### 1.4 Status-based host visibility

```yaml
status_filter: [active]      # только активные
status_filter: [active, on_hold]  # активные + на паузе
status_filter: []            # все (default)
```

Дополняет существующий group-based filter (через inbound_tags → squads/pools). **Берём в MVP.**

### 1.5 Multi-port inbound + random selection

Если у inbound'а несколько портов (`"8080,8443,9090"`) и у Host'а `port: null` — на каждый fetch подписки выбирается случайный. Полезно для port hopping.

### 1.6 XHTTP `download_settings` — ссылка на другой host

```yaml
transport_settings:
  xhttp:
    download_settings: <host_id>  # ID другого host'а для download
```

**Важно:** referenced host не может иметь свой download host (no nesting). Валидация в панели.

### 1.7 Bulk Group Operations

У нас в основном плане есть bulk user operations. Расширяем по образцу PasarGuard:

```
POST /api/groups/bulk/add
{
  "group_ids": [1, 2],
  "users": [10, 11, 12],     // конкретные юзеры
  "admins": [5, 6],          // все юзеры этих админов
  "has_group_ids": [3]       // только те, у кого уже есть группа 3
}
```

Если ни `users`, ни `admins` не указаны — действует на **всех** юзеров. Существующие ассоциации игнорятся (no duplicates).

### 1.8 Multi-inbound на одном порту (fallbacks)

Xray fallbacks — несколько inbounds на одном порту с разной маршрутизацией. У нас в плане это уже есть через Inbound Set. Подтверждаем.

---

## 2. Cascade Topology (Celerity killer-feature)

### 2.1 Что это

**Цепочки нод, где клиент подключается к одной, а трафик выходит через другую (возможно через несколько).** Это НЕ балансировка нагрузки — это **обход инфраструктурных ограничений**.

**Use-cases:**
- Portal за NAT / firewall (Reverse mode)
- Выход через IP «доверенного» хостера (operator-сети фильтруют по IP)
- Скрытие exit-IP
- Multi-hop для усложнения трассировки

### 2.2 Режимы

**Reverse Chain (Portal за NAT, Bridge за рубежом):**

```
                 ┌─────────────────┐
                 │     CLIENTS     │
                 └────────┬────────┘
                          │
                  Portal (entry)
                  (публичный или NAT)
                          │
                  туннель (инициирует bridge)
                          │
                  Bridge (exit, за NAT/abroad)
                          │
                       Internet
```

Bridge сам открывает persistent-соединение к Portal. Portal проксирует входящий трафик клиентов через этот туннель. Portal может быть за NAT.

**Forward Chain (все ноды публичные):**

```
                 ┌─────────────────┐
                 │     CLIENTS     │
                 └────────┬────────┘
                          │
                       Portal
                          │
                       Relay (опц.)
                          │
                       Bridge
                          │
                       Internet
```

Portal сам устанавливает соединения через цепочку outbound'ов. Все ноды публичные.

### 2.3 Xray-механизмы

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
    {
      "tag": "vless-in",
      "port": 443,
      "protocol": "vless",
      "settings": { "clients": [...] }
    }
  ],
  "outbounds": [
    {
      "tag": "tunnel",
      "protocol": "reverse",
      "settings": {
        "address": "127.0.0.1",  // bridge reverse listener
        "port": 4443,
        "flow": "xtls-rprx-vision"
      }
    }
  ],
  "routing": {
    "rules": [
      { "inboundTag": ["vless-in"], "outboundTag": "tunnel" }
    ]
  }
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
      "settings": {
        "clients": [],
        "decryption": "none"
      },
      "streamSettings": {
        "network": "tcp",
        "security": "reality",
        "realitySettings": {
          "dest": "www.google.com:443",
          "serverNames": ["www.google.com"],
          "privateKey": "<bridge_x25519_private>",
          "shortIds": ["<auto>"]
        }
      }
    },
    { "tag": "api", "port": 61000, "protocol": "dokodemo-door", "settings": { "address": "127.0.0.1" } }
  ],
  "outbounds": [
    { "tag": "direct", "protocol": "freedom" }
  ]
}
```

**В Portal ничего специфичного для bridge не пишется** — Portal просто форвардит всё в tunnel outbound, а bridge принимает это на своём tunnel-in inbound.

### 2.4 Ограничения (для валидации в панели)

Из Celerity-документации:

- **REALITY + WebSocket не работают** — WebSocket не поддерживает uTLS, REALITY требует uTLS fingerprint
- **Forward Chain требует public IP** на каждом хопе
- **Mixed modes в одной цепочке нельзя** — нельзя смешивать reverse и forward в одном pipeline
- **На relay нельзя один порт для двух хопов** — relay, который одновременно bridge и portal, требует разные порты для incoming/outgoing tunnel
- **XHTTP в cascade** — у Celerity в beta, поддержка зависит от версии Xray

### 2.5 Реализация в панели

**Новый Host type: `chain`.**

```yaml
Host:
  type: chain
  chain:
    role: portal | relay | bridge
    mode: reverse | forward
    upstream_node_id: uuid    # куда проксировать
    tunnel_port: int
    tunnel_reality:
      enabled: bool
      dest: string
      server_names: [string, ...]
      private_key: <auto-gen>
      public_key: <auto-gen>   # для клиента
      short_ids: [auto-gen, ...]
    transport: tcp | xhttp | grpc
```

**При создании chain host:**
1. Панель генерирует x25519 keypair + 3-5 shortIds для туннеля.
2. Панель генерирует конфиг для upstream-ноды (Portal) с `reverse` outbound, указывающим на bridge.
3. Панель генерирует конфиг для bridge-ноды с `vless` inbound + REALITY на tunnel_port.
4. **Валидация ограничений:** если пользователь выбрал `mode: forward` + `public_ip: false` на одной из нод → reject.
5. Apply конфигов на обе ноды через Agent.

**В подписке клиента:**
- chain host выглядит как обычный host с address = portal_address и inbound = tunnel-in
- Клиент коннектится к Portal, всё остальное — дело Portal.

**MCP-операции (см. секцию 3):**
- `manage_cascade: create | update | delete | deploy | undeploy | reconnect`
- `get_topology` — вернуть все ноды и связи

### 2.6 Roadmap для cascade

- **MVP**: не включаем. Усложняет модель данных, требует отдельной валидации и UX-тестирования.
- **Phase 2** (после MVP): добавляем базовый `chain` type с `reverse` mode. UI — Network Map (как у Celerity), drag-and-drop для построения цепочек.
- **Phase 3**: `forward` mode, `relay` role, мульти-хоп, MCP-управление.
- **Phase 4**: ACL-фильтрация на bridge-ноде, политики ротации x25519.

---

## 3. MCP-интеграция (Celerity-паттерн)

### 3.1 Что это

**Model Context Protocol** — стандарт для AI-ассистентов (Claude, Cursor, и т.д.) для прямого вызова инструментов панели. Не REST для людей, а structured tool calls для AI.

**Celerity уже это сделал** (https://docs.mcp-user-guide.md), и это уникально для индустрии.

### 3.2 Что добавляем в наш план

**Новый модуль: `mcp` (опциональный, в отдельном бинаре или embedded).**

```yaml
mcp:
  enabled: bool
  bind: "127.0.0.1:8081"      # localhost-only, не светить наружу
  auth: oauth2 | api_key
  rate_limit: 100 req/min per token
  audit_log: true              # все MCP-вызовы пишем в audit_log
```

**Tool set (Phase 2):**
```
# Users
list_users, get_user, create_user, update_user, delete_user, enable_user, disable_user

# Nodes
list_nodes, get_node, get_node_status, get_node_metrics, restart_core

# Hosts
list_hosts, get_host, create_host, update_host, enable_host, disable_host

# Cascades (Phase 3)
list_cascades, get_cascade, manage_cascade (create|update|delete|deploy|undeploy|reconnect), get_topology

# Stats
get_stats, get_user_traffic, get_node_metrics

# System
get_health, get_audit_log
```

**Сценарий:** AI-ассистент Claude Desktop подключён к панели через MCP. Оператор говорит: «Создай 5 нод в EU, разверни их через Hetzner, настрой cascade с portal в Frankfurt и bridge в Амстердаме, проверь что всё работает и пришли мне отчёт». AI вызывает tools в нужной последовательности.

### 3.3 Безопасность

- MCP-server **только на localhost** (Unix socket или 127.0.0.1)
- API key с минимальными scopes
- Все вызовы логируются в audit
- Rate-limit
- Dry-run mode для деструктивных операций

---

## 4. CC Agent (dynamic user management)

### 4.1 Что это

**HTTP API на ноде**, через который панель управляет Xray-клиентами **без рестарта ядра**. Xray умеет динамически добавлять/удалять клиентов через свой gRPC API.

**Достоинство:** при создании нового юзера не нужен restart Xray → нода не теряет активные сессии → zero-downtime.

### 4.2 Как устроено у Celerity

```
┌──────────────────┐
│  PANEL           │
│  (Mongo+Redis)   │
└────────┬─────────┘
         │ HTTPS / SSH
         ▼
┌──────────────────┐
│  CC Agent (HTTP) │  ← port 62080
│  on Node         │
└────────┬─────────┘
         │ gRPC
         ▼
┌──────────────────┐
│  Xray Core       │  ← port 61000 (gRPC API)
│  (with API ext.) │
└──────────────────┘
```

**CC Agent эндпоинты:**
- `POST /api/users/add` — добавить клиента в inbound
- `POST /api/users/remove` — удалить клиента
- `GET /api/users/online` — список онлайн-юзеров
- `GET /api/stats` — трафик
- `GET /api/health` — healthcheck

### 4.3 Наша реализация

У нас в основном плане Agent уже есть, но без явного выделения dynamic-user-management. Обновляем:

**Agent capabilities (раздел 8.2 основного плана):**
- ✅ Receive new config (mTLS)
- ✅ Apply config to Core
- ✅ Collect metrics
- **✅ Dynamic user management (NEW)**: add/remove user without core restart через Xray gRPC API
- **✅ Health check via Xray API** (статус через `StatsService.QueryStats`)

**API контракт Agent → Xray:**
- Inbound manipulation через gRPC `HandlerService.AddUser` / `RemoveUser`
- Альтернатива: hot-reload через `api` inbound с `StatsService`

**Гарантии:**
- Добавление/удаление юзера — O(1), не требует restart
- При сбое Xray — Agent перезапускает его с последним конфигом
- При сбое Agent — systemd его поднимает

---

## 5. Webhooks — детали

### 5.1 Подпись HMAC-SHA256 (Celerity-паттерн)

```
POST /webhook
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

**Проверка на стороне получателя:**
```python
expected = "sha256=" + hmac.new(
    secret.encode(),
    f"{timestamp}.{raw_body}".encode(),
    hashlib.sha256
).hexdigest()
assert hmac.compare_digest(expected, signature)
```

**Дополнительная защита:** reject если `abs(now - timestamp) > 5min` (anti-replay).

### 5.2 Disk alerts с hysteresis (Celerity-паттерн)

**События:**
- `host.disk_low` — free < warning% (default 20%)
- `host.disk_critical` — free < critical GB (default 5 GB)
- `host.disk_recovered` — free > warning%

**Поведение:**
- Событие срабатывает **один раз при пересечении** (не спам каждую минуту)
- `recovered` срабатывает только после `low` (не сразу)
- Thresholds настраиваются per-panel (Settings → Security → Webhooks)

**Реализация в нашем event bus:**
- Хранить `last_disk_state: ok | low | critical` per node
- На новом sample: если пересекли threshold и `state` изменился — emit event

---

## 6. ACL на ноде (Traffic Filtering)

### 6.1 Что это (Celerity-паттерн)

**Фильтрация трафика на самой ноде** через Hysteria/Xray ACL. Не глобально, а per-node.

### 6.2 Синтаксис

```
reject(suffix:doubleclick.net)     # Блокировка рекламы
reject(geoip:cn)                   # Блокировка китайских IP
direct(all)                        # Всё остальное напрямую
```

**С custom proxy:**
```
my-proxy(geoip:ru)                 # Через SOCKS5 только РФ
```

### 6.3 Реализация в нашей архитектуре

**Через Inbound Set template** (наш раздел 11):
- В шаблон inbound'а можно включить `routing.rules` с ACL
- Панель валидирует JSON-schema
- Поддержка GeoIP/GeoSite через файлы (монтируются в Core)

**Capability flag** (раздел 7): `ACL` — есть ли у ядра поддержка routing.rules с этими синтаксисами.

**Roadmap:** **Phase 3**. На MVP — скип, не критично для VPN-сервиса (большинство пользуются adblock на клиенте, не на сервере).

---

## 7. Уточнение стека и модели данных

### 7.1 Стек

**Наш основной выбор Go + PostgreSQL остаётся.** Celerity использует Node.js + MongoDB — это быстрее для MVP, но хуже для аналитики и consistency. Для серьёзного VPN-сервиса с большой базой юзеров PostgreSQL + ClickHouse — правильнее.

**Альтернатива для маленького MVP (≤1k users):** Python + FastAPI + MongoDB. Быстрее в разработке, проще для solo-разработчика. Но упрёмся в аналитику при росте.

### 7.2 Новые сущности в модели данных

```yaml
# Прямые дополнения к разделу 16 основного плана

# Disk alerts
DiskAlertThresholds:
  warning_percent: 20
  critical_gb: 5

# MCP
McpConfig:
  enabled: bool
  bind: string
  auth_type: oauth2 | api_key
  rate_limit_rpm: int
  allowed_tools: [string, ...]   # whitelist tools

# Cascade
Cascade:
  id: uuid
  name: string
  mode: reverse | forward
  hops: [CascadeHop, ...]        # упорядоченный список нод

CascadeHop:
  position: int                  # 0 = entry, N = exit
  node_id: uuid
  role: portal | relay | bridge
  tunnel_port: int
  transport: tcp | xhttp | grpc
  reality: { dest, server_names, private_key, public_key, short_ids }

# HMAC secret для webhooks
WebhookEndpoint:
  id: uuid
  url: string
  secret: string                 # для HMAC
  events: [string, ...]          # фильтр событий
  enabled: bool
  created_at: timestamp
```

### 7.3 Новые API scopes (для Cabinet API и MCP)

Заимствуем naming у Celerity:

```
users:read
users:write
nodes:read
nodes:write
nodes:control               # NEW: restart core, drain
hosts:read
hosts:write
cascades:read               # NEW: для Phase 2+
cascades:write              # NEW: для Phase 2+
stats:read
sync:write
system:read                 # NEW: для healthchecks, audit
mcp:invoke                  # NEW: для MCP-токенов
```

---

## 8. Итоговые апдейты к ARCHITECTURE.md

| Раздел основного плана | Что меняем |
| --- | --- |
| **7. Core abstraction** | Расширяем capability-флаги: `WIREGUARD`, `HYSTERIA2`, `ACL`, `CASCADE`, `DYNAMIC_USERS`, `WILDCARD_RANDOM` |
| **8. Nodes & Agents** | Подчёркиваем CC Agent pattern (dynamic user management) |
| **10. Host manager** | Полностью переписываем модель Host: rich overrides, format variables, wildcard random, status filter, priority, type=direct/balancer/**chain** |
| **NEW 10.3 Cascade Topology** | Новый раздел — полное описание chain type, режимы reverse/forward, Xray-механизмы, ограничения, MCP-операции |
| **11. Protocol configuration** | Уточняем: per-host transport overrides (ws, grpc, kcp, tcp, xhttp, mux, fragment, noise) |
| **13. Cabinet API** | Берём naming scopes у Celerity. HMAC-SHA256 для webhooks. Disk alerts с hysteresis. |
| **NEW 13.4 MCP** | Новый раздел: MCP-сервер, набор tools, безопасность, rate-limit |
| **16. Data model** | Добавляем сущности: Cascade, CascadeHop, McpConfig, WebhookEndpoint, DiskAlertThresholds |
| **17. Tech stack** | Подтверждаем Go + PostgreSQL. Celerity-style Node.js + Mongo отмечаем как «альтернатива для маленького MVP» |
| **20. Roadmap** | Phase 2: cascade (reverse), MCP, format variables, wildcard, disk alerts. Phase 3: cascade (forward), ACL. Phase 4: WireGuard, etc. |
| **21. Что добавлено «сверх минимума»** | Обновляем с новыми пунктами |

---

## 9. Что НЕ берём (и почему)

| Фича | Почему скипаем |
| --- | --- |
| PasarGuard: multi-inbound fallbacks на одном порту | У нас это есть через Inbound Set, не дублируем |
| Celerity: Real-time SSH terminal в UI | Nice-to-have, не критично для MVP, добавим в Phase 3 |
| Celerity: Hysteria 2 как core | У нас в MVP core = sing-box, Hysteria добавим в Phase 4 (через CoreProvider) |
| PasarGuard: WireGuard inbound | Опционально, требует отдельного типа подключения. Phase 4+ |
| Celerity: status-based filtering без group context | У нас есть и group-based, и status-based, не дублируем |
| PasarGuard: 50+ format variables | Берём базовый набор (~15), остальные по запросу |

---

## 10. Следующие шаги

1. **Обновить ARCHITECTURE.md** с патчами из этого addendum (можно выборочно по разделам или одним большим апдейтом).
2. **Зафиксировать tech-decisions:**
   - Cascade Topology в Phase 2 (не MVP)
   - MCP как опциональный модуль с самого начала
   - Format variables в MVP (легко реализуется, сильно прокачивает UX)
   - Wildcard `*` в MVP+
3. **Обновить модель данных** в БД-миграциях (когда начнём кодить).
4. **Согласовать формат Cabinet API** (scopes, webhook HMAC, disk events).
5. **Возвращаться к вопросам архитектуры** — что ещё хочешь разобрать?
