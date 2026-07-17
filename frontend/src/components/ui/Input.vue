<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue Input. Single-line text input styled
  for the Aegis look. Use `v-model` on the parent
  (no internal v-model — Vue 3.4+ allows `defineModel`).

  The component is a thin `<input>` wrapper: all
  native input attributes (type, autocomplete,
  placeholder, etc.) pass through to the underlying
  element. `type` defaults to "text".
-->
<script setup lang="ts">
import { computed, useAttrs } from 'vue'

import { cn } from '@/lib/utils'

defineOptions({ inheritAttrs: false })

const props = withDefaults(
  defineProps<{
    modelValue?: string | number
    type?: string
    class?: string
  }>(),
  {
    modelValue: '',
    type: 'text',
    class: undefined,
  },
)

const emit = defineEmits<{
  'update:modelValue': [value: string]
}>()

const attrs = useAttrs()

const classes = computed(() =>
  cn(
    'flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm transition-colors file:border-0 file:bg-transparent file:text-sm file:font-medium placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50',
    props.class,
  ),
)

function onInput(event: Event) {
  const target = event.target as HTMLInputElement
  emit('update:modelValue', target.value)
}
</script>

<template>
  <input
    :type="props.type"
    :value="props.modelValue"
    :class="classes"
    v-bind="attrs"
    @input="onInput"
  >
</template>
