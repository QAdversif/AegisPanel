<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue Select root. Wraps `SelectRoot` from
  radix-vue. Pair with SelectTrigger, SelectValue
  and SelectContent (which contains SelectItem
  children).

  The `modelValue` is always a string (radix-vue's
  contract). For multi-select, lift state into a
  custom array outside the component.

  v0.8.28.4: replaced `v-bind="props"` with explicit
  per-prop bindings and forwarded the missing
  `update:open` event. This was the FIRST half of
  the fix.

  v0.8.28.5: added `withDefaults({ open: undefined,
  defaultOpen: false, modelValue: undefined,
  defaultValue: undefined })` to BLOCK Vue 3's
  Boolean prop casting. **This is the actual fix
  for the click-handler bug tracked in #287.**

  ## Why the explicit `open: undefined` default matters

  In Vue 3, a prop declared as `Boolean` (e.g.
  `open?: boolean` in `SelectRootProps`) without
  an explicit default is **cast to `false` (not
  `undefined`) when absent**. The radix-vue 1.9.17
  `SelectRoot` setup uses the presence-vs-absence
  check to decide controlled-vs-uncontrolled mode:

  ```js
  const s = useVModel(e, "open", n, {
    defaultValue: e.defaultOpen,
    passive: e.open === void 0, // ← false when e.open is false (the casted value)
  });
  ```

  Without an explicit `open: undefined` default in
  the wrapper, `props.open` is always `false`, the
  `passive: e.open === void 0` check resolves to
  `false`, and radix-vue's `useVModel` enters
  **controlled mode pinned at `false`**. Internal
  open attempts emit `update:open`; the wrapper
  must re-emit them for the parent to write the
  prop back. Without the re-emit, nothing ever
  writes `open=true` → the select can never open.

  The `Tooltip`, `DropdownMenu`, `Sheet`, `Toast`,
  and `Dialog` wrappers all already have
  `open: undefined` in their `withDefaults` (this
  is the v0.1.0 shadcn-vue convention). `Select`
  was missing it from PR #51 onwards, which is why
  it was the only broken root.

  The fix here is a one-line addition to the
  `withDefaults` block, plus keeping the v0.8.28.4
  per-prop bindings + `update:open` forward (the
  latter is required for the v0.8.28.4 case where
  some consumer does want controlled mode via
  `v-model:open`).

  ## Why both the spread AND the explicit bindings

  Keeping the explicit `:model-value="..."` /
  `:open="..."` bindings (from v0.8.28.4) makes
  the prop-forwarding contract explicit. It also
  makes the regression test in `Select.test.ts`
  straightforward (it can assert each prop is
  individually present in the template, without
  having to enumerate every `SelectRootProps` key
  in a positive `v-bind="props"` match). The
  `withDefaults` is the load-bearing fix; the
  per-prop bindings are a clarity + guard
  improvement.
-->
<script setup lang="ts">
import { SelectRoot, type SelectRootEmits, type SelectRootProps } from 'radix-vue'

// The `open: undefined` and `defaultOpen: false`
// defaults are the load-bearing part of the v0.8.28.5
// fix. Without the `open: undefined` key, Vue 3's
// Boolean prop casting pins `props.open` to `false`
// even when the consumer does not pass it, which
// forces radix-vue's `useVModel` into controlled
// mode (because `e.open === void 0` resolves to
// `false`). The wrapper then becomes unable to
// open the popup because the controlled `open=false`
// prop is never written back by the consumer
// (the wrapper also does not re-emit `update:open`).
// `defaultOpen: false` is the corresponding
// `uncontrolled initial value` default that
// matches radix-vue's own internal default.
const props = withDefaults(defineProps<SelectRootProps>(), {
  open: undefined,
  defaultOpen: false,
  modelValue: undefined,
  defaultValue: undefined,
})

const emit = defineEmits<SelectRootEmits>()
</script>

<template>
  <SelectRoot
    :model-value="props.modelValue"
    :default-value="props.defaultValue"
    :open="props.open"
    :default-open="props.defaultOpen"
    :dir="props.dir"
    :name="props.name"
    :autocomplete="props.autocomplete"
    :disabled="props.disabled"
    :required="props.required"
    @update:model-value="(value) => emit('update:modelValue', value as string)"
    @update:open="(value) => emit('update:open', value)"
  >
    <slot />
  </SelectRoot>
</template>
