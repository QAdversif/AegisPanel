// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Shared test helpers for the dialog .test.ts files.
// Imported by each dialog's test module to keep the
// per-file line count under the 250-line cap set by
// the PR #270 brief.
//
// The `uiStubs` map covers the radix-vue Dialog root
// + the shadcn-vue Dialog*/FormField/Input/Textarea
// primitives + the Select/RadioGroup family. The
// stubs use render functions (not string templates)
// to avoid the vue-test-utils slot-template quirk
// where `<slot />` inside a string template fails to
// wrap multiple slot children.

import { h } from 'vue'

// `passthrough` returns a render stub that wraps
// its slot children in a `tag` with `attrs`. Used
// for the dialog-level primitives that the dialog's
// own logic does not depend on.
function passthrough(tag: string, attrs: Record<string, unknown> = {}) {
  return {
    render(this: { $slots: { default?: () => unknown[] } }) {
      return h(tag, attrs, this.$slots.default?.() as never)
    },
  }
}

export const uiStubs = {
  Dialog: passthrough('div', { 'data-test': 'dialog-root' }),
  DialogContent: passthrough('div', { 'data-test': 'dialog-content' }),
  DialogHeader: passthrough('div', { 'data-test': 'dialog-header' }),
  DialogTitle: passthrough('h2', { 'data-test': 'dialog-title' }),
  DialogDescription: passthrough('p', { 'data-test': 'dialog-description' }),
  DialogFooter: passthrough('div', { 'data-test': 'dialog-footer' }),
  DialogClose: passthrough('button', { 'data-test': 'dialog-close', type: 'button' }),
  // Input renders a real <input> with a 1:1 mapping
  // for the shadcn-vue Input used in every dialog.
  // The stub forwards the v-model so the test can
  // setValue() to fill the form.
  Input: {
    props: ['modelValue', 'type', 'readonly', 'class'],
    emits: ['update:modelValue'],
    render(this: {
      modelValue?: string
      type?: string
      readonly?: boolean
      $attrs?: { class?: string }
      $emit: (e: string, v: string) => void
    }) {
      return h('input', {
        type: this.type || 'text',
        value: this.modelValue ?? '',
        readonly: this.readonly,
        class: this.$attrs?.class,
        onInput: (e: Event) => {
          const target = e.target as HTMLInputElement
          this.$emit('update:modelValue', target.value)
        },
      })
    },
  },
  // Textarea mirrors the shadcn-vue Textarea
  // shape used in the dialogs.
  Textarea: {
    props: ['modelValue', 'rows', 'readonly', 'spellcheck'],
    emits: ['update:modelValue'],
    render(this: {
      modelValue?: string
      rows?: number
      readonly?: boolean
      $emit: (e: string, v: string) => void
    }) {
      return h('textarea', {
        value: this.modelValue ?? '',
        rows: this.rows,
        readonly: this.readonly,
        onInput: (e: Event) => {
          const target = e.target as HTMLTextAreaElement
          this.$emit('update:modelValue', target.value)
        },
      })
    },
  },
  // Select family — the dialogs use these for the
  // host type radio + the nodeId / inboundId pickers.
  Select: passthrough('div', { 'data-test': 'select-root' }),
  SelectTrigger: passthrough('div', { 'data-test': 'select-trigger' }),
  SelectValue: passthrough('span', { 'data-test': 'select-value' }),
  SelectContent: passthrough('div', { 'data-test': 'select-content' }),
  SelectItem: passthrough('div', { 'data-test': 'select-item' }),
  // Radio family — used by the node create +
  // provision dialogs for the auth-method picker.
  RadioGroup: passthrough('div', { 'data-test': 'radio-group' }),
  RadioGroupItem: passthrough('div', { 'data-test': 'radio-item' }),
}

// `makeI18n` returns a stub i18n instance where
// `t('key.path')` returns the key path verbatim.
// The dialogs render `t('nodes.name')` etc. in
// their labels; with the stub the rendered text
// contains the key path, which is enough for the
// "renders the form fields" assertions.
import { createI18n } from 'vue-i18n'
export function makeI18n() {
  return createI18n({
    legacy: false,
    locale: 'en',
    messages: { en: {}, ru: {} },
  })
}
