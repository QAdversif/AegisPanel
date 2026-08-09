# chore(frontend): lint cleanup — auto-fix 105 vue warnings in 5 files

## Что внутри

Mechanical fix 105 `vue/*` lint warnings в 5 файлах. `eslint --fix` + ручной review diff (без Prettier scope creep). Чистит долг из v0.8.x-bucket'а ROADMAP.

| Файл | Было | Стало |
|---|---|---|
| `src/views/PlansView.vue` | 43 | 0 |
| `src/views/NodesView.vue` | 33 | 0 |
| `src/views/CredentialsView.vue` | 13 | 0 |
| `src/views/WebhooksView.vue` | 12 | 0 |
| `src/components/WebhookEventsPicker.vue` | 4 | 0 |
| **TOTAL** | **105** | **0** |

## Какие правила триггерили

| Правило | Количество | Auto-fixable |
|---|---|---|
| `vue/max-attributes-per-line` | 37 | ✓ |
| `vue/multiline-html-element-content-newline` | 10 | ✓ |
| `vue/singleline-html-element-content-newline` | 8 | ✓ |
| `vue/html-self-closing` | 4 | ✓ |
| `vue/html-indent` | 3 | ✓ |

Все 105 — auto-fixable, фикс прошёл без ручной правки.

## Что меняет fix

- **`vue/max-attributes-per-line`** — multi-line attribute lists реформатятся, каждый attribute на отдельной строке. Улучшает readability длинных `<div class="..." :disabled="..." @click="...">` блоков.
- **`vue/multiline-html-element-content-newline`** — enforce newline между multiline-тегами.
- **`vue/singleline-html-element-content-newline`** — enforce single-line для short elements.
- **`vue/html-self-closing`** — HTML5 void elements (`<input>`, `<br>`, `<img>`) не должны быть self-closed (`<input />` → `<input>`).
- **`vue/html-indent`** — enforce consistent indentation в template.

Pure style fixes, no semantic change.

## Scope control

Фикс запущен ТОЛЬКО на 5 target файлах (`npx eslint <5 paths> --fix`), не на `eslint . --fix`. Это критично — eslint fix может зацепить Prettier (per project config), который реформатит ВСЁ `frontend/src/**` дерево. Запустил targeted, проверил `git diff --stat` — изменения только в 5 файлах.

## Pre-pr.sh

**10/10 ✓**:
- backend gofmt / go build / go vet (integration tags) / go test (short) / golangci-lint
- frontend codegen:check / type-check / lint / build
- docs markdownlint
- agent memory 30KB (under 70KB cap)

`npm run lint` — 0 errors, 0 warnings (было 105).

## Diff

```
 frontend/src/components/WebhookEventsPicker.vue |   8 +-
 frontend/src/views/CredentialsView.vue          |  61 ++++++++---
 frontend/src/views/NodesView.vue                | 130 +++++++++++++++++------
 frontend/src/views/PlansView.vue                | 131 +++++++++++++++++++-----
 frontend/src/views/WebhooksView.vue             |  36 +++++--
 5 files changed, 282 insertions(+), 84 deletions(-)
```

## Privacy

Никаких privacy-rule items. Только стиль кода.
