<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue Select root. Wraps `SelectRoot` from
  radix-vue. Pair with SelectTrigger, SelectValue
  and SelectContent (which contains SelectItem
  children).

  The `modelValue` is always a string (radix-vue's
  contract). For multi-select, lift state into a
  custom array outside the component.
-->
<script setup lang="ts">
import { SelectRoot, type SelectRootEmits, type SelectRootProps } from 'radix-vue'

const props = withDefaults(defineProps<SelectRootProps>(), {
  modelValue: undefined,
  defaultValue: undefined,
})

const emit = defineEmits<SelectRootEmits>()
</script>

<template>
  <SelectRoot
    v-bind="props"
    @update:model-value="(value) => emit('update:modelValue', value as string)"
  >
    <slot />
  </SelectRoot>
</template>
