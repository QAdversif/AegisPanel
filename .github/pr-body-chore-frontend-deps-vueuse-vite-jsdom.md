# chore(frontend-deps): bump @vueuse/core, vite, jsdom

## Что внутри

Только `frontend/package.json` + `frontend/package-lock.json`. Никаких изменений в коде.

| Пакет | Было | Стало | Тип |
|---|---|---|---|
| `@vueuse/core` | `^11.3.0` | `^14.4.0` | major (×3) |
| `vite` | `^7.3.6` | `^8.2.0` | major |
| `jsdom` | `^25.0.1` | `^30.0.1` | minor (×5) |

## Почему без изменений в коде

- **@vueuse/core 14.4.0** — декларирован в `package.json`, но **нигде в `src/` не импортируется** (проверил grep'ом). Три мажора прошли без сайд-эффектов, потому что пакет ни разу не вызывается. В долгосроке — либо начать использовать (Pinia + vueuse для storage / debounce / etc.), либо удалить из зависимостей. В этом PR не трогаю.
- **vite 8.2.1** — `@vitejs/plugin-vue@6.0.0` (текущий) объявляет peer `vite: ^5.0.0 || ^6.0.0 || ^7.0.0 || ^8.0.0` → совместим. Vite 8 теперь по умолчанию использует Rolldown (бывший `rolldown-vite`), build прошёл за 19.9s без warnings. `vite.config.ts` менять не пришлось.
- **jsdom 30.0.1** — используется только транзитивно через `vitest`. Версия 30 не имеет breaking changes, влияющих на текущую конфигурацию (vitest 4.1.10). `vite.config.ts` не задаёт `test` блок и `testEnvironment` — vitest использует `node` по умолчанию, jsdom только в lockfile как transitive.

## Pre-pr.sh

10/10 ✓ — все 10 проверок зелёные, включая `frontend build` (vite 8 + rolldown собирает за 19.9s) и `frontend type-check` (vue-tsc 3.3.8 совместим с vueuse 14 declarations).

## CI

Ожидаю 24/24 — `containers` job обычно no-op для frontend-deps (нет изменений в `Dockerfile`). Если упадёт — посмотрю конкретный шаг.

## Privacy

Никаких privacy-rule items.
