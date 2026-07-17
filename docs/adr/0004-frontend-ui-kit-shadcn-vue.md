# ADR-0004: Frontend UI-стек — shadcn-vue + Reka UI + TailwindCSS

**Status:** Accepted (2026-07-17)
**Drives:** ARCHITECTURE.md §1, §21 Phase 1 (MVP-0.1)
**Supersedes:** (нет — UI-стек не был зафиксирован ранее)
**Decision effort:** ~1-2 дня на интеграцию, легко обратимо. **Не архитектурный lock-in** → weekly review window не требуется (см. memory note про ADR-0001).

## Context

Перед MVP-0.1 (`v0.1.0-mvp-render`) нужно зафиксировать UI-стек админ-панели
Aegis. До этого решения в `frontend/package.json` есть только Vue 3.5 + TS +
Vite + Pinia + vue-i18n + VueUse + Axios + dayjs — UI-кит не подключён,
есть только `DashboardView.vue` (заглушка).

Требования к UI-стек, выведенные из сценариев Aegis:

1. **Типичные админ-экраны MVP:** таблицы (ноды, инбаунды, хосты, юзеры,
   подписки) с фильтрами/сортировкой/пагинацией; формы CRUD; modal/dialog
   для подтверждений; sheet/drawer для деталей; tabs для настроек; toast
   для feedback; sidebar + topbar layout; sub-page с QR-кодом.
2. **Solo-dev на MVP за 5-7 недель.** Скорость первичной разработки важна,
   но **гибкость на дистанции критичнее** — после MVP захочется
   «Aegis look», кастомный брендинг, возможно белый-лейбл для
   multi-instance оператора.
