/// <reference types="node" />

// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Regression test for the missing shadcn-vue
// animation keyframes (the "all dropdowns are
// invisible" bug, post-v0.8.28).
//
// Run in the Node environment (not jsdom) so we
// can read the source file via `node:fs` and
// `node:path` without any DOM polyfill overhead.
// The `@types/node` devDependency provides the
// ambient types.

// @vitest-environment node
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

const stylesPath = join(__dirname, 'styles.css')
const styles = readFileSync(stylesPath, 'utf-8')
//
// The Tailwind v3 -> v4 migration (PR B of the
// frontend-deps batch, v0.8.11 era) dropped the
// `tailwindcss-animate` plugin. The migration
// comment in `styles.css` explicitly said "its
// keyframes are inlined below" but only the two
// accordion keyframes were inlined. The 7 other
// keyframes (animate-in / animate-out / fade-in-0
// / zoom-in-95 / slide-in-from-{top,bottom,left,right}-2)
// were forgotten. Every shadcn-vue primitive
// <Content> component references the `animate-in`
// class; without the keyframe the entrance
// animation never runs and the popup sits at
// `opacity: 0` forever (the popup is in the DOM
// but invisible).
//
// This test reads the SOURCE `styles.css` file
// and asserts the required animation contract
// is present. If a future commit removes the
// keyframes, the utility classes, or the @theme
// --animate-in / --animate-out tokens, the
// `pnpm test` run (which feeds into the
// `frontend` CI job) fails with a clear error
// pointing to this file. The CI guard is cheap
// (single file read, runs in < 100 ms).
//
// This is the structural half of the regression
// guard. The runtime half is in
// `frontend/src/components/ui/Select.test.ts`
// which mounts a real <Select> and asserts the
// SelectContent has the expected className +
// data-state="open" wiring. Together they cover:
//   1. The CSS source has the keyframes (this test)
//   2. The CSS source has the state utility
//      classes (this test)
//   3. The CSS source has the @theme animation
//      tokens (this test)
//   4. The shadcn-vue primitive <Content> class
//      names reference the right data-state
//      attribute and the right animation classes
//      (the Select.test.ts runtime check)
//   5. The shadcn-vue primitive <Content>
//      structure wires `data-state="open"` correctly
//      (the Select.test.ts runtime check)
//
// The actual visual effect (opacity 1 at the
// resting state) requires a real CSS engine and
// is out of scope for jsdom. A Playwright E2E
// test could cover that, but Playwright is not
// currently set up in this project (would be a
// 1-2 day setup).

