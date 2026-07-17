<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue DropdownMenuLabel. A non-clickable
  header inside a DropdownMenuContent. Use to
  group items by category.
-->
<script setup lang="ts">
import { DropdownMenuLabel as RxDropdownMenuLabel } from 'radix-vue'

import { cn } from '@/lib/utils'

const props = withDefaults(
  defineProps<{
    inset?: boolean
    class?: string
  }>(),
  {
    inset: false,
    class: undefined,
  },
)
</script>

<template>
  <RxDropdownMenuLabel
    :class="
      cn(
        'px-2 py-1.5 text-sm font-semibold',
        props.inset && 'pl-8',
        props.class,
      )
    "
  >
    <slot />
  </RxDropdownMenuLabel>
</template>
