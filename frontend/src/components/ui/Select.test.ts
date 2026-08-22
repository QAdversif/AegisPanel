// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Regression test for the v0.8.28 "all dropdowns
// are invisible" bug, runtime-half. The
// structural CSS half (keyframes + utility
// classes) lives in `frontend/src/assets/styles.test.ts`.
// This file asserts the shadcn-vue <SelectContent>
// .vue component's className string wires the
// data-state="open" / data-state="closed"
// attribute selectors to the (now-present)
// animate-in / animate-out animations, and that
// the data-attribute modifier syntax matches
// what radix-vue's Presence component sets.
//
// We read the .vue SOURCE files rather than
// mounting the components in jsdom because
// radix-vue's Teleport + Floating UI's popper
// positioning + Presence's exit-animation
// keep-alive behaviour are fragile to set up in
// jsdom (the v0.8.28-era PR #270 dialog tests
// use the `uiStubs` render-function shims to
// avoid this). A pure source-level assertion is
// more robust and catches the same regression
// class: someone changes the SelectContent
// className to drop `animate-in` or the
// `data-[state=open]:` prefix, the assertion
// fires.
//
// The actual visual effect (opacity 1 at the
// resting state after the entrance animation
// runs) requires a real CSS engine and is out of
// scope for jsdom. A Playwright E2E test could
// cover that, but Playwright is not currently
// set up in this project (would be a 1-2 day
// setup).

/// <reference types="node" />

// @vitest-environment node
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

const selectContentPath = join(__dirname, 'SelectContent.vue')
const selectContent = readFileSync(selectContentPath, 'utf-8')

const selectPath = join(__dirname, 'Select.vue')
const select = readFileSync(selectPath, 'utf-8')

const selectItemPath = join(__dirname, 'SelectItem.vue')
const selectItem = readFileSync(selectItemPath, 'utf-8')

const selectTriggerPath = join(__dirname, 'SelectTrigger.vue')
const selectTrigger = readFileSync(selectTriggerPath, 'utf-8')

describe('shadcn-vue <Select> family (regression guard for the v0.8.28 dropdowns bug)', () => {
  describe('<SelectContent>', () => {
    it('includes the data-[state=open]:animate-in class (the entrance animation trigger)', () => {
      // Match the file's literal text. In a JS
      // regex literal, `\[` and `\]` match literal
      // `[` and `]` (no backslashes in the file).
      expect(selectContent).toMatch(/data-\[state=open\]:animate-in/)
    })

    it('includes the data-[state=closed]:animate-out class (the exit animation trigger)', () => {
      expect(selectContent).toMatch(/data-\[state=closed\]:animate-out/)
    })

    it('includes fade-in-0 + zoom-in-95 + slide-in-from-{top,bottom,left,right}-2 (the from-state utilities)', () => {
      expect(selectContent).toMatch(/\bfade-in-0\b/)
      expect(selectContent).toMatch(/\bzoom-in-95\b/)
      expect(selectContent).toMatch(/\bslide-in-from-top-2\b/)
      expect(selectContent).toMatch(/\bslide-in-from-bottom-2\b/)
      expect(selectContent).toMatch(/\bslide-in-from-left-2\b/)
      expect(selectContent).toMatch(/\bslide-in-from-right-2\b/)
    })

    it('wraps the content in <SelectPortal> (Vue Teleport to document.body)', () => {
      expect(selectContent).toMatch(/<SelectPortal>/)
      expect(selectContent).toMatch(/<\/SelectPortal>/)
    })

    it('renders the inner content via <RxSelectContent> (the radix-vue primitive)', () => {
      expect(selectContent).toMatch(/<RxSelectContent/)
    })
  })

  describe('<Select>', () => {
    it('forwards all SelectRootProps via v-bind (so v-model:open on <Select> wires to <SelectRoot>)', () => {
      // The minimal contract: <SelectRoot v-bind="props" ...>
      // ensures any prop on <Select> (including the
      // controlled `open` prop) is forwarded to the
      // radix-vue primitive.
      expect(select).toMatch(/<SelectRoot[^>]*v-bind="props"/)
    })

    it('emits update:modelValue (so v-model on the consumer side picks up value changes)', () => {
      // The brief forward of @update:model-value to
      // emit("update:modelValue", value) is what
      // makes v-model="value" on the consumer side
      // work. If this is removed the value is silently
      // never persisted.
      expect(select).toMatch(/emit\(['"]update:modelValue['"]/)
    })
  })

  describe('<SelectItem>', () => {
    it('wraps the option in <RxSelectItem> (the radix-vue primitive)', () => {
      expect(selectItem).toMatch(/<RxSelectItem/)
    })

    it('includes the focus:bg-accent utility (the focus state contract)', () => {
      // Guards against someone deleting the focus
      // utility class (which would make the
      // option un-highlightable on keyboard nav).
      expect(selectItem).toMatch(/\bfocus:bg-accent\b/)
    })
  })

  describe('<SelectTrigger>', () => {
    it('wraps the trigger in <RxSelectTrigger> (the radix-vue primitive)', () => {
      expect(selectTrigger).toMatch(/<RxSelectTrigger/)
    })

    it('includes the focus:ring-1 focus:ring-ring utility (the focus state contract)', () => {
      // Guards against someone deleting the focus
      // ring utility (which would make the
      // trigger un-focusable via keyboard).
      expect(selectTrigger).toMatch(/\bfocus:ring-1\b/)
      expect(selectTrigger).toMatch(/\bfocus:ring-ring\b/)
    })
  })
})