3. **TypeScript-first.** Весь стек уже на TS, UI-кит должен быть
   нативным, не «поддерживает @types/* через @ts-ignore».
4. **i18n (ru/en).** vue-i18n уже подключён, компоненты должны
   тривиально встраиваться в локализацию.
5. **Accessibility** (ARIA, keyboard nav) — не для галочки, а потому
   что админка оператора используется часами; `tab/enter/esc` должны
   работать везде.
6. **MIT license** (совместимость с AGPL-3.0 backend).
7. **Bundle size** — на MVP не критично, но на проде 30-80 KB лучше
   200+ KB.
8. **Не vendor lock-in** — компоненты должны быть модифицируемы
   без fight с framework.

## Decision

UI-стек Aegis v0.1.0+: **shadcn-vue + Reka UI + TailwindCSS**.

- **shadcn-vue** — коллекция копируемых Vue-компонентов (порт shadcn/ui
  из React-экосистемы). CLI `npx shadcn-vue@latest add <component>`
  копирует исходники в `src/components/ui/`. Код принадлежит проекту,
  правится без ограничений.
- **Reka UI** (бывший Radix Vue, переименован в 2025) — набор
  headless-примитивов с accessibility из коробки (Dialog, DropdownMenu,
  Tabs, Popover, Tooltip, Accordion, и т.д.). Используется под капотом
  shadcn-vue для сложных компонентов.
- **TailwindCSS v4** (или v3.x — на момент интеграции выбираем
  актуальный стабильный) — utility-first CSS-фреймворк. Основа для
  стилизации shadcn-vue-компонентов. Темы через CSS variables.

**Дополнительные зависимости:**

| Пакет | Зачем |
| --- | --- |
| `@tanstack/vue-table` | DataTable с фильтрами / сортировкой / пагинацией / virtual scroll. Headless, обёртку пишем сами поверх shadcn-vue `Table` примитива. Стандарт в индустрии. |
| `vee-validate` + `zod` + `@vee-validate/zod` | Формы CRUD (Nodes / Inbounds / Hosts / Users). `zod` схемы переиспользуем между клиентом и OpenAPI codegen (server-side валидация). |
| `class-variance-authority` (`cva`) | Варианты компонентов (button: primary/secondary/ghost/destructive, и т.д.). Стандарт для shadcn-vue. |
| `clsx` + `tailwind-merge` | `cn()` helper для class merging. Стандарт. |
| `lucide-vue-next` | Иконки. shadcn-vue по умолчанию использует Lucide. |
| `@radix-vue/colors` *(опционально)* | Пресеты цветов если стандартных Tailwind палитр не хватит. |

**Что НЕ используем:**

- `naive-ui` — vendor lock-in, не гибкий для кастомного брендинга.
- `primevue` — bundle size 200+ KB, overkill для MVP.
- `element-plus` — устаревший дизайн.
- `vuetify` — Material Design, не вяжется с dev-tool эстетикой.
- `ant-design-vue` — opinionated, не для кастомизации.
- Tailwind UI / shadcn Blocks (платные) — пока не нужны, бесплатных
  компонентов shadcn-vue хватает на 90% экранов.

## Alternatives considered

**A. Naive UI.** Готовых компонентов больше (DataTable с фильтрами
из коробки). **Отклонено:** vendor lock-in (компоненты в `node_modules`),
глобальный theme через JS-overrides (менее гибко для кастомного
брендинга), bundle size 150-200 KB. Подходит для проекта, где
скорость важнее гибкости. У нас наоборот.

**B. PrimeVue.** Самая большая коллекция компонентов (200+).
**Отклонено:** bundle size, сложный API, менее популярен в Vue community
(~12k⭐ vs shadcn-vue растёт с 12k+). Unstyled mode (PrimeVue v4)
сравним с shadcn-vue, но shadcn-vue проще в интеграции.

**C. Element Plus.** Много готовых компонентов, большое community
(русскоговорящее). **Отклонено:** дизайн «из 2018», менее современный
API, тяжеловесный. Подходит для enterprise с готовыми требованиями
к «китайскому» виду.

**D. Vuetify (Material Design).** **Отклонено:** opinionated,
нельзя сделать кастомный «Aegis look», bundle огромный.

**E. Hand-rolled headless (Reka UI + Tailwind без shadcn-vue CLI).**
**Рассмотрено как fallback** — если shadcn-vue CLI не подойдёт по
каким-то причинам, мы просто берём Reka UI напрямую + пишем компоненты
сами. Это дольше на старте, но даёт ещё больше контроля. Не выбрано
потому что shadcn-vue — это «готовая разметка + стилизация поверх
Reka UI», экономит дни работы.

**F. Без UI-кита (vanilla Vue 3 + кастомные компоненты).**
**Отклонено:** для solo-dev на MVP-таймлайне 5-7 недель это
непростительная трата времени.

## Consequences

**Положительные:**

- **Скорость + гибкость.** shadcn-vue даёт готовые компоненты за минуту
  (через CLI), но код в репо — правишь под себя без `!important`.
- **Кастомный брендинг = `tailwind.config.ts` + CSS variables.** Через
  полгода когда захочется «Aegis look» — это 30 минут работы, не
  переписывание админки.
- **Bundle size оптимален** — 30-80 KB на типичный кейс, против 200+ KB
  у PrimeVue.
- **TypeScript-first** — все компоненты типизированы, автокомплит
  работает.
- **Accessibility из коробки** — Dialog, Dropdown, Tabs корректно
  работают с клавиатурой и screen reader'ами (через Reka UI).
- **MIT license** — совместимо с AGPL-3.0 backend.
- **Активно развивается** — shadcn-vue ~12k⭐, растёт на 100-200⭐/мес.
  Не рискованный выбор.

**Отрицательные:**

- **DataTable собираем сами** поверх TanStack Table + shadcn-vue
  `Table` примитива. Это 1-2 дня работы для MVP-0.1.
- **TreeSelect / Cascader** — придётся собирать на Reka UI + Vue
  Composition API. На MVP нужны редко (только для выбора parent-host
  в форме inbound'а, можно обойтись `<Select>` с поиском).
- **Charts** для dashboard'а — нужно выбрать отдельно
  (`vue-echarts` или `unovis-vue`). На MVP-0.1 dashboard минимальный,
  charts не блокируют.
- **TailwindCSS learning curve** если не использовал раньше.
  Компенсируется utility-first природой — на второй день уже
  комфортно.

**Нейтральные:**

- shadcn-vue требует Node 18+ и Vue 3.4+. У нас 3.5 — ок.
- Обновления shadcn-vue не автоматические (компоненты в репо,
  обновляешь вручную при желании). Это **фича**, не баг — нет
  неожиданных breaking changes.

## Implementation

### MVP-0.1 (`v0.1.0-mvp-render`)

1. **PR-1 (init):** TailwindCSS + PostCSS + autoprefixer +
   `@tailwindcss/forms` + `@tailwindcss/typography`. `shadcn-vue` init
   (TypeScript: yes, CSS variables: yes, base color: zinc/slate).
   Базовый layout (sidebar + topbar).
2. **PR-2 (базовые компоненты):** `shadcn-vue add button card input
   label table dialog dropdown-menu select textarea toast tabs form
   sheet skeleton badge`. `lucide-vue-next`. `class-variance-authority`,
   `clsx`, `tailwind-merge`, `src/lib/utils.ts` с `cn()`.
3. **PR-3 (формы + валидация):** `vee-validate` + `zod` +
   `@vee-validate/zod`. Wrapper `<FormField>` поверх shadcn-vue
   `Form` + `Input`/`Select`/`Textarea`. Zod-схемы выносятся в
   `src/schemas/` для переиспользования с OpenAPI codegen.
4. **PR-4 (DataTable):** `@tanstack/vue-table` + wrapper
   `src/components/ui/DataTable.vue` (column def, pagination,
   sorting, filters). Используется на страницах Nodes / Inbounds /
   Hosts / Users / Subscriptions.
5. **PR-5 (страницы):** `NodesView.vue`, `InboundsView.vue`,
   `HostsView.vue`, `UsersView.vue`, `SubscriptionView.vue`. CRUD
   формы + DataTable'ы. `DashboardView.vue` расширяется.
6. **PR-6 (i18n):** все hardcoded строки → `t('key')`. Новые ключи в
   `ru.json` + `en.json`.

### Темы

Дефолт — light mode, dark mode через `prefers-color-scheme` или
toggle в topbar (выбираем в PR-1). CSS variables в
`src/assets/styles.css`:

```css
:root {
  --background: 0 0% 100%;
  --foreground: 240 10% 3.9%;
  --primary: 240 5.9% 10%;
  /* … полный набор shadcn-vue variables */
}

