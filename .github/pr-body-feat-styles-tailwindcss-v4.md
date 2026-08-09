# feat(styles): migrate to tailwindcss v4 (CSS-first config + @tailwindcss/vite)

## Что внутри

Полная миграция `tailwindcss` с v3 на v4 + переход с PostCSS-пайплайна на `@tailwindcss/vite` плагин.

| Что | Было (v3 + PostCSS) | Стало (v4 + @tailwindcss/vite) |
|---|---|---|
| **Pipeline** | `postcss.config.js` (tailwindcss + autoprefixer) | `@tailwindcss/vite` плагин в `vite.config.ts` |
| **Config** | `tailwind.config.ts` (117 строк, theme.extend, plugins) | CSS-first: `@theme { ... }` в `styles.css` |
| **CSS entrypoint** | `@tailwind base; @tailwind components; @tailwind utilities;` | `@import "tailwindcss";` |
| **Dark mode** | `darkMode: ['class']` в JS-конфиге | `@custom-variant dark (&:where(.dark, .dark *));` в CSS |
| **Plugins** | `plugins: [animate, forms, typography]` (JS) | `@plugin "@tailwindcss/forms"; @plugin "@tailwindcss/typography";` (CSS) |
| **Accordion animations** | `tailwindcss-animate` пакет | inline `@keyframes` + `--animate-accordion-*` в `@theme` |
| **Body styles** | `@apply bg-background text-foreground;` | `background-color: var(--color-background); color: var(--color-foreground);` (более v4-native) |
| **Files** | `postcss.config.js`, `tailwind.config.ts` | **удалены** (не нужны в v4) |

## package.json

```diff
- "tailwindcss": "3.4",
- "tailwindcss-animate": "1.0",
- "@tailwindcss/forms": "^0.5.11",
- "@tailwindcss/typography": "^0.5.20",
- "autoprefixer": "10.5",
- "postcss": "^8.5.25",
+ "tailwindcss": "^4.3.3",
+ "@tailwindcss/forms": "^0.5.11",
+ "@tailwindcss/typography": "^0.5.20",
+ "@tailwindcss/vite": "^4.3.3",
```

(Версии `@tailwindcss/forms` и `@tailwindcss/typography` не менялись — их v0.5.x ревизии peer-compatible с `tailwindcss@>=4.0.0`, проверено через `npm view <pkg> peerDependencies`.)

## vite.config.ts

```diff
+ import tailwindcss from '@tailwindcss/vite'
- plugins: [vue()],
+ plugins: [vue(), tailwindcss()],
```

PostCSS-конфиг удалён. `@tailwindcss/vite` подменяет PostCSS-пайплайн на Vite-плагин: обрабатывает CSS на этапе transform, без промежуточного PostCSS-слоя.

## styles.css (полностью переписан, ~190 строк)

```css
@import "tailwindcss";
@plugin "@tailwindcss/forms";
@plugin "@tailwindcss/typography";
@custom-variant dark (&:where(.dark, .dark *));

@keyframes accordion-down { from { height: 0; } to { height: var(--reka-accordion-content-height); } }
@keyframes accordion-up   { from { height: var(--reka-accordion-content-height); } to { height: 0; } }

@theme {
  --color-*: hsl(var(--*));      /* 18 color tokens, shadcn-vue convention */
  --radius-lg: var(--radius);    /* --radius-md, --radius-sm derived */
  --font-sans: system-ui, ...;   /* --font-mono for monospace */
  --animate-accordion-down: accordion-down 0.2s ease-out;
  --animate-accordion-up:   accordion-up   0.2s ease-out;
}

@layer base {
  :root  { --background: 0 0% 100%; ... }   /* shadcn-vue HSL token system */
  .dark  { --background: 222.2 47.4% 4.9%; ... }
  /* + legacy --aegis-* tokens для Phase 0 кода */
}
```

## Почему CSS-first config (а не legacy JS-конфиг)

v3 поддерживает `tailwind.config.{js,ts}` для обратной совместимости, но рекомендованный путь — `@theme` в CSS. Преимущества:
- **Single source of truth** — все design tokens в одном файле
- **CSS variables напрямую** — больше не нужно копировать HSL-значения в JS
- **MCP/IDE support** — Tailwind v4 подсвечивает `@theme` блоки в IDE
- **Нет build-step для config** — Vite-плагин парсит `styles.css` напрямую

## Что не делали (и почему)

- **Не бампили `@vueuse/core`, `vite`, `jsdom`** — это PR #196 (отдельный коммит)
- **Не трогали `:root` / `.dark` токены** — shadcn-vue HSL system сохранён 1-в-1
- **Не трогали `container` утилиту** — в коде она не используется (grep чист), убрали `container: { ... }` из v3 конфига
- **Не трогали `frontend/pnpm-lock.yaml`** — он untracked leftover от экспериментов, не в этой PR

## Verification

**Pre-pr.sh 10/10 ✓**:
- `frontend build` — vite 8.2.1 + @tailwindcss/vite + rolldown, 17s, 52.63 kB CSS (gzip 9.22 kB)
- `frontend type-check` — vue-tsc 3.3.8 ✓
- `frontend lint` — eslint ✓
- `frontend codegen:check` — openapi-typescript ✓
- `backend gofmt / go build / go vet (integration tags) / go test (short) / golangci-lint` — all green
- `docs markdownlint` — 0 errors

**CSS output check** (проверено через grep по `dist/assets/index-*.css`):
- `--color-background`, `--color-foreground`, etc. — все 18 color tokens в `:root`
- `--radius-sm/md/lg`, `--font-sans/mono` — присутствуют
- `@keyframes accordion-down` / `accordion-up` — инлайнятся
- `.bg-background`, `.text-foreground`, `.hover\:bg-primary\/90` (с opacity), `.dark\:bg-amber-950\/20` — utilities генерируются
- Forms + Typography плагины подключены (button/input/textarea базовые стили присутствуют)

## Privacy

Никаких privacy-rule items. Только frontend стили + конфиги.
