<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue Textarea. Multi-line analogue of
  `Input`. Resize is vertical-only (`resize-y`) to
  keep form layouts predictable. The same
  `min-h-[80px]` floor prevents the field from
  collapsing to a single line on initial render.
-->
<script setup lang="ts">
import { computed, useAttrs } from 'vue'

import { cn } from '@/lib/utils'

defineOptions({ inheritAttrs: false })

const props = withDefaults(
  defineProps<{
    modelValue?: string
    rows?: number
    class?: string | boolean | undefined
  }>(),
  {
    modelValue: '',
    rows: 4,
    class: undefined,
  },
)

const emit = defineEmits<{
  'update:modelValue': [value: string]
}>()

const attrs = useAttrs()

const classes = computed(() =>
  cn(
    'flex min-h-[80px] w-full rounded-md border border-input bg-transparent px-3 py-2 text-sm shadow-sm placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50',
    props.class,
  ),
)

function onInput(event: Event) {
  const target = event.target as HTMLTextAreaElement
  emit('update:modelValue', target.value)
}
</script>

<template>
  <textarea
    :value="props.modelValue"
    :rows="props.rows"
    :class="classes"
    v-bind="attrs"
    @input="onInput"
  />
</template>
