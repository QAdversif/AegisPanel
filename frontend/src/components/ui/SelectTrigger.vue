<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue SelectTrigger. The button-like field
  that opens the select. Renders a chevron icon
  and the currently-selected value placeholder.

  Use the `#default` slot of `SelectValue` to
  customise the trigger label (e.g. translated
  display strings for enum values).
-->
<script setup lang="ts">
import { computed } from 'vue'
import { SelectTrigger as RxSelectTrigger } from 'radix-vue'
import { ChevronDown } from 'lucide-vue-next'

import { cn } from '@/lib/utils'

const props = defineProps<{ class?: string | boolean | undefined }>()

const classes = computed(() =>
  cn(
    'flex h-9 w-full items-center justify-between whitespace-nowrap rounded-md border border-input bg-transparent px-3 py-2 text-sm shadow-sm ring-offset-background placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring disabled:cursor-not-allowed disabled:opacity-50 [&>span]:line-clamp-1',
    props.class,
  ),
)
</script>

<template>
  <RxSelectTrigger :class="classes">
    <slot />
    <ChevronDown class="h-4 w-4 opacity-50" />
  </RxSelectTrigger>
</template>
