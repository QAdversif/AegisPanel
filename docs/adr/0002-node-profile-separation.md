# ADR-0002: Node Profile separation (reality-direct vs caddy-fronted)

**Status:** Accepted (2026-07-17)
**Drives:** ARCHITECTURE.md §19.4.4, §7 (validator)
**Supersedes:** Неявное предположение «Caddy → sing-box на 127.0.0.1:10000
работает для всего» (ARCHITECTURE.md §19.4.2 / §26.6.2 до v8)

## Context

В исходных примерах Caddyfile для ноды (ARCHITECTURE.md §19.4.2, §26.6.2)
был один паттерн:

```caddyfile
node01.example.com:443 {
    reverse_proxy 127.0.0.1:10000  # sing-box/Xray
}
```

Это работает для WebSocket, gRPC, XHTTP поверх обычного TLS-сертификата.
**Не работает для REALITY.**

REALITY требует:

1. **Сырого ClientHello** от клиента — для uTLS-фингерпринта.
2. **SNI в `serverNames`** ядра + существования домена у dest-сервера.

При reverse-proxy через Caddy (терминация TLS) ядро получает соединение
**от Caddy** (localhost), а не от клиента. uTLS-фингерпринт потерян. Маскировка
REALITY ломается.

И обратное: некоторые транспорты (WS, gRPC) **требуют** reverse-proxy, потому
что клиент не умеет сам терминировать TLS с нестандартным SNI.

**Wildcard `*` с random salt + REALITY** — вторая несовместимая пара.
REALITY ретранслирует handshake dest-сервера, SNI обязан быть реальным
доменом. Случайный `a1b2c3d4.example.com` не существует у dest'а → handshake
падает.

## Decision

Ввести **Node Profile** — поле `nodes.profile` (`reality-direct` или
`caddy-fronted`). Валидатор в `CoreProvider.ValidateConfig` проверяет
совместимость профиля с транспортом.

**Матрица совместимости:**

| Транспорт | `reality-direct` | `caddy-fronted` |
| --- | --- | --- |
| REALITY | ✅ (на 443) | ❌ `ErrRealityRequiresDirectProfile` |
| WS / gRPC / XHTTP | ✅ (alt-порты через Caddy) | ✅ (через 443) |
| HY2 / TUIC | ✅ (на 443) | ⚠️ редко используется |
| Wildcard-SNI + REALITY | ❌ `ErrWildcardSniIncompatibleWithReality` | ❌ то же |
| Wildcard-SNI + обычный TLS | ✅ | ✅ |

**Дефолт профиля при создании ноды через Ansible: `reality-direct`.**
Оператор явно переключает на `caddy-fronted` только если нода за CDN
(Cloudflare, BunnyCDN) с обычным TLS-сертификатом.

**Реализация валидатора:** `internal/cores/validate.go`:

```go
func (v *Validator) CheckProfileTransport(profile NodeProfile, t Transport) error {
    if t.Security == "reality" && profile == ProfileCaddyFronted {
        return ErrRealityRequiresDirectProfile
    }
    if t.SNIHasWildcard() && t.Security == "reality" {
        return ErrWildcardSniIncompatibleWithReality
    }
    return nil
}
```

## Alternatives considered

**A. Запретить Caddy на нодах вообще.** Отклонено: Caddy нужен для
WS/gRPC/XHTTP и для маскировки decoy-сайта. Без Caddy теряем
популярные транспорты.

**B. Один профиль `caddy-fronted`, REALITY через Cloudflare.** Рассмотрено,
отклонено: Cloudflare не передаёт uTLS-фингерпринт на origin, REALITY
ломается так же.

**C. Сделать профили опциональными (не валидировать).** Отклонено: операторы
скопируют примеры из доки, не прочитают раздел, получат неработающую
конфигурацию. Валидатор — дешёвая страховка.

## Consequences

**Положительные:**

- Явная модель — оператор сразу понимает компромиссы.
- Валидатор ловит несовместимости на этапе config save, не в проде.
- Примеры Caddyfile в документации теперь соответствуют профилям.

**Отрицательные:**

- Дополнительный код в `CoreProvider.ValidateConfig` (один switch + 2 ошибки).
- Примеры Caddyfile в §19.4.2 нужно сопровождать с учётом профиля (уже сделано в v8).

## Implementation

- ARCHITECTURE.md §19.4.4 (новый раздел, v8) — описание профилей + матрица.
- ARCHITECTURE.md §10.1.2 — добавлен явный запрет wildcard-SNI + REALITY.
- PR **#53** (после #50-#52): реализовать `internal/cores/validate.go`
  с проверкой профиля + транспорта.
- Тесты: `internal/cores/validate_test.go` — покрытие матрицы.

## References

- ARCHITECTURE.md §19.4.4 (новый, v8)
- ARCHITECTURE.md §10.1.2 (обновлён, v8)
- ARCHITECTURE.md §25 (v8 entry)
- Внешнее ревью: «AegisPanel architectural review» (2026-07-17)
- Xray REALITY docs: <https://github.com/XTLS/REALITY>
- sing-box REALITY docs: <https://sing-box.sagernet.org/configuration/inbound/vless/#reality>