describe('shadcn-vue animation contract (regression guard for the v0.8.28 dropdowns bug)', () => {
  describe('keyframes', () => {
    it('defines @keyframes animate-in (the from->to entrance animation for Select, Dialog, Popover, Tooltip, DropdownMenu, Sheet)', () => {
      expect(styles).toMatch(/@keyframes\s+animate-in\s*\{/)
    })

    it('defines @keyframes animate-out (the to->from exit animation)', () => {
      expect(styles).toMatch(/@keyframes\s+animate-out\s*\{/)
    })

    it('animate-in keyframe interpolates from the per-utility --tw-enter-* variables to the resting state (opacity 1, scale 1, translate 0)', () => {
      // The from block MUST read from the per-utility
      // CSS variables so the fade-in-0 / zoom-in-95 /
      // slide-in-from-*-2 utility classes can
      // override them at the element level.
      const animateInBlock = styles.match(/@keyframes\s+animate-in\s*\{([\s\S]*?)\n\}/)
      expect(animateInBlock, '@keyframes animate-in is missing entirely').toBeTruthy()
      const body = animateInBlock![1]
      expect(body).toMatch(/var\(--tw-enter-opacity/)
      expect(body).toMatch(/var\(--tw-enter-translate-x/)
      expect(body).toMatch(/var\(--tw-enter-translate-y/)
      expect(body).toMatch(/var\(--tw-enter-scale/)
    })

    it('animate-in keyframe respects the per-component --tw-rest-translate-* resting offset (v0.8.28.2 fix for the DialogContent centring regression)', () => {
      // Pre-v0.8.28.2 the keyframe `to` state was
      // `transform: translate(0) scale(1)`. DialogContent
      // uses `left-[50%] top-[50%] translate-x-[-50%]
      // translate-y-[-50%]` for centring — the animation
      // overwrote the centring translate, leaving the
      // panel at top-left-of-centre (its `left:50%
      // top:50%` corner, not the panel centre).
      //
      // The fix: the keyframe reads `--tw-rest-translate-x`
      // / `--tw-rest-translate-y` (default 0,0) and the
      // `to` state is `translate(--tw-rest-*, 0) scale(1)`.
      // DialogContent sets these to -50%/-50% so the
      // panel ends at the same centring translate the
      // static CSS class established. The `from` state
      // adds the per-utility --tw-enter-* offsets on top
      // of the rest so the slide-in direction is
      // preserved.
      const animateInBlock = styles.match(/@keyframes\s+animate-in\s*\{([\s\S]*?)\n\}/)
      expect(animateInBlock, '@keyframes animate-in is missing entirely').toBeTruthy()
      const body = animateInBlock![1]
      // to-block: must reference the rest translate
      // (not hardcode translate(0)).
      const toBlock = body.match(/\bto\s*\{([\s\S]*?)\}/)
      expect(toBlock, '@keyframes animate-in: missing `to` block').toBeTruthy()
      expect(toBlock![1]).toMatch(/var\(--tw-rest-translate-x/)
      expect(toBlock![1]).toMatch(/var\(--tw-rest-translate-y/)
      // from-block: must add the rest translate to
      // the per-utility enter translate so the slide
      // direction is preserved.
      const fromBlock = body.match(/\bfrom\s*\{([\s\S]*?)\}/)
      expect(fromBlock, '@keyframes animate-in: missing `from` block').toBeTruthy()
      expect(fromBlock![1]).toMatch(/var\(--tw-rest-translate-x/)
      expect(fromBlock![1]).toMatch(/var\(--tw-rest-translate-y/)
      expect(fromBlock![1]).toMatch(/var\(--tw-enter-translate-x/)
      expect(fromBlock![1]).toMatch(/var\(--tw-enter-translate-y/)
    })

    it('animate-out keyframe interpolates the reverse (resting state to the per-utility --tw-exit-* variables)', () => {
      const animateOutBlock = styles.match(/@keyframes\s+animate-out\s*\{([\s\S]*?)\n\}/)
      expect(animateOutBlock, '@keyframes animate-out is missing entirely').toBeTruthy()
      const body = animateOutBlock![1]
      expect(body).toMatch(/var\(--tw-exit-opacity/)
      expect(body).toMatch(/var\(--tw-exit-translate-x/)
      expect(body).toMatch(/var\(--tw-exit-translate-y/)
      expect(body).toMatch(/var\(--tw-exit-scale/)
      // The reverse keyframe also respects the rest
      // translate so a Sheet/Dialog centred via
      // translate(-50%, -50%) doesn't snap to
      // translate(0) when the exit starts.
      expect(body).toMatch(/var\(--tw-rest-translate-x/)
      expect(body).toMatch(/var\(--tw-rest-translate-y/)
    })

    it('keeps the legacy accordion keyframes (the v3 -> v4 migration was supposed to inline them)', () => {
      // These were the only two keyframes that
      // WERE inlined. The bug was forgetting the
      // 7 others. This assertion guards against
      // someone "cleaning up" by removing the
      // accordion ones too.
      expect(styles).toMatch(/@keyframes\s+accordion-down\s*\{/)
      expect(styles).toMatch(/@keyframes\s+accordion-up\s*\{/)
    })
  })

  describe('@theme animation tokens', () => {
    it('defines --animate-in with the standard shadcn "out-quint" easing (cubic-bezier(0.16, 1, 0.3, 1))', () => {
      // The @theme block has no nested braces (the
      // cubic-bezier uses parens, not braces), so a
      // flat search of the whole styles file works
      // just as well as extracting the @theme body.
      expect(styles).toMatch(/--animate-in:\s*animate-in\s+0\.2s\s+cubic-bezier\(0\.16,\s*1,\s*0\.3,\s*1\)/)
    })

    it('defines --animate-out (the reverse)', () => {
      expect(styles).toMatch(/--animate-out:\s*animate-out\s+0\.15s\s+cubic-bezier\(0\.16,\s*1,\s*0\.3,\s*1\)/)
    })
  })

  describe('state utility classes (set the per-utility --tw-enter-* and --tw-exit-* CSS variables)', () => {
    it('defines .fade-in-0 / .fade-out-0 (opacity)', () => {
      expect(styles).toMatch(/\.fade-in-0\s*\{[\s\S]*?--tw-enter-opacity:\s*0/)
      expect(styles).toMatch(/\.fade-out-0\s*\{[\s\S]*?--tw-exit-opacity:\s*0/)
    })

    it('defines .zoom-in-95 / .zoom-out-95 (scale)', () => {
      expect(styles).toMatch(/\.zoom-in-95\s*\{[\s\S]*?--tw-enter-scale:\s*0\.95/)
      expect(styles).toMatch(/\.zoom-out-95\s*\{[\s\S]*?--tw-exit-scale:\s*0\.95/)
    })

    it('defines .slide-in-from-top-2 / -bottom-2 / -left-2 / -right-2 (translate)', () => {
      expect(styles).toMatch(/\.slide-in-from-top-2\s*\{[\s\S]*?--tw-enter-translate-y:\s*-0\.5rem/)
      expect(styles).toMatch(/\.slide-in-from-bottom-2\s*\{[\s\S]*?--tw-enter-translate-y:\s*0\.5rem/)
      expect(styles).toMatch(/\.slide-in-from-left-2\s*\{[\s\S]*?--tw-enter-translate-x:\s*-0\.5rem/)
      expect(styles).toMatch(/\.slide-in-from-right-2\s*\{[\s\S]*?--tw-enter-translate-x:\s*0\.5rem/)
    })
  })

  describe('data-attribute variant utility classes (wire radix-vue data-state to the animation)', () => {
    it('defines .data-[state=open]:animate-in (radix-vue mounts the content with data-state="open" when it opens)', () => {
      // Tailwind v4 escapes FOUR characters in a
      // data-attribute class selector: `[`, `=`,
      // `]`, and `:`. So the file's literal text is
      // `.data-\[state\=open\]\:animate-in` —
      // with backslashes before EACH of `[`, `=`,
      // `]`, and `:`. In a JS regex literal:
      //   `\\` matches a literal `\`
      //   `\[` matches a literal `[`
      //   `\=` is rejected by ESLint (use plain `=`)
      //   `\]` matches a literal `]`
      //   `\:` is rejected by ESLint (use plain `:`)
      // The trailing `\\` before the `:` is required
      // to consume the backslash that Tailwind
      // emits in the source.
      /* eslint-disable no-useless-escape */
      expect(styles).toMatch(/\.data-\\\[state\\=open\\\]\\\:animate-in/)
    })

    it('defines .data-[state=closed]:animate-out (radix-vue sets data-state="closed" when the content closes)', () => {
      /* eslint-disable no-useless-escape */
      expect(styles).toMatch(/\.data-\\\[state\\=closed\\\]\\\:animate-out/)
    })
  })
})