.dark {
  --background: 240 10% 3.9%;
  --foreground: 0 0% 98%;
  /* … */
}
```

Кастомизация под бренд Aegis — в PR-1, цвета в HSL.

### Definition of Done для UI-стека

- [ ] `pnpm install` проходит чисто
- [ ] `pnpm run type-check` — 0 ошибок
- [ ] `pnpm run lint` — 0 ошибок
- [ ] `pnpm run build` — production bundle собирается
- [ ] `src/components/ui/` — 10+ базовых компонентов
- [ ] Один полный flow работает end-to-end: открыть страницу → создать
  сущность через форму → увидеть в DataTable → отредактировать → удалить
- [ ] Темы переключаются (light/dark)
- [ ] Lighthouse accessibility score ≥ 90 на dashboard странице
- [ ] Bundle size DataTable-страницы ≤ 200 KB gzipped

## References

- [shadcn-vue](https://www.shadcn-vue.com/) — официальный сайт, документация
- [Reka UI](https://reka-ui.com/) (бывший radix-vue)
- [TailwindCSS](https://tailwindcss.com/)
- [TanStack Table (Vue)](https://tanstack.com/table/latest)
- [vee-validate](https://vee-validate.logaretm.com/)
- [zod](https://zod.dev/)
- [lucide-vue-next](https://lucide.dev/guide/packages/lucide-vue-next)
- ARCHITECTURE.md §1 (границы MVP), §21 Phase 1 / MVP-0.1
- [ADR-0003](./0003-mvp-singbox-vertical-slice.md) — общий контекст MVP

## Open questions

- **Tailwind v3 vs v4?** На момент интеграции (2026-07) проверить
  актуальный статус v4. Если v4 stable — используем v4, иначе v3.
  Решаем в PR-1.
- **Charts library для dashboard'а** — `vue-echarts` (больше фич)
  vs `unovis-vue` (легче, Vue-native). Решаем в Phase 2 (v1.5.0),
  на MVP-0.1 dashboard без графиков.
- **Lucide vs Heroicons** — shadcn-vue по умолчанию Lucide. Если
  предпочитаешь Heroicons — поменяем, но Lucide шире по набору.
