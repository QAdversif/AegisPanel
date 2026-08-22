<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue Select root. Wraps `SelectRoot` from
  radix-vue. Pair with SelectTrigger, SelectValue
  and SelectContent (which contains SelectItem
  children).

  The `modelValue` is always a string (radix-vue's
  contract). For multi-select, lift state into a
  custom array outside the component.

  v0.8.28.4: replaced `v-bind="props"` + `withDefaults`
  with explicit per-prop bindings and a forwarded
  `update:open` event. The previous spread forced
  `modelValue: undefined` and `defaultValue: undefined`
  onto `SelectRoot` even when the consumer did not
  pass them, which radix-vue's `useVModel` interprets
  as a deliberate assignment (not a missing prop).
  In radix-vue 1.9.17 this can interact poorly with
  the `passive: e.modelValue === void 0` check that
  decides controlled-vs-uncontrolled mode. Explicit
  per-prop bindings make the "not passed" state
  unambiguous: when the consumer does not pass
  `modelValue`, the prop is undefined by Vue's normal
  defaulting and `useVModel` enters uncontrolled mode
  cleanly.

  Also forwards `update:open`. The wrapper declared
  the event via `defineEmits<SelectRootEmits>()` but
  the previous template only forwarded
  `update:model-value`, so any consumer using
  `v-model:open` on `<Select>` would have a broken
  controlled-state loop. Not used in the v0.1.0
  views, but the API is now consistent.
-->
<script setup lang="ts">
import { SelectRoot, type SelectRootEmits, type SelectRootProps } from 'radix-vue'

const props = defineProps<SelectRootProps>()
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
